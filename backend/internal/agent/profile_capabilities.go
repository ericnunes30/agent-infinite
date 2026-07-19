package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var capabilityNamePattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func claudeMCPSpec(spec map[string]any) map[string]any {
	result := cloneSpec(spec)
	if command, ok := result["command"].([]any); ok && len(command) > 0 {
		result["command"] = fmt.Sprint(command[0])
		result["args"] = command[1:]
	}
	if result["type"] == "remote" {
		result["type"] = "http"
	}
	if environment, ok := result["environment"]; ok {
		result["env"] = environment
		delete(result, "environment")
	}
	return result
}

func openCodeMCPSpec(spec map[string]any) map[string]any {
	result := cloneSpec(spec)
	if command, ok := result["command"].(string); ok {
		values := []any{command}
		if args, ok := result["args"].([]any); ok {
			values = append(values, args...)
		}
		result["command"], result["type"] = values, "local"
		delete(result, "args")
	}
	if result["type"] == "stdio" {
		result["type"] = "local"
	}
	if result["type"] == "http" {
		result["type"] = "remote"
	}
	if env, ok := result["env"]; ok {
		result["environment"] = env
		delete(result, "env")
	}
	result["enabled"] = true
	return result
}

func codexCapabilityArgs(options LaunchOptions) []string {
	args := []string{}
	for _, capability := range options.BlockedMCPs {
		args = append(args, "-c", fmt.Sprintf("mcp_servers.%s.enabled=false", codexMCPKey(capability.Name)))
	}
	for _, capability := range options.MCPs {
		prefix, spec := "mcp_servers."+codexMCPKey(capability.Name)+".", capability.Spec
		if url, ok := spec["url"].(string); ok {
			args = append(args, "-c", prefix+"url="+tomlString(url), "-c", prefix+"enabled=true")
		}
		if command, ok := spec["command"].(string); ok {
			args = append(args, "-c", prefix+"command="+tomlString(command), "-c", prefix+"enabled=true")
			if values := stringSlice(spec["args"]); len(values) > 0 {
				args = append(args, "-c", prefix+"args="+tomlArray(values))
			}
		}
		if headers, ok := spec["headers"].(map[string]any); ok && len(headers) > 0 {
			mapping := map[string]string{}
			for header := range headers {
				mapping[header] = codexHeaderEnv(capability.Name, header)
			}
			args = append(args, "-c", prefix+"env_http_headers="+tomlStringMap(mapping))
		}
	}
	configs := []string{}
	for _, skill := range options.Skills {
		if skill.Path != "" {
			configs = append(configs, fmt.Sprintf("{path=%s,enabled=true}", tomlString(skill.Path)))
		}
	}
	for _, skill := range options.BlockedSkills {
		if skill.Path != "" {
			configs = append(configs, fmt.Sprintf("{path=%s,enabled=false}", tomlString(skill.Path)))
		}
	}
	if len(configs) > 0 {
		args = append(args, "-c", "skills.config=["+strings.Join(configs, ",")+"]")
	}
	return args
}

func materializeCodexProfile(options LaunchOptions) (string, []string, []string, error) {
	config := map[string]any{}
	sourceHome := os.Getenv("CODEX_HOME")
	if sourceHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, nil, err
		}
		sourceHome = filepath.Join(home, ".codex")
	}
	if data, err := os.ReadFile(filepath.Join(sourceHome, "config.toml")); err == nil {
		if err := toml.Unmarshal(data, &config); err != nil {
			return "", nil, nil, fmt.Errorf("parse Codex config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", nil, nil, fmt.Errorf("read Codex config: %w", err)
	}

	servers := map[string]any{
		"agent_infinite": map[string]any{
			"url":                  mcpURL(options.MCPBaseURL, options.NodeID),
			"bearer_token_env_var": "AGENT_INFINITE_MCP_TOKEN",
			"enabled":              true,
		},
	}
	environment := []string{}
	for _, capability := range options.MCPs {
		name := safeCapabilityName(capability.Name)
		if _, duplicate := servers[name]; duplicate {
			return "", nil, nil, fmt.Errorf("Codex MCP names collide after normalization: %q", capability.Name)
		}
		server := map[string]any{"enabled": true}
		if url, ok := capability.Spec["url"].(string); ok {
			server["url"] = url
		}
		if command, ok := capability.Spec["command"].(string); ok {
			server["command"] = command
		}
		if command := stringSlice(capability.Spec["command"]); len(command) > 0 {
			server["command"] = command[0]
			if len(command) > 1 {
				server["args"] = command[1:]
			}
		}
		if args := stringSlice(capability.Spec["args"]); len(args) > 0 {
			server["args"] = args
		}
		for _, key := range []string{"startup_timeout_sec", "tool_timeout_sec", "enabled_tools", "disabled_tools"} {
			if value, ok := capability.Spec[key]; ok {
				server[key] = value
			}
		}
		if env, ok := capability.Spec["env"].(map[string]any); ok {
			for key, value := range env {
				environment = append(environment, key+"="+fmt.Sprint(value))
			}
		}
		headers := map[string]string{}
		for _, key := range []string{"headers", "http_headers"} {
			if values, ok := capability.Spec[key].(map[string]any); ok {
				for header, value := range values {
					envName := codexHeaderEnv(capability.Name, header)
					headers[header] = envName
					environment = append(environment, envName+"="+fmt.Sprint(value))
				}
			}
		}
		if len(headers) > 0 {
			server["env_http_headers"] = headers
		}
		if envName, ok := capability.Spec["bearer_token_env_var"].(string); ok && envName != "" {
			server["bearer_token_env_var"] = envName
		}
		if token, ok := capability.Spec["bearer_token"].(string); ok && token != "" {
			envName := codexHeaderEnv(capability.Name, "authorization")
			server["bearer_token_env_var"] = envName
			environment = append(environment, envName+"="+token)
		}
		servers[name] = server
	}
	for _, capability := range options.BlockedMCPs {
		name := safeCapabilityName(capability.Name)
		if _, exists := servers[name]; !exists {
			if server, ok := codexDisabledServer(capability.Spec); ok {
				servers[name] = server
			}
		}
	}
	config["mcp_servers"] = servers
	contract := sessionInstructions(options)
	if existing, ok := config["developer_instructions"].(string); ok && strings.TrimSpace(existing) != "" {
		config["developer_instructions"] = strings.TrimSpace(existing) + "\n\n" + contract
	} else {
		config["developer_instructions"] = contract
	}

	skills := []map[string]any{}
	for _, skill := range options.Skills {
		if skill.Path != "" {
			skills = append(skills, map[string]any{"path": skill.Path, "enabled": true})
		}
	}
	for _, skill := range options.BlockedSkills {
		if skill.Path != "" {
			skills = append(skills, map[string]any{"path": skill.Path, "enabled": false})
		}
	}
	if len(skills) > 0 {
		config["skills"] = map[string]any{"config": skills}
	}
	if options.Hooks.Enabled {
		features, _ := config["features"].(map[string]any)
		if features == nil {
			features = map[string]any{}
		}
		// This disposable profile contains only the vetted Agent Infinite bridge.
		// Force the feature on here even when the user's external profile disables
		// hooks; the external profile itself remains unchanged.
		features["hooks"] = true
		config["features"] = features
		command := hookForwardCommand()
		hooks := map[string]any{}
		for _, event := range []string{"SessionStart", "UserPromptSubmit", "Stop", "SubagentStart", "SubagentStop"} {
			hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "command_windows": command, "timeout": 5}}}}
		}
		config["hooks"] = hooks
	}
	// The temporary CODEX_HOME deliberately does not copy hooks.json. Remove
	// plugin activation from the isolated session so bypassing trust applies
	// only to the Agent Infinite hook table materialized above.
	delete(config, "plugins")
	delete(config, "plugin")
	// Project hook sources are not part of Agent Infinite's vetted bridge. Keep
	// the selected worktree untrusted only inside this disposable CODEX_HOME so
	// Codex skips project .codex hooks without changing the user's real config.
	projects, _ := config["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	projects[filepath.Clean(options.WorkDir)] = map[string]any{"trust_level": "untrusted"}
	config["projects"] = projects

	configHome := filepath.Join(options.RuntimeDir, "codex-home")
	if err := os.MkdirAll(configHome, 0o700); err != nil {
		return "", nil, nil, err
	}
	data, err := toml.Marshal(config)
	if err != nil {
		return "", nil, nil, fmt.Errorf("encode Codex config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "config.toml"), data, 0o600); err != nil {
		return "", nil, nil, fmt.Errorf("write Codex config: %w", err)
	}
	if auth, err := os.ReadFile(filepath.Join(sourceHome, "auth.json")); err == nil {
		if err := os.WriteFile(filepath.Join(configHome, "auth.json"), auth, 0o600); err != nil {
			return "", nil, nil, fmt.Errorf("copy Codex authentication: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", nil, nil, fmt.Errorf("read Codex authentication: %w", err)
	}

	// Command-line overrides have the highest precedence over project config.
	// Only blocked names are emitted here; full definitions live in config.toml
	// so large inventories cannot exceed Windows' command-line limit.
	args := []string{}
	if options.Hooks.Enabled {
		// The disposable profile contains only the internal non-managed hook;
		// project and plugin hook sources were excluded above.
		args = append(args, "--dangerously-bypass-hook-trust")
	}
	for _, capability := range options.BlockedMCPs {
		if _, ok := codexDisabledServer(capability.Spec); ok {
			args = append(args, "-c", fmt.Sprintf("mcp_servers.%s.enabled=false", codexMCPKey(capability.Name)))
		}
	}
	return configHome, args, environment, nil
}

func codexDisabledServer(spec map[string]any) (map[string]any, bool) {
	server := map[string]any{"enabled": false}
	if url, ok := spec["url"].(string); ok && url != "" {
		server["url"] = url
		return server, true
	}
	if command, ok := spec["command"].(string); ok && command != "" {
		server["command"] = command
		if args := stringSlice(spec["args"]); len(args) > 0 {
			server["args"] = args
		}
		return server, true
	}
	if command := stringSlice(spec["command"]); len(command) > 0 {
		server["command"] = command[0]
		if len(command) > 1 {
			server["args"] = command[1:]
		}
		return server, true
	}
	return nil, false
}

func materializeClaudeSkills(runtimeDir string, skills []Capability) ([]string, error) {
	paths := []string{}
	for _, skill := range skills {
		if skill.Path == "" {
			continue
		}
		root := filepath.Join(runtimeDir, "claude-skills", safeCapabilityName(skill.Name))
		skillDir := filepath.Join(root, "skills", safeCapabilityName(skill.Name))
		if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o700); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(skillDir, 0o700); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(skill.Path)
		if err != nil {
			return nil, err
		}
		manifest, _ := json.Marshal(map[string]any{"name": "agent-infinite-" + safeCapabilityName(skill.Name), "version": "1.0.0"})
		if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), manifest, 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), data, 0o600); err != nil {
			return nil, err
		}
		paths = append(paths, root)
	}
	return paths, nil
}

func materializeOpenCodeSkills(configDir string, skills []Capability) error {
	for _, skill := range skills {
		if skill.Path == "" {
			continue
		}
		data, err := os.ReadFile(skill.Path)
		if err != nil {
			return err
		}
		root := filepath.Join(configDir, "skills", safeCapabilityName(skill.Name))
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "SKILL.md"), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func cloneSpec(spec map[string]any) map[string]any {
	data, _ := json.Marshal(spec)
	result := map[string]any{}
	_ = json.Unmarshal(data, &result)
	return result
}
func safeCapabilityName(value string) string {
	value = strings.Trim(capabilityNamePattern.ReplaceAllString(value, "-"), "-")
	if value == "" {
		return "capability"
	}
	return value
}

// Codex -c dotted paths are not TOML documents: quoting a segment can be
// interpreted as a distinct key by the Windows CLI path parser. The generated
// config uses the same normalized bare key, so the override must target it.
func codexMCPKey(value string) string { return safeCapabilityName(value) }
func tomlArray(values []string) string {
	encoded := make([]string, len(values))
	for i, value := range values {
		encoded[i] = tomlString(value)
	}
	return "[" + strings.Join(encoded, ",") + "]"
}
func tomlStringMap(values map[string]string) string {
	parts := make([]string, 0, len(values))
	for key, value := range values {
		parts = append(parts, tomlString(key)+"="+tomlString(value))
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ",") + "}"
}
func codexHeaderEnv(server, header string) string {
	value := strings.ToUpper(safeCapabilityName(server + "_" + header))
	return "AGENT_INFINITE_MCP_HEADER_" + strings.ReplaceAll(value, "-", "_")
}
func stringSlice(value any) []string {
	result := []string{}
	switch values := value.(type) {
	case []any:
		for _, item := range values {
			result = append(result, fmt.Sprint(item))
		}
	case []string:
		result = append(result, values...)
	}
	return result
}
