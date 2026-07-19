package terminal

import (
	"context"
	"strings"
	"testing"

	"github.com/agent-infinite/agent-infinite/backend/internal/detector"
)

func TestPreviewTrimsBlankViewportAndKeepsUnicodeTail(t *testing.T) {
	got := preview("old\nMOCK_DONE: ação"+strings.Repeat(" ", 500), 16)
	if got != "\nMOCK_DONE: ação" {
		t.Fatalf("preview() = %q", got)
	}
}

func TestRuntimeRecoveryIncludesCheapTerminalPreview(t *testing.T) {
	screen := detector.NewScreen(80, 24)
	screen.Write([]byte("provider ready\r\n> "))
	session := &Session{id: "session", screen: screen, status: detector.Idle}
	manager := NewManager(context.Background())
	manager.nodes["node"] = session.id
	manager.sessions[session.id] = session
	runtimes := manager.Runtimes()
	if len(runtimes) != 1 || !strings.Contains(runtimes[0].Preview, "provider ready") {
		t.Fatalf("runtime preview was not recovered: %#v", runtimes)
	}
}

func TestRuntimeTracksMCPConnectionSeparatelyFromHooks(t *testing.T) {
	screen := detector.NewScreen(80, 24)
	session := &Session{id: "session", screen: screen, status: detector.Idle, integrationMode: "hooks-pending"}
	manager := NewManager(context.Background())
	manager.nodes["node"] = session.id
	manager.sessions[session.id] = session

	if !manager.MarkMCPConnected("node") {
		t.Fatal("first MCP contact was not reported as a state change")
	}
	if manager.MarkMCPConnected("node") {
		t.Fatal("duplicate MCP contact was reported as a state change")
	}
	runtimes := manager.Runtimes()
	if len(runtimes) != 1 || !runtimes[0].MCPConnected || runtimes[0].IntegrationMode != "hooks-pending" {
		t.Fatalf("independent integration state was not preserved: %#v", runtimes)
	}
}

func TestLifecycleReadyUpdatesLiveNodeSession(t *testing.T) {
	screen := detector.NewScreen(80, 24)
	session := &Session{id: "session", screen: screen, status: detector.Working, alive: true}
	manager := NewManager(context.Background())
	manager.nodes["node"] = session.id
	manager.sessions[session.id] = session

	if !manager.SetLifecycleReady("node", true) {
		t.Fatal("SessionStart did not find the live node")
	}
	session.mu.Lock()
	observed, ready := session.lifecycleObserved, session.lifecycleReady
	session.mu.Unlock()
	if !observed || !ready {
		t.Fatalf("SessionStart readiness was not persisted: observed=%t ready=%t", observed, ready)
	}
	if !manager.SetLifecycleReady("node", false) || session.Status() != detector.Working {
		t.Fatalf("before_agent_start did not mark the node busy: %s", session.Status())
	}
	if manager.SetLifecycleReady("missing", true) {
		t.Fatal("missing node was reported as updated")
	}
}

func TestRepeatedTerminalAttachDetachDoesNotLeakSubscribers(t *testing.T) {
	session := &Session{ring: NewRing(1024), subs: make(map[uint64]chan []byte)}
	for range 250 {
		_, _, detach := session.Attach()
		detach()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.subs) != 0 {
		t.Fatalf("detached terminal subscribers leaked: %d", len(session.subs))
	}
}
