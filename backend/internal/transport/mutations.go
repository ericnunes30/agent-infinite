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
	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/detector"
	"github.com/agent-infinite/agent-infinite/backend/internal/hookbridge"
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
		}
		writeError(w, http.StatusConflict, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessionId": session.ID(), "status": session.Status(),
		"integrationMode": session.IntegrationMode(), "hookSessionId": session.HookSessionID(),
	})
}

func (h *HTTP) launchNode(snapshot contracts.Snapshot, node contracts.Node) (*terminal.Session, error) {
	if existing, existingErr := h.terminals.GetByNode(node.ID); existingErr == nil {
		if existing.Status() != detector.Dead {
			return existing, nil
		}
		_ = h.terminals.StopNode(node.ID)
	}
	worktreeID := node.WorktreeID
	workDir := snapshot.WorkspacePath
	if worktreeID == "" {
		worktreeID = node.TeamID // schema v1 compatibility
	}
	if worktreeID != "" {
		workDir = h.worktrees.Path(snapshot.WorkspaceID, worktreeID)
	}
	runtimeDir := filepath.Join(h.runtimeRoot, snapshot.WorkspaceID, node.ID)
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
		Provider: node.Provider, WorkDir: workDir, RuntimeDir: runtimeDir, NodeID: node.ID,
		MCPBaseURL: h.baseURL, MCPToken: h.token, Hooks: hookLaunch,
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
		go h.watchHookActivation(node.ID, hookSession.ID, policy)
	}
	return session, nil
}

func (h *HTTP) watchHookActivation(nodeID, hookSessionID, policy string) {
	timer := time.NewTimer(hookActivationTimeout)
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
			"hookSessionId": hookSessionID, "message": "Required provider hooks did not activate within 10 seconds.",
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
	err := h.terminals.StopNode(r.PathValue("id"))
	if errors.Is(err, terminal.ErrSessionNotFound) {
		writeError(w, http.StatusNotFound, "session_not_found", "The node has no running session.", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "terminal_stop_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) createTeam(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name                  string `json:"name"`
		Color                 string `json:"color"`
		OrchestratorProvider  string `json:"orchestratorProvider"`
		CreateInitialWorktree *bool  `json:"createInitialWorktree"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 80 || request.Color == "" || !validProvider(request.OrchestratorProvider) {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Team name, color, and provider are required.", nil)
		return
	}
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	teamID, nodeID, worktreeID := newID(), newID(), newID()
	createInitialWorktree := request.CreateInitialWorktree == nil || *request.CreateInitialWorktree
	team := contracts.Team{ID: teamID, Name: request.Name, Color: request.Color, CreatedAt: time.Now().UTC()}
	var worktree contracts.Worktree
	if createInitialWorktree {
		branch, baseRef, _, createErr := h.worktrees.Create(r.Context(), snapshot, worktreeID, request.Name)
		if createErr != nil {
			writeError(w, http.StatusConflict, "worktree_create_failed", "The team worktree could not be created.", map[string]any{"cause": createErr.Error()})
			return
		}
		worktree = contracts.Worktree{ID: worktreeID, TeamID: teamID, Name: request.Name, Branch: branch, BaseRef: baseRef, CreatedAt: time.Now().UTC()}
		team.Branch, team.BaseRef = branch, baseRef // schema v1 compatibility
	}
	node := contracts.Node{ID: nodeID, Kind: "orchestrator", Provider: request.OrchestratorProvider, TeamID: teamID, WorktreeID: worktree.ID, Label: request.Name + " Lead", Role: "Coordinates team delegation", Position: contracts.Point{X: 120, Y: 120}, Size: contracts.Size{Width: 320, Height: 220}}
	_, err = h.workspace.Update(func(next *contracts.Snapshot) error {
		next.Teams = append(next.Teams, team)
		if createInitialWorktree {
			next.Worktrees = append(next.Worktrees, worktree)
		}
		next.Nodes = append(next.Nodes, node)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "canvas_save_failed", "The team could not be persisted.", nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"team": team, "orchestrator": node})
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
	for _, linked := range worktreesForTeam(snapshot, id) {
		if err := h.worktrees.Delete(r.Context(), snapshot, linked); err != nil {
			if errors.Is(err, worktree.ErrDirty) {
				writeError(w, http.StatusConflict, "worktree_dirty", "The worktree has uncommitted changes.", nil)
			} else {
				writeError(w, http.StatusConflict, "worktree_remove_failed", "The worktree could not be removed.", map[string]any{"cause": err.Error()})
			}
			return
		}
	}
	_, err = h.workspace.Update(func(next *contracts.Snapshot) error {
		removedNodes := make(map[string]bool)
		next.Teams = filterTeams(next.Teams, id)
		next.Worktrees = filterWorktrees(next.Worktrees, func(item contracts.Worktree) bool { return item.TeamID != id })
		next.Nodes = filterNodes(next.Nodes, func(node contracts.Node) bool {
			if node.TeamID == id {
				removedNodes[node.ID] = true
				return false
			}
			return true
		})
		next.Edges = filterEdges(next.Edges, func(edge contracts.Edge) bool { return !removedNodes[edge.Source] && !removedNodes[edge.Target] })
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "canvas_save_failed", "The team deletion could not be persisted.", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) createWorktree(w http.ResponseWriter, r *http.Request) {
	var request struct {
		TeamID string `json:"teamId"`
		Name   string `json:"name"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	request.TeamID, request.Name = strings.TrimSpace(request.TeamID), strings.TrimSpace(request.Name)
	if request.TeamID == "" || request.Name == "" || len(request.Name) > 80 {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Team and worktree name are required.", nil)
		return
	}
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
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
	worktreeID := newID()
	branch, baseRef, _, err := h.worktrees.Create(r.Context(), snapshot, worktreeID, request.Name)
	if err != nil {
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
	for _, node := range snapshot.Nodes {
		if node.WorktreeID == id {
			writeError(w, http.StatusConflict, "worktree_in_use", "Move or delete the nodes assigned to this worktree first.", map[string]any{"nodeId": node.ID})
			return
		}
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
		writeError(w, http.StatusInternalServerError, "canvas_save_failed", "The worktree deletion could not be persisted.", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) createNode(w http.ResponseWriter, r *http.Request) {
	var request struct {
		TeamID     string `json:"teamId"`
		WorktreeID string `json:"worktreeId"`
		Provider   string `json:"provider"`
		Label      string `json:"label"`
		Role       string `json:"role"`
		AutoStart  bool   `json:"autoStart"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	request.Label, request.Role = strings.TrimSpace(request.Label), strings.TrimSpace(request.Role)
	if request.TeamID == "" || request.Label == "" || len(request.Label) > 80 || len(request.Role) > 240 || !validProvider(request.Provider) {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Team, label, role, and provider are invalid.", nil)
		return
	}
	node := contracts.Node{ID: newID(), Kind: "agent", Provider: request.Provider, TeamID: request.TeamID, WorktreeID: request.WorktreeID, Label: request.Label, Role: request.Role, AutoStart: request.AutoStart, Position: contracts.Point{X: 520, Y: 160}, Size: contracts.Size{Width: 300, Height: 210}}
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		teamExists := false
		for _, team := range next.Teams {
			if team.ID == request.TeamID {
				teamExists = true
				break
			}
		}
		if !teamExists {
			return errors.New("team not found")
		}
		if node.WorktreeID == "" {
			for _, item := range next.Worktrees {
				if item.TeamID == request.TeamID {
					node.WorktreeID = item.ID
					break
				}
			}
		} else {
			for _, item := range next.Worktrees {
				if item.ID == node.WorktreeID {
					if item.TeamID != request.TeamID {
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

func (h *HTTP) patchNode(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Label      *string `json:"label"`
		Role       *string `json:"role"`
		Provider   *string `json:"provider"`
		AutoStart  *bool   `json:"autoStart"`
		WorktreeID *string `json:"worktreeId"`
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
			if request.Provider != nil {
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
						if item.ID == candidate && item.TeamID == next.Nodes[index].TeamID {
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
	found, orchestrator := false, false
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		for _, node := range next.Nodes {
			if node.ID == r.PathValue("id") {
				found, orchestrator = true, node.Kind == "orchestrator"
			}
		}
		if !found || orchestrator {
			return nil
		}
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
	if !found {
		writeError(w, http.StatusNotFound, "node_not_found", "The node does not exist.", nil)
		return
	}
	if orchestrator {
		writeError(w, http.StatusConflict, "orchestrator_required", "Delete the team to remove its orchestrator.", nil)
		return
	}
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
	return provider == "claude" || provider == "codex" || (provider == "mock" && os.Getenv("AGENT_INFINITE_TEST_MODE") == "1")
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
