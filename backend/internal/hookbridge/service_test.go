package hookbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionTokenIsNodeAndWorkspaceScoped(t *testing.T) {
	service := New()
	session := service.Register("reviewer", "workspace-a", "codex", "hooks")
	token := service.Token(session.ID)
	raw := json.RawMessage(`{"hook_event_name":"SessionStart"}`)
	event, err := service.Handle(token, Callback{SessionID: session.ID, NodeID: "reviewer", WorkspaceID: "workspace-a", Provider: "codex", Raw: raw})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if event.Name != "SessionStart" || event.Session.Mode != "hooks" {
		t.Fatalf("unexpected event: %#v", event)
	}
	_, err = service.Handle(token, Callback{SessionID: session.ID, NodeID: "other", WorkspaceID: "workspace-a", Provider: "codex", Raw: json.RawMessage(`{"hook_event_name":"Stop"}`)})
	if err != ErrMismatch {
		t.Fatalf("identity mismatch error = %v", err)
	}
	service.Close(session.ID)
	_, err = service.Handle(token, Callback{SessionID: session.ID, NodeID: "reviewer", WorkspaceID: "workspace-a", Provider: "codex", Raw: json.RawMessage(`{"hook_event_name":"Stop"}`)})
	if err != ErrUnauthorized {
		t.Fatalf("closed session error = %v", err)
	}
}

func TestDuplicateCallbackIsRejected(t *testing.T) {
	service := New()
	session := service.Register("node", "workspace", "claude", "hooks")
	start := Callback{SessionID: session.ID, NodeID: "node", WorkspaceID: "workspace", Provider: "claude", Raw: json.RawMessage(`{"hook_event_name":"SessionStart"}`)}
	if _, err := service.Handle(service.Token(session.ID), start); err != nil {
		t.Fatal(err)
	}
	callback := Callback{SessionID: session.ID, NodeID: "node", WorkspaceID: "workspace", Provider: "claude", Raw: json.RawMessage(`{"hook_event_name":"Stop","turn":"one"}`)}
	if _, err := service.Handle(service.Token(session.ID), callback); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Handle(service.Token(session.ID), callback); err != ErrDuplicate {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestCallbacksRejectOutOfOrderAndExpiredSessions(t *testing.T) {
	service := New()
	session := service.Register("node", "workspace", "codex", "hooks")
	token := service.Token(session.ID)
	stop := Callback{SessionID: session.ID, NodeID: "node", WorkspaceID: "workspace", Provider: "codex", Raw: json.RawMessage(`{"hook_event_name":"Stop"}`)}
	if _, err := service.Handle(token, stop); err != ErrOutOfOrder {
		t.Fatalf("pre-start Stop error = %v", err)
	}
	service.mu.Lock()
	service.sessions[session.ID].ExpiresAt = time.Now().Add(-time.Second)
	service.mu.Unlock()
	start := Callback{SessionID: session.ID, NodeID: "node", WorkspaceID: "workspace", Provider: "codex", Raw: json.RawMessage(`{"hook_event_name":"SessionStart"}`)}
	if _, err := service.Handle(token, start); err != ErrUnauthorized {
		t.Fatalf("expired session error = %v", err)
	}
}

func TestForwardUsesEnvironmentCredentialsAndRawStdin(t *testing.T) {
	var received Callback
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Agent-Infinite-Hook-Token") != "secret" {
			t.Errorf("unexpected token")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"hookOutput":"{\"hookSpecificOutput\":{\"additionalContext\":\"canvas identity\"}}"}`))
	}))
	defer server.Close()
	t.Setenv("AGENT_INFINITE_BACKEND_URL", server.URL)
	t.Setenv("AGENT_INFINITE_HOOK_TOKEN", "secret")
	t.Setenv("AGENT_INFINITE_HOOK_SESSION_ID", "session")
	t.Setenv("AGENT_INFINITE_NODE_ID", "node")
	t.Setenv("AGENT_INFINITE_WORKSPACE_ID", "workspace")
	t.Setenv("AGENT_INFINITE_PROVIDER", "codex")
	raw := []byte(`{"hook_event_name":"Stop","transcript":"done"}`)
	var stdout bytes.Buffer
	if err := Forward(context.Background(), bytes.NewReader(raw), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if received.SessionID != "session" || !bytes.Equal(received.Raw, raw) {
		t.Fatalf("received = %#v", received)
	}
	if !strings.Contains(stdout.String(), "canvas identity") {
		t.Fatalf("forwarded hook stdout = %q", stdout.String())
	}
}
