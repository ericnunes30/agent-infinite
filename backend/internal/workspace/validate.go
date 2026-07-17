package workspace

import (
	"fmt"
	"os"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
)

func Validate(snapshot contracts.Snapshot) error {
	if snapshot.SchemaVersion != schemaVersion {
		return fmt.Errorf("unsupported schemaVersion %d", snapshot.SchemaVersion)
	}
	if snapshot.WorkspaceID == "" {
		return fmt.Errorf("workspaceId is required")
	}
	if snapshot.Teams == nil || snapshot.Nodes == nil || snapshot.Edges == nil {
		return fmt.Errorf("teams, nodes, and edges must be arrays")
	}
	if snapshot.Viewport.Zoom <= 0 {
		return fmt.Errorf("viewport zoom must be positive")
	}
	if snapshot.Integration.Hooks != "" && snapshot.Integration.Hooks != "auto" && snapshot.Integration.Hooks != "off" && snapshot.Integration.Hooks != "required" {
		return fmt.Errorf("integration hooks must be auto, off, or required")
	}
	teams := make(map[string]struct{}, len(snapshot.Teams))
	orchestrators := make(map[string]int, len(snapshot.Teams))
	for _, team := range snapshot.Teams {
		if team.ID == "" {
			return fmt.Errorf("team id is required")
		}
		if _, duplicate := teams[team.ID]; duplicate {
			return fmt.Errorf("duplicate team id %q", team.ID)
		}
		teams[team.ID] = struct{}{}
	}
	worktrees := make(map[string]contracts.Worktree, len(snapshot.Worktrees))
	for _, worktree := range snapshot.Worktrees {
		if worktree.ID == "" {
			return fmt.Errorf("worktree id is required")
		}
		if _, duplicate := worktrees[worktree.ID]; duplicate {
			return fmt.Errorf("duplicate worktree id %q", worktree.ID)
		}
		if _, exists := teams[worktree.TeamID]; !exists {
			return fmt.Errorf("worktree %q references unknown team", worktree.ID)
		}
		if worktree.Branch == "" {
			return fmt.Errorf("worktree %q branch is required", worktree.ID)
		}
		worktrees[worktree.ID] = worktree
	}
	nodes := make(map[string]contracts.Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if _, exists := teams[node.TeamID]; !exists {
			return fmt.Errorf("node %q references unknown team", node.ID)
		}
		if node.WorktreeID != "" {
			worktree, exists := worktrees[node.WorktreeID]
			if !exists || worktree.TeamID != node.TeamID {
				return fmt.Errorf("node %q references an invalid worktree", node.ID)
			}
		}
		if _, duplicate := nodes[node.ID]; duplicate || node.ID == "" {
			return fmt.Errorf("duplicate or empty node id %q", node.ID)
		}
		if node.Kind != "agent" && node.Kind != "orchestrator" {
			return fmt.Errorf("node %q has invalid kind", node.ID)
		}
		if node.Provider != "claude" && node.Provider != "codex" && !(node.Provider == "mock" && os.Getenv("AGENT_INFINITE_TEST_MODE") == "1") {
			return fmt.Errorf("node %q has invalid provider", node.ID)
		}
		if node.Kind == "orchestrator" {
			orchestrators[node.TeamID]++
		}
		nodes[node.ID] = node
	}
	for teamID := range teams {
		if orchestrators[teamID] != 1 {
			return fmt.Errorf("team %q must have exactly one orchestrator", teamID)
		}
	}
	adjacency := make(map[string][]string)
	edges := make(map[string]struct{}, len(snapshot.Edges))
	for _, edge := range snapshot.Edges {
		if _, duplicate := edges[edge.ID]; duplicate || edge.ID == "" {
			return fmt.Errorf("duplicate or empty edge id %q", edge.ID)
		}
		edges[edge.ID] = struct{}{}
		source, sourceExists := nodes[edge.Source]
		_, targetExists := nodes[edge.Target]
		if !sourceExists || !targetExists || source.Kind != "orchestrator" || edge.Type != "delegates_to" || edge.Source == edge.Target {
			return fmt.Errorf("edge %q violates delegation invariants", edge.ID)
		}
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
	}
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) bool
	visit = func(node string) bool {
		if visiting[node] {
			return true
		}
		if visited[node] {
			return false
		}
		visiting[node] = true
		for _, target := range adjacency[node] {
			if visit(target) {
				return true
			}
		}
		visiting[node] = false
		visited[node] = true
		return false
	}
	for node := range nodes {
		if visit(node) {
			return fmt.Errorf("delegation graph contains a cycle")
		}
	}
	return nil
}
