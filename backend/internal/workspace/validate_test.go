package workspace

import (
	"strings"
	"testing"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
)

func TestValidateRejectsDelegationCycle(t *testing.T) {
	snapshot := contracts.Snapshot{
		SchemaVersion: 1,
		WorkspaceID:   "workspace",
		Teams:         []contracts.Team{{ID: "one"}, {ID: "two"}},
		Nodes: []contracts.Node{
			{ID: "one-lead", TeamID: "one", Kind: "orchestrator", Provider: "claude"},
			{ID: "two-lead", TeamID: "two", Kind: "orchestrator", Provider: "codex"},
		},
		Edges: []contracts.Edge{
			{ID: "a", Source: "one-lead", Target: "two-lead", Type: "delegates_to"},
			{ID: "b", Source: "two-lead", Target: "one-lead", Type: "delegates_to"},
		},
		Viewport: contracts.Viewport{Zoom: 1},
	}
	if err := Validate(snapshot); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("Validate() error = %v, want cycle error", err)
	}
}

func TestValidateAllowsMockOnlyInTestMode(t *testing.T) {
	snapshot := contracts.Snapshot{
		SchemaVersion: 1,
		WorkspaceID:   "workspace",
		Teams:         []contracts.Team{{ID: "one"}},
		Nodes:         []contracts.Node{{ID: "lead", TeamID: "one", Kind: "orchestrator", Provider: "mock"}},
		Edges:         []contracts.Edge{},
		Viewport:      contracts.Viewport{Zoom: 1},
	}
	t.Setenv("AGENT_INFINITE_TEST_MODE", "")
	if err := Validate(snapshot); err == nil {
		t.Fatal("expected mock provider to be rejected outside test mode")
	}
	t.Setenv("AGENT_INFINITE_TEST_MODE", "1")
	if err := Validate(snapshot); err != nil {
		t.Fatalf("Validate() in test mode: %v", err)
	}
}
