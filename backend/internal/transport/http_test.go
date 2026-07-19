package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/eventbus"
	"github.com/agent-infinite/agent-infinite/backend/internal/hookbridge"
	"github.com/agent-infinite/agent-infinite/backend/internal/orchestration"
	"github.com/agent-infinite/agent-infinite/backend/internal/terminal"
	"github.com/agent-infinite/agent-infinite/backend/internal/workspace"
	"github.com/agent-infinite/agent-infinite/backend/internal/worktree"
)

func newTestHTTP(t *testing.T) http.Handler {
	t.Helper()
	workspaceService := workspace.NewService()
	terminalManager := terminal.NewManager(context.Background())
	orchestrationService := orchestration.New(context.Background(), workspaceService, terminalManager)
	return NewHTTP("secret", "test", "http://127.0.0.1", t.TempDir(), workspaceService, terminalManager, worktree.NewManagerAt(t.TempDir()), http.NotFoundHandler(), eventbus.New(), hookbridge.New(), orchestrationService)
}

func TestSessionStartCallbackReturnsCanvasIdentityContext(t *testing.T) {
	repository := t.TempDir()
	if output, err := exec.Command("git", "init", repository).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspaceService := workspace.NewService()
	if _, err := workspaceService.Open(context.Background(), repository); err != nil {
		t.Fatal(err)
	}
	_, err := workspaceService.Update(func(snapshot *contracts.Snapshot) error {
		snapshot.Teams = []contracts.Team{{ID: "team", Name: "Team"}}
		snapshot.Nodes = []contracts.Node{
			{ID: "source", TeamID: "team", Kind: "orchestrator", Provider: "codex", Label: "Lead", Role: "coordinate", Size: contracts.Size{Width: 300, Height: 200}},
			{ID: "target", TeamID: "team", Kind: "agent", Provider: "claude", Label: "Reviewer", Role: "review", Size: contracts.Size{Width: 300, Height: 200}},
		}
		snapshot.Edges = []contracts.Edge{{ID: "edge", Source: "source", Target: "target", Type: "delegates_to"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	hooks := hookbridge.New()
	hookSession := hooks.Register("source", "workspace", "codex", "hooks")
	terminalManager := terminal.NewManager(context.Background())
	orchestrationService := orchestration.New(context.Background(), workspaceService, terminalManager)
	httpTransport := NewHTTP("secret", "test", "http://127.0.0.1", t.TempDir(), workspaceService, terminalManager, worktree.NewManagerAt(t.TempDir()), http.NotFoundHandler(), eventbus.New(), hooks, orchestrationService)
	callback := hookbridge.Callback{SessionID: hookSession.ID, NodeID: "source", WorkspaceID: "workspace", Provider: "codex", Raw: json.RawMessage(`{"hook_event_name":"SessionStart"}`)}
	body, _ := json.Marshal(callback)
	request := httptest.NewRequest(http.MethodPost, "/internal/hooks/events", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Agent-Infinite-Hook-Token", hooks.Token(hookSession.ID))
	response := httptest.NewRecorder()
	httpTransport.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{"Lead", "Reviewer", "Provider-native subagents are not a fallback"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("callback response missing %q: %s", expected, response.Body.String())
		}
	}
}

func TestHookActivationFallbackAndRequiredFailure(t *testing.T) {
	previous := hookActivationTimeout
	hookActivationTimeout = 5 * time.Millisecond
	defer func() { hookActivationTimeout = previous }()

	for _, test := range []struct {
		policy, wantEvent, wantMode string
		wantClosed                  bool
	}{
		{policy: "auto", wantEvent: "integration.degraded", wantMode: "detector"},
		{policy: "required", wantEvent: "integration.required_failed", wantClosed: true},
	} {
		t.Run(test.policy, func(t *testing.T) {
			hooks := hookbridge.New()
			events := eventbus.New()
			stream, unsubscribe := events.Subscribe()
			defer unsubscribe()
			terminalManager := terminal.NewManager(context.Background())
			httpTransport := &HTTP{hooks: hooks, events: events, terminals: terminalManager}
			session := hooks.Register("node", "workspace", "mock", "hooks")
			go httpTransport.watchHookActivation("node", session.ID, test.policy, hookActivationTimeout)
			select {
			case event := <-stream:
				if event.Type != test.wantEvent {
					t.Fatalf("event = %q, want %q", event.Type, test.wantEvent)
				}
			case <-time.After(time.Second):
				t.Fatal("activation watchdog emitted no event")
			}
			actual, exists := hooks.Session(session.ID)
			if test.wantClosed && exists {
				t.Fatal("required hook session was not invalidated")
			}
			if !test.wantClosed && (!exists || actual.Mode != test.wantMode) {
				t.Fatalf("fallback session = %#v, exists %v", actual, exists)
			}
		})
	}
}

func TestHealthDoesNotRequireAuthentication(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	newTestHTTP(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}

func TestAPIRequiresBearerToken(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/snapshot", nil)
	response := httptest.NewRecorder()
	newTestHTTP(t).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestSnapshotReportsConflictBeforeWorkspaceOpen(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/snapshot", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	newTestHTTP(t).ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
}

func TestModelInventoryAndScanValidation(t *testing.T) {
	handler := newTestHTTP(t)
	inventoryRequest := httptest.NewRequest(http.MethodGet, "/api/models/inventory", nil)
	inventoryRequest.Header.Set("Authorization", "Bearer secret")
	inventoryResponse := httptest.NewRecorder()
	handler.ServeHTTP(inventoryResponse, inventoryRequest)
	if inventoryResponse.Code != http.StatusOK || !strings.Contains(inventoryResponse.Body.String(), `"providers"`) {
		t.Fatalf("inventory response = %d: %s", inventoryResponse.Code, inventoryResponse.Body.String())
	}

	scanRequest := httptest.NewRequest(http.MethodPost, "/api/models/scan", bytes.NewBufferString(`{"provider":"invalid"}`))
	scanRequest.Header.Set("Authorization", "Bearer secret")
	scanRequest.Header.Set("Content-Type", "application/json")
	scanResponse := httptest.NewRecorder()
	handler.ServeHTTP(scanResponse, scanRequest)
	if scanResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("scan response = %d: %s", scanResponse.Code, scanResponse.Body.String())
	}
}

func TestCreateWorktreeCreatesExplicitNewBranch(t *testing.T) {
	repository := initializedRepository(t)
	workspaceService := workspace.NewService()
	if _, err := workspaceService.Open(context.Background(), repository); err != nil {
		t.Fatal(err)
	}
	terminalManager := terminal.NewManager(context.Background())
	handler := NewHTTP("secret", "test", "http://127.0.0.1", t.TempDir(), workspaceService, terminalManager, worktree.NewManagerAt(t.TempDir()), http.NotFoundHandler(), eventbus.New(), hookbridge.New(), orchestration.New(context.Background(), workspaceService, terminalManager))
	body := bytes.NewBufferString(`{"name":"Explicit branch","baseRef":"HEAD","newBranch":"feature/from-dialog"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/worktrees", body)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	snapshot, err := workspaceService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Worktrees) != 1 || snapshot.Worktrees[0].Branch != "feature/from-dialog" {
		t.Fatalf("worktree branch was not persisted: %#v", snapshot.Worktrees)
	}
	command := exec.Command("git", "-C", repository, "show-ref", "--verify", "refs/heads/feature/from-dialog")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("explicit branch was not created: %v: %s", err, output)
	}
}

func TestCreateNodeInIndependentWorktreeKeepsStandaloneContext(t *testing.T) {
	repository := t.TempDir()
	if output, err := exec.Command("git", "init", repository).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspaceService := workspace.NewService()
	if _, err := workspaceService.Open(context.Background(), repository); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceService.Update(func(snapshot *contracts.Snapshot) error {
		snapshot.Worktrees = append(snapshot.Worktrees, contracts.Worktree{ID: "independent", Name: "Independent", Branch: "feature/independent", BaseRef: "main", CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	terminalManager := terminal.NewManager(context.Background())
	handler := NewHTTP("secret", "test", "http://127.0.0.1", t.TempDir(), workspaceService, terminalManager, worktree.NewManagerAt(t.TempDir()), http.NotFoundHandler(), eventbus.New(), hookbridge.New(), orchestration.New(context.Background(), workspaceService, terminalManager))
	body := bytes.NewBufferString(`{"worktreeId":"independent","provider":"codex","model":"gpt-5.4","label":"Builder","role":"Implement"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/nodes", body)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	snapshot, err := workspaceService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Teams) != 0 || snapshot.Worktrees[0].TeamID != "" {
		t.Fatalf("standalone worktree was unexpectedly linked to a team: %#v %#v", snapshot.Worktrees, snapshot.Teams)
	}
	if len(snapshot.Nodes) != 1 {
		t.Fatalf("expected one standalone worktree agent, got %#v", snapshot.Nodes)
	}
	if snapshot.Nodes[0].WorktreeID != "independent" || snapshot.Nodes[0].TeamID != "" || snapshot.Nodes[0].Model != "gpt-5.4" {
		t.Fatalf("created agent has the wrong context: %#v", snapshot.Nodes[0])
	}
}

func TestDeleteTeamPreservesLinkedWorktreeAndItsAgents(t *testing.T) {
	repository := initializedRepository(t)
	workspaceService := workspace.NewService()
	if _, err := workspaceService.Open(context.Background(), repository); err != nil {
		t.Fatal(err)
	}
	initial, err := workspaceService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	manager := worktree.NewManagerAt(t.TempDir())
	branch, baseRef, path, err := manager.Create(context.Background(), initial, "linked", "Linked worktree")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceService.Update(func(snapshot *contracts.Snapshot) error {
		snapshot.Teams = []contracts.Team{{ID: "team", Name: "Team", Color: "#b7f34a"}}
		snapshot.Worktrees = []contracts.Worktree{{ID: "linked", TeamID: "team", Name: "Linked worktree", Branch: branch, BaseRef: baseRef, CreatedAt: time.Now().UTC()}}
		snapshot.Nodes = []contracts.Node{
			{ID: "lead", TeamID: "team", Kind: "orchestrator", Provider: "codex", Label: "Lead", Role: "Coordinate", Size: contracts.Size{Width: 300, Height: 200}},
			{ID: "worker", TeamID: "team", WorktreeID: "linked", Kind: "agent", Provider: "codex", Label: "Worker", Role: "Implement", Size: contracts.Size{Width: 300, Height: 200}},
		}
		snapshot.Edges = []contracts.Edge{{ID: "delegates", Source: "lead", Target: "worker", Type: "delegates_to"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	terminalManager := terminal.NewManager(context.Background())
	handler := NewHTTP("secret", "test", "http://127.0.0.1", t.TempDir(), workspaceService, terminalManager, manager, http.NotFoundHandler(), eventbus.New(), hookbridge.New(), orchestration.New(context.Background(), workspaceService, terminalManager))
	request := httptest.NewRequest(http.MethodDelete, "/api/teams/team", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("linked worktree was removed from disk: %v", err)
	}
	snapshot, err := workspaceService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Teams) != 0 || len(snapshot.Worktrees) != 1 || snapshot.Worktrees[0].TeamID != "" {
		t.Fatalf("worktree was not detached safely: %#v %#v", snapshot.Teams, snapshot.Worktrees)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].ID != "worker" || snapshot.Nodes[0].TeamID != "" || snapshot.Nodes[0].WorktreeID != "linked" {
		t.Fatalf("worktree agent was not preserved as standalone: %#v", snapshot.Nodes)
	}
	if len(snapshot.Edges) != 0 {
		t.Fatalf("definition edges were not removed: %#v", snapshot.Edges)
	}
}

func TestDeleteWorktreeRemovesRuntimeNodesAndPreservesTeamDefinition(t *testing.T) {
	repository := initializedRepository(t)
	workspaceService := workspace.NewService()
	if _, err := workspaceService.Open(context.Background(), repository); err != nil {
		t.Fatal(err)
	}
	initial, err := workspaceService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	manager := worktree.NewManagerAt(t.TempDir())
	branch, baseRef, path, err := manager.Create(context.Background(), initial, "runtime", "Runtime worktree")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceService.Update(func(snapshot *contracts.Snapshot) error {
		snapshot.Teams = []contracts.Team{{ID: "team", Name: "Delivery", Color: "#b7f34a", Branch: branch, BaseRef: baseRef}}
		snapshot.Worktrees = []contracts.Worktree{{ID: "runtime", TeamID: "team", Name: "Runtime worktree", Branch: branch, BaseRef: baseRef, CreatedAt: time.Now().UTC()}}
		snapshot.Nodes = []contracts.Node{
			{ID: "definition-lead", TeamID: "team", Kind: "orchestrator", Provider: "codex", Label: "Definition Lead", Role: "Coordinate", Size: contracts.Size{Width: 300, Height: 200}},
			{ID: "definition-worker", TeamID: "team", Kind: "agent", Provider: "codex", Label: "Definition Worker", Role: "Implement", Size: contracts.Size{Width: 300, Height: 200}},
			{ID: "runtime-lead", TeamID: "team", WorktreeID: "runtime", Kind: "orchestrator", Provider: "codex", Label: "Runtime Lead", Role: "Coordinate", Size: contracts.Size{Width: 300, Height: 200}},
			{ID: "runtime-worker", TeamID: "team", WorktreeID: "runtime", Kind: "agent", Provider: "codex", Label: "Runtime Worker", Role: "Implement", Size: contracts.Size{Width: 300, Height: 200}},
		}
		snapshot.Edges = []contracts.Edge{
			{ID: "definition-edge", Source: "definition-lead", Target: "definition-worker", Type: "delegates_to"},
			{ID: "runtime-edge", Source: "runtime-lead", Target: "runtime-worker", Type: "delegates_to"},
			{ID: "cross-edge", Source: "definition-lead", Target: "runtime-worker", Type: "delegates_to"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	terminalManager := terminal.NewManager(context.Background())
	transport := NewHTTP("secret", "test", "http://127.0.0.1", t.TempDir(), workspaceService, terminalManager, manager, http.NotFoundHandler(), eventbus.New(), hookbridge.New(), orchestration.New(context.Background(), workspaceService, terminalManager))
	transport.executions["team"] = contracts.TeamExecution{TeamID: "team", WorktreeID: "runtime", StartedNode: []string{"definition-lead", "definition-worker"}}
	request := httptest.NewRequest(http.MethodDelete, "/api/worktrees/runtime", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	transport.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
	snapshot, err := workspaceService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Teams) != 1 || snapshot.Teams[0].ID != "team" {
		t.Fatalf("Team definition was deleted: %#v", snapshot.Teams)
	}
	if len(snapshot.Worktrees) != 0 {
		t.Fatalf("worktree was not removed: %#v", snapshot.Worktrees)
	}
	if len(snapshot.Nodes) != 2 || snapshot.Nodes[0].ID != "definition-lead" || snapshot.Nodes[1].ID != "definition-worker" {
		t.Fatalf("runtime nodes or Team definition were not separated correctly: %#v", snapshot.Nodes)
	}
	if len(snapshot.Edges) != 1 || snapshot.Edges[0].ID != "definition-edge" {
		t.Fatalf("runtime edges were not removed correctly: %#v", snapshot.Edges)
	}
	if _, exists := transport.executions["team"]; exists {
		t.Fatal("active execution still references the removed worktree")
	}
}

func TestDeleteDirtyWorktreeKeepsNodesAndTeamDefinition(t *testing.T) {
	repository := initializedRepository(t)
	workspaceService := workspace.NewService()
	if _, err := workspaceService.Open(context.Background(), repository); err != nil {
		t.Fatal(err)
	}
	initial, err := workspaceService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	manager := worktree.NewManagerAt(t.TempDir())
	branch, baseRef, path, err := manager.Create(context.Background(), initial, "dirty", "Dirty worktree")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "uncommitted.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceService.Update(func(snapshot *contracts.Snapshot) error {
		snapshot.Teams = []contracts.Team{{ID: "team", Name: "Delivery", Color: "#b7f34a"}}
		snapshot.Worktrees = []contracts.Worktree{{ID: "dirty", TeamID: "team", Name: "Dirty worktree", Branch: branch, BaseRef: baseRef, CreatedAt: time.Now().UTC()}}
		snapshot.Nodes = []contracts.Node{
			{ID: "definition-lead", TeamID: "team", Kind: "orchestrator", Provider: "codex", Label: "Definition Lead", Role: "Coordinate", Size: contracts.Size{Width: 300, Height: 200}},
			{ID: "runtime-lead", TeamID: "team", WorktreeID: "dirty", Kind: "orchestrator", Provider: "codex", Label: "Runtime Lead", Role: "Coordinate", Size: contracts.Size{Width: 300, Height: 200}},
		}
		snapshot.Edges = []contracts.Edge{{ID: "runtime-edge", Source: "definition-lead", Target: "runtime-lead", Type: "delegates_to"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	terminalManager := terminal.NewManager(context.Background())
	transport := NewHTTP("secret", "test", "http://127.0.0.1", t.TempDir(), workspaceService, terminalManager, manager, http.NotFoundHandler(), eventbus.New(), hookbridge.New(), orchestration.New(context.Background(), workspaceService, terminalManager))
	request := httptest.NewRequest(http.MethodDelete, "/api/worktrees/dirty", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	transport.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "worktree_dirty") {
		t.Fatalf("response = %d: %s", response.Code, response.Body.String())
	}
	snapshot, err := workspaceService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Worktrees) != 1 || len(snapshot.Nodes) != 2 || len(snapshot.Edges) != 1 || len(snapshot.Teams) != 1 {
		t.Fatalf("dirty worktree changed the canvas: %#v", snapshot)
	}
}

func TestDeleteNodeRemovesAgentAndProtectsOrchestrator(t *testing.T) {
	repository := initializedRepository(t)
	workspaceService := workspace.NewService()
	if _, err := workspaceService.Open(context.Background(), repository); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceService.Update(func(snapshot *contracts.Snapshot) error {
		snapshot.Teams = []contracts.Team{{ID: "team", Name: "Team", Color: "#b7f34a"}}
		snapshot.Nodes = []contracts.Node{
			{ID: "lead", TeamID: "team", Kind: "orchestrator", Provider: "codex", Label: "Lead", Role: "Coordinate", Size: contracts.Size{Width: 300, Height: 200}},
			{ID: "worker", TeamID: "team", Kind: "agent", Provider: "codex", Label: "Worker", Role: "Implement", Size: contracts.Size{Width: 300, Height: 200}},
		}
		snapshot.Edges = []contracts.Edge{{ID: "delegates", Source: "lead", Target: "worker", Type: "delegates_to"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	terminalManager := terminal.NewManager(context.Background())
	handler := NewHTTP("secret", "test", "http://127.0.0.1", t.TempDir(), workspaceService, terminalManager, worktree.NewManagerAt(t.TempDir()), http.NotFoundHandler(), eventbus.New(), hookbridge.New(), orchestration.New(context.Background(), workspaceService, terminalManager))
	deleteRequest := func(id string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodDelete, "/api/nodes/"+id, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := deleteRequest("worker"); response.Code != http.StatusNoContent {
		t.Fatalf("worker delete = %d: %s", response.Code, response.Body.String())
	}
	snapshot, err := workspaceService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].ID != "lead" || len(snapshot.Edges) != 0 {
		t.Fatalf("agent or edge was not removed correctly: %#v %#v", snapshot.Nodes, snapshot.Edges)
	}
	if response := deleteRequest("lead"); response.Code != http.StatusConflict {
		t.Fatalf("orchestrator delete = %d: %s", response.Code, response.Body.String())
	}
}

func TestImportExistingTeamWorkflowIntoStandaloneWorktree(t *testing.T) {
	repository := initializedRepository(t)
	workspaceService := workspace.NewService()
	if _, err := workspaceService.Open(context.Background(), repository); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceService.Update(func(snapshot *contracts.Snapshot) error {
		snapshot.Teams = []contracts.Team{{ID: "team", Name: "Delivery", Color: "#b7f34a"}}
		snapshot.Worktrees = []contracts.Worktree{{ID: "tree", Name: "Feature", Branch: "feature/import", BaseRef: "main", CreatedAt: time.Now().UTC()}}
		snapshot.Nodes = []contracts.Node{
			{ID: "lead", TeamID: "team", Kind: "orchestrator", Provider: "codex", Label: "Lead", Role: "Coordinate", Size: contracts.Size{Width: 300, Height: 200}},
			{ID: "worker", TeamID: "team", Kind: "agent", Provider: "claude", Label: "Worker", Role: "Implement", Size: contracts.Size{Width: 300, Height: 200}},
		}
		snapshot.Edges = []contracts.Edge{{ID: "delegates", Source: "lead", Target: "worker", Type: "delegates_to"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	terminalManager := terminal.NewManager(context.Background())
	handler := NewHTTP("secret", "test", "http://127.0.0.1", t.TempDir(), workspaceService, terminalManager, worktree.NewManagerAt(t.TempDir()), http.NotFoundHandler(), eventbus.New(), hookbridge.New(), orchestration.New(context.Background(), workspaceService, terminalManager))
	request := httptest.NewRequest(http.MethodPost, "/api/worktrees/tree/teams/team/import", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	snapshot, err := workspaceService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Worktrees[0].TeamID != "team" || len(snapshot.Teams) != 1 {
		t.Fatalf("worktree was not associated with the existing Team: %#v %#v", snapshot.Worktrees, snapshot.Teams)
	}
	runtimeNodes := 0
	for _, node := range snapshot.Nodes {
		if node.WorktreeID == "tree" && node.TeamID == "team" {
			runtimeNodes++
		}
	}
	if runtimeNodes != 2 || len(snapshot.Edges) != 2 {
		t.Fatalf("imported workflow is incomplete: nodes=%#v edges=%#v", snapshot.Nodes, snapshot.Edges)
	}
}

func initializedRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "e2e@agent-infinite.local"}, {"config", "user.name", "Agent Infinite"}} {
		if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("# test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "initial"}} {
		if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return repository
}
