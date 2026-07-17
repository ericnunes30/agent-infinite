package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
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
			go httpTransport.watchHookActivation("node", session.ID, test.policy)
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
