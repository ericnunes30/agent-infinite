package capabilities

import "time"

const (
	KindMCP   = "mcp"
	KindSkill = "skill"

	OriginManaged  = "managed"
	OriginExternal = "external"
	OriginInternal = "internal"

	PolicyProviderDefault = "provider_default"
	PolicyCurated         = "curated"
	PolicyBlocked         = "blocked"
)

type Item struct {
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	Name            string         `json:"name"`
	Description     string         `json:"description,omitempty"`
	Origin          string         `json:"origin"`
	Provider        string         `json:"provider"`
	Scope           string         `json:"scope"`
	SourcePath      string         `json:"sourcePath,omitempty"`
	NativeKey       string         `json:"nativeKey,omitempty"`
	Fingerprint     string         `json:"fingerprint"`
	GroupID         string         `json:"groupId,omitempty"`
	Status          string         `json:"status"`
	Policy          string         `json:"policy"`
	Enforceable     bool           `json:"enforceable"`
	Spec            map[string]any `json:"spec,omitempty"`
	SecretNames     []string       `json:"secretNames,omitempty"`
	SkillPath       string         `json:"skillPath,omitempty"`
	EstimatedTokens int            `json:"estimatedTokens,omitempty"`
	MetadataTokens  int            `json:"metadataTokens,omitempty"`
	ContentTokens   int            `json:"contentTokens,omitempty"`
	ToolCount       int            `json:"toolCount,omitempty"`
	FirstSeenAt     time.Time      `json:"firstSeenAt"`
	LastSeenAt      time.Time      `json:"lastSeenAt"`
	Archived        bool           `json:"archived,omitempty"`
	Changes         []string       `json:"changes,omitempty"`
}

type Catalog struct {
	Version int    `json:"version"`
	Items   []Item `json:"items"`
}

type Resolution struct {
	MCPs    []Item
	Skills  []Item
	Blocked []Item
}

type ScanResult struct {
	Items      []Item            `json:"items"`
	ScanErrors map[string]string `json:"scanErrors"`
	ScannedAt  time.Time         `json:"scannedAt"`
}

func validPolicy(value string) bool {
	return value == PolicyProviderDefault || value == PolicyCurated || value == PolicyBlocked
}
