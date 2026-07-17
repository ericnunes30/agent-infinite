package contracts

import "time"

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type Viewport struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zoom float64 `json:"zoom"`
}

type Team struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	// Branch and BaseRef are retained for schema v1 compatibility.
	Branch    string    `json:"branch"`
	BaseRef   string    `json:"baseRef"`
	CreatedAt time.Time `json:"createdAt"`
}

type Worktree struct {
	ID        string    `json:"id"`
	TeamID    string    `json:"teamId"`
	Name      string    `json:"name"`
	Branch    string    `json:"branch"`
	BaseRef   string    `json:"baseRef"`
	CreatedAt time.Time `json:"createdAt"`
}

type Node struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Provider   string `json:"provider"`
	TeamID     string `json:"teamId"`
	WorktreeID string `json:"worktreeId,omitempty"`
	Label      string `json:"label"`
	Role       string `json:"role"`
	AutoStart  bool   `json:"autoStart"`
	Position   Point  `json:"position"`
	Size       Size   `json:"size"`
}

type Edge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

type Integration struct {
	Hooks string `json:"hooks"`
}

type Snapshot struct {
	SchemaVersion int         `json:"schemaVersion"`
	WorkspaceID   string      `json:"workspaceId"`
	WorkspacePath string      `json:"workspacePath,omitempty"`
	Teams         []Team      `json:"teams"`
	Worktrees     []Worktree  `json:"worktrees"`
	Nodes         []Node      `json:"nodes"`
	Edges         []Edge      `json:"edges"`
	Viewport      Viewport    `json:"viewport"`
	Integration   Integration `json:"integration"`
}

type APIError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

type Ready struct {
	Type    string `json:"type"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	Version string `json:"version"`
}
