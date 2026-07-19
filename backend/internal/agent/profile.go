package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrExecutableMissing = errors.New("provider executable is not on PATH")
var ErrProviderIncompatible = errors.New("provider executable is incompatible")

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
	Provider      string
	Model         string
	WorkDir       string
	RuntimeDir    string
	NodeID        string
	NodeLabel     string
	NodeRole      string
	NodeKind      string
	TeamID        string
	Connections   []SessionConnection
	MCPBaseURL    string
	MCPToken      string
	Hooks         HookLaunch
	MCPs          []Capability
	BlockedMCPs   []Capability
	Skills        []Capability
	BlockedSkills []Capability
}

type SessionConnection struct {
	ID        string
	Label     string
	Role      string
	Kind      string
	Provider  string
	Direction string
}

type Capability struct {
	Name string
	Path string
	Spec map[string]any
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
	case "pi":
		return buildPi(options)
	case "opencode":
		return buildOpenCode(options)
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
	servers := map[string]any{"agent_infinite": map[string]any{
		"type": "http", "url": mcpURL(options.MCPBaseURL, options.NodeID), "headers": map[string]string{"Authorization": "Bearer " + options.MCPToken},
	}}
	for _, capability := range options.MCPs {
		name := safeCapabilityName(capability.Name)
		if _, duplicate := servers[name]; duplicate {
			return LaunchSpec{}, fmt.Errorf("Claude MCP names collide after normalization: %q", capability.Name)
		}
		servers[name] = claudeMCPSpec(capability.Spec)
	}
	config := map[string]any{"mcpServers": servers}
	if err := writePrivateJSON(mcpPath, config); err != nil {
		return LaunchSpec{}, fmt.Errorf("write Claude MCP config: %w", err)
	}
	paths := []string{mcpPath}
	instructionsPath := filepath.Join(options.RuntimeDir, "agent-infinite-instructions.md")
	if err := writePrivateText(instructionsPath, sessionInstructions(options)); err != nil {
		return LaunchSpec{}, fmt.Errorf("write Claude session instructions: %w", err)
	}
	paths = append(paths, instructionsPath)
	allowedTools := []string{"mcp__agent_infinite__list_connected_agents", "mcp__agent_infinite__delegate_task", "mcp__agent_infinite__get_dispatch_result"}
	for _, capability := range options.MCPs {
		allowedTools = append(allowedTools, "mcp__"+safeCapabilityName(capability.Name)+"__*")
	}
	args := []string{
		"--mcp-config", mcpPath,
		"--strict-mcp-config",
		"--setting-sources", "",
		"--append-system-prompt-file", instructionsPath,
		"--allowed-tools", strings.Join(allowedTools, " "),
	}
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	skillPaths, err := materializeClaudeSkills(options.RuntimeDir, options.Skills)
	if err != nil {
		return LaunchSpec{}, err
	}
	paths = append(paths, skillPaths...)
	for _, path := range skillPaths {
		args = append(args, "--plugin-dir", path)
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
	configHome, args, environment, err := materializeCodexProfile(options)
	if err != nil {
		return LaunchSpec{}, err
	}
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	launch := spec(options, "codex", executable, args, options.WorkDir, nil, func() {})
	launch.Env = append(launch.Env, "CODEX_HOME="+configHome)
	launch.Env = append(launch.Env, environment...)
	return launch, nil
}

func buildPi(options LaunchOptions) (LaunchSpec, error) {
	executable, err := discover("pi")
	if err != nil {
		return LaunchSpec{}, fmt.Errorf("Pi is not installed: %w", err)
	}
	if err := os.MkdirAll(options.RuntimeDir, 0o700); err != nil {
		return LaunchSpec{}, err
	}
	for _, capability := range options.MCPs {
		typeName, _ := capability.Spec["type"].(string)
		if _, hasURL := capability.Spec["url"].(string); !hasURL && typeName != "remote" && typeName != "http" {
			return LaunchSpec{}, fmt.Errorf("Pi cannot enforce local MCP %q in this release: %w", capability.Name, ErrProviderIncompatible)
		}
	}
	extensionPath := filepath.Join(options.RuntimeDir, "agent-infinite-pi.ts")
	if err := writePrivateText(extensionPath, piExtension(options)); err != nil {
		return LaunchSpec{}, fmt.Errorf("write Pi extension: %w", err)
	}
	args := []string{"--extension", extensionPath, "--no-skills"}
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	for _, skill := range options.Skills {
		if skill.Path != "" {
			args = append(args, "--skill", skill.Path)
		}
	}
	return spec(options, "pi", executable, args, options.WorkDir, nil, func() {}), nil
}

func buildOpenCode(options LaunchOptions) (LaunchSpec, error) {
	executable, err := discoverOpenCode()
	if err != nil {
		return LaunchSpec{}, err
	}
	if err := ensureOpenCodeVersion(executable); err != nil {
		return LaunchSpec{}, err
	}
	configDir := filepath.Join(options.RuntimeDir, "opencode")
	pluginDir := filepath.Join(configDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		return LaunchSpec{}, err
	}
	pluginPath := filepath.Join(pluginDir, "agent-infinite.ts")
	if err := writePrivateText(pluginPath, openCodePlugin()); err != nil {
		return LaunchSpec{}, fmt.Errorf("write OpenCode plugin: %w", err)
	}
	instructionsPath := filepath.Join(configDir, "agent-infinite-instructions.md")
	if err := writePrivateText(instructionsPath, sessionInstructions(options)); err != nil {
		return LaunchSpec{}, fmt.Errorf("write OpenCode session instructions: %w", err)
	}
	servers := map[string]any{"agent_infinite": map[string]any{
		"type": "remote", "url": mcpURL(options.MCPBaseURL, options.NodeID), "enabled": true, "oauth": false,
		"headers": map[string]string{"Authorization": "Bearer {env:AGENT_INFINITE_MCP_TOKEN}"},
	}}
	for _, capability := range options.MCPs {
		servers[capability.Name] = openCodeMCPSpec(capability.Spec)
	}
	for _, capability := range options.BlockedMCPs {
		servers[capability.Name] = map[string]any{"enabled": false}
	}
	permission := map[string]any{"skill": map[string]any{"*": "deny"}}
	for _, skill := range options.Skills {
		permission["skill"].(map[string]any)[skill.Name] = "allow"
	}
	config := map[string]any{"mcp": servers, "permission": permission, "instructions": []any{instructionsPath}}
	if err := materializeOpenCodeSkills(configDir, options.Skills); err != nil {
		return LaunchSpec{}, err
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return LaunchSpec{}, err
	}
	args := []string{}
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	launch := spec(options, "opencode", executable, args, options.WorkDir, nil, func() {})
	launch.Env = append(launch.Env, "OPENCODE_CONFIG_DIR="+configDir, "OPENCODE_CONFIG_CONTENT="+string(configJSON))
	return launch, nil
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
			_ = os.RemoveAll(options.RuntimeDir)
			if options.Hooks.Cleanup != nil {
				options.Hooks.Cleanup()
			}
		},
	}
}

func writePrivateText(path, value string) error { return os.WriteFile(path, []byte(value), 0o600) }

func discoverOpenCode() (string, error) {
	// npm's shim is preferred: it is how Agent Infinite updates OpenCode and it
	// must win over an older standalone binary earlier on PATH.
	if output, err := exec.Command("npm.cmd", "prefix", "-g").Output(); err == nil {
		candidate := filepath.Join(strings.TrimSpace(string(output)), "node_modules", "opencode-ai", "bin", "opencode.exe")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	executable, err := discover("opencode")
	if err != nil {
		return "", fmt.Errorf("OpenCode is not installed: %w", err)
	}
	return executable, nil
}

func ensureOpenCodeVersion(executable string) error {
	output, err := exec.Command(executable, "--version").Output()
	if err != nil {
		return fmt.Errorf("check OpenCode version: %w", err)
	}
	version := strings.TrimSpace(strings.TrimPrefix(string(output), "v"))
	majorText, _, _ := strings.Cut(version, ".")
	major, parseErr := strconv.Atoi(majorText)
	if parseErr != nil || major < 1 {
		return fmt.Errorf("OpenCode %q is unsupported; update it with npm install -g opencode-ai@latest: %w", version, ErrProviderIncompatible)
	}
	return nil
}

func piExtension(options LaunchOptions) string {
	endpoint := mcpURL(options.MCPBaseURL, options.NodeID)
	contextText := sessionInstructions(options)
	externalServers := map[string]map[string]any{}
	for _, capability := range options.MCPs {
		externalServers[capability.Name] = capability.Spec
	}
	externalJSON, _ := json.Marshal(externalServers)
	return fmt.Sprintf(`import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

const endpoint = %s;
const contextText = %s;
let requestID = 0;
let initialized = false;
let sessionID = "";
const externalServers = %s;
const externalSessions = new Map<string, string>();
async function lifecycle(event: string, raw: unknown) {
  if (!process.env.AGENT_INFINITE_HOOK_TOKEN) return;
  await fetch(process.env.AGENT_INFINITE_BACKEND_URL + "/internal/hooks/events", {method: "POST", headers: {"Content-Type":"application/json", "X-Agent-Infinite-Hook-Token": process.env.AGENT_INFINITE_HOOK_TOKEN}, body: JSON.stringify({sessionId: process.env.AGENT_INFINITE_HOOK_SESSION_ID, nodeId: process.env.AGENT_INFINITE_NODE_ID, workspaceId: process.env.AGENT_INFINITE_WORKSPACE_ID, provider: "pi", event, raw})}).catch(() => undefined);
}
async function request(method: string, params?: Record<string, unknown>, notification = false) {
  const headers: Record<string, string> = {"Content-Type":"application/json", "Accept":"application/json", "Authorization":"Bearer " + process.env.AGENT_INFINITE_MCP_TOKEN};
  if (sessionID) headers["Mcp-Session-Id"] = sessionID;
  const body: Record<string, unknown> = {jsonrpc:"2.0", method};
  if (!notification) body.id = ++requestID;
  if (params) body.params = params;
  const response = await fetch(endpoint, {method:"POST", headers, body: JSON.stringify(body)});
  sessionID = response.headers.get("Mcp-Session-Id") || sessionID;
  const payload = response.status === 202 ? {} : await response.json();
  if (!response.ok || payload.error) throw new Error(payload.error?.message || "Agent Infinite MCP request failed");
  return payload;
}
async function callTool(name: string, arguments_: Record<string, unknown>) {
  if (!initialized) {
    await request("initialize", {protocolVersion:"2025-06-18", capabilities:{}, clientInfo:{name:"agent-infinite-pi", version:"0.15.5"}});
    await request("notifications/initialized", undefined, true);
    initialized = true;
  }
  const payload = await request("tools/call", {name, arguments: arguments_});
  return (payload.result?.content || []).map((item: {text?: string}) => item.text || "").join("\n") || JSON.stringify(payload.result);
}
async function externalRequest(serverName: string, method: string, params?: Record<string, unknown>, notification = false) {
  const server = externalServers[serverName] as any;
  if (!server?.url) throw new Error("Only remote MCP servers are supported by the Pi bridge");
  const headers: Record<string,string> = {"Content-Type":"application/json", "Accept":"application/json", ...(server.headers || server.http_headers || {})};
  const known = externalSessions.get(serverName); if (known) headers["Mcp-Session-Id"] = known;
  const body: Record<string,unknown> = {jsonrpc:"2.0", method}; if (!notification) body.id = ++requestID; if (params) body.params = params;
  const response = await fetch(server.url, {method:"POST", headers, body:JSON.stringify(body)});
  const next = response.headers.get("Mcp-Session-Id"); if (next) externalSessions.set(serverName, next);
  const payload: any = response.status === 202 ? {} : await response.json();
  if (!response.ok || payload.error) throw new Error(payload.error?.message || "External MCP request failed");
  return payload.result || {};
}
export default async function agentInfinite(pi: ExtensionAPI) {
  pi.on("session_start", (event) => lifecycle("SessionStart", event));
  pi.on("before_agent_start", async (event) => { await lifecycle("UserPromptSubmit", {prompt: event.prompt}); return {systemPrompt: event.systemPrompt + "\n\n" + contextText}; });
  pi.on("agent_settled", (event) => lifecycle("Stop", event));
  pi.on("session_shutdown", (event) => lifecycle("SessionEnd", event));
  pi.registerTool({name:"agent_infinite_list_connected_agents", label:"Agent Infinite connections", description:"List authorized connected canvas agents.", parameters: Type.Object({}), execute: async () => ({content:[{type:"text", text:await callTool("list_connected_agents", {})}], details:undefined})});
  pi.registerTool({name:"agent_infinite_delegate_task", label:"Delegate via Agent Infinite", description:"Delegate one task to an authorized connected canvas agent.", parameters: Type.Object({target: Type.String(), task: Type.String()}), execute: async (_id, params) => ({content:[{type:"text", text:await callTool("delegate_task", params)}], details:undefined})});
  pi.registerTool({name:"agent_infinite_get_dispatch_result", label:"Read Agent Infinite result", description:"Read a known Agent Infinite dispatch result only for recovery.", parameters: Type.Object({dispatch_id: Type.String(), max_lines: Type.Optional(Type.Number())}), execute: async (_id, params) => ({content:[{type:"text", text:await callTool("get_dispatch_result", params)}], details:undefined})});
  for (const serverName of Object.keys(externalServers)) {
    await externalRequest(serverName, "initialize", {protocolVersion:"2025-06-18", capabilities:{}, clientInfo:{name:"agent-infinite-pi-bridge", version:"0.15.5"}});
    await externalRequest(serverName, "notifications/initialized", undefined, true);
    const listed: any = await externalRequest(serverName, "tools/list", {});
    for (const tool of listed.tools || []) {
      const registeredName = (serverName + "_" + tool.name).replace(/[^a-zA-Z0-9_-]/g, "_");
      pi.registerTool({name:registeredName, label:tool.title || tool.name, description:tool.description || ("Tool from " + serverName), parameters:Type.Unsafe(tool.inputSchema || {type:"object",properties:{}}), execute:async (_id, params) => { const result:any = await externalRequest(serverName, "tools/call", {name:tool.name, arguments:params}); return {content:result.content || [{type:"text",text:JSON.stringify(result)}], details:undefined}; }});
    }
  }
}
`, tomlString(endpoint), tomlString(contextText), string(externalJSON))
}

func sessionInstructions(options LaunchOptions) string {
	label := strings.TrimSpace(options.NodeLabel)
	if label == "" {
		label = options.NodeID
	}
	kind := strings.TrimSpace(options.NodeKind)
	if kind == "" {
		kind = "agent"
	}
	role := strings.TrimSpace(options.NodeRole)
	if role == "" {
		role = "General implementation agent"
	}
	connections := "none"
	if len(options.Connections) > 0 {
		items := make([]string, 0, len(options.Connections))
		for _, connection := range options.Connections {
			items = append(items, fmt.Sprintf("- %s: %q (id %q, role %q, kind %q, provider %q)", connection.Direction, connection.Label, connection.ID, connection.Role, connection.Kind, connection.Provider))
		}
		connections = strings.Join(items, "\n")
	}
	base := fmt.Sprintf(`AGENT INFINITE SESSION CONTRACT
You are the Agent Infinite canvas node %q (id %q), kind %q, in team %q.
Your assigned role is:
%s

Connected canvas topology for this session:
%s

For this Agent Infinite-launched session, this contract controls your identity, assigned role, and canvas delegation behavior if generic provider instructions conflict with it.
The Agent Infinite MCP is the authoritative channel for interacting with connected canvas agents. Its tools are list_connected_agents, delegate_task, and get_dispatch_result (Pi exposes the same tools with an agent_infinite_ prefix). Do not confuse connected canvas agents with provider-native subagents.`, label, options.NodeID, kind, options.TeamID, role, connections)
	if kind == "orchestrator" {
		return base + `

You are the team orchestrator. Before delegating, call list_connected_agents and use only the returned connected targets. Delegate through delegate_task, retain its dispatch id, and wait for the Agent Infinite completion notification. Use get_dispatch_result only to recover a known dispatch. Never create or substitute provider-native subagents for canvas agents unless the user explicitly requests native subagents. If a target is unavailable, report that fact instead of silently replacing it.`
	}
	return base + `

You are a worker agent, not the team orchestrator. Execute tasks delivered in an "Agent Infinite dispatch" message according to the assigned role and return a complete result in the current turn. Do not delegate the task to provider-native subagents unless the user explicitly requests that.`
}

func openCodePlugin() string {
	return `export const AgentInfinitePlugin = async () => ({
  event: async ({ event }) => {
	const mapped = event.type === "session.created" ? "SessionStart" : event.type === "session.idle" ? "Stop" : event.type === "session.error" ? "Error" : "";
    if (!mapped || !process.env.AGENT_INFINITE_HOOK_TOKEN) return;
    await fetch(process.env.AGENT_INFINITE_BACKEND_URL + "/internal/hooks/events", {
      method: "POST", headers: {"Content-Type":"application/json", "X-Agent-Infinite-Hook-Token": process.env.AGENT_INFINITE_HOOK_TOKEN},
      body: JSON.stringify({sessionId: process.env.AGENT_INFINITE_HOOK_SESSION_ID, nodeId: process.env.AGENT_INFINITE_NODE_ID, workspaceId: process.env.AGENT_INFINITE_WORKSPACE_ID, provider:"opencode", event:mapped, raw:event})
    }).catch(() => undefined);
  }
});
`
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
