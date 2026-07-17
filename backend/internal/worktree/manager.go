package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
)

var (
	ErrDirty   = errors.New("worktree has uncommitted changes")
	ErrInvalid = errors.New("worktree is invalid")
)

type Manager struct {
	root string
	mu   sync.Mutex
}

func NewManager() *Manager {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		root = os.TempDir()
	}
	return &Manager{root: filepath.Join(root, "AgentInfinite", "worktrees")}
}

func NewManagerAt(root string) *Manager { return &Manager{root: root} }

func (m *Manager) Create(ctx context.Context, snapshot contracts.Snapshot, worktreeID, name string) (string, string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	baseRef, err := gitOutput(ctx, snapshot.WorkspacePath, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("resolve HEAD: %w", err)
	}
	branch := fmt.Sprintf("agent-infinite/%s-%s", slug(name), short(worktreeID))
	path := m.Path(snapshot.WorkspaceID, worktreeID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", "", err
	}
	if _, err := gitOutput(ctx, snapshot.WorkspacePath, "worktree", "add", "-b", branch, path, baseRef); err != nil {
		return "", "", "", fmt.Errorf("create worktree: %w", err)
	}
	return branch, baseRef, path, nil
}

func (m *Manager) Reconcile(ctx context.Context, snapshot contracts.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, worktree := range worktreesFor(snapshot) {
		path := m.Path(snapshot.WorkspaceID, worktree.ID)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			root, err := gitOutput(ctx, path, "rev-parse", "--show-toplevel")
			if err != nil || filepath.Clean(root) != filepath.Clean(path) {
				return fmt.Errorf("%w: %s", ErrInvalid, path)
			}
			continue
		}
		if _, err := gitOutput(ctx, snapshot.WorkspacePath, "show-ref", "--verify", "refs/heads/"+worktree.Branch); err != nil {
			return fmt.Errorf("%w: missing branch %s", ErrInvalid, worktree.Branch)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if _, err := gitOutput(ctx, snapshot.WorkspacePath, "worktree", "add", path, worktree.Branch); err != nil {
			return fmt.Errorf("reconcile worktree: %w", err)
		}
	}
	return nil
}

func (m *Manager) Delete(ctx context.Context, snapshot contracts.Snapshot, worktree contracts.Worktree) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path := m.Path(snapshot.WorkspaceID, worktree.ID)
	status, err := gitOutput(ctx, path, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("inspect worktree: %w", err)
	}
	if status != "" {
		return ErrDirty
	}
	if _, err := gitOutput(ctx, snapshot.WorkspacePath, "worktree", "remove", path); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	return nil
}

func worktreesFor(snapshot contracts.Snapshot) []contracts.Worktree {
	if len(snapshot.Worktrees) > 0 {
		return snapshot.Worktrees
	}
	legacy := make([]contracts.Worktree, 0, len(snapshot.Teams))
	for _, team := range snapshot.Teams {
		if team.Branch == "" {
			continue
		}
		legacy = append(legacy, contracts.Worktree{
			ID: team.ID, TeamID: team.ID, Name: team.Name, Branch: team.Branch, BaseRef: team.BaseRef, CreatedAt: team.CreatedAt,
		})
	}
	return legacy
}

func (m *Manager) Path(workspaceID, worktreeID string) string {
	return filepath.Join(m.root, workspaceID, worktreeID)
}

func gitOutput(ctx context.Context, repository string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", repository}, args...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(value string) string {
	value = nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "team"
	}
	if len(value) > 32 {
		value = strings.Trim(value[:32], "-")
	}
	return value
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
