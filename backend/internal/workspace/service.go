package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
)

const schemaVersion = 1

var (
	ErrNotOpen       = errors.New("workspace is not open")
	ErrNotDirectory  = errors.New("workspace path is not a directory")
	ErrNotGit        = errors.New("workspace path is not a git repository")
	ErrInvalidCanvas = errors.New("canvas.json is invalid")
)

type Service struct {
	mu         sync.RWMutex
	snapshot   *contracts.Snapshot
	canvasPath string
}

func NewService() *Service { return &Service{} }

func (s *Service) Open(ctx context.Context, path string) (contracts.Snapshot, error) {
	root, err := gitRoot(ctx, path)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	directory := filepath.Join(root, ".agent-infinite")
	if err := os.MkdirAll(filepath.Join(directory, "runtime"), 0o755); err != nil {
		return contracts.Snapshot{}, fmt.Errorf("create workspace metadata: %w", err)
	}
	ignorePath := filepath.Join(directory, ".gitignore")
	if _, err := os.Stat(ignorePath); errors.Is(err, os.ErrNotExist) {
		if err := atomicWrite(ignorePath, []byte("runtime/\n"), 0o644); err != nil {
			return contracts.Snapshot{}, err
		}
	}

	canvasPath := filepath.Join(directory, "canvas.json")
	snapshot, exists, err := loadCanvas(canvasPath)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	if !exists {
		snapshot = emptySnapshot(root)
		if err := persistCanvas(canvasPath, snapshot); err != nil {
			return contracts.Snapshot{}, err
		}
	} else if normalizeSnapshot(&snapshot) {
		if err := persistCanvas(canvasPath, snapshot); err != nil {
			return contracts.Snapshot{}, err
		}
	}
	if err := Validate(snapshot); err != nil {
		return contracts.Snapshot{}, fmt.Errorf("%w: %v", ErrInvalidCanvas, err)
	}
	snapshot.WorkspacePath = root
	s.mu.Lock()
	s.snapshot = &snapshot
	s.canvasPath = canvasPath
	s.mu.Unlock()
	return clone(snapshot), nil
}

func (s *Service) Snapshot() (contracts.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot == nil {
		return contracts.Snapshot{}, ErrNotOpen
	}
	return clone(*s.snapshot), nil
}

func (s *Service) Update(mutator func(*contracts.Snapshot) error) (contracts.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot == nil {
		return contracts.Snapshot{}, ErrNotOpen
	}
	next := clone(*s.snapshot)
	if err := mutator(&next); err != nil {
		return contracts.Snapshot{}, err
	}
	if err := Validate(next); err != nil {
		return contracts.Snapshot{}, err
	}
	if err := persistCanvas(s.canvasPath, next); err != nil {
		return contracts.Snapshot{}, err
	}
	s.snapshot = &next
	return clone(next), nil
}

func gitRoot(ctx context.Context, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", ErrNotDirectory
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", ErrNotDirectory
	}
	output, err := exec.CommandContext(ctx, "git", "-C", abs, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", ErrNotGit
	}
	return filepath.Clean(strings.TrimSpace(string(output))), nil
}

func emptySnapshot(root string) contracts.Snapshot {
	sum := sha256.Sum256([]byte(strings.ToLower(root)))
	return contracts.Snapshot{
		SchemaVersion: schemaVersion,
		WorkspaceID:   hex.EncodeToString(sum[:8]),
		WorkspacePath: root,
		Teams:         []contracts.Team{},
		Worktrees:     []contracts.Worktree{},
		Nodes:         []contracts.Node{},
		Edges:         []contracts.Edge{},
		Viewport:      contracts.Viewport{Zoom: 1},
		Integration:   contracts.Integration{Hooks: "auto"},
	}
}

func loadCanvas(path string) (contracts.Snapshot, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return contracts.Snapshot{}, false, nil
	}
	if err != nil {
		return contracts.Snapshot{}, false, fmt.Errorf("read canvas: %w", err)
	}
	var snapshot contracts.Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return contracts.Snapshot{}, true, fmt.Errorf("%w: %v", ErrInvalidCanvas, err)
	}
	// schema v1 workspaces created before provider hooks existed are migrated in
	// memory to the safe default and persisted on the next explicit update.
	if snapshot.Integration.Hooks == "" {
		snapshot.Integration.Hooks = "auto"
	}
	return snapshot, true, nil
}

// normalizeSnapshot adds the worktree relation to schema v1 canvases without
// changing their identifiers or moving their existing checkout directories.
func normalizeSnapshot(snapshot *contracts.Snapshot) bool {
	changed := false
	if snapshot.Worktrees == nil {
		snapshot.Worktrees = []contracts.Worktree{}
		changed = true
	}
	for _, team := range snapshot.Teams {
		found := false
		for _, worktree := range snapshot.Worktrees {
			if worktree.TeamID == team.ID {
				found = true
				break
			}
		}
		if found || team.Branch == "" {
			continue
		}
		snapshot.Worktrees = append(snapshot.Worktrees, contracts.Worktree{
			ID: team.ID, TeamID: team.ID, Name: team.Name, Branch: team.Branch,
			BaseRef: team.BaseRef, CreatedAt: team.CreatedAt,
		})
		changed = true
	}
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].WorktreeID != "" {
			continue
		}
		for _, worktree := range snapshot.Worktrees {
			if worktree.TeamID == snapshot.Nodes[index].TeamID {
				snapshot.Nodes[index].WorktreeID = worktree.ID
				changed = true
				break
			}
		}
	}
	return changed
}

func persistCanvas(path string, snapshot contracts.Snapshot) error {
	snapshot.WorkspacePath = ""
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode canvas: %w", err)
	}
	data = append(data, '\n')
	return atomicWrite(path, data, 0o644)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".agent-infinite-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

func clone(snapshot contracts.Snapshot) contracts.Snapshot {
	teams := make([]contracts.Team, len(snapshot.Teams))
	worktrees := make([]contracts.Worktree, len(snapshot.Worktrees))
	nodes := make([]contracts.Node, len(snapshot.Nodes))
	edges := make([]contracts.Edge, len(snapshot.Edges))
	copy(teams, snapshot.Teams)
	copy(worktrees, snapshot.Worktrees)
	copy(nodes, snapshot.Nodes)
	copy(edges, snapshot.Edges)
	snapshot.Teams, snapshot.Worktrees, snapshot.Nodes, snapshot.Edges = teams, worktrees, nodes, edges
	return snapshot
}
