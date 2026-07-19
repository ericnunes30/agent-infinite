//go:build windows

package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstalledProvidersParseEphemeralHookConfiguration(t *testing.T) {
	if os.Getenv("AGENT_INFINITE_REAL_PROVIDERS") != "1" {
		t.Skip("set AGENT_INFINITE_REAL_PROVIDERS=1 to validate ephemeral capability isolation with installed providers")
	}
	backendExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"codex", "claude", "pi", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			if _, lookErr := exec.LookPath(provider); lookErr != nil {
				t.Skipf("%s is not installed", provider)
			}
			runtimeDir := filepath.Join(t.TempDir(), "runtime")
			skillRoot := t.TempDir()
			skillA, skillB := filepath.Join(skillRoot, "a", "SKILL.md"), filepath.Join(skillRoot, "b", "SKILL.md")
			for _, path := range []string{skillA, skillB} {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("---\nname: portable\n---\nPortable instructions."), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			model := "test-model-id"
			if provider == "pi" || provider == "opencode" {
				model = "provider/test-model-id"
			}
			options := LaunchOptions{
				Provider: provider, WorkDir: t.TempDir(), RuntimeDir: runtimeDir, NodeID: "node",
				Model:      model,
				MCPBaseURL: "http://127.0.0.1:12345", MCPToken: "mcp-secret",
				Hooks: HookLaunch{
					Enabled: true, Policy: "auto", SessionID: "session", Token: "hook-secret",
					WorkspaceID: "workspace", BackendExecutable: backendExecutable,
				},
				MCPs:        []Capability{{Name: "allowed_one", Spec: map[string]any{"type": "http", "url": "https://127.0.0.1:9/one"}}, {Name: "allowed_two", Spec: map[string]any{"type": "http", "url": "https://127.0.0.1:9/two"}}},
				BlockedMCPs: []Capability{{Name: "blocked_one"}, {Name: "blocked_two"}},
				Skills:      []Capability{{Name: "portable-a", Path: skillA}, {Name: "portable-b", Path: skillB}},
			}
			spec, buildErr := BuildLaunch(options)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if strings.Contains(spec.CommandLine, "mcp-secret") || strings.Contains(spec.CommandLine, "hook-secret") {
				t.Fatalf("session secret leaked into command line: %s", spec.CommandLine)
			}
			if !containsArgumentPair(spec.Args, "--model", model) {
				t.Fatalf("provider launch did not receive explicit model: %#v", spec.Args)
			}
			command := exec.Command(spec.Executable, append(spec.Args, "--version")...)
			command.Env = spec.Env
			done := make(chan struct{})
			var output []byte
			var runErr error
			go func() {
				output, runErr = command.CombinedOutput()
				close(done)
			}()
			select {
			case <-done:
				if runErr != nil {
					t.Fatalf("provider rejected generated config: %v\n%s", runErr, output)
				}
			case <-time.After(15 * time.Second):
				_ = command.Process.Kill()
				t.Fatal("provider config parse check timed out")
			}
			spec.Cleanup()
			if _, statErr := os.Stat(runtimeDir); !os.IsNotExist(statErr) {
				t.Fatalf("ephemeral runtime directory survived cleanup: %v", statErr)
			}
		})
	}
}

func TestInstalledCodexParsesSelectedAndBlockedMCPsTogether(t *testing.T) {
	if os.Getenv("AGENT_INFINITE_REAL_PROVIDERS") != "1" {
		t.Skip("set AGENT_INFINITE_REAL_PROVIDERS=1 to validate Codex MCP policy composition")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	spec, err := BuildLaunch(LaunchOptions{
		Provider: "codex", WorkDir: t.TempDir(), RuntimeDir: t.TempDir(), NodeID: "node",
		MCPBaseURL: "http://127.0.0.1:12345", MCPToken: "mcp-secret",
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
	defer spec.Cleanup()
	command := exec.Command(spec.Executable, append(spec.Args, "mcp", "list")...)
	command.Env = spec.Env
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Codex rejected combined MCP policies: %v\n%s", err, output)
	}
	text := string(output)
	for _, expected := range []string{"mcp-tool-search", "github", "disabled"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Codex MCP list is missing %q:\n%s", expected, text)
		}
	}
}

func containsArgumentPair(args []string, key, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key && args[index+1] == value {
			return true
		}
	}
	return false
}
