package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/agent"
	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/eventbus"
	"github.com/agent-infinite/agent-infinite/backend/internal/hookbridge"
	"github.com/agent-infinite/agent-infinite/backend/internal/orchestration"
	"github.com/agent-infinite/agent-infinite/backend/internal/terminal"
	"github.com/agent-infinite/agent-infinite/backend/internal/workspace"
	"github.com/agent-infinite/agent-infinite/backend/internal/worktree"
)

func TestDeleteWorktreeStopsAssignedTerminal(t *testing.T) {
	t.Setenv("AGENT_INFINITE_TEST_MODE", "1")
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
		snapshot.Worktrees = []contracts.Worktree{{ID: "runtime", Name: "Runtime worktree", Branch: branch, BaseRef: baseRef, CreatedAt: time.Now().UTC()}}
		snapshot.Nodes = []contracts.Node{{
			ID: "runtime-agent", WorktreeID: "runtime", Kind: "agent", Provider: "codex", Label: "Runtime agent", Role: "Implement",
			Size: contracts.Size{Width: 300, Height: 200},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	terminalManager := terminal.NewManager(context.Background())
	defer terminalManager.CloseAll()
	spec, err := agent.BuildLaunch(agent.LaunchOptions{
		Provider: "mock", WorkDir: path, RuntimeDir: t.TempDir(), NodeID: "runtime-agent", MCPBaseURL: "http://127.0.0.1", MCPToken: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := terminalManager.StartNode("runtime-agent", spec); err != nil {
		t.Fatal(err)
	}
	transport := NewHTTP("secret", "test", "http://127.0.0.1", t.TempDir(), workspaceService, terminalManager, manager, http.NotFoundHandler(), eventbus.New(), hookbridge.New(), orchestration.New(context.Background(), workspaceService, terminalManager))
	request := httptest.NewRequest(http.MethodDelete, "/api/worktrees/runtime", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	transport.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if _, err := terminalManager.GetByNode("runtime-agent"); !errors.Is(err, terminal.ErrSessionNotFound) {
		t.Fatalf("worktree terminal remained active: %v", err)
	}
}
