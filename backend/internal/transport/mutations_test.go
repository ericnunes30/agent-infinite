package transport

import (
	"testing"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
)

func TestSessionConnectionsDescribeBothSidesOfCanvasDelegation(t *testing.T) {
	snapshot := contracts.Snapshot{
		Nodes: []contracts.Node{
			{ID: "lead", Label: "Release Lead", Role: "Coordinate", Kind: "orchestrator", Provider: "codex"},
			{ID: "reviewer", Label: "Reviewer", Role: "Review", Kind: "agent", Provider: "claude"},
		},
		Edges: []contracts.Edge{{ID: "edge", Source: "lead", Target: "reviewer", Type: "delegates_to"}},
	}

	lead := sessionConnections(snapshot, "lead")
	if len(lead) != 1 || lead[0].ID != "reviewer" || lead[0].Direction != "delegates_to" {
		t.Fatalf("orchestrator topology = %#v", lead)
	}
	worker := sessionConnections(snapshot, "reviewer")
	if len(worker) != 1 || worker[0].ID != "lead" || worker[0].Direction != "delegated_by" {
		t.Fatalf("worker topology = %#v", worker)
	}
}
