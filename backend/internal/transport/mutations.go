package transport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/agent"
	"github.com/agent-infinite/agent-infinite/backend/internal/capabilities"
	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/detector"
	"github.com/agent-infinite/agent-infinite/backend/internal/hookbridge"
	"github.com/agent-infinite/agent-infinite/backend/internal/models"
	"github.com/agent-infinite/agent-infinite/backend/internal/terminal"
	"github.com/agent-infinite/agent-infinite/backend/internal/worktree"
)

var hookActivationTimeout = 10 * time.Second

func (h *HTTP) startNode(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	var node *contracts.Node
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].ID == r.PathValue("id") {
			node = &snapshot.Nodes[index]
			break
		}
	}
	if node == nil {
		writeError(w, http.StatusNotFound, "node_not_found", "The node does not exist.", nil)
		return
	}
	session, err := h.launchNode(snapshot, *node)
	if err != nil {
		code := "provider_start_failed"
		if errors.Is(err, agent.ErrExecutableMissing) {
			code = "provider_missing"
		} else if errors.Is(err, agent.ErrProviderIncompatible) {
			code = "provider_incompatible"
		} else if errors.Is(err, models.ErrUnavailable) {
			code = "model_unavailable"
		}
		writeError(w, http.StatusConflict, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessionId": session.ID(), "status": session.Status(),
		"integrationMode": session.IntegrationMode(), "hookSessionId": session.HookSessionID(), "mcpConnected": session.MCPConnected(),
	})
}

func (h *HTTP) launchNode(snapshot contracts.Snapshot, node contracts.Node) (*terminal.Session, error) {
	worktreeID := node.WorktreeID
	if worktreeID == "" {
		var ok bool
		worktreeID, ok = h.executionWorktree(node.TeamID)
		if !ok {
			return nil, errors.New("team_worktree_required")
		}
	}
	return h.launchNodeInWorktree(snapshot, node, worktreeID)
}

func (h *HTTP) launchNodeInWorktree(snapshot contracts.Snapshot, node contracts.Node, worktreeID string) (*terminal.Session, error) {
	if existing, existingErr := h.terminals.GetByNode(node.ID); existingErr == nil {
		if existing.Status() != detector.Dead {
			return existing, nil
		}
		_ = h.terminals.StopNode(node.ID)
	}
	workDir := h.worktrees.Path(snapshot.WorkspaceID, worktreeID)
	runtimeDir := filepath.Join(h.runtimeRoot, snapshot.WorkspaceID, node.ID)
	h.models.RefreshIfStale(context.Background(), snapshot.WorkspacePath, node.Provider)
	modelResolution, err := h.models.Resolve(node.Provider, node.Model)
	if err != nil {
		return nil, err
	}
	if modelResolution.Warning != "" {
		h.events.Emit("model.catalog_stale", node.ID, map[string]any{"provider": node.Provider, "message": modelResolution.Warning})
	}
	h.capabilities.Scan(snapshot.WorkspacePath)
	resolved := h.capabilities.Resolve(node.Provider, node.MCPIDs, node.SkillIDs)
	for _, item := range resolved.Blocked {
		if !item.Enforceable {
			return nil, fmt.Errorf("enforcement_unsupported for %s %q: %w", item.Kind, item.Name, agent.ErrProviderIncompatible)
		}
	}
	toLaunchCapabilities := func(items []capabilities.Item) []agent.Capability {
		result := make([]agent.Capability, 0, len(items))
		for _, item := range items {
			result = append(result, agent.Capability{Name: item.Name, Path: item.SkillPath, Spec: item.Spec})
		}
		return result
	}
	connections := sessionConnections(snapshot, node.ID)
	policy := snapshot.Integration.Hooks
	if policy == "" {
		policy = "auto"
	}
	var hookSession hookbridge.Session
	hookLaunch := agent.HookLaunch{Policy: policy}
	if policy != "off" {
		backendExecutable := os.Getenv("AGENT_INFINITE_BACKEND_EXECUTABLE")
		var executableErr error
		if backendExecutable == "" {
			backendExecutable, executableErr = os.Executable()
		}
		if executableErr != nil {
			if policy == "required" {
				return nil, fmt.Errorf("hooks are required but the backend executable could not be resolved: %w", executableErr)
			}
		} else {
			hookSession = h.hooks.Register(node.ID, snapshot.WorkspaceID, node.Provider, "hooks")
			hookLaunch = agent.HookLaunch{
				Enabled: true, Policy: policy, SessionID: hookSession.ID, Token: h.hooks.Token(hookSession.ID),
				WorkspaceID: snapshot.WorkspaceID, BackendExecutable: backendExecutable,
				Cleanup: func() { h.hooks.Close(hookSession.ID) },
			}
		}
	}
	spec, err := agent.BuildLaunch(agent.LaunchOptions{
		Provider: node.Provider, Model: modelResolution.Model, WorkDir: workDir, RuntimeDir: runtimeDir, NodeID: node.ID,
		NodeLabel: node.Label, NodeRole: node.Role, NodeKind: node.Kind, TeamID: node.TeamID, Connections: connections,
		MCPBaseURL: h.baseURL, MCPToken: h.token, Hooks: hookLaunch,
		MCPs: toLaunchCapabilities(resolved.MCPs), Skills: toLaunchCapabilities(resolved.Skills),
		BlockedMCPs:   toLaunchCapabilities(filterCapabilities(resolved.Blocked, capabilities.KindMCP)),
		BlockedSkills: toLaunchCapabilities(filterCapabilities(resolved.Blocked, capabilities.KindSkill)),
	})
	if err != nil {
		if hookLaunch.Cleanup != nil {
			hookLaunch.Cleanup()
		}
		return nil, err
	}
	session, err := h.terminals.StartNode(node.ID, spec)
	if err != nil {
		spec.Cleanup()
		return nil, err
	}
	if hookSession.ID != "" {
		go h.watchHookActivation(node.ID, hookSession.ID, policy, hookActivationTimeoutFor(node.Provider))
	}
	return session, nil
}

func sessionConnections(snapshot contracts.Snapshot, nodeID string) []agent.SessionConnection {
	result := make([]agent.SessionConnection, 0)
	appendNode := func(connectedID, direction string) {
		for _, connected := range snapshot.Nodes {
			if connected.ID != connectedID {
				continue
			}
			result = append(result, agent.SessionConnection{
				ID: connected.ID, Label: connected.Label, Role: connected.Role,
				Kind: connected.Kind, Provider: connected.Provider, Direction: direction,
			})
			return
		}
	}
	for _, edge := range snapshot.Edges {
		if edge.Type != "delegates_to" {
			continue
		}
		if edge.Source == nodeID {
			appendNode(edge.Target, "delegates_to")
		} else if edge.Target == nodeID {
			appendNode(edge.Source, "delegated_by")
		}
	}
	return result
}

func (h *HTTP) executionWorktree(teamID string) (string, bool) {
	h.executionMu.Lock()
	defer h.executionMu.Unlock()
	execution, ok := h.executions[teamID]
	return execution.WorktreeID, ok
}

func (h *HTTP) runTeam(w http.ResponseWriter, r *http.Request) {
	var request struct {
		WorktreeID string `json:"worktreeId"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	request.WorktreeID = strings.TrimSpace(request.WorktreeID)
	if request.WorktreeID == "" {
		writeError(w, http.StatusUnprocessableEntity, "team_worktree_required", "Select an existing Git worktree before executing a Team.", nil)
		return
	}
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	teamID := r.PathValue("id")
	worktreeFound := false
	for _, worktree := range snapshot.Worktrees {
		if worktree.ID == request.WorktreeID {
			worktreeFound = true
			break
		}
	}
	if !worktreeFound {
		writeError(w, http.StatusUnprocessableEntity, "worktree_not_found", "The selected Git worktree does not exist.", nil)
		return
	}
	definition := make([]contracts.Node, 0)
	for _, node := range snapshot.Nodes {
		if node.TeamID == teamID && node.WorktreeID == "" && (node.Kind == "orchestrator" || node.AutoStart) {
			definition = append(definition, node)
		}
	}
	if len(definition) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "team_definition_missing", "This Team has no independent workflow to execute.", nil)
		return
	}
	h.executionMu.Lock()
	if active, exists := h.executions[teamID]; exists && active.WorktreeID != request.WorktreeID {
		h.executionMu.Unlock()
		writeError(w, http.StatusConflict, "team_execution_active", "Stop the active Team execution before selecting another worktree.", nil)
		return
	}
	h.executions[teamID] = contracts.TeamExecution{TeamID: teamID, WorktreeID: request.WorktreeID, StartedAt: time.Now().UTC()}
	h.executionMu.Unlock()
	started := make([]string, 0, len(definition))
	for _, node := range definition {
		if _, launchErr := h.launchNodeInWorktree(snapshot, node, request.WorktreeID); launchErr != nil {
			for _, startedID := range started {
				_ = h.terminals.StopNode(startedID)
			}
			h.executionMu.Lock()
			delete(h.executions, teamID)
			h.executionMu.Unlock()
			writeError(w, http.StatusConflict, "team_start_failed", launchErr.Error(), nil)
			return
		}
		started = append(started, node.ID)
	}
	h.executionMu.Lock()
	execution := h.executions[teamID]
	execution.StartedNode = started
	h.executions[teamID] = execution
	h.executionMu.Unlock()
	writeJSON(w, http.StatusOK, execution)
}

func hookActivationTimeoutFor(provider string) time.Duration {
	if provider == "opencode" {
		return 30 * time.Second
	}
	return hookActivationTimeout
}

func (h *HTTP) watchHookActivation(nodeID, hookSessionID, policy string, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	<-timer.C
	session, exists := h.hooks.Session(hookSessionID)
	if !exists || session.State != "pending" {
		return
	}
	if policy == "required" {
		_ = h.terminals.StopNode(nodeID)
		h.hooks.Close(hookSessionID)
		h.events.Emit("integration.required_failed", nodeID, map[string]any{
			"hookSessionId": hookSessionID, "message": fmt.Sprintf("Required provider hooks did not activate within %s.", timeout),
		})
		return
	}
	h.hooks.MarkDegraded(hookSessionID)
	h.events.Emit("integration.degraded", nodeID, map[string]any{
		"hookSessionId": hookSessionID, "mode": "detector", "message": "Provider hooks did not activate; terminal detector fallback is active.",
	})
}

// StartNodeByID is used by the orchestration queue when an authorized existing
// target is offline. It never creates or connects a node implicitly.
func (h *HTTP) StartNodeByID(_ context.Context, nodeID string) (*terminal.Session, error) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		return nil, err
	}
	for _, node := range snapshot.Nodes {
		if node.ID == nodeID {
			return h.launchNode(snapshot, node)
		}
	}
	return nil, fmt.Errorf("node %q does not exist", nodeID)
}

// RestartNodeByID replaces one existing provider session without changing the
// canvas node or its project-local integration configuration. The orchestration
// service uses it only after a provider explicitly reports that a restart is
// required before task delivery.
func (h *HTTP) RestartNodeByID(_ context.Context, nodeID string) (*terminal.Session, error) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		return nil, err
	}
	var target *contracts.Node
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].ID == nodeID {
			target = &snapshot.Nodes[index]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("node %q does not exist", nodeID)
	}
	if _, err := h.terminals.GetByNode(nodeID); err == nil {
		if err := h.terminals.StopNode(nodeID); err != nil {
			return nil, fmt.Errorf("stop node before restart: %w", err)
		}
	}
	return h.launchNode(snapshot, *target)
}

func (h *HTTP) stopNode(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	err := h.terminals.StopNode(nodeID)
	if errors.Is(err, terminal.ErrSessionNotFound) {
		writeError(w, http.StatusNotFound, "session_not_found", "The node has no running session.", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "terminal_stop_failed", err.Error(), nil)
		return
	}
	h.pruneTeamExecutions()
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) pruneTeamExecutions() {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		return
	}
	h.executionMu.Lock()
	defer h.executionMu.Unlock()
	for teamID := range h.executions {
		active := false
		for _, node := range snapshot.Nodes {
			if node.TeamID == teamID && node.WorktreeID == "" {
				if _, err := h.terminals.GetByNode(node.ID); err == nil {
					active = true
					break
				}
			}
		}
		if !active {
			delete(h.executions, teamID)
		}
	}
}

func (h *HTTP) createTeam(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name                 string `json:"name"`
		Color                string `json:"color"`
		OrchestratorProvider string `json:"orchestratorProvider"`
		OrchestratorModel    string `json:"orchestratorModel"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	request.Name, request.OrchestratorModel = strings.TrimSpace(request.Name), strings.TrimSpace(request.OrchestratorModel)
	if request.Name == "" || len(request.Name) > 80 || request.Color == "" || !validProvider(request.OrchestratorProvider) {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Team name, color, and provider are required.", nil)
		return
	}
	if err := h.models.Validate(request.OrchestratorProvider, request.OrchestratorModel); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_model", err.Error(), nil)
		return
	}
	teamID, nodeID := newID(), newID()
	team := contracts.Team{ID: teamID, Name: request.Name, Color: request.Color, CreatedAt: time.Now().UTC()}
	node := contracts.Node{ID: nodeID, Kind: "orchestrator", Provider: request.OrchestratorProvider, Model: request.OrchestratorModel, TeamID: teamID, Label: request.Name + " Lead", Role: "Coordinates team delegation", Position: contracts.Point{X: 120, Y: 120}, Size: contracts.Size{Width: 320, Height: 220}}
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		next.Teams = append(next.Teams, team)
		next.Nodes = append(next.Nodes, node)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "canvas_save_failed", "The team could not be persisted.", nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"team": team, "orchestrator": node})
}

func (h *HTTP) createCustomRole(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || len(name) > 80 {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Role name must contain 1 to 80 characters.", nil)
		return
	}
	updated, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		for _, role := range next.CustomRoles {
			if strings.EqualFold(role, name) {
				return fmt.Errorf("custom role already exists")
			}
		}
		next.CustomRoles = append(next.CustomRoles, name)
		return nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeError(w, http.StatusConflict, "role_exists", "This custom role already exists.", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "role_save_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"roles": updated.CustomRoles})
}

func (h *HTTP) deleteCustomRole(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusNotFound, "role_not_found", "The custom role does not exist.", nil)
		return
	}
	found := false
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		roles := make([]string, 0, len(next.CustomRoles))
		for _, role := range next.CustomRoles {
			if role == name {
				found = true
				continue
			}
			roles = append(roles, role)
		}
		next.CustomRoles = roles
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "role_delete_failed", err.Error(), nil)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "role_not_found", "The custom role does not exist.", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) listTeamTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"templates": h.templates.List()})
}

// extractTeamWorkflow preserves legacy worktree canvases while creating an independent Team
// definition from the selected operational canvas. It never alters the source nodes.
func (h *HTTP) extractTeamWorkflow(w http.ResponseWriter, r *http.Request) {
	var request struct {
		WorktreeID string `json:"worktreeId"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	teamID := r.PathValue("id")
	var hasWorktree bool
	for _, candidate := range snapshot.Worktrees {
		if candidate.ID == request.WorktreeID && candidate.TeamID == teamID {
			hasWorktree = true
			break
		}
	}
	if !hasWorktree {
		writeError(w, http.StatusUnprocessableEntity, "team_worktree_mismatch", "The selected Git worktree does not belong to this Team.", nil)
		return
	}
	for _, node := range snapshot.Nodes {
		if node.TeamID == teamID && node.WorktreeID == "" {
			writeError(w, http.StatusConflict, "team_definition_exists", "This Team already has an independent workflow.", nil)
			return
		}
	}
	ids := map[string]string{}
	nodes := []contracts.Node{}
	for _, source := range snapshot.Nodes {
		if source.TeamID != teamID || source.WorktreeID != request.WorktreeID {
			continue
		}
		id := newID()
		ids[source.ID] = id
		source.ID, source.WorktreeID = id, ""
		nodes = append(nodes, source)
	}
	if len(nodes) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "workflow_missing", "The selected worktree has no nodes to extract.", nil)
		return
	}
	edges := []contracts.Edge{}
	for _, source := range snapshot.Edges {
		if ids[source.Source] == "" || ids[source.Target] == "" {
			continue
		}
		source.ID, source.Source, source.Target = newID(), ids[source.Source], ids[source.Target]
		edges = append(edges, source)
	}
	if _, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		next.Nodes = append(next.Nodes, nodes...)
		next.Edges = append(next.Edges, edges...)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "workflow_extract_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"teamId": teamID, "nodes": len(nodes), "edges": len(edges)})
}

func (h *HTTP) saveTeamTemplate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		TeamID               string `json:"teamId"`
		Name                 string `json:"name"`
		Description          string `json:"description"`
		Color                string `json:"color"`
		OrchestratorProvider string `json:"orchestratorProvider"`
		OrchestratorModel    string `json:"orchestratorModel"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	request.TeamID, request.Name = strings.TrimSpace(request.TeamID), strings.TrimSpace(request.Name)
	request.OrchestratorModel = strings.TrimSpace(request.OrchestratorModel)
	if request.TeamID == "" {
		if request.Name == "" || len(request.Name) > 80 || request.Color == "" || !validProvider(request.OrchestratorProvider) {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Template name, color, and orchestrator provider are required.", nil)
			return
		}
		if err := h.models.Validate(request.OrchestratorProvider, request.OrchestratorModel); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_model", err.Error(), nil)
			return
		}
		node := contracts.Node{
			ID: newID(), Kind: "orchestrator", Provider: request.OrchestratorProvider, Model: request.OrchestratorModel,
			Label: request.Name + " Lead", Role: "Coordinates team delegation",
			Position: contracts.Point{X: 120, Y: 120}, Size: contracts.Size{Width: 320, Height: 220},
		}
		template, saveErr := h.templates.Save(contracts.TeamTemplate{
			Name: request.Name, Description: strings.TrimSpace(request.Description), Color: request.Color,
			OrchestratorProvider: request.OrchestratorProvider, OrchestratorModel: request.OrchestratorModel, Nodes: []contracts.Node{node}, Edges: []contracts.Edge{},
		})
		if saveErr != nil {
			writeError(w, http.StatusInternalServerError, "template_save_failed", saveErr.Error(), nil)
			return
		}
		writeJSON(w, http.StatusCreated, template)
		return
	}
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	var team *contracts.Team
	for index := range snapshot.Teams {
		if snapshot.Teams[index].ID == request.TeamID {
			team = &snapshot.Teams[index]
			break
		}
	}
	if team == nil {
		writeError(w, http.StatusNotFound, "team_not_found", "The team does not exist.", nil)
		return
	}
	nodes := []contracts.Node{}
	nodeIDs := map[string]bool{}
	provider := ""
	orchestratorModel := ""
	for _, node := range snapshot.Nodes {
		if node.TeamID != team.ID || node.WorktreeID != "" {
			continue
		}
		nodes = append(nodes, node)
		nodeIDs[node.ID] = true
		if node.Kind == "orchestrator" {
			provider = node.Provider
			orchestratorModel = node.Model
		}
	}
	if len(nodes) == 0 || provider == "" {
		writeError(w, http.StatusUnprocessableEntity, "team_definition_missing", "The Team has no independent workflow to save.", nil)
		return
	}
	edges := []contracts.Edge{}
	for _, edge := range snapshot.Edges {
		if nodeIDs[edge.Source] && nodeIDs[edge.Target] {
			edges = append(edges, edge)
		}
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = team.Name
	}
	template, saveErr := h.templates.Save(contracts.TeamTemplate{Name: name, Description: strings.TrimSpace(request.Description), Color: team.Color, OrchestratorProvider: provider, OrchestratorModel: orchestratorModel, Nodes: nodes, Edges: edges})
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "template_save_failed", saveErr.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, template)
}

func (h *HTTP) updateTeamTemplate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name                 string           `json:"name"`
		Description          string           `json:"description"`
		Color                string           `json:"color"`
		OrchestratorProvider string           `json:"orchestratorProvider"`
		OrchestratorModel    string           `json:"orchestratorModel"`
		Nodes                []contracts.Node `json:"nodes"`
		Edges                []contracts.Edge `json:"edges"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 80 || request.Color == "" || !validProvider(request.OrchestratorProvider) || request.Nodes == nil || request.Edges == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Template metadata, nodes, and edges are invalid.", nil)
		return
	}
	nodeIDs := make(map[string]contracts.Node, len(request.Nodes))
	orchestrators := 0
	orchestratorModel := ""
	for index := range request.Nodes {
		node := &request.Nodes[index]
		if node.ID == "" || (node.Kind != "agent" && node.Kind != "orchestrator") || !validProvider(node.Provider) {
			writeError(w, http.StatusUnprocessableEntity, "template_node_invalid", "The template contains an invalid node.", nil)
			return
		}
		node.Model = strings.TrimSpace(node.Model)
		if err := h.models.Validate(node.Provider, node.Model); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "model_invalid", err.Error(), nil)
			return
		}
		if _, duplicate := nodeIDs[node.ID]; duplicate {
			writeError(w, http.StatusUnprocessableEntity, "template_node_duplicate", "The template contains duplicate node IDs.", nil)
			return
		}
		node.TeamID, node.WorktreeID = "", ""
		if node.Kind == "orchestrator" {
			orchestrators++
			orchestratorModel = node.Model
		}
		nodeIDs[node.ID] = *node
	}
	if orchestrators != 1 {
		writeError(w, http.StatusUnprocessableEntity, "template_orchestrator_invalid", "A template must contain exactly one orchestrator.", nil)
		return
	}
	edgeIDs := make(map[string]bool, len(request.Edges))
	for _, edge := range request.Edges {
		source, sourceExists := nodeIDs[edge.Source]
		_, targetExists := nodeIDs[edge.Target]
		if edge.ID == "" || edgeIDs[edge.ID] || !sourceExists || !targetExists || source.Kind != "orchestrator" || edge.Source == edge.Target || edge.Type != "delegates_to" {
			writeError(w, http.StatusUnprocessableEntity, "template_edge_invalid", "The template contains an invalid connection.", nil)
			return
		}
		edgeIDs[edge.ID] = true
	}
	updated, err := h.templates.Update(r.PathValue("id"), contracts.TeamTemplate{
		Name: request.Name, Description: strings.TrimSpace(request.Description), Color: request.Color,
		OrchestratorProvider: request.OrchestratorProvider, OrchestratorModel: orchestratorModel, Nodes: request.Nodes, Edges: request.Edges,
	})
	if err != nil {
		if err.Error() == "template not found" {
			writeError(w, http.StatusNotFound, "template_not_found", "The template does not exist.", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "template_save_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *HTTP) deleteTeamTemplate(w http.ResponseWriter, r *http.Request) {
	if err := h.templates.Delete(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "template_not_found", "The template does not exist.", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) applyTeamTemplate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	template, found := h.templates.Find(r.PathValue("id"))
	if !found {
		writeError(w, http.StatusNotFound, "template_not_found", "The template does not exist.", nil)
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = template.Name
	}
	teamID := newID()
	ids := map[string]string{}
	nodes := make([]contracts.Node, 0, len(template.Nodes))
	for _, source := range template.Nodes {
		id := newID()
		ids[source.ID] = id
		source.ID, source.TeamID, source.WorktreeID = id, teamID, ""
		nodes = append(nodes, source)
	}
	edges := make([]contracts.Edge, 0, len(template.Edges))
	for _, source := range template.Edges {
		source.ID, source.Source, source.Target = newID(), ids[source.Source], ids[source.Target]
		edges = append(edges, source)
	}
	team := contracts.Team{ID: teamID, Name: name, Color: template.Color, CreatedAt: time.Now().UTC()}
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		next.Teams = append(next.Teams, team)
		next.Nodes = append(next.Nodes, nodes...)
		next.Edges = append(next.Edges, edges...)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "template_apply_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"team": team})
}

func (h *HTTP) deleteTeam(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	id := r.PathValue("id")
	var team *contracts.Team
	for index := range snapshot.Teams {
		if snapshot.Teams[index].ID == id {
			team = &snapshot.Teams[index]
			break
		}
	}
	if team == nil {
		writeError(w, http.StatusNotFound, "team_not_found", "The team does not exist.", nil)
		return
	}
	for _, node := range snapshot.Nodes {
		if node.TeamID != id || node.WorktreeID != "" {
			continue
		}
		if err := h.terminals.StopNode(node.ID); err != nil && !errors.Is(err, terminal.ErrSessionNotFound) {
			writeError(w, http.StatusConflict, "team_stop_failed", "The Team could not be stopped before deletion.", map[string]any{"cause": err.Error()})
			return
		}
	}
	_, err = h.workspace.Update(func(next *contracts.Snapshot) error {
		removedNodes := make(map[string]bool)
		next.Teams = filterTeams(next.Teams, id)
		linkedWorktrees := make(map[string]bool)
		for index := range next.Worktrees {
			if next.Worktrees[index].TeamID == id {
				linkedWorktrees[next.Worktrees[index].ID] = true
				next.Worktrees[index].TeamID = ""
			}
		}
		remainingNodes := make([]contracts.Node, 0, len(next.Nodes))
		for _, node := range next.Nodes {
			if node.TeamID != id {
				remainingNodes = append(remainingNodes, node)
				continue
			}
			if node.WorktreeID != "" && linkedWorktrees[node.WorktreeID] {
				node.TeamID = ""
				remainingNodes = append(remainingNodes, node)
				continue
			}
			removedNodes[node.ID] = true
		}
		next.Nodes = remainingNodes
		next.Edges = filterEdges(next.Edges, func(edge contracts.Edge) bool { return !removedNodes[edge.Source] && !removedNodes[edge.Target] })
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "canvas_save_failed", "The team deletion could not be persisted.", nil)
		return
	}
	h.executionMu.Lock()
	delete(h.executions, id)
	h.executionMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) createWorktree(w http.ResponseWriter, r *http.Request) {
	var request struct {
		TeamID         string `json:"teamId"`
		Name           string `json:"name"`
		BaseRef        string `json:"baseRef"`
		NewBranch      string `json:"newBranch"`
		ExistingBranch string `json:"existingBranch"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	request.TeamID, request.Name = strings.TrimSpace(request.TeamID), strings.TrimSpace(request.Name)
	request.BaseRef = strings.TrimSpace(request.BaseRef)
	request.NewBranch = strings.TrimSpace(request.NewBranch)
	request.ExistingBranch = strings.TrimSpace(request.ExistingBranch)
	if request.Name == "" || len(request.Name) > 80 {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Worktree name is required.", nil)
		return
	}
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	if request.TeamID != "" {
		teamExists := false
		for _, team := range snapshot.Teams {
			if team.ID == request.TeamID {
				teamExists = true
				break
			}
		}
		if !teamExists {
			writeError(w, http.StatusNotFound, "team_not_found", "The selected team does not exist.", nil)
			return
		}
	}
	worktreeID := newID()
	branch, baseRef, _, err := h.worktrees.CreateFrom(r.Context(), snapshot, worktreeID, request.Name, request.BaseRef, request.NewBranch, request.ExistingBranch)
	if err != nil {
		if errors.Is(err, worktree.ErrInvalidBranch) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_branch", "Enter a valid Git branch name.", map[string]any{"cause": err.Error()})
			return
		}
		writeError(w, http.StatusConflict, "worktree_create_failed", "The worktree could not be created.", map[string]any{"cause": err.Error()})
		return
	}
	item := contracts.Worktree{ID: worktreeID, TeamID: request.TeamID, Name: request.Name, Branch: branch, BaseRef: baseRef, CreatedAt: time.Now().UTC()}
	_, err = h.workspace.Update(func(next *contracts.Snapshot) error {
		next.Worktrees = append(next.Worktrees, item)
		for index := range next.Teams {
			if next.Teams[index].ID == request.TeamID && next.Teams[index].Branch == "" {
				next.Teams[index].Branch, next.Teams[index].BaseRef = branch, baseRef
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "canvas_save_failed", "The worktree could not be persisted.", nil)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *HTTP) gitBranches(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	branches, err := h.worktrees.Branches(r.Context(), snapshot.WorkspacePath)
	if err != nil {
		writeError(w, http.StatusConflict, "git_branches_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, branches)
}

func (h *HTTP) deleteWorktree(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	id := r.PathValue("id")
	var item *contracts.Worktree
	for index := range snapshot.Worktrees {
		if snapshot.Worktrees[index].ID == id {
			item = &snapshot.Worktrees[index]
			break
		}
	}
	if item == nil {
		writeError(w, http.StatusNotFound, "worktree_not_found", "The worktree does not exist.", nil)
		return
	}
	// Preflight first: a dirty checkout must not interrupt the active Team.
	if err := h.worktrees.CheckDelete(r.Context(), snapshot, *item); err != nil {
		if errors.Is(err, worktree.ErrDirty) {
			writeError(w, http.StatusConflict, "worktree_dirty", "The worktree has uncommitted changes.", nil)
		} else {
			writeError(w, http.StatusConflict, "worktree_remove_failed", "The worktree could not be removed.", map[string]any{"cause": err.Error()})
		}
		return
	}
	if err := h.stopWorktreeTerminals(snapshot, id); err != nil {
		writeError(w, http.StatusConflict, "worktree_stop_failed", "The worktree agents could not be stopped before deletion.", map[string]any{"cause": err.Error()})
		return
	}
	if err := h.worktrees.Delete(r.Context(), snapshot, *item); err != nil {
		if errors.Is(err, worktree.ErrDirty) {
			writeError(w, http.StatusConflict, "worktree_dirty", "The worktree has uncommitted changes.", nil)
		} else {
			writeError(w, http.StatusConflict, "worktree_remove_failed", "The worktree could not be removed.", map[string]any{"cause": err.Error()})
		}
		return
	}
	_, err = h.workspace.Update(func(next *contracts.Snapshot) error {
		removedNodes := make(map[string]bool)
		for _, node := range next.Nodes {
			if node.WorktreeID == id {
				removedNodes[node.ID] = true
			}
		}
		next.Nodes = filterNodes(next.Nodes, func(node contracts.Node) bool { return !removedNodes[node.ID] })
		next.Edges = filterEdges(next.Edges, func(edge contracts.Edge) bool {
			return !removedNodes[edge.Source] && !removedNodes[edge.Target]
		})
		next.Worktrees = filterWorktrees(next.Worktrees, func(candidate contracts.Worktree) bool { return candidate.ID != id })
		for index := range next.Teams {
			if next.Teams[index].ID == item.TeamID && next.Teams[index].Branch == item.Branch {
				next.Teams[index].Branch, next.Teams[index].BaseRef = "", ""
				for _, remaining := range next.Worktrees {
					if remaining.ID != id && remaining.TeamID == item.TeamID {
						next.Teams[index].Branch, next.Teams[index].BaseRef = remaining.Branch, remaining.BaseRef
						break
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		if restoreErr := h.worktrees.Restore(r.Context(), snapshot, *item); restoreErr != nil {
			writeError(w, http.StatusInternalServerError, "canvas_save_failed", "The worktree deletion could not be persisted and its checkout could not be restored.", map[string]any{"cause": err.Error(), "restoreCause": restoreErr.Error()})
			return
		}
		writeError(w, http.StatusInternalServerError, "canvas_save_failed", "The worktree deletion could not be persisted; its checkout was restored.", map[string]any{"cause": err.Error()})
		return
	}
	h.removeWorktreeExecutions(id)
	w.WriteHeader(http.StatusNoContent)
}

// stopWorktreeTerminals stops both nodes persisted inside the worktree and
// definition nodes currently executing against it. The latter keep an empty
// WorktreeID by design, so the transient execution table is the authoritative
// source while a Team is running.
func (h *HTTP) stopWorktreeTerminals(snapshot contracts.Snapshot, worktreeID string) error {
	nodeIDs := make(map[string]bool)
	for _, node := range snapshot.Nodes {
		if node.WorktreeID == worktreeID {
			nodeIDs[node.ID] = true
		}
	}
	h.executionMu.Lock()
	for teamID, execution := range h.executions {
		if execution.WorktreeID != worktreeID {
			continue
		}
		for _, nodeID := range execution.StartedNode {
			nodeIDs[nodeID] = true
		}
		// runTeam writes the execution record before its launch loop. Include
		// the complete launch set as well, closing the small race where deletion
		// arrives after a terminal starts but before StartedNode is recorded.
		for _, node := range snapshot.Nodes {
			if node.TeamID == teamID && node.WorktreeID == "" && (node.Kind == "orchestrator" || node.AutoStart) {
				nodeIDs[node.ID] = true
			}
		}
	}
	h.executionMu.Unlock()
	for nodeID := range nodeIDs {
		if err := h.terminals.StopNode(nodeID); err != nil && !errors.Is(err, terminal.ErrSessionNotFound) {
			return fmt.Errorf("stop node %q: %w", nodeID, err)
		}
	}
	return nil
}

func (h *HTTP) removeWorktreeExecutions(worktreeID string) {
	h.executionMu.Lock()
	defer h.executionMu.Unlock()
	for teamID, execution := range h.executions {
		if execution.WorktreeID == worktreeID {
			delete(h.executions, teamID)
		}
	}
}

func (h *HTTP) createNode(w http.ResponseWriter, r *http.Request) {
	var request struct {
		TeamID        string   `json:"teamId"`
		WorktreeID    string   `json:"worktreeId"`
		Provider      string   `json:"provider"`
		Label         string   `json:"label"`
		Role          string   `json:"role"`
		Model         string   `json:"model"`
		RoleProfileID string   `json:"roleProfileId"`
		MCPIDs        []string `json:"mcpIds"`
		SkillIDs      []string `json:"skillIds"`
		AutoStart     bool     `json:"autoStart"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	request.Label, request.Role, request.Model = strings.TrimSpace(request.Label), strings.TrimSpace(request.Role), strings.TrimSpace(request.Model)
	if (request.TeamID == "" && request.WorktreeID == "") || request.Label == "" || len(request.Label) > 80 || len(request.Role) > 240 || !validProvider(request.Provider) {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Team, label, role, and provider are invalid.", nil)
		return
	}
	if err := h.models.Validate(request.Provider, request.Model); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_model", err.Error(), nil)
		return
	}
	if err := h.capabilities.ValidateSelection(request.Provider, request.MCPIDs, request.SkillIDs); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "capability_review_required", err.Error(), nil)
		return
	}
	node := contracts.Node{ID: newID(), Kind: "agent", Provider: request.Provider, Model: request.Model, TeamID: request.TeamID, WorktreeID: request.WorktreeID, Label: request.Label, Role: request.Role, RoleProfileID: request.RoleProfileID, MCPIDs: request.MCPIDs, SkillIDs: request.SkillIDs, AutoStart: request.AutoStart, Position: contracts.Point{X: 520, Y: 160}, Size: contracts.Size{Width: 300, Height: 210}}
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		if node.TeamID == "" && node.WorktreeID != "" {
			worktreeFound := false
			for index := range next.Worktrees {
				if next.Worktrees[index].ID != node.WorktreeID {
					continue
				}
				worktreeFound = true
				if next.Worktrees[index].TeamID != "" {
					node.TeamID = next.Worktrees[index].TeamID
				}
				break
			}
			if !worktreeFound {
				return errors.New("worktree not found")
			}
		}
		teamExists := node.TeamID == ""
		for _, team := range next.Teams {
			if team.ID == node.TeamID {
				teamExists = true
				break
			}
		}
		if !teamExists {
			return errors.New("team not found")
		}
		if node.WorktreeID != "" {
			for _, item := range next.Worktrees {
				if item.ID == node.WorktreeID {
					if item.TeamID != "" && item.TeamID != node.TeamID {
						return errors.New("worktree does not belong to team")
					}
					next.Nodes = append(next.Nodes, node)
					return nil
				}
			}
			return errors.New("worktree not found")
		}
		next.Nodes = append(next.Nodes, node)
		return nil
	})
	if err != nil {
		code, message := "team_not_found", "The selected team does not exist."
		if err.Error() == "worktree not found" {
			code, message = "worktree_not_found", "The selected worktree does not exist."
		} else if err.Error() == "worktree does not belong to team" {
			code, message = "worktree_team_mismatch", "The selected worktree belongs to another team."
		}
		writeError(w, http.StatusUnprocessableEntity, code, message, nil)
		return
	}
	writeJSON(w, http.StatusCreated, node)
}

func (h *HTTP) importNodeToWorktree(w http.ResponseWriter, r *http.Request) {
	var request struct {
		NodeID string `json:"nodeId"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	worktreeID := r.PathValue("id")
	var target *contracts.Worktree
	for index := range snapshot.Worktrees {
		if snapshot.Worktrees[index].ID == worktreeID {
			target = &snapshot.Worktrees[index]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "worktree_not_found", "The selected worktree does not exist.", nil)
		return
	}
	var source *contracts.Node
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].ID == request.NodeID {
			source = &snapshot.Nodes[index]
			break
		}
	}
	if source == nil {
		writeError(w, http.StatusNotFound, "node_not_found", "The selected agent does not exist.", nil)
		return
	}
	if target.TeamID != "" && target.TeamID != source.TeamID {
		writeError(w, http.StatusUnprocessableEntity, "worktree_team_mismatch", "This worktree is linked to another Team.", nil)
		return
	}
	clone := *source
	clone.ID, clone.WorktreeID = newID(), target.ID
	clone.Kind = "agent"
	clone.AutoStart = false
	clone.Position = contracts.Point{X: 520, Y: 160}
	if _, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		next.Nodes = append(next.Nodes, clone)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "node_import_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, clone)
}

func (h *HTTP) importTemplateToWorktree(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	worktreeID := r.PathValue("id")
	var target *contracts.Worktree
	for index := range snapshot.Worktrees {
		if snapshot.Worktrees[index].ID == worktreeID {
			target = &snapshot.Worktrees[index]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "worktree_not_found", "The selected worktree does not exist.", nil)
		return
	}
	template, found := h.templates.Find(r.PathValue("templateId"))
	if !found {
		writeError(w, http.StatusNotFound, "template_not_found", "The template does not exist.", nil)
		return
	}
	if len(template.Nodes) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "template_empty", "The selected template has no nodes.", nil)
		return
	}
	h.importWorkflowToWorktree(w, snapshot, *target, template, "")
}

func (h *HTTP) importTeamToWorktree(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	var target *contracts.Worktree
	for index := range snapshot.Worktrees {
		if snapshot.Worktrees[index].ID == r.PathValue("id") {
			target = &snapshot.Worktrees[index]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "worktree_not_found", "The selected worktree does not exist.", nil)
		return
	}
	template, found := teamWorkflowTemplate(snapshot, r.PathValue("teamId"))
	if !found {
		writeError(w, http.StatusNotFound, "team_workflow_not_found", "The Team does not exist or has no workflow definition.", nil)
		return
	}
	h.importWorkflowToWorktree(w, snapshot, *target, template, r.PathValue("teamId"))
}

func (h *HTTP) importWorkflowToWorktree(w http.ResponseWriter, snapshot contracts.Snapshot, target contracts.Worktree, template contracts.TeamTemplate, sourceTeamID string) {
	teamID := target.TeamID
	createDefinition := teamID == ""
	if createDefinition {
		teamID = sourceTeamID
		createDefinition = teamID == ""
		if createDefinition {
			teamID = newID()
		}
	}
	cloneGraph := func(worktreeID string) ([]contracts.Node, []contracts.Edge) {
		ids := make(map[string]string, len(template.Nodes))
		nodes := make([]contracts.Node, 0, len(template.Nodes))
		for _, source := range template.Nodes {
			id := newID()
			ids[source.ID] = id
			source.ID, source.TeamID, source.WorktreeID = id, teamID, worktreeID
			nodes = append(nodes, source)
		}
		edges := make([]contracts.Edge, 0, len(template.Edges))
		for _, source := range template.Edges {
			source.ID, source.Source, source.Target = newID(), ids[source.Source], ids[source.Target]
			edges = append(edges, source)
		}
		return nodes, edges
	}

	runtimeNodes, runtimeEdges := cloneGraph(target.ID)
	var definitionNodes []contracts.Node
	var definitionEdges []contracts.Edge
	if createDefinition {
		definitionNodes, definitionEdges = cloneGraph("")
	}
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		if createDefinition {
			next.Teams = append(next.Teams, contracts.Team{
				ID: teamID, Name: template.Name, Color: template.Color, CreatedAt: time.Now().UTC(),
			})
			next.Nodes = append(next.Nodes, definitionNodes...)
			next.Edges = append(next.Edges, definitionEdges...)
		}
		for index := range next.Worktrees {
			if next.Worktrees[index].ID == target.ID && next.Worktrees[index].TeamID == "" {
				next.Worktrees[index].TeamID = teamID
				break
			}
		}
		next.Nodes = append(next.Nodes, runtimeNodes...)
		next.Edges = append(next.Edges, runtimeEdges...)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "template_import_failed", err.Error(), nil)
		return
	}
	nodeIDs := make([]string, 0, len(runtimeNodes))
	for _, node := range runtimeNodes {
		nodeIDs = append(nodeIDs, node.ID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"teamId": teamID, "worktreeId": target.ID, "nodeIds": nodeIDs,
	})
}

func teamWorkflowTemplate(snapshot contracts.Snapshot, teamID string) (contracts.TeamTemplate, bool) {
	var team *contracts.Team
	for index := range snapshot.Teams {
		if snapshot.Teams[index].ID == teamID {
			team = &snapshot.Teams[index]
			break
		}
	}
	if team == nil {
		return contracts.TeamTemplate{}, false
	}
	nodeIDs := map[string]bool{}
	nodes := []contracts.Node{}
	provider := ""
	orchestratorModel := ""
	for _, node := range snapshot.Nodes {
		if node.TeamID != teamID || node.WorktreeID != "" {
			continue
		}
		nodes = append(nodes, node)
		nodeIDs[node.ID] = true
		if node.Kind == "orchestrator" {
			provider = node.Provider
			orchestratorModel = node.Model
		}
	}
	if len(nodes) == 0 || provider == "" {
		return contracts.TeamTemplate{}, false
	}
	edges := []contracts.Edge{}
	for _, edge := range snapshot.Edges {
		if nodeIDs[edge.Source] && nodeIDs[edge.Target] {
			edges = append(edges, edge)
		}
	}
	return contracts.TeamTemplate{Name: team.Name, Color: team.Color, OrchestratorProvider: provider, OrchestratorModel: orchestratorModel, Nodes: nodes, Edges: edges}, true
}

func (h *HTTP) patchNode(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Label         *string   `json:"label"`
		Role          *string   `json:"role"`
		Model         *string   `json:"model"`
		RoleProfileID *string   `json:"roleProfileId"`
		MCPIDs        *[]string `json:"mcpIds"`
		SkillIDs      *[]string `json:"skillIds"`
		Provider      *string   `json:"provider"`
		AutoStart     *bool     `json:"autoStart"`
		WorktreeID    *string   `json:"worktreeId"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	var updated contracts.Node
	found := false
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		for index := range next.Nodes {
			if next.Nodes[index].ID != r.PathValue("id") {
				continue
			}
			found = true
			if request.Label != nil {
				next.Nodes[index].Label = strings.TrimSpace(*request.Label)
			}
			if request.Role != nil {
				next.Nodes[index].Role = strings.TrimSpace(*request.Role)
			}
			if request.Model != nil {
				next.Nodes[index].Model = strings.TrimSpace(*request.Model)
			}
			if request.RoleProfileID != nil {
				next.Nodes[index].RoleProfileID = strings.TrimSpace(*request.RoleProfileID)
			}
			if request.MCPIDs != nil {
				next.Nodes[index].MCPIDs = append([]string(nil), (*request.MCPIDs)...)
			}
			if request.SkillIDs != nil {
				next.Nodes[index].SkillIDs = append([]string(nil), (*request.SkillIDs)...)
			}
			if request.Provider != nil {
				if !validProvider(*request.Provider) {
					return errors.New("provider is invalid")
				}
				next.Nodes[index].Provider = *request.Provider
			}
			if request.AutoStart != nil {
				next.Nodes[index].AutoStart = *request.AutoStart
			}
			if request.WorktreeID != nil {
				candidate := strings.TrimSpace(*request.WorktreeID)
				if candidate != "" {
					valid := false
					for _, item := range next.Worktrees {
						if item.ID == candidate && (item.TeamID == "" || item.TeamID == next.Nodes[index].TeamID) {
							valid = true
							break
						}
					}
					if !valid {
						return errors.New("worktree does not belong to node team")
					}
				}
				next.Nodes[index].WorktreeID = candidate
			}
			if err := h.capabilities.ValidateSelection(next.Nodes[index].Provider, next.Nodes[index].MCPIDs, next.Nodes[index].SkillIDs); err != nil {
				return fmt.Errorf("capability review required: %w", err)
			}
			if err := h.models.Validate(next.Nodes[index].Provider, next.Nodes[index].Model); err != nil {
				return fmt.Errorf("model is invalid: %w", err)
			}
			updated = next.Nodes[index]
			return nil
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "node_not_found", "The node does not exist.", nil)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *HTTP) deleteNode(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	found, orchestrator := false, false
	for _, node := range snapshot.Nodes {
		if node.ID == r.PathValue("id") {
			found, orchestrator = true, node.Kind == "orchestrator"
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "node_not_found", "The node does not exist.", nil)
		return
	}
	if orchestrator {
		writeError(w, http.StatusConflict, "orchestrator_required", "Delete the team to remove its orchestrator.", nil)
		return
	}
	if err := h.terminals.StopNode(r.PathValue("id")); err != nil && !errors.Is(err, terminal.ErrSessionNotFound) {
		writeError(w, http.StatusConflict, "node_stop_failed", "The agent could not be stopped before deletion.", map[string]any{"cause": err.Error()})
		return
	}
	_, err = h.workspace.Update(func(next *contracts.Snapshot) error {
		next.Nodes = filterNodes(next.Nodes, func(node contracts.Node) bool { return node.ID != r.PathValue("id") })
		next.Edges = filterEdges(next.Edges, func(edge contracts.Edge) bool {
			return edge.Source != r.PathValue("id") && edge.Target != r.PathValue("id")
		})
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "canvas_save_failed", err.Error(), nil)
		return
	}
	h.pruneTeamExecutions()
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) replaceLayout(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Nodes    []contracts.Node   `json:"nodes"`
		Edges    []contracts.Edge   `json:"edges"`
		Viewport contracts.Viewport `json:"viewport"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	snapshot, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		layout := make(map[string]contracts.Node, len(request.Nodes))
		for _, node := range request.Nodes {
			layout[node.ID] = node
		}
		for index := range next.Nodes {
			if node, ok := layout[next.Nodes[index].ID]; ok {
				next.Nodes[index].Position, next.Nodes[index].Size = node.Position, node.Size
			}
		}
		next.Edges, next.Viewport = request.Edges, request.Viewport
		return nil
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_layout", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func decodeBody(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request body is invalid.", nil)
		return false
	}
	return true
}

func validProvider(provider string) bool {
	return provider == "claude" || provider == "codex" || provider == "pi" || provider == "opencode" || (provider == "mock" && os.Getenv("AGENT_INFINITE_TEST_MODE") == "1")
}

func filterCapabilities(items []capabilities.Item, kind string) []capabilities.Item {
	result := make([]capabilities.Item, 0, len(items))
	for _, item := range items {
		if item.Kind == kind {
			result = append(result, item)
		}
	}
	return result
}

func worktreesForTeam(snapshot contracts.Snapshot, teamID string) []contracts.Worktree {
	if len(snapshot.Worktrees) > 0 {
		items := make([]contracts.Worktree, 0)
		for _, item := range snapshot.Worktrees {
			if item.TeamID == teamID {
				items = append(items, item)
			}
		}
		return items
	}
	for _, team := range snapshot.Teams {
		if team.ID == teamID && team.Branch != "" {
			return []contracts.Worktree{{ID: team.ID, TeamID: team.ID, Name: team.Name, Branch: team.Branch, BaseRef: team.BaseRef, CreatedAt: team.CreatedAt}}
		}
	}
	return nil
}

func newID() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
func filterTeams(items []contracts.Team, remove string) []contracts.Team {
	result := items[:0]
	for _, item := range items {
		if item.ID != remove {
			result = append(result, item)
		}
	}
	return result
}
func filterWorktrees(items []contracts.Worktree, keep func(contracts.Worktree) bool) []contracts.Worktree {
	result := items[:0]
	for _, item := range items {
		if keep(item) {
			result = append(result, item)
		}
	}
	return result
}
func filterNodes(items []contracts.Node, keep func(contracts.Node) bool) []contracts.Node {
	result := items[:0]
	for _, item := range items {
		if keep(item) {
			result = append(result, item)
		}
	}
	return result
}
func filterEdges(items []contracts.Edge, keep func(contracts.Edge) bool) []contracts.Edge {
	result := items[:0]
	for _, item := range items {
		if keep(item) {
			result = append(result, item)
		}
	}
	return result
}
