package capabilities

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

var secretKeyPattern = regexp.MustCompile(`(?i)(token|secret|password|authorization|api[_-]?key|credential)`)

type scanCandidate struct {
	Item           Item
	RawFingerprint string
}

func (s *Service) Scan(workspacePath string) ScanResult {
	now := time.Now().UTC()
	candidates, scanErrors := discover(workspacePath, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate.Item.Fingerprint = candidate.RawFingerprint
		seen[candidate.Item.ID] = true
		found := false
		for index := range s.catalog.Items {
			current := &s.catalog.Items[index]
			if current.ID != candidate.Item.ID {
				continue
			}
			found = true
			policy, firstSeen, toolCount := current.Policy, current.FirstSeenAt, current.ToolCount
			status := "unchanged"
			if current.Fingerprint != candidate.RawFingerprint {
				status = "changed"
				candidate.Item.Changes = structuralDiff(current.Spec, candidate.Item.Spec, "")
				if candidate.Item.Kind == KindSkill {
					candidate.Item.Changes = []string{"content changed"}
				}
			}
			*current = candidate.Item
			current.Policy, current.FirstSeenAt, current.Status, current.ToolCount = policy, firstSeen, status, toolCount
			break
		}
		if !found {
			s.catalog.Items = append(s.catalog.Items, candidate.Item)
		}
	}
	for index := range s.catalog.Items {
		item := &s.catalog.Items[index]
		if _, failed := scanErrors[item.SourcePath]; item.Origin == OriginExternal && failed {
			item.Status = "scan_error"
			item.Changes = []string{"source scan error"}
			continue
		}
		if item.Origin == OriginExternal && !seen[item.ID] {
			item.Status = "missing"
			item.Changes = []string{"source missing"}
		}
	}
	_ = s.persistLocked()
	return ScanResult{Items: cloneItems(s.catalog.Items), ScanErrors: scanErrors, ScannedAt: now}
}

func discover(workspacePath string, now time.Time) ([]scanCandidate, map[string]string) {
	home, _ := os.UserHomeDir()
	if override := strings.TrimSpace(os.Getenv("AGENT_INFINITE_PROVIDER_HOME")); override != "" {
		home = override
	}
	items, failures := []scanCandidate{}, map[string]string{}
	jsonSources := []struct{ provider, scope, path, key string }{
		{"claude", "user", filepath.Join(home, ".claude.json"), "mcpServers"},
		{"claude", "user", filepath.Join(home, ".claude", "settings.json"), "mcpServers"},
		{"opencode", "user", filepath.Join(home, ".config", "opencode", "opencode.json"), "mcp"},
	}
	if custom := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); custom != "" {
		jsonSources = append(jsonSources, struct{ provider, scope, path, key string }{"opencode", "user", custom, "mcp"})
	}
	if workspacePath != "" {
		jsonSources = append(jsonSources,
			struct{ provider, scope, path, key string }{"claude", "project", filepath.Join(workspacePath, ".mcp.json"), "mcpServers"},
			struct{ provider, scope, path, key string }{"opencode", "project", filepath.Join(workspacePath, "opencode.json"), "mcp"},
			struct{ provider, scope, path, key string }{"opencode", "project", filepath.Join(workspacePath, ".opencode", "opencode.json"), "mcp"},
		)
	}
	for _, source := range jsonSources {
		found, err := scanJSONMCP(source.provider, source.scope, source.path, source.key, now)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			failures[source.path] = safeScanError(err)
		}
		items = append(items, found...)
		if source.provider == "claude" && source.path == filepath.Join(home, ".claude.json") {
			projectItems, projectErr := scanClaudeProjects(source.path, now)
			if projectErr != nil && !errors.Is(projectErr, os.ErrNotExist) {
				failures[source.path] = safeScanError(projectErr)
			}
			items = append(items, projectItems...)
		}
	}
	if inline := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_CONTENT")); inline != "" {
		found, err := scanJSONMCPBytes("opencode", "user", "env:OPENCODE_CONFIG_CONTENT", "mcp", []byte(inline), now)
		if err != nil {
			failures["env:OPENCODE_CONFIG_CONTENT"] = safeScanError(err)
		} else {
			items = append(items, found...)
		}
	}
	tomlSources := []struct{ scope, path string }{{"user", filepath.Join(home, ".codex", "config.toml")}}
	if workspacePath != "" {
		tomlSources = append(tomlSources, struct{ scope, path string }{"project", filepath.Join(workspacePath, ".codex", "config.toml")})
	}
	for _, source := range tomlSources {
		found, err := scanCodexMCP(source.scope, source.path, now)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			failures[source.path] = safeScanError(err)
		}
		items = append(items, found...)
	}
	skillRoots := []struct{ provider, scope, path string }{
		{"claude", "user", filepath.Join(home, ".claude", "skills")},
		{"codex", "user", filepath.Join(home, ".agents", "skills")},
		{"codex", "user", filepath.Join(home, ".codex", "skills")},
		{"pi", "user", filepath.Join(home, ".pi", "agent", "skills")},
		{"opencode", "user", filepath.Join(home, ".config", "opencode", "skills")},
	}
	if workspacePath != "" {
		skillRoots = append(skillRoots,
			struct{ provider, scope, path string }{"claude", "project", filepath.Join(workspacePath, ".claude", "skills")},
			struct{ provider, scope, path string }{"codex", "project", filepath.Join(workspacePath, ".agents", "skills")},
			struct{ provider, scope, path string }{"pi", "project", filepath.Join(workspacePath, ".pi", "skills")},
			struct{ provider, scope, path string }{"opencode", "project", filepath.Join(workspacePath, ".opencode", "skills")},
		)
	}
	for _, root := range skillRoots {
		items = append(items, scanSkills(root.provider, root.scope, root.path, now)...)
	}
	pluginRoots := []struct{ provider, path string }{
		{"claude", filepath.Join(home, ".claude", "plugins")},
		{"codex", filepath.Join(home, ".codex", "plugins")},
		{"pi", filepath.Join(home, ".pi", "agent", "extensions")},
		{"opencode", filepath.Join(home, ".config", "opencode", "plugins")},
	}
	if workspacePath != "" {
		pluginRoots = append(pluginRoots,
			struct{ provider, path string }{"claude", filepath.Join(workspacePath, ".claude", "plugins")},
			struct{ provider, path string }{"codex", filepath.Join(workspacePath, ".codex", "plugins")},
			struct{ provider, path string }{"opencode", filepath.Join(workspacePath, ".opencode", "plugins")},
		)
	}
	for _, root := range pluginRoots {
		items = append(items, scanSkillTree(root.provider, "plugin", root.path, now)...)
		items = append(items, scanMCPFiles(root.provider, "plugin", root.path, now, failures)...)
	}
	return items, failures
}

func scanMCPFiles(provider, scope, root string, now time.Time, failures map[string]string) []scanCandidate {
	result := []scanCandidate{}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			failures[path] = safeScanError(err)
			return nil
		}
		if entry.IsDir() && transientPluginDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != ".mcp.json" {
			return nil
		}
		found, scanErr := scanJSONMCP(provider, scope, path, "mcpServers", now)
		if scanErr != nil {
			failures[path] = safeScanError(scanErr)
		} else {
			result = append(result, found...)
		}
		return nil
	})
	return result
}

func scanSkillTree(provider, scope, root string, now time.Time) []scanCandidate {
	result := []scanCandidate{}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil {
			return nil
		}
		if entry.IsDir() && transientPluginDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.EqualFold(entry.Name(), "SKILL.md") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		name := filepath.Base(filepath.Dir(path))
		description := skillDescription(string(data))
		fingerprint := fingerprintValue(string(data))
		result = append(result, scanCandidate{Item: Item{ID: externalID(KindSkill, provider, scope, path, name), Kind: KindSkill, Name: name, Description: description, Origin: OriginExternal, Provider: provider, Scope: scope, SourcePath: path, NativeKey: name, SkillPath: path, Status: "new", Policy: PolicyProviderDefault, Enforceable: true, GroupID: "skill-" + fingerprint[:24], EstimatedTokens: estimateTokens(name + description), MetadataTokens: estimateTokens(name + description), ContentTokens: estimateTokens(string(data)), FirstSeenAt: now, LastSeenAt: now}, RawFingerprint: fingerprint})
		return nil
	})
	return result
}

func transientPluginDirectory(name string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), ".staging")
}

func scanJSONMCP(provider, scope, path, key string, now time.Time) ([]scanCandidate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return scanJSONMCPBytes(provider, scope, path, key, data, now)
}

func scanJSONMCPBytes(provider, scope, path, key string, data []byte, now time.Time) ([]scanCandidate, error) {
	root := map[string]any{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	servers, _ := root[key].(map[string]any)
	result := []scanCandidate{}
	for name, raw := range servers {
		spec, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		result = append(result, externalMCP(provider, scope, path, name, spec, now))
	}
	return result, nil
}

func scanCodexMCP(scope, path string, now time.Time) ([]scanCandidate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if err := toml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	result := []scanCandidate{}
	for name, raw := range servers {
		if spec, ok := raw.(map[string]any); ok {
			result = append(result, externalMCP("codex", scope, path, name, spec, now))
		}
	}
	profiles, _ := root["profiles"].(map[string]any)
	for profileName, rawProfile := range profiles {
		profile, _ := rawProfile.(map[string]any)
		profileServers, _ := profile["mcp_servers"].(map[string]any)
		for name, raw := range profileServers {
			if spec, ok := raw.(map[string]any); ok {
				candidate := externalMCP("codex", scope, path, profileName+"\x1f"+name, spec, now)
				candidate.Item.Name = name + " (profile " + profileName + ")"
				result = append(result, candidate)
			}
		}
	}
	return result, nil
}

func scanClaudeProjects(path string, now time.Time) ([]scanCandidate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	projects, _ := root["projects"].(map[string]any)
	result := []scanCandidate{}
	for projectPath, rawProject := range projects {
		project, _ := rawProject.(map[string]any)
		servers, _ := project["mcpServers"].(map[string]any)
		for name, raw := range servers {
			if spec, ok := raw.(map[string]any); ok {
				candidate := externalMCP("claude", "project", path, projectPath+"\x1f"+name, spec, now)
				candidate.Item.Name = name
				result = append(result, candidate)
			}
		}
	}
	return result, nil
}

func externalMCP(provider, scope, path, name string, spec map[string]any, now time.Time) scanCandidate {
	redacted := redactMap(spec)
	fingerprint := fingerprintValue(redacted)
	return scanCandidate{Item: Item{
		ID: externalID(KindMCP, provider, scope, path, name), Kind: KindMCP, Name: name,
		Origin: OriginExternal, Provider: provider, Scope: scope, SourcePath: path, NativeKey: name,
		Spec: redacted, Status: "new", Policy: PolicyProviderDefault, Enforceable: true, GroupID: "mcp-" + fingerprint[:24],
		EstimatedTokens: estimateTokens(string(mustJSON(redacted))), FirstSeenAt: now, LastSeenAt: now,
	}, RawFingerprint: fingerprint}
}

func scanSkills(provider, scope, root string, now time.Time) []scanCandidate {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	result := []scanCandidate{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		description := skillDescription(string(data))
		fingerprint := fingerprintValue(string(data))
		result = append(result, scanCandidate{Item: Item{
			ID: externalID(KindSkill, provider, scope, path, entry.Name()), Kind: KindSkill, Name: entry.Name(), Description: description,
			Origin: OriginExternal, Provider: provider, Scope: scope, SourcePath: path, NativeKey: entry.Name(), SkillPath: path,
			Status: "new", Policy: PolicyProviderDefault, Enforceable: true, GroupID: "skill-" + fingerprint[:24], EstimatedTokens: estimateTokens(entry.Name() + description), MetadataTokens: estimateTokens(entry.Name() + description), ContentTokens: estimateTokens(string(data)),
			FirstSeenAt: now, LastSeenAt: now,
		}, RawFingerprint: fingerprint})
	}
	return result
}

func readExternalMCPSpec(item Item) (map[string]any, error) {
	if item.Provider == "codex" {
		data, err := os.ReadFile(item.SourcePath)
		if err != nil {
			return nil, err
		}
		root := map[string]any{}
		if err := toml.Unmarshal(data, &root); err != nil {
			return nil, err
		}
		servers, nativeKey := root["mcp_servers"], item.NativeKey
		if parts := strings.SplitN(item.NativeKey, "\x1f", 2); len(parts) == 2 {
			profiles, _ := root["profiles"].(map[string]any)
			profile, _ := profiles[parts[0]].(map[string]any)
			servers, nativeKey = profile["mcp_servers"], parts[1]
		}
		serverMap, _ := servers.(map[string]any)
		spec, _ := serverMap[nativeKey].(map[string]any)
		if spec == nil {
			return nil, errors.New("external MCP no longer exists")
		}
		return spec, nil
	}
	data, err := os.ReadFile(item.SourcePath)
	if item.SourcePath == "env:OPENCODE_CONFIG_CONTENT" {
		data, err = []byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), nil
	}
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	key := "mcpServers"
	if item.Provider == "opencode" {
		key = "mcp"
	}
	servers, nativeKey := root[key], item.NativeKey
	if item.Provider == "claude" {
		if parts := strings.SplitN(item.NativeKey, "\x1f", 2); len(parts) == 2 {
			projects, _ := root["projects"].(map[string]any)
			project, _ := projects[parts[0]].(map[string]any)
			servers, nativeKey = project["mcpServers"], parts[1]
		}
	}
	serverMap, _ := servers.(map[string]any)
	spec, _ := serverMap[nativeKey].(map[string]any)
	if spec == nil {
		return nil, errors.New("external MCP no longer exists")
	}
	return spec, nil
}

func redactMap(input map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range input {
		if strings.EqualFold(key, "url") {
			if raw, ok := value.(string); ok {
				if parsed, err := url.Parse(raw); err == nil {
					query := parsed.Query()
					for name := range query {
						query.Set(name, "***")
					}
					parsed.RawQuery = query.Encode()
					result[key] = parsed.String()
					continue
				}
			}
		}
		if strings.EqualFold(key, "env") || strings.EqualFold(key, "headers") {
			if values, ok := value.(map[string]any); ok {
				masked := map[string]any{}
				for name := range values {
					masked[name] = "***"
				}
				result[key] = masked
				continue
			}
		}
		if secretKeyPattern.MatchString(key) {
			result[key] = "***"
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			result[key] = redactMap(typed)
		case []any:
			values := make([]any, len(typed))
			for i, item := range typed {
				if nested, ok := item.(map[string]any); ok {
					values[i] = redactMap(nested)
				} else {
					values[i] = item
				}
			}
			result[key] = values
		default:
			result[key] = value
		}
	}
	return result
}

func fingerprintValue(value any) string {
	sum := sha256.Sum256(mustJSON(value))
	return hex.EncodeToString(sum[:])
}
func externalID(kind, provider, scope, path, key string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.Join([]string{kind, provider, scope, filepath.Clean(path), key}, "\x00"))))
	return "ext-" + hex.EncodeToString(sum[:12])
}
func mustJSON(value any) []byte { data, _ := json.Marshal(value); return data }
func skillDescription(value string) string {
	match := regexp.MustCompile(`(?m)^description:\s*["']?([^\r\n"']+)`).FindStringSubmatch(value)
	if len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}
func providerConfigKey(provider string) string {
	if provider == "opencode" {
		return "mcp"
	}
	return "mcpServers"
}
func scanError(path string, err error) error { return fmt.Errorf("scan %s: %w", path, err) }
func safeScanError(err error) string {
	if errors.Is(err, os.ErrPermission) {
		return "access denied"
	}
	return "configuration could not be parsed or read"
}

func structuralDiff(before, after map[string]any, prefix string) []string {
	changes := []string{}
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	for key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		left, leftOK := before[key]
		right, rightOK := after[key]
		if !leftOK {
			changes = append(changes, path+" added")
			continue
		}
		if !rightOK {
			changes = append(changes, path+" removed")
			continue
		}
		leftMap, leftMapOK := left.(map[string]any)
		rightMap, rightMapOK := right.(map[string]any)
		if leftMapOK && rightMapOK {
			changes = append(changes, structuralDiff(leftMap, rightMap, path)...)
			continue
		}
		if !reflect.DeepEqual(left, right) {
			changes = append(changes, path+" changed")
		}
	}
	sort.Strings(changes)
	return changes
}
