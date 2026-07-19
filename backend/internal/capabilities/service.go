package capabilities

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var managedIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
var portableSkillNamePattern = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

type Service struct {
	mu          sync.RWMutex
	root        string
	catalogPath string
	catalog     Catalog
	vault       *secretVault
}

func New(root string) *Service {
	service := &Service{
		root: root, catalogPath: filepath.Join(root, "capabilities.json"),
		catalog: Catalog{Version: 1, Items: []Item{}},
		vault:   newSecretVault(filepath.Join(root, "capability-secrets.json")),
	}
	if data, err := os.ReadFile(service.catalogPath); err == nil {
		_ = json.Unmarshal(data, &service.catalog)
	}
	service.ensureInternal()
	return service
}

func (s *Service) ensureInternal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.catalog.Items {
		if item.ID == "internal-agent-infinite" {
			return
		}
	}
	now := time.Now().UTC()
	s.catalog.Items = append(s.catalog.Items, Item{
		ID: "internal-agent-infinite", Kind: KindMCP, Name: "Agent Infinite",
		Description: "Canvas delegation and dispatch tools", Origin: OriginInternal,
		Provider: "all", Scope: "session", Fingerprint: "internal", Status: "unchanged",
		Policy: PolicyProviderDefault, Enforceable: true, FirstSeenAt: now, LastSeenAt: now,
	})
	_ = s.persistLocked()
}

func (s *Service) List() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := cloneItems(s.catalog.Items)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind == items[j].Kind {
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		}
		return items[i].Kind < items[j].Kind
	})
	return items
}

func (s *Service) SetPolicy(id, policy string) (Item, error) {
	if !validPolicy(policy) {
		return Item{}, errors.New("invalid capability policy")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.catalog.Items {
		if s.catalog.Items[index].ID != id {
			continue
		}
		if s.catalog.Items[index].Origin == OriginInternal {
			return Item{}, errors.New("internal capabilities cannot be changed")
		}
		s.catalog.Items[index].Policy = policy
		if err := s.persistLocked(); err != nil {
			return Item{}, err
		}
		return cloneItem(s.catalog.Items[index]), nil
	}
	return Item{}, errors.New("capability not found")
}

func (s *Service) SetPolicies(ids []string, policy string) ([]Item, error) {
	if !validPolicy(policy) {
		return nil, errors.New("invalid capability policy")
	}
	wanted := stringSet(ids)
	if len(wanted) == 0 {
		return nil, errors.New("at least one capability is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	indexes := make([]int, 0, len(wanted))
	for index := range s.catalog.Items {
		item := s.catalog.Items[index]
		if !wanted[item.ID] {
			continue
		}
		if item.Origin == OriginInternal {
			return nil, errors.New("internal capabilities cannot be changed")
		}
		indexes = append(indexes, index)
	}
	if len(indexes) != len(wanted) {
		return nil, errors.New("one or more capabilities were not found")
	}
	previous := make([]string, len(indexes))
	for position, index := range indexes {
		previous[position] = s.catalog.Items[index].Policy
		s.catalog.Items[index].Policy = policy
	}
	if err := s.persistLocked(); err != nil {
		for position, index := range indexes {
			s.catalog.Items[index].Policy = previous[position]
		}
		return nil, err
	}
	updated := make([]Item, 0, len(indexes))
	for _, index := range indexes {
		updated = append(updated, cloneItem(s.catalog.Items[index]))
	}
	return updated, nil
}

func (s *Service) Promote(id string, secrets map[string]string) (Item, error) {
	s.mu.RLock()
	var source Item
	found := false
	for _, item := range s.catalog.Items {
		if item.ID == id && item.Origin == OriginExternal {
			source, found = cloneItem(item), true
			break
		}
	}
	s.mu.RUnlock()
	if !found {
		return Item{}, errors.New("external capability not found")
	}
	source.ID = ""
	source.SourcePath, source.NativeKey = "", ""
	if source.Kind == KindSkill {
		markdown, err := os.ReadFile(source.SkillPath)
		if err != nil {
			return Item{}, err
		}
		return s.UpsertManaged(source, string(markdown), nil)
	}
	// The catalog copy is intentionally redacted. Credentials must be supplied again.
	return s.UpsertManaged(source, "", secrets)
}

func (s *Service) ItemForTest(id string) (Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.catalog.Items {
		if item.ID != id || item.Kind != KindMCP || item.Archived {
			continue
		}
		result := cloneItem(item)
		if result.Origin == OriginExternal {
			if spec, err := readExternalMCPSpec(result); err == nil {
				result.Spec = spec
			}
		}
		if result.Origin == OriginManaged {
			for _, name := range result.SecretNames {
				if value, err := s.vault.Get(result.ID, name); err == nil {
					injectManagedSecret(result.Spec, name, value)
				}
			}
		}
		return result, nil
	}
	return Item{}, errors.New("MCP server not found")
}

func (s *Service) UpsertManaged(input Item, skillMarkdown string, secrets map[string]string) (Item, error) {
	if input.Kind != KindMCP && input.Kind != KindSkill {
		return Item{}, errors.New("kind must be mcp or skill")
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return Item{}, errors.New("name is required")
	}
	if input.Provider == "" {
		input.Provider = "all"
	}
	if input.Provider != "all" && input.Provider != "claude" && input.Provider != "codex" && input.Provider != "pi" && input.Provider != "opencode" {
		return Item{}, errors.New("invalid capability provider")
	}
	existingSecrets := []string{}
	if input.ID != "" {
		foundManaged := false
		s.mu.RLock()
		for _, item := range s.catalog.Items {
			if item.ID == input.ID && item.Origin == OriginManaged {
				existingSecrets = append(existingSecrets, item.SecretNames...)
				foundManaged = true
				break
			}
		}
		s.mu.RUnlock()
		if !foundManaged {
			return Item{}, errors.New("managed capability not found")
		}
	}
	input.Spec = cloneMap(input.Spec)
	secrets = normalizeManagedSecrets(input.Spec, secrets)
	if missing := missingManagedSecrets(input.Spec, secrets, existingSecrets); len(missing) > 0 {
		return Item{}, fmt.Errorf("values required for %s", strings.Join(missing, ", "))
	}
	if input.Kind == KindMCP {
		_, hasURL := input.Spec["url"].(string)
		_, hasCommand := input.Spec["command"]
		if !hasURL && !hasCommand {
			return Item{}, errors.New("MCP spec requires url or command")
		}
	}
	if input.ID == "" {
		input.ID = randomID()
	} else if !managedIDPattern.MatchString(input.ID) {
		return Item{}, errors.New("invalid managed capability id")
	}
	now := time.Now().UTC()
	input.Origin, input.Scope, input.Policy = OriginManaged, "app", PolicyCurated
	input.Enforceable, input.Status, input.LastSeenAt = true, "unchanged", now
	if input.FirstSeenAt.IsZero() {
		input.FirstSeenAt = now
	}
	if input.Kind == KindSkill {
		skillMarkdown = canonicalSkill(input.Name, input.Description, skillMarkdown)
		path := filepath.Join(s.root, "skills", input.ID, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return Item{}, err
		}
		if err := os.WriteFile(path, []byte(skillMarkdown), 0o600); err != nil {
			return Item{}, err
		}
		input.SkillPath = path
		input.MetadataTokens = estimateTokens(input.Name + input.Description)
		input.ContentTokens = estimateTokens(skillMarkdown)
		input.EstimatedTokens = input.MetadataTokens
	}
	if input.Kind == KindMCP {
		input.Fingerprint = fingerprintValue(input.Spec)
	} else {
		input.Fingerprint = fingerprintValue(skillMarkdown)
	}
	input.GroupID = input.Kind + "-" + input.Fingerprint[:24]
	input.SecretNames = append(input.SecretNames[:0], existingSecrets...)
	for name, value := range secrets {
		if err := s.vault.Set(input.ID, name, value); err != nil {
			return Item{}, err
		}
		if !stringSet(input.SecretNames)[name] {
			input.SecretNames = append(input.SecretNames, name)
		}
	}
	sort.Strings(input.SecretNames)
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.catalog.Items {
		if s.catalog.Items[index].ID == input.ID {
			input.FirstSeenAt = s.catalog.Items[index].FirstSeenAt
			s.catalog.Items[index] = input
			if err := s.persistLocked(); err != nil {
				return Item{}, err
			}
			return cloneItem(input), nil
		}
	}
	s.catalog.Items = append(s.catalog.Items, input)
	if err := s.persistLocked(); err != nil {
		return Item{}, err
	}
	return cloneItem(input), nil
}

func canonicalSkill(name, description, markdown string) string {
	markdown = strings.TrimSpace(markdown)
	if strings.HasPrefix(markdown, "---") {
		if end := strings.Index(markdown[3:], "\n---"); end >= 0 {
			markdown = strings.TrimSpace(markdown[3+end+4:])
		}
	}
	slug := strings.ToLower(strings.Trim(portableSkillNamePattern.ReplaceAllString(name, "-"), "-"))
	if slug == "" {
		slug = "skill"
	}
	descriptionJSON, _ := json.Marshal(description)
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", slug, descriptionJSON, markdown)
}

func normalizeManagedSecrets(spec map[string]any, supplied map[string]string) map[string]string {
	result := map[string]string{}
	for name, value := range supplied {
		result[name] = value
	}
	for _, target := range []struct{ field, prefix string }{{"env", "ENV."}, {"headers", "HEADER."}} {
		values, _ := spec[target.field].(map[string]any)
		for name, raw := range values {
			value, secretName := fmt.Sprint(raw), target.prefix+name
			if value != "" && value != "***" {
				result[secretName] = value
			}
			values[name] = "***"
		}
	}
	return result
}

func injectManagedSecret(spec map[string]any, name, value string) {
	field, key := "env", name
	if strings.HasPrefix(name, "ENV.") {
		key = strings.TrimPrefix(name, "ENV.")
	}
	if strings.HasPrefix(name, "HEADER.") {
		field, key = "headers", strings.TrimPrefix(name, "HEADER.")
	}
	values, _ := spec[field].(map[string]any)
	if values == nil {
		values = map[string]any{}
	}
	values[key], spec[field] = value, values
}

func missingManagedSecrets(spec map[string]any, supplied map[string]string, existing []string) []string {
	missing := []string{}
	configured := stringSet(existing)
	for _, target := range []struct{ field, prefix string }{{"env", "ENV."}, {"headers", "HEADER."}} {
		values, _ := spec[target.field].(map[string]any)
		for name, raw := range values {
			if fmt.Sprint(raw) == "***" && supplied[target.prefix+name] == "" && supplied[name] == "" && !configured[target.prefix+name] && !configured[name] {
				missing = append(missing, target.prefix+name)
			}
		}
	}
	return missing
}

func (s *Service) SkillMarkdown(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.catalog.Items {
		if item.ID == id && item.Kind == KindSkill && item.Origin == OriginManaged && !item.Archived {
			data, err := os.ReadFile(item.SkillPath)
			return string(data), err
		}
	}
	return "", errors.New("managed skill not found")
}

func (s *Service) RecordToolCount(id string, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.catalog.Items {
		if s.catalog.Items[index].ID == id {
			s.catalog.Items[index].ToolCount = count
			_ = s.persistLocked()
			return
		}
	}
}

func (s *Service) ValidateSelection(provider string, mcpIDs, skillIDs []string) error {
	if provider == "mock" {
		return nil
	}
	wanted := map[string]string{}
	for _, id := range mcpIDs {
		wanted[id] = KindMCP
	}
	for _, id := range skillIDs {
		wanted[id] = KindSkill
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, kind := range wanted {
		found := false
		for _, item := range s.catalog.Items {
			if item.ID != id {
				continue
			}
			found = true
			if item.Kind != kind || item.Archived || item.Status == "missing" || item.Status == "scan_error" {
				return fmt.Errorf("capability %q is unavailable", id)
			}
			if item.Policy != PolicyCurated {
				return fmt.Errorf("capability %q must be curated before selection", id)
			}
			if !compatibleWithProvider(item, provider) {
				return fmt.Errorf("capability %q is incompatible with provider %s", id, provider)
			}
			break
		}
		if !found {
			return fmt.Errorf("unknown capability %q", id)
		}
	}
	return nil
}

func (s *Service) Archive(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.catalog.Items {
		if s.catalog.Items[index].ID != id {
			continue
		}
		if s.catalog.Items[index].Origin == OriginInternal {
			return errors.New("internal capabilities cannot be archived")
		}
		s.catalog.Items[index].Archived = true
		if err := s.persistLocked(); err != nil {
			return err
		}
		return s.vault.DeleteItem(id)
	}
	return errors.New("capability not found")
}

func (s *Service) Resolve(provider string, selectedMCPs, selectedSkills []string) Resolution {
	selectedMCP, selectedSkill := stringSet(selectedMCPs), stringSet(selectedSkills)
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := Resolution{}
	includedGroups := map[string]bool{}
	for _, stored := range s.catalog.Items {
		if stored.Archived || stored.Origin == OriginInternal {
			continue
		}
		native := stored.Provider == "all" || stored.Provider == provider
		selectedExplicitly := (stored.Kind == KindMCP && selectedMCP[stored.ID]) || (stored.Kind == KindSkill && selectedSkill[stored.ID])
		if stored.Policy == PolicyProviderDefault && !native {
			continue
		}
		if stored.Policy == PolicyBlocked {
			if native {
				result.Blocked = append(result.Blocked, cloneItem(stored))
			}
			continue
		}
		selected := stored.Policy == PolicyProviderDefault || (selectedExplicitly && compatibleWithProvider(stored, provider))
		if !selected {
			if native && stored.Origin == OriginExternal && stored.Policy == PolicyCurated {
				result.Blocked = append(result.Blocked, cloneItem(stored))
			}
			continue
		}
		if stored.Status == "missing" || stored.Status == "scan_error" {
			continue
		}
		groupKey := stored.GroupID
		if groupKey == "" {
			groupKey = stored.ID
		}
		if includedGroups[groupKey] {
			continue
		}
		includedGroups[groupKey] = true
		item := cloneItem(stored)
		if item.Origin == OriginExternal && item.Kind == KindMCP {
			if spec, err := readExternalMCPSpec(item); err == nil {
				item.Spec = spec
			}
		}
		if item.Origin == OriginManaged && item.Kind == KindMCP && len(item.SecretNames) > 0 {
			for _, name := range item.SecretNames {
				if value, err := s.vault.Get(item.ID, name); err == nil {
					injectManagedSecret(item.Spec, name, value)
				}
			}
		}
		if item.Kind == KindMCP {
			result.MCPs = append(result.MCPs, item)
		} else {
			result.Skills = append(result.Skills, item)
		}
	}
	return result
}

func compatibleWithProvider(item Item, provider string) bool {
	if item.Provider == "all" || item.Provider == provider || item.Kind == KindSkill {
		return true
	}
	if item.Kind != KindMCP || provider != "pi" {
		return item.Kind == KindMCP
	}
	if _, ok := item.Spec["url"].(string); ok {
		return true
	}
	typeName, _ := item.Spec["type"].(string)
	return typeName == "remote" || typeName == "http"
}

func (s *Service) SecretValues(item Item) (map[string]string, error) {
	values := map[string]string{}
	for _, name := range item.SecretNames {
		value, err := s.vault.Get(item.ID, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		values[name] = value
	}
	return values, nil
}

func (s *Service) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.catalogPath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.catalog, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.catalogPath), ".capabilities-*.tmp")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(path, s.catalogPath)
}

func randomID() string {
	data := make([]byte, 12)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}
func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}
func estimateTokens(value string) int {
	if value == "" {
		return 0
	}
	return (len(value) + 3) / 4
}
func cloneItems(values []Item) []Item {
	result := make([]Item, len(values))
	for i := range values {
		result[i] = cloneItem(values[i])
	}
	return result
}
func cloneItem(value Item) Item {
	value.Spec = cloneMap(value.Spec)
	value.SecretNames = append([]string(nil), value.SecretNames...)
	value.Changes = append([]string(nil), value.Changes...)
	return value
}
func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	data, _ := json.Marshal(value)
	result := map[string]any{}
	_ = json.Unmarshal(data, &result)
	return result
}
