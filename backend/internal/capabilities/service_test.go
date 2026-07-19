package capabilities

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScanRedactsSecretsPreservesSourcesAndTracksDrift(t *testing.T) {
	home, workspace, catalogRoot := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", home)
	claudePath := filepath.Join(home, ".claude.json")
	writeFixture(t, claudePath, `{"mcpServers":{"remote":{"url":"https://example.test/mcp?token=top-secret","headers":{"Authorization":"Bearer top-secret"},"env":{"OPAQUE":"top-secret"}}}}`)
	codexPath := filepath.Join(home, ".codex", "config.toml")
	writeFixture(t, codexPath, "[mcp_servers.local]\ncommand = \"fixture-server\"\n[mcp_servers.local.env]\nTOKEN = \"codex-secret\"\n")
	skillPath := filepath.Join(workspace, ".agents", "skills", "review", "SKILL.md")
	writeFixture(t, skillPath, "---\nname: review\ndescription: Review changes\n---\nReview carefully.")
	portableSkill := "---\nname: portable\ndescription: Portable fixture\n---\nPortable."
	writeFixture(t, filepath.Join(home, ".pi", "agent", "skills", "portable", "SKILL.md"), portableSkill)
	writeFixture(t, filepath.Join(home, ".config", "opencode", "skills", "portable", "SKILL.md"), portableSkill)
	before := fileHash(t, claudePath)
	beforeCodex := fileHash(t, codexPath)

	service := New(catalogRoot)
	first := service.Scan(workspace)
	if len(first.ScanErrors) != 0 {
		t.Fatalf("unexpected scan errors: %v", first.ScanErrors)
	}
	if before != fileHash(t, claudePath) {
		t.Fatal("scanner modified an external provider file")
	}
	if beforeCodex != fileHash(t, codexPath) {
		t.Fatal("scanner modified Codex config")
	}
	catalogData, err := os.ReadFile(filepath.Join(catalogRoot, "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(catalogData), "top-secret") || strings.Contains(string(catalogData), "codex-secret") {
		t.Fatal("external secret leaked into catalog")
	}
	providers := map[string]bool{}
	for _, item := range first.Items {
		providers[item.Provider] = true
	}
	for _, provider := range []string{"claude", "codex", "pi", "opencode"} {
		if !providers[provider] {
			t.Fatalf("provider fixture %s was not discovered", provider)
		}
	}
	portableGroups := map[string]bool{}
	for _, item := range first.Items {
		if item.Kind == KindSkill && item.Name == "portable" {
			portableGroups[item.GroupID] = true
		}
	}
	if len(portableGroups) != 1 {
		t.Fatalf("identical skills were not grouped: %v", portableGroups)
	}

	var mcp Item
	for _, item := range first.Items {
		if item.Kind == KindMCP && item.Origin == OriginExternal && item.Provider == "claude" {
			mcp = item
		}
	}
	if mcp.ID == "" || mcp.Policy != PolicyProviderDefault || mcp.GroupID == "" {
		t.Fatalf("unexpected MCP discovery: %+v", mcp)
	}
	if got := service.Resolve("claude", nil, nil); len(got.MCPs) != 1 {
		t.Fatalf("provider_default should be inherited, got %d", len(got.MCPs))
	}
	if _, err := service.SetPolicy(mcp.ID, PolicyCurated); err != nil {
		t.Fatal(err)
	}
	if got := service.Resolve("claude", nil, nil); len(got.MCPs) != 0 || len(got.Blocked) != 1 {
		t.Fatalf("unselected curated MCP should be excluded: %+v", got)
	}
	if got := service.Resolve("claude", []string{mcp.ID}, nil); len(got.MCPs) != 1 {
		t.Fatal("selected curated MCP was not resolved")
	}

	writeFixture(t, claudePath, `{"mcpServers":{"remote":{"url":"https://changed.test/mcp","headers":{"Authorization":"another-secret"}}}}`)
	second := service.Scan(workspace)
	for _, item := range second.Items {
		if item.ID == mcp.ID && item.Status != "changed" {
			t.Fatalf("expected changed, got %s", item.Status)
		}
	}
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("skill source was changed: %v", err)
	}
}

func TestScanIgnoresTransientPluginStagingTrees(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENT_INFINITE_PROVIDER_HOME", home)
	markdown := "---\nname: access\ndescription: Manage Discord access\n---\nInstructions."
	writeFixture(t, filepath.Join(home, ".claude", "plugins", "marketplaces", "official", "discord", "skills", "access", "SKILL.md"), markdown)
	writeFixture(t, filepath.Join(home, ".claude", "plugins", "marketplaces", "official.staging", "discord", "skills", "access", "SKILL.md"), markdown)
	result := New(t.TempDir()).Scan("")
	accessSkills := 0
	for _, item := range result.Items {
		if item.Kind == KindSkill && item.Name == "access" && item.Status != "missing" {
			accessSkills++
			if strings.Contains(strings.ToLower(item.SourcePath), ".staging") {
				t.Fatalf("staging skill was inventoried: %s", item.SourcePath)
			}
		}
	}
	if accessSkills != 1 {
		t.Fatalf("discovered %d active access skills, want 1", accessSkills)
	}
}

func TestScanArchivesMissingExternalRecord(t *testing.T) {
	home, catalogRoot := t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	writeFixture(t, path, `{"mcp":{"docs":{"type":"remote","url":"https://example.test"}}}`)
	service := New(catalogRoot)
	first := service.Scan("")
	var id string
	for _, item := range first.Items {
		if item.Provider == "opencode" {
			id = item.ID
		}
	}
	if id == "" {
		t.Fatal("OpenCode MCP was not discovered")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second := service.Scan("")
	for _, item := range second.Items {
		if item.ID == id && item.Status == "missing" {
			return
		}
	}
	t.Fatal("missing external record was not retained")
}

func TestSetPoliciesUpdatesCatalogAtomically(t *testing.T) {
	service := New(t.TempDir())
	first, err := service.UpsertManaged(Item{Kind: KindMCP, Name: "first", Provider: "all", Spec: map[string]any{"command": "first-server"}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.UpsertManaged(Item{Kind: KindMCP, Name: "second", Provider: "all", Spec: map[string]any{"command": "second-server"}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.SetPolicies([]string{first.ID, second.ID}, PolicyBlocked)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 2 || updated[0].Policy != PolicyBlocked || updated[1].Policy != PolicyBlocked {
		t.Fatalf("bulk update = %#v", updated)
	}
	if _, err := service.SetPolicies([]string{first.ID, "missing"}, PolicyProviderDefault); err == nil {
		t.Fatal("bulk update with an unknown capability should fail")
	}
	for _, item := range service.List() {
		if (item.ID == first.ID || item.ID == second.ID) && item.Policy != PolicyBlocked {
			t.Fatalf("failed bulk update partially changed catalog: %#v", item)
		}
	}
}

func TestParseErrorIsPerSourceAndDoesNotExposeFileContents(t *testing.T) {
	home, catalogRoot := t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".claude.json")
	writeFixture(t, path, `{"mcpServers":{"docs":{"url":"https://example.test"}}}`)
	service := New(catalogRoot)
	first := service.Scan("")
	var id string
	for _, item := range first.Items {
		if item.Provider == "claude" && item.Origin == OriginExternal {
			id = item.ID
		}
	}
	writeFixture(t, path, `{invalid e2e-secret`)
	second := service.Scan("")
	if strings.Contains(second.ScanErrors[path], "e2e-secret") {
		t.Fatal("parser error exposed source contents")
	}
	for _, item := range second.Items {
		if item.ID == id && item.Status == "scan_error" {
			return
		}
	}
	t.Fatal("previous capability was not retained as scan_error")
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func TestCatalogIsValidJSON(t *testing.T) {
	root := t.TempDir()
	service := New(root)
	service.Scan("")
	data, err := os.ReadFile(filepath.Join(root, "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded Catalog
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
}

func TestManagedMCPSecretsAreProtectedAndInjectedOnlyAtResolution(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI is Windows-only")
	}
	root := t.TempDir()
	service := New(root)
	item, err := service.UpsertManaged(Item{Kind: KindMCP, Name: "private", Provider: "codex", Spec: map[string]any{"command": "server", "env": map[string]any{"TOKEN": "plain-token"}, "headers": map[string]any{"Authorization": "Bearer private"}}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "plain-token") || strings.Contains(string(data), "Bearer private") {
		t.Fatal("managed secret leaked into catalog")
	}
	resolved := service.Resolve("codex", []string{item.ID}, nil)
	if len(resolved.MCPs) != 1 {
		t.Fatal("managed MCP was not resolved")
	}
	environment, _ := resolved.MCPs[0].Spec["env"].(map[string]any)
	headers, _ := resolved.MCPs[0].Spec["headers"].(map[string]any)
	if environment["TOKEN"] != "plain-token" || headers["Authorization"] != "Bearer private" {
		t.Fatal("protected secrets were not injected at resolution")
	}
}

func TestCuratedCapabilitiesArePortableAcrossProviders(t *testing.T) {
	service := New(t.TempDir())
	mcp, err := service.UpsertManaged(Item{Kind: KindMCP, Name: "portable", Provider: "claude", Spec: map[string]any{"command": "portable-server"}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	skill, err := service.UpsertManaged(Item{Kind: KindSkill, Name: "portable-skill", Provider: "claude"}, "# Portable\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateSelection("codex", []string{mcp.ID}, []string{skill.ID}); err != nil {
		t.Fatalf("cross-provider curated selection was rejected: %v", err)
	}
	resolved := service.Resolve("codex", []string{mcp.ID}, []string{skill.ID})
	if len(resolved.MCPs) != 1 || len(resolved.Skills) != 1 {
		t.Fatalf("portable resolution = %#v, want one MCP and one skill", resolved)
	}
	if err := service.ValidateSelection("pi", []string{mcp.ID}, nil); err == nil {
		t.Fatal("local MCP should remain incompatible with Pi")
	}
}

func TestResolveLoadsOnlyOneCapabilityPerFingerprintGroup(t *testing.T) {
	service := New(t.TempDir())
	first, err := service.UpsertManaged(Item{Kind: KindMCP, Name: "duplicate-a", Provider: "all", Spec: map[string]any{"command": "same-server"}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.UpsertManaged(Item{Kind: KindMCP, Name: "duplicate-b", Provider: "all", Spec: map[string]any{"command": "same-server"}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.GroupID != second.GroupID {
		t.Fatalf("identical MCPs were not grouped: %q != %q", first.GroupID, second.GroupID)
	}
	resolved := service.Resolve("codex", []string{first.ID, second.ID}, nil)
	if len(resolved.MCPs) != 1 {
		t.Fatalf("resolved %d identical MCPs, want 1", len(resolved.MCPs))
	}
}
