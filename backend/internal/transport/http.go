package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/capabilities"
	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/eventbus"
	"github.com/agent-infinite/agent-infinite/backend/internal/hookbridge"
	"github.com/agent-infinite/agent-infinite/backend/internal/models"
	"github.com/agent-infinite/agent-infinite/backend/internal/orchestration"
	"github.com/agent-infinite/agent-infinite/backend/internal/terminal"
	"github.com/agent-infinite/agent-infinite/backend/internal/workspace"
	"github.com/agent-infinite/agent-infinite/backend/internal/worktree"
	"github.com/coder/websocket"
)

type HTTP struct {
	handler       http.Handler
	token         string
	version       string
	baseURL       string
	runtimeRoot   string
	workspace     *workspace.Service
	terminals     *terminal.Manager
	worktrees     *worktree.Manager
	events        *eventbus.Bus
	hooks         *hookbridge.Service
	orchestration *orchestration.Service
	templates     *templateStore
	capabilities  *capabilities.Service
	models        *models.Service
	executions    map[string]contracts.TeamExecution
	executionMu   sync.Mutex
}

func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.handler.ServeHTTP(w, r) }

func NewHTTP(token, version, baseURL, runtimeRoot string, workspaceService *workspace.Service, terminals *terminal.Manager, worktrees *worktree.Manager, mcpHandler http.Handler, events *eventbus.Bus, hooks *hookbridge.Service, orchestrationService *orchestration.Service) *HTTP {
	capabilityService := capabilities.New(filepath.Dir(runtimeRoot))
	capabilityService.Scan("")
	modelService := models.New(filepath.Dir(runtimeRoot))
	h := &HTTP{token: token, version: version, baseURL: strings.TrimRight(baseURL, "/"), runtimeRoot: runtimeRoot, workspace: workspaceService, terminals: terminals, worktrees: worktrees, events: events, hooks: hooks, orchestration: orchestrationService, templates: newTemplateStore(filepath.Join(filepath.Dir(runtimeRoot), "team-templates.json")), capabilities: capabilityService, models: modelService, executions: make(map[string]contracts.TeamExecution)}
	if version != "test" {
		go modelService.Scan(context.Background(), "", "")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("POST /api/workspaces/open", h.openWorkspace)
	mux.HandleFunc("GET /api/snapshot", h.snapshot)
	mux.HandleFunc("PATCH /api/workspaces/integration", h.patchIntegration)
	mux.HandleFunc("GET /api/runtime", h.runtime)
	mux.HandleFunc("GET /api/dispatches", h.dispatches)
	mux.HandleFunc("POST /api/teams", h.createTeam)
	mux.HandleFunc("POST /api/teams/{id}/extract", h.extractTeamWorkflow)
	mux.HandleFunc("POST /api/teams/{id}/run", h.runTeam)
	mux.HandleFunc("GET /api/team-templates", h.listTeamTemplates)
	mux.HandleFunc("POST /api/team-templates", h.saveTeamTemplate)
	mux.HandleFunc("PATCH /api/team-templates/{id}", h.updateTeamTemplate)
	mux.HandleFunc("DELETE /api/team-templates/{id}", h.deleteTeamTemplate)
	mux.HandleFunc("POST /api/team-templates/{id}/apply", h.applyTeamTemplate)
	mux.HandleFunc("DELETE /api/teams/{id}", h.deleteTeam)
	mux.HandleFunc("POST /api/roles", h.createCustomRole)
	mux.HandleFunc("DELETE /api/roles/{name}", h.deleteCustomRole)
	mux.HandleFunc("GET /api/role-profiles", h.listRoleProfiles)
	mux.HandleFunc("POST /api/role-profiles", h.createRoleProfile)
	mux.HandleFunc("PATCH /api/role-profiles/{id}", h.patchRoleProfile)
	mux.HandleFunc("DELETE /api/role-profiles/{id}", h.deleteRoleProfile)
	mux.HandleFunc("GET /api/tools/inventory", h.capabilityInventory)
	mux.HandleFunc("POST /api/tools/scan", h.scanCapabilities)
	mux.HandleFunc("PATCH /api/tools/policies", h.patchCapabilityPolicies)
	mux.HandleFunc("POST /api/tools/mcp-servers", h.upsertManagedMCP)
	mux.HandleFunc("PATCH /api/tools/mcp-servers/{id}", h.upsertManagedMCP)
	mux.HandleFunc("PATCH /api/tools/mcp-servers/{id}/policy", h.patchCapabilityPolicy)
	mux.HandleFunc("POST /api/tools/mcp-servers/{id}/promote", h.promoteCapability)
	mux.HandleFunc("POST /api/tools/mcp-servers/{id}/test", h.testMCP)
	mux.HandleFunc("DELETE /api/tools/mcp-servers/{id}", h.archiveCapability)
	mux.HandleFunc("POST /api/tools/skills", h.upsertManagedSkill)
	mux.HandleFunc("PATCH /api/tools/skills/{id}", h.upsertManagedSkill)
	mux.HandleFunc("GET /api/tools/skills/{id}", h.managedSkillContent)
	mux.HandleFunc("PATCH /api/tools/skills/{id}/policy", h.patchCapabilityPolicy)
	mux.HandleFunc("POST /api/tools/skills/{id}/promote", h.promoteCapability)
	mux.HandleFunc("DELETE /api/tools/skills/{id}", h.archiveCapability)
	mux.HandleFunc("GET /api/models/inventory", h.modelInventory)
	mux.HandleFunc("POST /api/models/scan", h.scanModels)
	mux.HandleFunc("POST /api/worktrees", h.createWorktree)
	mux.HandleFunc("GET /api/git/branches", h.gitBranches)
	mux.HandleFunc("DELETE /api/worktrees/{id}", h.deleteWorktree)
	mux.HandleFunc("POST /api/worktrees/{id}/nodes/import", h.importNodeToWorktree)
	mux.HandleFunc("POST /api/worktrees/{id}/templates/{templateId}/import", h.importTemplateToWorktree)
	mux.HandleFunc("POST /api/worktrees/{id}/teams/{teamId}/import", h.importTeamToWorktree)
	mux.HandleFunc("POST /api/nodes", h.createNode)
	mux.HandleFunc("PATCH /api/nodes/{id}", h.patchNode)
	mux.HandleFunc("DELETE /api/nodes/{id}", h.deleteNode)
	mux.HandleFunc("POST /api/nodes/{id}/start", h.startNode)
	mux.HandleFunc("POST /api/nodes/{id}/stop", h.stopNode)
	mux.HandleFunc("PUT /api/canvas/layout", h.replaceLayout)
	mux.HandleFunc("POST /api/terminals/powershell", h.startPowerShell)
	mux.HandleFunc("GET /ws/terminals/{sessionId}", h.terminalWebSocket)
	mux.HandleFunc("GET /ws/events", h.eventWebSocket)
	mux.HandleFunc("POST /internal/hooks/events", h.hookEvent)
	mux.Handle("/mcp/{sourceNodeId}", mcpHandler)
	h.handler = h.cors(h.authenticate(mux))
	return h
}

func (h *HTTP) startPowerShell(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	session, err := h.terminals.StartPowerShell(snapshot.WorkspacePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "terminal_start_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"sessionId": session.ID()})
}

func (h *HTTP) terminalWebSocket(w http.ResponseWriter, r *http.Request) {
	session, err := h.terminals.Get(r.PathValue("sessionId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "terminal_not_found", "The terminal session does not exist.", nil)
		return
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(64 * 1024)
	initial, live, detach := session.Attach()
	defer detach()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var writeMu sync.Mutex
	write := func(messageType websocket.MessageType, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		writeCtx, writeCancel := context.WithTimeout(ctx, 2*time.Second)
		defer writeCancel()
		return connection.Write(writeCtx, messageType, data)
	}
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		if len(initial) > 0 && write(websocket.MessageBinary, initial) != nil {
			cancel()
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-live:
				if !ok {
					cancel()
					return
				}
				if write(websocket.MessageBinary, chunk) != nil {
					cancel()
					return
				}
			}
		}
	}()
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			break
		}
		if messageType == websocket.MessageBinary {
			if err := session.Write(data); err != nil {
				break
			}
			continue
		}
		var control struct {
			Type string `json:"type"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if json.Unmarshal(data, &control) != nil {
			continue
		}
		switch control.Type {
		case "resize":
			if control.Cols >= 2 && control.Cols <= 500 && control.Rows >= 2 && control.Rows <= 300 {
				_ = session.Resize(control.Cols, control.Rows)
			}
		case "ping":
			_ = write(websocket.MessageText, []byte(`{"type":"pong"}`))
		}
	}
	cancel()
	<-writerDone
}

func (h *HTTP) eventWebSocket(w http.ResponseWriter, r *http.Request) {
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	events, unsubscribe := h.events.Subscribe()
	defer unsubscribe()
	for event := range events {
		data, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		err := connection.Write(ctx, websocket.MessageText, data)
		cancel()
		if err != nil {
			return
		}
	}
}

func (h *HTTP) health(w http.ResponseWriter, _ *http.Request) {
	journalStatus, journalError := h.events.JournalHealth()
	status := "ok"
	if journalStatus == "degraded" {
		status = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "version": h.version, "hookSessions": len(h.hooks.Sessions()),
		"activityJournal": map[string]string{"status": journalStatus, "error": journalError},
	})
}

func (h *HTTP) hookEvent(w http.ResponseWriter, r *http.Request) {
	var callback hookbridge.Callback
	if !decodeBody(w, r, &callback) {
		return
	}
	event, err := h.hooks.Handle(r.Header.Get("X-Agent-Infinite-Hook-Token"), callback)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, hookbridge.ErrDuplicate) || errors.Is(err, hookbridge.ErrOutOfOrder) {
			status = http.StatusConflict
		}
		writeError(w, status, "hook_callback_rejected", err.Error(), nil)
		return
	}
	hookOutput := ""
	if event.Name == "SessionStart" {
		contextText := h.hookSessionContext(event.Session.NodeID)
		if contextText != "" {
			data, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
				"hookEventName": "SessionStart", "additionalContext": contextText,
			}})
			hookOutput = string(data)
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "event": event.Name, "hookOutput": hookOutput})
}

func (h *HTTP) hookSessionContext(nodeID string) string {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		return ""
	}
	var caller *contracts.Node
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].ID == nodeID {
			caller = &snapshot.Nodes[index]
			break
		}
	}
	if caller == nil {
		return ""
	}
	targets := make([]string, 0)
	for _, edge := range snapshot.Edges {
		if edge.Source != nodeID {
			continue
		}
		for _, node := range snapshot.Nodes {
			if node.ID == edge.Target {
				targets = append(targets, fmt.Sprintf("%s (role: %s, provider: %s)", node.Label, node.Role, node.Provider))
			}
		}
	}
	return fmt.Sprintf("Agent Infinite canvas identity: %s; role: %s; team: %s. Connected targets: %s. When the user asks for an agent or names a connected label/role, use the Agent Infinite MCP tools. Provider-native subagents are not a fallback for missing, ambiguous, offline, or failed canvas targets.", caller.Label, caller.Role, caller.TeamID, strings.Join(targets, "; "))
}

func (h *HTTP) openWorkspace(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path string `json:"path"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request body is invalid.", nil)
		return
	}
	snapshot, err := h.workspace.Open(r.Context(), request.Path)
	if err != nil {
		switch {
		case errors.Is(err, workspace.ErrNotDirectory):
			writeError(w, http.StatusUnprocessableEntity, "workspace_not_directory", "The selected path is not a directory.", nil)
		case errors.Is(err, workspace.ErrNotGit):
			writeError(w, http.StatusUnprocessableEntity, "workspace_not_git", "The selected folder is not a Git repository.", nil)
		default:
			writeError(w, http.StatusInternalServerError, "workspace_open_failed", "The workspace could not be opened.", nil)
		}
		return
	}
	h.capabilities.Scan(snapshot.WorkspacePath)
	if h.version != "test" {
		h.models.Scan(r.Context(), snapshot.WorkspacePath, "")
	}
	if err := h.worktrees.Reconcile(r.Context(), snapshot); err != nil {
		writeError(w, http.StatusConflict, "worktree_reconcile_failed", "Existing team worktrees could not be reconciled.", map[string]any{"cause": err.Error()})
		return
	}
	if err := h.orchestration.Reconcile(snapshot.WorkspaceID); err != nil {
		h.events.Emit("backend.error", snapshot.WorkspaceID, map[string]any{"code": "dispatch_reconcile_failed", "message": err.Error()})
	}
	for _, node := range snapshot.Nodes {
		if !node.AutoStart || node.WorktreeID == "" {
			continue
		}
		if _, startErr := h.launchNode(snapshot, node); startErr != nil {
			h.events.Emit("backend.error", node.ID, map[string]any{"code": "autostart_failed", "message": startErr.Error()})
		}
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *HTTP) snapshot(w http.ResponseWriter, _ *http.Request) {
	snapshot, err := h.workspace.Snapshot()
	if errors.Is(err, workspace.ErrNotOpen) {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *HTTP) patchIntegration(w http.ResponseWriter, r *http.Request) {
	var request contracts.Integration
	if !decodeBody(w, r, &request) {
		return
	}
	if request.Hooks != "auto" && request.Hooks != "off" && request.Hooks != "required" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_hook_policy", "Hooks must be auto, off, or required.", nil)
		return
	}
	snapshot, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		next.Integration = request
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "canvas_save_failed", "The integration policy could not be persisted.", nil)
		return
	}
	h.events.Emit("integration.policy_changed", snapshot.WorkspaceID, request)
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *HTTP) runtime(w http.ResponseWriter, _ *http.Request) {
	h.pruneTeamExecutions()
	runtimes := h.terminals.Runtimes()
	for index := range runtimes {
		if runtimes[index].HookSessionID == "" {
			continue
		}
		if hookSession, ok := h.hooks.Session(runtimes[index].HookSessionID); ok {
			runtimes[index].IntegrationMode = hookSession.Mode
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": runtimes})
}

func (h *HTTP) dispatches(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"dispatches": h.orchestration.Dispatches()})
}

func (h *HTTP) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/health" || r.URL.Path == "/internal/hooks/events" {
			next.ServeHTTP(w, r)
			return
		}
		authorized := r.Header.Get("Authorization") == "Bearer "+h.token
		if strings.HasPrefix(r.URL.Path, "/ws/") {
			authorized = r.URL.Query().Get("token") == h.token
		}
		if !authorized {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid backend token is required.", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *HTTP) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "null" || strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	writeJSON(w, status, contracts.APIError{Code: code, Message: message, Details: details})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(fmt.Errorf("encode JSON response: %w", err))
	}
}
