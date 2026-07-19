package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestCodexCapabilityArgsCarryOnlyEffectiveAndExplicitlyBlockedItems(t *testing.T) {
	options := LaunchOptions{
		MCPs:          []Capability{{Name: "selected-a", Spec: map[string]any{"url": "https://a.test"}}, {Name: "selected-b", Spec: map[string]any{"command": "server", "args": []any{"--safe"}}}},
		BlockedMCPs:   []Capability{{Name: "blocked-a"}, {Name: "blocked-b"}},
		Skills:        []Capability{{Name: "skill-a", Path: `C:\skills\a\SKILL.md`}, {Name: "skill-b", Path: `C:\skills\b\SKILL.md`}},
		BlockedSkills: []Capability{{Name: "skill-c", Path: `C:\skills\c\SKILL.md`}},
	}
	joined := strings.Join(codexCapabilityArgs(options), " ")
	for _, expected := range []string{"selected-a", "selected-b", "blocked-a", "blocked-b", "skills.config", "enabled=false"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in %s", expected, joined)
		}
	}
	if strings.Contains(joined, "unknown-server") {
		t.Fatal("an unselected capability was emitted")
	}
}

func TestCodexProfileKeepsLargeInventoriesOutOfTheCommandLine(t *testing.T) {
	sourceHome := t.TempDir()
	t.Setenv("CODEX_HOME", sourceHome)
	capabilities := make([]Capability, 0, 250)
	for index := 0; index < 250; index++ {
		capabilities = append(capabilities, Capability{
			Name: fmt.Sprintf("server-%03d-with-a-descriptive-name", index),
			Spec: map[string]any{"url": fmt.Sprintf("https://example.test/mcp/%03d", index)},
		})
	}
	options := LaunchOptions{
		RuntimeDir: t.TempDir(),
		NodeID:     "node-large",
		MCPBaseURL: "http://127.0.0.1:9137",
		MCPs:       capabilities,
		BlockedMCPs: []Capability{
			{Name: "blocked-one", Spec: map[string]any{"command": "blocked-one-server"}},
			{Name: "blocked-two", Spec: map[string]any{"url": "https://blocked-two.test"}},
		},
	}
	home, args, _, err := materializeCodexProfile(options)
	if err != nil {
		t.Fatal(err)
	}
	commandLine := WindowsCommandLine(`C:\Program Files\Codex\codex.exe`, args)
	if len(commandLine) >= 4096 {
		t.Fatalf("expected a short command line, got %d characters", len(commandLine))
	}
	if strings.Contains(commandLine, "server-249") {
		t.Fatal("selected MCP definition leaked into the command line")
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	for _, expected := range []string{"agent_infinite", "server-249-with-a-descriptive-name", "blocked-one"} {
		if !strings.Contains(config, expected) {
			t.Fatalf("materialized config is missing %q", expected)
		}
	}
}

func TestCodexProfileKeepsManagedSecretsOutOfConfig(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	home, _, environment, err := materializeCodexProfile(LaunchOptions{
		RuntimeDir: t.TempDir(), NodeID: "node", MCPBaseURL: "http://127.0.0.1:1",
		MCPs: []Capability{{Name: "private", Spec: map[string]any{
			"url": "https://private.test", "headers": map[string]any{"X-API-Key": "top-secret"}, "bearer_token": "bearer-secret",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "top-secret") || strings.Contains(string(data), "bearer-secret") {
		t.Fatal("a managed secret was written to config.toml")
	}
	joinedEnvironment := strings.Join(environment, "\n")
	if !strings.Contains(joinedEnvironment, "top-secret") || !strings.Contains(joinedEnvironment, "bearer-secret") {
		t.Fatal("managed secrets were not passed through the child environment")
	}
}

func TestCodexProfileInjectsIdentityAndVettedHooks(t *testing.T) {
	sourceHome := t.TempDir()
	source := `developer_instructions = "Keep the repository conventions."

[plugins."external-plugin"]
enabled = true
`
	if err := os.WriteFile(filepath.Join(sourceHome, "config.toml"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", sourceHome)
	workDir := filepath.Join(t.TempDir(), "repo")
	home, args, _, err := materializeCodexProfile(LaunchOptions{
		RuntimeDir: t.TempDir(), WorkDir: workDir, NodeID: "lead-id", NodeLabel: "Lead", NodeRole: "Coordinate agents", NodeKind: "orchestrator", TeamID: "team", MCPBaseURL: "http://127.0.0.1:1",
		Hooks: HookLaunch{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(args, "--dangerously-bypass-hook-trust") {
		t.Fatalf("Codex hook trust bypass missing from isolated launch args: %v", args)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{}
	if err := toml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	instructions, _ := config["developer_instructions"].(string)
	for _, expected := range []string{"Keep the repository conventions.", "Lead", "Coordinate agents", "list_connected_agents"} {
		if !strings.Contains(instructions, expected) {
			t.Fatalf("Codex instructions missing %q: %s", expected, instructions)
		}
	}
	if _, exists := config["plugins"]; exists {
		t.Fatal("external plugins must not enter the isolated hook trust boundary")
	}
	projects, _ := config["projects"].(map[string]any)
	project, _ := projects[filepath.Clean(workDir)].(map[string]any)
	if project["trust_level"] != "untrusted" {
		t.Fatalf("isolated worktree trust = %#v", project)
	}
	if _, exists := config["hooks"]; !exists {
		t.Fatal("Agent Infinite hook table was not materialized")
	}
	features, _ := config["features"].(map[string]any)
	if features["hooks"] != true {
		t.Fatalf("isolated Codex hooks feature was not forced on: %#v", features)
	}
}

func TestCodexProfileOverridesExternallyDisabledHooksOnlyInDisposableHome(t *testing.T) {
	sourceHome := t.TempDir()
	source := "[features]\nhooks = false\n"
	if err := os.WriteFile(filepath.Join(sourceHome, "config.toml"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", sourceHome)
	home, _, _, err := materializeCodexProfile(LaunchOptions{
		RuntimeDir: t.TempDir(), WorkDir: t.TempDir(), NodeID: "node", MCPBaseURL: "http://127.0.0.1:1",
		Hooks: HookLaunch{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{}
	if err := toml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	features, _ := config["features"].(map[string]any)
	if features["hooks"] != true {
		t.Fatalf("temporary hooks flag = %#v", features["hooks"])
	}
	original, err := os.ReadFile(filepath.Join(sourceHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != source {
		t.Fatalf("external Codex config was modified: %q", original)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestClaudeSkillMaterializationCopiesOnlyEffectiveSkills(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "source", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(selected), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(selected, []byte("---\nname: selected\n---\nAllowed"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := materializeClaudeSkills(root, []Capability{{Name: "selected", Path: selected}})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected one plugin, got %d", len(paths))
	}
	data, err := os.ReadFile(filepath.Join(paths[0], "skills", "selected", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Allowed") {
		t.Fatal("selected skill content was not materialized")
	}
	if _, err := os.Stat(filepath.Join(root, "claude-skills", "blocked")); !os.IsNotExist(err) {
		t.Fatal("blocked skill was materialized")
	}
}

func TestPiExtensionContainsOnlyAuthorizedRemoteMCPs(t *testing.T) {
	value := piExtension(LaunchOptions{NodeID: "node", MCPBaseURL: "http://127.0.0.1:1", MCPs: []Capability{{Name: "allowed-a", Spec: map[string]any{"url": "https://a.test"}}, {Name: "allowed-b", Spec: map[string]any{"url": "https://b.test"}}}, BlockedMCPs: []Capability{{Name: "blocked"}}})
	if !strings.Contains(value, "allowed-a") || !strings.Contains(value, "allowed-b") {
		t.Fatal("authorized MCP missing from Pi bridge")
	}
	if strings.Contains(value, `"blocked"`) {
		t.Fatal("blocked MCP leaked into Pi bridge")
	}
}

func TestCodexDisabledMCPKeepsAValidTransport(t *testing.T) {
	stdio, ok := codexDisabledServer(map[string]any{"type": "stdio", "command": "github-mcp", "args": []any{"serve"}})
	if !ok || stdio["command"] != "github-mcp" || stdio["enabled"] != false {
		t.Fatalf("stdio disabled server = %#v, %v", stdio, ok)
	}
	httpServer, ok := codexDisabledServer(map[string]any{"type": "http", "url": "https://mcp.example.test"})
	if !ok || httpServer["url"] != "https://mcp.example.test" || httpServer["enabled"] != false {
		t.Fatalf("http disabled server = %#v, %v", httpServer, ok)
	}
	if _, ok := codexDisabledServer(map[string]any{"type": "stdio"}); ok {
		t.Fatal("transport without command or URL must not create an invalid Codex MCP table")
	}
}

func TestCodexBlockedOverrideUsesTheMaterializedServerKey(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	home, args, _, err := materializeCodexProfile(LaunchOptions{
		RuntimeDir: t.TempDir(), NodeID: "node", MCPBaseURL: "http://127.0.0.1:1",
		BlockedMCPs: []Capability{{
			Name: "Git Hub/Enterprise",
			Spec: map[string]any{"type": "http", "url": "https://example.test/mcp"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, "mcp_servers.Git-Hub-Enterprise.enabled=false") {
		t.Fatalf("override does not target the normalized materialized key: %s", joined)
	}
	if strings.Contains(joined, `mcp_servers."`) || strings.Contains(joined, "Git Hub/Enterprise") {
		t.Fatalf("override creates a second quoted/display-name MCP key: %s", joined)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{}
	if err := toml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	servers, _ := config["mcp_servers"].(map[string]any)
	server, _ := servers["Git-Hub-Enterprise"].(map[string]any)
	if server["url"] != "https://example.test/mcp" || server["enabled"] != false {
		t.Fatalf("materialized blocked server lost its transport: %#v", server)
	}
}

func TestCodexSelectedAndBlockedMCPsKeepIndependentTransports(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	home, args, _, err := materializeCodexProfile(LaunchOptions{
		RuntimeDir: t.TempDir(), NodeID: "node", MCPBaseURL: "http://127.0.0.1:1",
		MCPs: []Capability{{
			Name: "mcp-tool-search",
			Spec: map[string]any{"type": "stdio", "command": "tool-search", "args": []any{"serve"}},
		}},
		BlockedMCPs: []Capability{{
			Name: "github",
			Spec: map[string]any{"type": "http", "url": "https://api.githubcopilot.com/mcp/"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{}
	if err := toml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	servers, _ := config["mcp_servers"].(map[string]any)
	toolSearch, _ := servers["mcp-tool-search"].(map[string]any)
	github, _ := servers["github"].(map[string]any)
	if toolSearch["command"] != "tool-search" || github["url"] != "https://api.githubcopilot.com/mcp/" {
		t.Fatalf("combined transports = tool-search %#v, github %#v", toolSearch, github)
	}
	if strings.Contains(strings.Join(args, "\n"), `mcp_servers."github"`) {
		t.Fatalf("github override uses a quoted shadow key: %v", args)
	}
}
