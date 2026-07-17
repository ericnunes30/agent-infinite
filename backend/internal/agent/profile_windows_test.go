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
	backendExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			if _, lookErr := exec.LookPath(provider); lookErr != nil {
				t.Skipf("%s is not installed", provider)
			}
			runtimeDir := filepath.Join(t.TempDir(), "runtime")
			options := LaunchOptions{
				Provider: provider, WorkDir: t.TempDir(), RuntimeDir: runtimeDir, NodeID: "node",
				MCPBaseURL: "http://127.0.0.1:12345", MCPToken: "mcp-secret",
				Hooks: HookLaunch{
					Enabled: true, Policy: "auto", SessionID: "session", Token: "hook-secret",
					WorkspaceID: "workspace", BackendExecutable: backendExecutable,
				},
			}
			spec, buildErr := BuildLaunch(options)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if strings.Contains(spec.CommandLine, "mcp-secret") || strings.Contains(spec.CommandLine, "hook-secret") {
				t.Fatalf("session secret leaked into command line: %s", spec.CommandLine)
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
