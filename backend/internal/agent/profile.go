package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrExecutableMissing = errors.New("provider executable is not on PATH")

type HookLaunch struct {
	Enabled           bool
	Policy            string
	SessionID         string
	Token             string
	WorkspaceID       string
	BackendExecutable string
	Cleanup           func()
}

type LaunchOptions struct {
	Provider   string
	WorkDir    string
	RuntimeDir string
	NodeID     string
	MCPBaseURL string
	MCPToken   string
	Hooks      HookLaunch
}

type LaunchSpec struct {
	Provider        string
	Executable      string
	Args            []string
	CommandLine     string
	WorkDir         string
	Env             []string
	IntegrationMode string
	HookSessionID   string
	Cleanup         func()
}

func BuildLaunch(options LaunchOptions) (LaunchSpec, error) {
	switch options.Provider {
	case "claude":
		return buildClaude(options)
	case "codex":
		return buildCodex(options)
	case "mock":
		return buildMock(options)
	default:
		return LaunchSpec{}, fmt.Errorf("unknown provider %q", options.Provider)
	}
}

func buildMock(options LaunchOptions) (LaunchSpec, error) {
	if os.Getenv("AGENT_INFINITE_TEST_MODE") != "1" {
		return LaunchSpec{}, errors.New("mock provider is available only in test mode")
	}
	executable, err := discover("powershell.exe")
	if err != nil {
		return LaunchSpec{}, err
	}
	hook := ""
	if options.Hooks.Enabled {
		hook = `$forward = $env:AGENT_INFINITE_BACKEND_EXE; '{"hook_event_name":"SessionStart"}' | & $forward hook-forward | Out-Null; `
	}
	script := `$OutputEncoding = [Console]::OutputEncoding = [Text.UTF8Encoding]::new(); ` + hook + `[Console]::Write('> '); while (($line = [Console]::In.ReadLine()) -ne $null) { `
	if options.Hooks.Enabled {
		script += `$prompt = @{hook_event_name='UserPromptSubmit'; prompt=$line} | ConvertTo-Json -Compress; $prompt | & $forward hook-forward | Out-Null; `
	}
	script += `[Console]::WriteLine(); [Console]::WriteLine('MOCK_DONE: ' + $line); Start-Sleep -Milliseconds 150; `
	if options.Hooks.Enabled {
		script += `'{"hook_event_name":"Stop"}' | & $forward hook-forward | Out-Null; `
	}
	script += `[Console]::Write('> ') }`
	args := []string{"-NoLogo", "-NoProfile", "-Command", script}
	return spec(options, "mock", executable, args, options.WorkDir, nil, func() {}), nil
}

func buildClaude(options LaunchOptions) (LaunchSpec, error) {
	executable, err := discover("claude")
	if err != nil {
		return LaunchSpec{}, fmt.Errorf("Claude Code is not installed: %w", err)
	}
	if err := os.MkdirAll(options.RuntimeDir, 0o700); err != nil {
		return LaunchSpec{}, err
	}
	mcpPath := filepath.Join(options.RuntimeDir, "mcp.json")
	config := map[string]any{"mcpServers": map[string]any{"agent_infinite": map[string]any{
		"type": "http", "url": mcpURL(options.MCPBaseURL, options.NodeID), "headers": map[string]string{"Authorization": "Bearer " + options.MCPToken},
	}}}
	if err := writePrivateJSON(mcpPath, config); err != nil {
		return LaunchSpec{}, fmt.Errorf("write Claude MCP config: %w", err)
	}
	paths := []string{mcpPath}
	args := []string{
		"--mcp-config", mcpPath,
		"--strict-mcp-config",
		"--allowed-tools", "mcp__agent_infinite__list_connected_agents mcp__agent_infinite__delegate_task mcp__agent_infinite__get_dispatch_result",
	}
	if options.Hooks.Enabled {
		settingsPath := filepath.Join(options.RuntimeDir, "settings.json")
		if err := writePrivateJSON(settingsPath, map[string]any{"hooks": hookJSON([]string{"SessionStart", "UserPromptSubmit", "Stop", "SubagentStart", "SubagentStop", "SessionEnd"})}); err != nil {
			return LaunchSpec{}, fmt.Errorf("write Claude hook settings: %w", err)
		}
		paths = append(paths, settingsPath)
		args = append(args, "--settings", settingsPath)
	}
	return spec(options, "claude", executable, args, options.WorkDir, paths, func() {}), nil
}

func buildCodex(options LaunchOptions) (LaunchSpec, error) {
	executable, err := discover("codex")
	if err != nil {
		return LaunchSpec{}, fmt.Errorf("Codex is not installed: %w", err)
	}
	args := []string{
		"-c", "mcp_servers.agent_infinite.url=" + tomlString(mcpURL(options.MCPBaseURL, options.NodeID)),
		"-c", "mcp_servers.agent_infinite.bearer_token_env_var=" + tomlString("AGENT_INFINITE_MCP_TOKEN"),
		"-c", "mcp_servers.agent_infinite.enabled=true",
	}
	if options.Hooks.Enabled {
		command := hookForwardCommand()
		definition := fmt.Sprintf(`[{hooks=[{type="command",command=%s,command_windows=%s,timeout=5}]}]`, tomlString(command), tomlString(command))
		for _, event := range []string{"SessionStart", "UserPromptSubmit", "Stop", "SubagentStart", "SubagentStop"} {
			args = append(args, "-c", "hooks."+event+"="+definition)
		}
	}
	return spec(options, "codex", executable, args, options.WorkDir, nil, func() {}), nil
}

func spec(options LaunchOptions, provider, executable string, args []string, workDir string, temporaryPaths []string, cleanup func()) LaunchSpec {
	env := append([]string(nil), os.Environ()...)
	env = append(env, "AGENT_INFINITE_MCP_TOKEN="+options.MCPToken)
	mode := "detector"
	if options.Hooks.Enabled {
		mode = "hooks-pending"
		env = append(env,
			"AGENT_INFINITE_BACKEND_EXE="+options.Hooks.BackendExecutable,
			"AGENT_INFINITE_BACKEND_URL="+strings.TrimRight(options.MCPBaseURL, "/"),
			"AGENT_INFINITE_HOOK_TOKEN="+options.Hooks.Token,
			"AGENT_INFINITE_HOOK_SESSION_ID="+options.Hooks.SessionID,
			"AGENT_INFINITE_NODE_ID="+options.NodeID,
			"AGENT_INFINITE_WORKSPACE_ID="+options.Hooks.WorkspaceID,
			"AGENT_INFINITE_PROVIDER="+provider,
		)
	}
	return LaunchSpec{
		Provider: provider, Executable: executable, Args: args, CommandLine: WindowsCommandLine(executable, args),
		WorkDir: workDir, Env: env, IntegrationMode: mode, HookSessionID: options.Hooks.SessionID,
		Cleanup: func() {
			cleanup()
			for _, path := range temporaryPaths {
				_ = os.Remove(path)
			}
			_ = os.Remove(options.RuntimeDir)
			if options.Hooks.Cleanup != nil {
				options.Hooks.Cleanup()
			}
		},
	}
}

func hookJSON(events []string) map[string]any {
	command := hookForwardCommand()
	result := make(map[string]any, len(events))
	for _, event := range events {
		handler := map[string]any{"type": "command", "command": command, "commandWindows": command, "timeout": 5}
		result[event] = []any{map[string]any{"hooks": []any{handler}}}
	}
	return result
}

func hookForwardCommand() string {
	// Claude Code invokes command hooks through Bash even on Windows. Keeping
	// the PowerShell program in single quotes protects $env from Bash expansion;
	// the actual executable path and all session credentials remain inherited
	// environment variables. Codex also receives this same stable definition so
	// its one-time trust hash does not change when Agent Infinite is upgraded or
	// installed in a different directory.
	return `powershell.exe -NoLogo -NoProfile -NonInteractive -Command '& $env:AGENT_INFINITE_BACKEND_EXE hook-forward'`
}

func writePrivateJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func discover(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", ErrExecutableMissing
	}
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		return path, nil
	}
	if output, whereErr := exec.Command("where.exe", name).Output(); whereErr == nil {
		for _, candidate := range strings.Split(string(output), "\n") {
			candidate = strings.TrimSpace(candidate)
			if strings.EqualFold(filepath.Ext(candidate), ".exe") {
				return candidate, nil
			}
		}
	}
	return path, nil
}

func mcpURL(baseURL, nodeID string) string { return strings.TrimRight(baseURL, "/") + "/mcp/" + nodeID }

func tomlString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func WindowsCommandLine(executable string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteWindowsArg(executable))
	for _, arg := range args {
		parts = append(parts, quoteWindowsArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteWindowsArg(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n\v\"") {
		return value
	}
	var result strings.Builder
	result.WriteByte('"')
	backslashes := 0
	for _, character := range value {
		if character == '\\' {
			backslashes++
			continue
		}
		if character == '"' {
			result.WriteString(strings.Repeat("\\", backslashes*2+1))
			result.WriteRune(character)
			backslashes = 0
			continue
		}
		result.WriteString(strings.Repeat("\\", backslashes))
		backslashes = 0
		result.WriteRune(character)
	}
	result.WriteString(strings.Repeat("\\", backslashes*2))
	result.WriteByte('"')
	return result.String()
}
