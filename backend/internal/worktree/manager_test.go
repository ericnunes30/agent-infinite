package worktree

import (
	"context"
	"errors"
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
	item := contracts.Worktree{ID: "team-dirty", Branch: branch}
	err = manager.CheckDelete(context.Background(), snapshot, item)
	if err != ErrDirty {
		t.Fatalf("CheckDelete() error = %v, want ErrDirty", err)
	}
	err = manager.Delete(context.Background(), snapshot, item)
	if err != ErrDirty {
		t.Fatalf("Delete() error = %v, want ErrDirty", err)
	}
}

func TestRestoreRecreatesDeletedWorktree(t *testing.T) {
	repository := initializedRepository(t)
	manager := NewManagerAt(filepath.Join(t.TempDir(), "worktrees"))
	snapshot := contracts.Snapshot{WorkspaceID: "workspace", WorkspacePath: repository}
	branch, baseRef, path, err := manager.Create(context.Background(), snapshot, "team-restore", "Restore")
	if err != nil {
		t.Fatal(err)
	}
	item := contracts.Worktree{ID: "team-restore", Branch: branch, BaseRef: baseRef}
	if err := manager.Delete(context.Background(), snapshot, item); err != nil {
		t.Fatal(err)
	}
	if err := manager.Restore(context.Background(), snapshot, item); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("restored worktree path: %v", err)
	}
	if output, err := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel").CombinedOutput(); err != nil {
		t.Fatalf("restored path is not a worktree: %v: %s", err, output)
	}
}

func TestCreateFromExistingAvailableBranch(t *testing.T) {
	repository := initializedRepository(t)
	command := exec.Command("git", "-C", repository, "branch", "feature/existing")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create branch: %v: %s", err, output)
	}
	manager := NewManagerAt(filepath.Join(t.TempDir(), "worktrees"))
	before, err := manager.Branches(context.Background(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if !containsBranch(before.Available, "feature/existing") {
		t.Fatalf("available branches = %v", before.Available)
	}
	snapshot := contracts.Snapshot{WorkspaceID: "workspace", WorkspacePath: repository}
	branch, _, path, err := manager.CreateFrom(context.Background(), snapshot, "existing-12345678", "Existing", "", "", "feature/existing")
	if err != nil {
		t.Fatal(err)
	}
	if branch != "feature/existing" {
		t.Fatalf("branch = %q", branch)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree path: %v", err)
	}
}

func TestCreateFromCreatesExplicitNewBranch(t *testing.T) {
	repository := initializedRepository(t)
	manager := NewManagerAt(filepath.Join(t.TempDir(), "worktrees"))
	snapshot := contracts.Snapshot{WorkspaceID: "workspace", WorkspacePath: repository}
	branch, _, path, err := manager.CreateFrom(context.Background(), snapshot, "new-12345678", "New", "HEAD", "feature/explicit", "")
	if err != nil {
		t.Fatal(err)
	}
	if branch != "feature/explicit" {
		t.Fatalf("branch = %q", branch)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree path: %v", err)
	}
	command := exec.Command("git", "-C", repository, "show-ref", "--verify", "refs/heads/feature/explicit")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("explicit branch was not created: %v: %s", err, output)
	}
}

func TestCreateFromRejectsInvalidNewBranch(t *testing.T) {
	repository := initializedRepository(t)
	manager := NewManagerAt(filepath.Join(t.TempDir(), "worktrees"))
	snapshot := contracts.Snapshot{WorkspaceID: "workspace", WorkspacePath: repository}
	_, _, _, err := manager.CreateFrom(context.Background(), snapshot, "invalid", "Invalid", "HEAD", "invalid branch", "")
	if !errors.Is(err, ErrInvalidBranch) {
		t.Fatalf("CreateFrom() error = %v, want ErrInvalidBranch", err)
	}
}

func containsBranch(branches []string, target string) bool {
	for _, branch := range branches {
		if branch == target {
			return true
		}
	}
	return false
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
