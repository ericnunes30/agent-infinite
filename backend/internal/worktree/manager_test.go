package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
)

func TestCreateReconcileAndDeleteWorktree(t *testing.T) {
	repository := initializedRepository(t)
	manager := NewManagerAt(filepath.Join(t.TempDir(), "worktrees"))
	snapshot := contracts.Snapshot{WorkspaceID: "workspace", WorkspacePath: repository}
	branch, baseRef, path, err := manager.Create(context.Background(), snapshot, "team-12345678", "Build Team")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if branch != "agent-infinite/build-team-team-123" || baseRef == "" {
		t.Fatalf("unexpected branch/base: %q %q", branch, baseRef)
	}
	item := contracts.Worktree{ID: "team-12345678", TeamID: "team-12345678", Branch: branch}
	snapshot.Worktrees = []contracts.Worktree{item}
	if err := manager.Reconcile(context.Background(), snapshot); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if err := manager.Delete(context.Background(), snapshot, item); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
	command := exec.Command("git", "-C", repository, "show-ref", "--verify", "refs/heads/"+branch)
	if err := command.Run(); err != nil {
		t.Fatalf("branch was not preserved: %v", err)
	}
}

func TestDirtyWorktreeCannotBeDeleted(t *testing.T) {
	repository := initializedRepository(t)
	manager := NewManagerAt(filepath.Join(t.TempDir(), "worktrees"))
	snapshot := contracts.Snapshot{WorkspaceID: "workspace", WorkspacePath: repository}
	branch, _, path, err := manager.Create(context.Background(), snapshot, "team-dirty", "Dirty")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = manager.Delete(context.Background(), snapshot, contracts.Worktree{ID: "team-dirty", Branch: branch})
	if err != ErrDirty {
		t.Fatalf("Delete() error = %v, want ErrDirty", err)
	}
}

func initializedRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	commands := [][]string{
		{"init"},
		{"config", "user.email", "tests@agent-infinite.local"},
		{"config", "user.name", "Agent Infinite Tests"},
		{"commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range commands {
		command := exec.Command("git", append([]string{"-C", repository}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return repository
}
