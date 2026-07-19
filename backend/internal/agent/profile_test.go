package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWindowsCommandLineQuotesSpacesAndQuotes(t *testing.T) {
	got := WindowsCommandLine(`C:\Program Files\agent.exe`, []string{`plain`, `has space`, `say "yes"`, `trailing\`})
	wantParts := []string{`"C:\Program Files\agent.exe"`, `plain`, `"has space"`, `"say \"yes\""`, `trailing\`}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("WindowsCommandLine() = %q, missing %q", got, want)
		}
	}
}

func TestHookDefinitionContainsStableForwarderWithoutSecrets(t *testing.T) {
	hooks := hookJSON([]string{"SessionStart", "Stop"})
	data, err := json.Marshal(hooks)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"SessionStart", "Stop", "hook-forward", "commandWindows", "AGENT_INFINITE_BACKEND_EXE"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("hook config missing %q: %s", expected, text)
		}
	}
	for _, forbidden := range []string{"AGENT_INFINITE_HOOK_TOKEN", "http://", "workspace", `C:\\Program Files`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hook config leaked session data %q: %s", forbidden, text)
		}
	}
}

func TestTomlStringEscapesValues(t *testing.T) {
	if got := tomlString(`http://localhost/a"b`); got != `"http://localhost/a\"b"` {
		t.Fatalf("tomlString() = %q", got)
	}
}

func TestMockProviderIsTestModeOnly(t *testing.T) {
	t.Setenv("AGENT_INFINITE_TEST_MODE", "")
	options := LaunchOptions{Provider: "mock", WorkDir: t.TempDir(), RuntimeDir: t.TempDir(), NodeID: "node", MCPBaseURL: "http://127.0.0.1", MCPToken: "token"}
	if _, err := BuildLaunch(options); err == nil {
		t.Fatal("expected mock provider to be rejected outside test mode")
	}
	t.Setenv("AGENT_INFINITE_TEST_MODE", "1")
	spec, err := BuildLaunch(options)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Provider != "mock" || !strings.Contains(spec.CommandLine, "MOCK_DONE") {
		t.Fatalf("unexpected mock launch: %#v", spec)
	}
}

func TestPiExtensionUsesScopedMCPAndLifecycleBridge(t *testing.T) {
	text := piExtension(LaunchOptions{NodeID: "pi-node", NodeLabel: "Lead", NodeRole: "Coordinate delivery", NodeKind: "orchestrator", TeamID: "team-one", MCPBaseURL: "http://127.0.0.1:4312", MCPToken: "secret"})
	for _, expected := range []string{"agent_infinite_delegate_task", "agent_infinite_list_connected_agents", "agent_infinite_get_dispatch_result", "agent_settled", "AGENT_INFINITE_HOOK_TOKEN", "/mcp/pi-node", "Lead", "Coordinate delivery", "team orchestrator"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Pi extension missing %q", expected)
		}
	}
	if strings.Contains(text, "secret") {
		t.Fatal("Pi extension must read the MCP token from the process environment")
	}
}

func TestSessionInstructionsDefineOrchestratorAndWorkerIdentity(t *testing.T) {
	orchestrator := sessionInstructions(LaunchOptions{
		NodeID: "lead-id", NodeLabel: "Release Lead", NodeRole: "Coordinate the release", NodeKind: "orchestrator", TeamID: "release-team",
		Connections: []SessionConnection{{ID: "reviewer-id", Label: "Reviewer", Role: "Review changes", Kind: "agent", Provider: "codex", Direction: "delegates_to"}},
	})
	for _, expected := range []string{"Release Lead", "lead-id", "Coordinate the release", "release-team", "list_connected_agents", "delegate_task", "Never create or substitute provider-native subagents", "delegates_to", "reviewer-id", "Review changes"} {
		if !strings.Contains(orchestrator, expected) {
			t.Fatalf("orchestrator contract missing %q: %s", expected, orchestrator)
		}
	}
	worker := sessionInstructions(LaunchOptions{NodeID: "reviewer-id", NodeLabel: "Reviewer", NodeRole: "Review changes", NodeKind: "agent"})
	for _, expected := range []string{"Reviewer", "Review changes", "worker agent", "Agent Infinite dispatch"} {
		if !strings.Contains(worker, expected) {
			t.Fatalf("worker contract missing %q: %s", expected, worker)
		}
	}
}

func TestOpenCodePluginUsesScopedLifecycleBridge(t *testing.T) {
	text := openCodePlugin()
	for _, expected := range []string{"session.created", "session.idle", "session.error", "AGENT_INFINITE_HOOK_TOKEN", "/internal/hooks/events"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("OpenCode plugin missing %q", expected)
		}
	}
}
