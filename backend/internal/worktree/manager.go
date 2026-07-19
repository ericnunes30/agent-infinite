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
	ErrDirty         = errors.New("worktree has uncommitted changes")
	ErrInvalid       = errors.New("worktree is invalid")
	ErrInvalidBranch = errors.New("branch name is invalid")
)

type Manager struct {
	root string
	mu   sync.Mutex
}

type Branches struct {
	All       []string `json:"all"`
	Available []string `json:"available"`
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
	return m.CreateFrom(ctx, snapshot, worktreeID, name, "", "", "")
}

// CreateFrom creates a new branch from baseRef, or checks out an existing available branch.
func (m *Manager) CreateFrom(ctx context.Context, snapshot contracts.Snapshot, worktreeID, name, baseRef, newBranch, existingBranch string) (string, string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	newBranch = strings.TrimSpace(newBranch)
	existingBranch = strings.TrimSpace(existingBranch)
	if newBranch != "" && existingBranch != "" {
		return "", "", "", fmt.Errorf("%w: choose either a new or an existing branch", ErrInvalidBranch)
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}
	resolvedBase, err := gitOutput(ctx, snapshot.WorkspacePath, "rev-parse", baseRef)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve base ref: %w", err)
	}
	branch := existingBranch
	if branch == "" {
		branch = newBranch
		if branch == "" {
			branch = fmt.Sprintf("agent-infinite/%s-%s", slug(name), short(worktreeID))
		}
	}
	if _, err := gitOutput(ctx, snapshot.WorkspacePath, "check-ref-format", "--branch", branch); err != nil {
		return "", "", "", fmt.Errorf("%w: %s", ErrInvalidBranch, branch)
	}
	path := m.Path(snapshot.WorkspaceID, worktreeID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", "", err
	}
	args := []string{"worktree", "add"}
	if existingBranch == "" {
		args = append(args, "-b", branch, path, resolvedBase)
	} else {
		args = append(args, path, branch)
	}
	if _, err := gitOutput(ctx, snapshot.WorkspacePath, args...); err != nil {
		return "", "", "", fmt.Errorf("create worktree: %w", err)
	}
	return branch, resolvedBase, path, nil
}

func (m *Manager) Branches(ctx context.Context, repository string) (Branches, error) {
	output, err := gitOutput(ctx, repository, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return Branches{}, err
	}
	checkedOutput, err := gitOutput(ctx, repository, "worktree", "list", "--porcelain")
	if err != nil {
		return Branches{}, err
	}
	checked := map[string]bool{}
	for _, line := range strings.Split(checkedOutput, "\n") {
		if strings.HasPrefix(line, "branch refs/heads/") {
			checked[strings.TrimPrefix(line, "branch refs/heads/")] = true
		}
	}
	branches := Branches{All: []string{}, Available: []string{}}
	for _, branch := range strings.Split(output, "\n") {
		branch = strings.TrimSpace(branch)
		if branch == "" {
			continue
		}
		branches.All = append(branches.All, branch)
		if !checked[branch] {
			branches.Available = append(branches.Available, branch)
		}
	}
	return branches, nil
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
	path, err := m.deletePath(ctx, snapshot, worktree)
	if err != nil {
		return err
	}
	if _, err := gitOutput(ctx, snapshot.WorkspacePath, "worktree", "remove", path); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	return nil
}

// CheckDelete verifies the worktree can be removed without modifying the Git
// checkout. Callers use it before stopping sessions or changing the canvas so
// a dirty checkout leaves the running workflow untouched.
func (m *Manager) CheckDelete(ctx context.Context, snapshot contracts.Snapshot, worktree contracts.Worktree) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.deletePath(ctx, snapshot, worktree)
	return err
}

// Restore recreates a checkout after the Git removal succeeded but the canvas
// transaction could not be persisted. It is intentionally narrow: unlike
// Reconcile, it never changes unrelated worktrees.
func (m *Manager) Restore(ctx context.Context, snapshot contracts.Snapshot, worktree contracts.Worktree) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path := m.Path(snapshot.WorkspaceID, worktree.ID)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		root, rootErr := gitOutput(ctx, path, "rev-parse", "--show-toplevel")
		if rootErr == nil && filepath.Clean(root) == filepath.Clean(path) {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrInvalid, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := gitOutput(ctx, snapshot.WorkspacePath, "worktree", "add", path, worktree.Branch); err != nil {
		return fmt.Errorf("restore worktree: %w", err)
	}
	return nil
}

func (m *Manager) deletePath(ctx context.Context, snapshot contracts.Snapshot, worktree contracts.Worktree) (string, error) {
	path := m.Path(snapshot.WorkspaceID, worktree.ID)
	status, err := gitOutput(ctx, path, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("inspect worktree: %w", err)
	}
	if status != "" {
		return "", ErrDirty
	}
	return path, nil
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
