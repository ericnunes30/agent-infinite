package orchestration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/detector"
	"github.com/agent-infinite/agent-infinite/backend/internal/terminal"
)

const MaxTaskBytes = 32 * 1024

var (
	targetReadinessTimeout = 10 * time.Minute
	targetReadinessPoll    = 200 * time.Millisecond
)

const codexRestartMarker = "update ran successfully! please restart codex"

var (
	ErrInvalidSource    = errors.New("source node is not an orchestrator")
	ErrTargetDenied     = errors.New("target is not connected by a delegation edge")
	ErrTargetAmbiguous  = errors.New("target reference is ambiguous")
	ErrTargetDead       = errors.New("target agent could not be started")
	ErrInvalidTask      = errors.New("task must contain 1 to 32768 bytes")
	ErrDispatchNotFound = errors.New("dispatch was not found for this orchestrator")
	ErrTargetQueueFull  = errors.New("target dispatch queue is full")
)

type Workspace interface {
	Snapshot() (contracts.Snapshot, error)
}

type Runtime interface {
	GetByNode(string) (*terminal.Session, error)
}

type NodeStarter func(context.Context, string) (*terminal.Session, error)
type NodeRestarter func(context.Context, string) (*terminal.Session, error)

type ConnectedAgent struct {
	ID       string          `json:"id"`
	Label    string          `json:"label"`
	Role     string          `json:"role"`
	Provider string          `json:"provider"`
	TeamID   string          `json:"team_id"`
	Status   detector.Status `json:"status"`
}

type ConnectedAgents struct {
	WorkspaceID string           `json:"workspace_id"`
	Caller      ConnectedAgent   `json:"caller"`
	Targets     []ConnectedAgent `json:"targets"`
}

type Result struct {
	Status      string          `json:"status"`
	Output      string          `json:"output,omitempty"`
	Sequence    uint64          `json:"sequence,omitempty"`
	Truncated   bool            `json:"truncated,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	AgentStatus detector.Status `json:"agent_status,omitempty"`
	Error       string          `json:"error,omitempty"`
}

type Dispatch struct {
	ID                  string     `json:"dispatch_id"`
	WorkspaceID         string     `json:"workspace_id"`
	SourceNodeID        string     `json:"source_node_id"`
	SourceLabel         string     `json:"source_label"`
	TargetNodeID        string     `json:"target_node_id"`
	TargetLabel         string     `json:"target_label"`
	TargetProvider      string     `json:"target_provider,omitempty"`
	Task                string     `json:"task"`
	Status              string     `json:"status"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	Notified            bool       `json:"notified"`
	Result              Result     `json:"result"`
	DeliveryConfirmedBy string     `json:"delivery_confirmed_by,omitempty"`
	baselineText        string
	baselineSequence    uint64
}

type Output struct {
	AgentID   string          `json:"agent_id"`
	Status    detector.Status `json:"status"`
	Text      string          `json:"text"`
	Sequence  uint64          `json:"sequence"`
	Truncated bool            `json:"truncated"`
}

type AmbiguousTargetError struct {
	Reference  string
	Candidates []ConnectedAgent
}

func (e *AmbiguousTargetError) Error() string {
	names := make([]string, 0, len(e.Candidates))
	for _, candidate := range e.Candidates {
		names = append(names, fmt.Sprintf("%s (%s, id %s)", candidate.Label, candidate.Role, candidate.ID))
	}
	return fmt.Sprintf("%v %q; candidates: %s", ErrTargetAmbiguous, e.Reference, strings.Join(names, "; "))
}

func (e *AmbiguousTargetError) Unwrap() error { return ErrTargetAmbiguous }

type Service struct {
	ctx         context.Context
	workspace   Workspace
	runtime     Runtime
	mu          sync.RWMutex
	persistMu   sync.Mutex
	dispatches  map[string]*Dispatch
	queues      map[string]chan string
	updates     map[string]chan struct{}
	starter     NodeStarter
	restarter   NodeRestarter
	emit        func(string, string, any)
	storageRoot string
}

func New(ctx context.Context, workspace Workspace, runtime Runtime) *Service {
	return &Service{
		ctx: ctx, workspace: workspace, runtime: runtime,
		dispatches: make(map[string]*Dispatch), queues: make(map[string]chan string), updates: make(map[string]chan struct{}),
		emit: func(string, string, any) {},
	}
}

func (s *Service) SetEmitter(emitter func(string, string, any)) {
	s.mu.Lock()
	s.emit = emitter
	s.mu.Unlock()
}

func (s *Service) SetStarter(starter NodeStarter) {
	s.mu.Lock()
	s.starter = starter
	s.mu.Unlock()
}

func (s *Service) SetRestarter(restarter NodeRestarter) {
	s.mu.Lock()
	s.restarter = restarter
	s.mu.Unlock()
}

func (s *Service) SetStorageRoot(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	s.mu.Lock()
	s.storageRoot = root
	s.mu.Unlock()
	return nil
}

// Reconcile restores dispatch history for the opened workspace. Any dispatch
// that was non-terminal when the previous backend disappeared is explicitly
// failed; a new process cannot prove delivery or continue owning that PTY.
func (s *Service) Reconcile(workspaceID string) error {
	s.mu.RLock()
	root := s.storageRoot
	s.mu.RUnlock()
	if root == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(root, workspaceID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var restored []Dispatch
	if err := json.Unmarshal(data, &restored); err != nil {
		return fmt.Errorf("decode dispatch recovery: %w", err)
	}
	now := time.Now().UTC()
	s.mu.Lock()
	s.dispatches = make(map[string]*Dispatch, len(restored))
	s.updates = make(map[string]chan struct{}, len(restored))
	for index := range restored {
		dispatch := restored[index]
		if !terminalDispatchState(dispatch.Status) {
			dispatch.Status = "failed"
			dispatch.UpdatedAt = now
			dispatch.CompletedAt = &now
			dispatch.Result.Status = "failed"
			dispatch.Result.Error = "backend restarted before dispatch completion"
			dispatch.Result.CompletedAt = &now
		}
		s.dispatches[dispatch.ID] = &dispatch
		s.updates[dispatch.ID] = make(chan struct{})
	}
	s.mu.Unlock()
	return s.persist(workspaceID)
}

func (s *Service) ListConnectedAgents(source string) (ConnectedAgents, error) {
	snapshot, err := s.workspace.Snapshot()
	if err != nil {
		return ConnectedAgents{}, err
	}
	sourceNode, ok := findNode(snapshot.Nodes, source)
	if !ok || sourceNode.Kind != "orchestrator" {
		return ConnectedAgents{}, ErrInvalidSource
	}
	result := ConnectedAgents{WorkspaceID: snapshot.WorkspaceID, Caller: s.agent(sourceNode)}
	for _, edge := range snapshot.Edges {
		if edge.Source != source || edge.Type != "delegates_to" {
			continue
		}
		if target, exists := findNode(snapshot.Nodes, edge.Target); exists {
			result.Targets = append(result.Targets, s.agent(target))
		}
	}
	return result, nil
}

func (s *Service) DelegateTask(source, targetReference, task string) (Dispatch, error) {
	task = strings.TrimSpace(task)
	if task == "" || len([]byte(task)) > MaxTaskBytes {
		return Dispatch{}, ErrInvalidTask
	}
	connected, err := s.ListConnectedAgents(source)
	if err != nil {
		return Dispatch{}, err
	}
	target, err := resolveTarget(targetReference, connected.Targets)
	if err != nil {
		return Dispatch{}, err
	}
	now := time.Now().UTC()
	dispatch := Dispatch{
		ID: newID(), WorkspaceID: connected.WorkspaceID, SourceNodeID: source, SourceLabel: connected.Caller.Label,
		TargetNodeID: target.ID, TargetLabel: target.Label, TargetProvider: target.Provider, Task: task,
		Status: "created", CreatedAt: now, UpdatedAt: now, Result: Result{Status: "created"},
	}
	s.mu.Lock()
	s.dispatches[dispatch.ID] = &dispatch
	s.updates[dispatch.ID] = make(chan struct{})
	emit := s.emit
	queue := s.queues[target.ID]
	if queue == nil {
		queue = make(chan string, 128)
		s.queues[target.ID] = queue
		go s.runTargetQueue(target.ID, queue)
	}
	s.mu.Unlock()
	emit("dispatch.created", dispatch.ID, dispatch)
	s.persistOrEmit(dispatch.WorkspaceID)
	s.transition(dispatch.ID, "queued", nil)
	select {
	case queue <- dispatch.ID:
		return s.dispatch(dispatch.ID), nil
	case <-s.ctx.Done():
		s.fail(dispatch.ID, "backend is shutting down")
		return s.dispatch(dispatch.ID), s.ctx.Err()
	default:
		s.fail(dispatch.ID, ErrTargetQueueFull.Error())
		return s.dispatch(dispatch.ID), ErrTargetQueueFull
	}
}

// DispatchTask is kept as a compatibility alias for callers compiled against
// 0.1.x. New MCP clients use DelegateTask and may pass a label, role, or ID.
func (s *Service) DispatchTask(source, target, task string) (Dispatch, error) {
	return s.DelegateTask(source, target, task)
}

func (s *Service) GetDispatchResult(source, dispatchID string, maxLines int) (Dispatch, error) {
	maxLines, err := normalizeMaxLines(maxLines)
	if err != nil {
		return Dispatch{}, err
	}
	s.mu.RLock()
	dispatch, exists := s.dispatches[dispatchID]
	if !exists || dispatch.SourceNodeID != source {
		s.mu.RUnlock()
		return Dispatch{}, ErrDispatchNotFound
	}
	result := *dispatch
	s.mu.RUnlock()
	result.Result.Output, result.Result.Truncated = limitLines(result.Result.Output, maxLines)
	return result, nil
}

// WaitDispatchResult suspends the MCP request inside the backend until the
// dispatch reaches a terminal state. Waiting here does not create additional
// model turns or consume tokens through repeated tool calls.
func (s *Service) WaitDispatchResult(ctx context.Context, source, dispatchID string, maxLines int) (Dispatch, error) {
	maxLines, err := normalizeMaxLines(maxLines)
	if err != nil {
		return Dispatch{}, err
	}
	for {
		s.mu.RLock()
		dispatch, exists := s.dispatches[dispatchID]
		if !exists || dispatch.SourceNodeID != source {
			s.mu.RUnlock()
			return Dispatch{}, ErrDispatchNotFound
		}
		result := *dispatch
		updates := s.updates[dispatchID]
		terminal := terminalDispatchState(result.Status)
		s.mu.RUnlock()
		if terminal {
			s.markResultDelivered(dispatchID)
			result.Notified = true
			result.Result.Output, result.Result.Truncated = limitLines(result.Result.Output, maxLines)
			return result, nil
		}
		if updates == nil {
			return Dispatch{}, ErrDispatchNotFound
		}
		select {
		case <-ctx.Done():
			return Dispatch{}, ctx.Err()
		case <-updates:
		}
	}
}

func normalizeMaxLines(maxLines int) (int, error) {
	if maxLines == 0 {
		maxLines = 120
	}
	if maxLines < 1 || maxLines > 500 {
		return 0, fmt.Errorf("max_lines must be between 1 and 500")
	}
	return maxLines, nil
}

func (s *Service) markResultDelivered(dispatchID string) {
	s.mu.Lock()
	dispatch := s.dispatches[dispatchID]
	if dispatch == nil || dispatch.Notified {
		s.mu.Unlock()
		return
	}
	dispatch.Notified = true
	workspaceID := dispatch.WorkspaceID
	s.mu.Unlock()
	s.persistOrEmit(workspaceID)
}

func (s *Service) GetOutput(agentID string, maxLines int) (Output, error) {
	if maxLines == 0 {
		maxLines = 120
	}
	if maxLines < 1 || maxLines > 500 {
		return Output{}, fmt.Errorf("max_lines must be between 1 and 500")
	}
	session, err := s.runtime.GetByNode(agentID)
	if err != nil {
		return Output{}, ErrTargetDead
	}
	text, truncated := limitLines(session.CleanText(), maxLines)
	return Output{AgentID: agentID, Status: session.Status(), Text: text, Sequence: session.Sequence(), Truncated: truncated}, nil
}

func (s *Service) GetStatus(agentID string) (map[string]detector.Status, error) {
	snapshot, err := s.workspace.Snapshot()
	if err != nil {
		return nil, err
	}
	result := make(map[string]detector.Status)
	for _, node := range snapshot.Nodes {
		if agentID != "" && node.ID != agentID {
			continue
		}
		result[node.ID] = s.status(node.ID)
	}
	return result, nil
}

func (s *Service) Dispatches() []Dispatch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Dispatch, 0, len(s.dispatches))
	for _, dispatch := range s.dispatches {
		result = append(result, *dispatch)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result
}

// CompleteFromLifecycle lets a provider hook finish the active dispatch. It is
// intentionally node-scoped here; target queues guarantee at most one running
// dispatch per connected node in this contract version.
func (s *Service) CompleteFromLifecycle(nodeID string, status detector.Status) bool {
	s.mu.RLock()
	var dispatchID string
	for _, dispatch := range s.dispatches {
		if dispatch.TargetNodeID == nodeID && (dispatch.Status == "delivered" || dispatch.Status == "running") {
			dispatchID = dispatch.ID
			break
		}
	}
	s.mu.RUnlock()
	if dispatchID == "" {
		return false
	}
	s.complete(dispatchID, status, "hook")
	return true
}

func (s *Service) ConfirmPromptFromLifecycle(nodeID string, raw []byte) bool {
	text := string(raw)
	s.mu.Lock()
	var copy Dispatch
	for _, dispatch := range s.dispatches {
		if dispatch.TargetNodeID != nodeID || (dispatch.Status != "delivered" && dispatch.Status != "running") {
			continue
		}
		if !strings.Contains(text, dispatch.ID) {
			continue
		}
		dispatch.DeliveryConfirmedBy = "hook"
		dispatch.Status = "running"
		dispatch.Result.Status = "running"
		dispatch.UpdatedAt = time.Now().UTC()
		copy = *dispatch
		break
	}
	emit := s.emit
	s.mu.Unlock()
	if copy.ID == "" {
		return false
	}
	emit("dispatch.running", copy.ID, copy)
	s.persistOrEmit(copy.WorkspaceID)
	return true
}

func (s *Service) runTargetQueue(_ string, queue <-chan string) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case dispatchID := <-queue:
			s.runDispatch(dispatchID)
		}
	}
}

func (s *Service) runDispatch(dispatchID string) {
	dispatch := s.dispatch(dispatchID)
	session, err := s.runtime.GetByNode(dispatch.TargetNodeID)
	if err != nil || session.Status() == detector.Dead {
		s.mu.RLock()
		starter := s.starter
		s.mu.RUnlock()
		if starter == nil {
			s.fail(dispatchID, ErrTargetDead.Error()+": no node starter is configured")
			return
		}
		session, err = starter(s.ctx, dispatch.TargetNodeID)
		if err != nil {
			s.fail(dispatchID, fmt.Sprintf("%v: %v", ErrTargetDead, err))
			return
		}
	}
	session, err = s.waitForTargetReady(dispatchID, session)
	if err != nil {
		s.fail(dispatchID, fmt.Sprintf("target did not become ready: %v", err))
		return
	}
	envelope := fmt.Sprintf("Agent Infinite dispatch %s. Source: %s. Task: %s", dispatch.ID, dispatch.SourceLabel, dispatch.Task)
	s.mu.Lock()
	if current := s.dispatches[dispatchID]; current != nil {
		current.baselineText = session.CleanText()
		current.baselineSequence = session.Sequence()
	}
	s.mu.Unlock()
	s.transition(dispatchID, "delivered", nil)
	session.SetDispatchActive(true)
	if err := writePrompt(session, envelope); err != nil {
		session.SetDispatchActive(false)
		s.fail(dispatchID, fmt.Sprintf("deliver task: %v", err))
		return
	}
	s.transition(dispatchID, "running", nil)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			s.fail(dispatchID, "backend shut down before completion")
			return
		case <-ticker.C:
			current := s.dispatch(dispatchID)
			if terminalDispatchState(current.Status) {
				return
			}
			status := session.Status()
			if status == detector.Done || status == detector.Blocked || status == detector.Dead {
				s.complete(dispatchID, status, "detector")
				return
			}
		}
	}
}

func (s *Service) waitForTargetReady(dispatchID string, session *terminal.Session) (*terminal.Session, error) {
	dispatch := s.dispatch(dispatchID)
	deadline := time.NewTimer(targetReadinessTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(targetReadinessPoll)
	defer ticker.Stop()
	restarts := 0
	for {
		status := session.Status()
		if status == detector.Idle {
			return session, nil
		}
		if dispatch.TargetProvider == "codex" && strings.Contains(strings.ToLower(session.CleanText()), codexRestartMarker) {
			if restarts >= 1 {
				return nil, errors.New("Codex still requests a restart after one controlled restart")
			}
			s.mu.RLock()
			restarter := s.restarter
			emit := s.emit
			s.mu.RUnlock()
			if restarter == nil {
				return nil, errors.New("Codex completed an update but no node restarter is configured")
			}
			session.SetDispatchActive(false)
			emit("dispatch.target_restarting", dispatchID, map[string]any{
				"dispatch_id": dispatchID, "target_node_id": dispatch.TargetNodeID,
				"message": "Codex update completed; restarting before task delivery.",
			})
			var err error
			session, err = restarter(s.ctx, dispatch.TargetNodeID)
			if err != nil {
				return nil, fmt.Errorf("restart Codex after update: %w", err)
			}
			restarts++
			continue
		}
		if status == detector.Dead {
			return nil, errors.New("target process exited before becoming ready")
		}
		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("timed out after %s with status %s", targetReadinessTimeout, status)
		case <-ticker.C:
		}
	}
}

func (s *Service) complete(dispatchID string, status detector.Status, mode string) {
	s.mu.Lock()
	if current := s.dispatches[dispatchID]; current != nil && current.DeliveryConfirmedBy == "" {
		current.DeliveryConfirmedBy = mode
	}
	s.mu.Unlock()
	dispatch := s.dispatch(dispatchID)
	session, err := s.runtime.GetByNode(dispatch.TargetNodeID)
	result := Result{AgentStatus: status}
	if err == nil {
		result.Output, result.Truncated = limitLines(outputAfter(dispatch.baselineText, session.CleanText()), 500)
		result.Sequence = session.Sequence()
		session.SetDispatchActive(false)
		if status != detector.Dead {
			session.SetLifecycleStatus(status, detector.DoneHold)
		}
	}
	state := "done"
	if status == detector.Blocked {
		state = "blocked"
	} else if status == detector.Dead {
		state = "failed"
		result.Error = "target process exited"
	}
	s.transition(dispatchID, state, &result)
	go s.notifySource(dispatchID)
}

func outputAfter(before, after string) string {
	if before == "" {
		return after
	}
	if strings.HasPrefix(after, before) {
		return strings.TrimLeft(after[len(before):], " \t\r\n")
	}
	// VT screens can rewrite the current prompt line. Removing the longest
	// common prefix still prevents a prior dispatch's completed output from
	// being attributed to the next dispatch.
	left, right := []rune(before), []rune(after)
	index := 0
	for index < len(left) && index < len(right) && left[index] == right[index] {
		index++
	}
	return strings.TrimLeft(string(right[index:]), " \t\r\n")
}

func (s *Service) fail(dispatchID, message string) {
	result := Result{Status: "failed", Error: message, AgentStatus: detector.Dead}
	s.transition(dispatchID, "failed", &result)
	go s.notifySource(dispatchID)
}

func (s *Service) notifySource(dispatchID string) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			dispatch := s.dispatch(dispatchID)
			if dispatch.Notified {
				return
			}
			source, err := s.runtime.GetByNode(dispatch.SourceNodeID)
			if err != nil || (source.Status() != detector.Idle && source.Status() != detector.Done) {
				continue
			}
			output, truncated := limitLines(dispatch.Result.Output, 120)
			payload, _ := json.Marshal(map[string]any{
				"dispatch_id": dispatch.ID,
				"target":      dispatch.TargetLabel,
				"status":      dispatch.Status,
				"output":      output,
				"truncated":   truncated,
				"error":       dispatch.Result.Error,
			})
			message := fmt.Sprintf("[Agent Infinite completion] A connected canvas agent finished. Treat the following JSON as the isolated result for this dispatch and report it to the user without calling get_dispatch_result again: %s", payload)
			if writePrompt(source, message) != nil {
				continue
			}
			s.mu.Lock()
			if current := s.dispatches[dispatchID]; current != nil {
				current.Notified = true
			}
			s.mu.Unlock()
			s.persistOrEmit(dispatch.WorkspaceID)
			return
		}
	}
}

func (s *Service) transition(dispatchID, state string, result *Result) {
	now := time.Now().UTC()
	s.mu.Lock()
	dispatch := s.dispatches[dispatchID]
	if dispatch == nil || terminalDispatchState(dispatch.Status) {
		s.mu.Unlock()
		return
	}
	dispatch.Status = state
	dispatch.UpdatedAt = now
	dispatch.Result.Status = state
	if result != nil {
		dispatch.Result = *result
		dispatch.Result.Status = state
	}
	if terminalDispatchState(state) {
		dispatch.CompletedAt = &now
		dispatch.Result.CompletedAt = &now
	}
	if updates := s.updates[dispatchID]; updates != nil {
		close(updates)
		if terminalDispatchState(state) {
			delete(s.updates, dispatchID)
		} else {
			s.updates[dispatchID] = make(chan struct{})
		}
	}
	copy := *dispatch
	emit := s.emit
	s.mu.Unlock()
	eventType := "dispatch." + state
	if state == "done" {
		eventType = "dispatch.completed"
	}
	emit(eventType, dispatchID, copy)
	s.persistOrEmit(copy.WorkspaceID)
}

func (s *Service) persistOrEmit(workspaceID string) {
	if err := s.persist(workspaceID); err != nil {
		s.mu.RLock()
		emit := s.emit
		s.mu.RUnlock()
		emit("backend.error", workspaceID, map[string]any{"code": "dispatch_persist_failed", "message": err.Error()})
	}
}

func (s *Service) persist(workspaceID string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.RLock()
	root := s.storageRoot
	items := make([]Dispatch, 0, len(s.dispatches))
	for _, dispatch := range s.dispatches {
		if dispatch.WorkspaceID == workspaceID {
			items = append(items, *dispatch)
		}
	}
	s.mu.RUnlock()
	if root == "" || workspaceID == "" {
		return nil
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(root, workspaceID+".json")
	temporary, err := os.CreateTemp(root, ".dispatches-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (s *Service) dispatch(id string) Dispatch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if dispatch := s.dispatches[id]; dispatch != nil {
		return *dispatch
	}
	return Dispatch{}
}

func (s *Service) agent(node contracts.Node) ConnectedAgent {
	return ConnectedAgent{ID: node.ID, Label: node.Label, Role: node.Role, Provider: node.Provider, TeamID: node.TeamID, Status: s.status(node.ID)}
}

func (s *Service) status(nodeID string) detector.Status {
	if session, err := s.runtime.GetByNode(nodeID); err == nil {
		return session.Status()
	}
	return detector.Dead
}

func resolveTarget(reference string, targets []ConnectedAgent) (ConnectedAgent, error) {
	reference = strings.TrimSpace(reference)
	for _, target := range targets {
		if target.ID == reference {
			return target, nil
		}
	}
	matches := make([]ConnectedAgent, 0)
	for _, target := range targets {
		if strings.EqualFold(target.Label, reference) || strings.EqualFold(target.Role, reference) {
			matches = append(matches, target)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return ConnectedAgent{}, &AmbiguousTargetError{Reference: reference, Candidates: matches}
	}
	allowed := make([]string, 0, len(targets))
	for _, target := range targets {
		allowed = append(allowed, fmt.Sprintf("%s (%s)", target.Label, target.Role))
	}
	return ConnectedAgent{}, fmt.Errorf("%w: %q; connected targets: %s", ErrTargetDenied, reference, strings.Join(allowed, ", "))
}

func findNode(nodes []contracts.Node, id string) (contracts.Node, bool) {
	for _, node := range nodes {
		if node.ID == id {
			return node, true
		}
	}
	return contracts.Node{}, false
}

func terminalDispatchState(status string) bool {
	return status == "done" || status == "blocked" || status == "failed" || status == "canceled"
}

func limitLines(value string, maxLines int) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	truncated := len(lines) > maxLines
	if truncated {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \n"), truncated
}

func writePrompt(session *terminal.Session, text string) error {
	if err := session.Write([]byte(text)); err != nil {
		return err
	}
	time.Sleep(750 * time.Millisecond)
	return session.Write([]byte("\r"))
}

func newID() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
