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
