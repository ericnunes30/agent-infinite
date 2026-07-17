package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
)

func TestOpenValidGitRepository(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "init", root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}

	service := NewService()
	snapshot, err := service.Open(context.Background(), root)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if snapshot.SchemaVersion != 1 || snapshot.WorkspaceID == "" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if snapshot.Integration.Hooks != "auto" {
		t.Fatalf("default hook policy = %q, want auto", snapshot.Integration.Hooks)
	}
	if snapshot.WorkspacePath != filepath.Clean(root) {
		t.Fatalf("WorkspacePath = %q, want %q", snapshot.WorkspacePath, filepath.Clean(root))
	}
}

func TestUpdatePersistsAndReopensCanvas(t *testing.T) {
	root := t.TempDir()
	if output, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	service := NewService()
	opened, err := service.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Update(func(snapshot *contracts.Snapshot) error {
		snapshot.Integration.Hooks = "off"
		snapshot.Teams = append(snapshot.Teams, contracts.Team{ID: "team", Name: "Team", Color: "#fff", Branch: "branch", BaseRef: "HEAD", CreatedAt: time.Now()})
		snapshot.Nodes = append(snapshot.Nodes, contracts.Node{ID: "lead", TeamID: "team", Kind: "orchestrator", Provider: "claude", Label: "Lead", Size: contracts.Size{Width: 300, Height: 200}})
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".agent-infinite", "canvas.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "workspacePath") {
		t.Fatal("runtime workspacePath was persisted")
	}
	reopened, err := NewService().Open(context.Background(), root)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	if reopened.WorkspaceID != opened.WorkspaceID || len(reopened.Teams) != 1 || len(reopened.Worktrees) != 1 || reopened.Worktrees[0].ID != "team" || reopened.Nodes[0].WorktreeID != "team" || reopened.Integration.Hooks != "off" {
		t.Fatalf("unexpected reopened snapshot: %#v", reopened)
	}
}

func TestInvalidCanvasIsNeverOverwritten(t *testing.T) {
	root := t.TempDir()
	if output, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	directory := filepath.Join(root, ".agent-infinite")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("{ definitely not json")
	path := filepath.Join(directory, "canvas.json")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewService().Open(context.Background(), root)
	if !errors.Is(err, ErrInvalidCanvas) {
		t.Fatalf("Open() error = %v, want ErrInvalidCanvas", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(original) {
		t.Fatalf("invalid canvas was overwritten: %q", after)
	}
}

func TestOpenRejectsNonGitDirectory(t *testing.T) {
	_, err := NewService().Open(context.Background(), t.TempDir())
	if err != ErrNotGit {
		t.Fatalf("Open() error = %v, want ErrNotGit", err)
	}
}

func TestSnapshotRequiresOpenWorkspace(t *testing.T) {
	_, err := NewService().Snapshot()
	if err != ErrNotOpen {
		t.Fatalf("Snapshot() error = %v, want ErrNotOpen", err)
	}
}
