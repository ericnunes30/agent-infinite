package models

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func scanProvider(ctx context.Context, provider, workspacePath string) (ProviderCatalog, error) {
	catalog := ProviderCatalog{Provider: provider, Status: ScanOK, ScannedAt: time.Now().UTC(), Models: []Model{}}
	executable, err := exec.LookPath(provider)
	if err != nil {
		return catalog, os.ErrNotExist
	}
	catalog.CLIVersion = commandVersion(ctx, executable)
	home, _ := os.UserHomeDir()
	if override := strings.TrimSpace(os.Getenv("AGENT_INFINITE_PROVIDER_HOME")); override != "" {
		home = override
	}
	switch provider {
	case "claude":
		catalog.DefaultModel, catalog.DefaultSource, err = claudeDefault(home, workspacePath)
		if err != nil {
			return catalog, err
		}
		catalog.Models, err = claudeModels(ctx, executable)
	case "codex":
		catalog.DefaultModel, catalog.DefaultSource, err = codexDefault(home, workspacePath)
		if err != nil {
			return catalog, err
		}
		catalog.Models, err = codexModels(ctx, executable)
	case "pi":
		catalog.DefaultModel, catalog.DefaultSource, err = piDefault(home, workspacePath)
		if err != nil {
			return catalog, err
		}
		catalog.Models, err = lineModels(ctx, executable, []string{"--list-models"}, parsePiModels)
	case "opencode":
		catalog.DefaultModel, catalog.DefaultSource, err = openCodeDefault(home, workspacePath)
		if err != nil {
			return catalog, err
		}
		catalog.Models, err = lineModels(ctx, executable, []string{"models"}, parseOpenCodeModels)
	default:
		err = errors.New("unsupported provider")
	}
	if err != nil {
		return catalog, err
	}
	if catalog.DefaultModel == "" {
		for _, model := range catalog.Models {
			if model.IsDefault {
				catalog.DefaultModel = model.ID
				catalog.DefaultSource = "provider catalog"
				break
			}
		}
	}
	for index := range catalog.Models {
		if catalog.Models[index].ID == catalog.DefaultModel {
			catalog.Models[index].IsDefault = true
		}
	}
	sort.Slice(catalog.Models, func(i, j int) bool { return catalog.Models[i].ID < catalog.Models[j].ID })
	return catalog, nil
}

func commandVersion(ctx context.Context, executable string) string {
	output, err := exec.CommandContext(ctx, executable, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	lines := strings.Fields(strings.TrimSpace(ansiPattern.ReplaceAllString(string(output), "")))
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, " ")
}

func lineModels(ctx context.Context, executable string, args []string, parser func(string) []Model) ([]Model, error) {
	output, err := exec.CommandContext(ctx, executable, args...).CombinedOutput()
	if err != nil {
		return nil, err
	}
	return parser(ansiPattern.ReplaceAllString(string(output), "")), nil
}

func parsePiModels(output string) []Model {
	models := []Model{}
	seen := map[string]bool{}
	for index, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if index == 0 || len(fields) < 2 || fields[0] == "provider" {
			continue
		}
		id := fields[0] + "/" + fields[1]
		if !seen[id] {
			seen[id] = true
			models = append(models, Model{ID: id, DisplayName: fields[1], Source: "cli", Status: StatusAvailable})
		}
	}
	return models
}

func parseOpenCodeModels(output string) []Model {
	models := []Model{}
	seen := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		id := strings.TrimSpace(line)
		if id == "" || strings.ContainsAny(id, " \t") || !strings.Contains(id, "/") || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, Model{ID: id, DisplayName: id, Source: "cli", Status: StatusAvailable})
	}
	return models
}

func claudeModels(ctx context.Context, executable string) ([]Model, error) {
	output, err := exec.CommandContext(ctx, executable, "--help").CombinedOutput()
	if err != nil {
		return nil, err
	}
	text := ansiPattern.ReplaceAllString(string(output), "")
	start := strings.Index(text, "--model <model>")
	if start < 0 {
		return []Model{}, nil
	}
	section := text[start:]
	if next := strings.Index(section[1:], "\n  --"); next >= 0 {
		section = section[:next+1]
	}
	matches := regexp.MustCompile(`'([A-Za-z0-9._-]+)'`).FindAllStringSubmatch(section, -1)
	models := []Model{}
	seen := map[string]bool{}
	for _, match := range matches {
		id := match[1]
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, Model{ID: id, DisplayName: id, Source: "cli_alias", Status: StatusUnverified})
	}
	return models, nil
}

func codexModels(ctx context.Context, executable string) ([]Model, error) {
	command := exec.CommandContext(ctx, executable, "app-server")
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, err
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	encoder := json.NewEncoder(stdin)
	if err := encoder.Encode(map[string]any{"method": "initialize", "id": 1, "params": map[string]any{"clientInfo": map[string]string{"name": "agent_infinite", "title": "Agent Infinite", "version": "0.15.5"}}}); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(stdout)
	if _, err := waitCodexResponse(reader, 1); err != nil {
		return nil, err
	}
	if err := encoder.Encode(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return nil, err
	}
	models := []Model{}
	cursor := ""
	requestID := 2
	for {
		params := map[string]any{"limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := encoder.Encode(map[string]any{"method": "model/list", "id": requestID, "params": params}); err != nil {
			return nil, err
		}
		response, err := waitCodexResponse(reader, requestID)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Data []struct {
				ID, Model, DisplayName string
				Hidden, IsDefault      bool
			} `json:"data"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(response, &payload); err != nil {
			return nil, err
		}
		for _, item := range payload.Data {
			if item.Hidden {
				continue
			}
			id := item.Model
			if id == "" {
				id = item.ID
			}
			models = append(models, Model{ID: id, DisplayName: item.DisplayName, Source: "app_server", Status: StatusAvailable, IsDefault: item.IsDefault})
		}
		cursor = payload.NextCursor
		if cursor == "" {
			break
		}
		requestID++
	}
	_ = stdin.Close()
	return models, nil
}

func waitCodexResponse(reader *bufio.Reader, id int) (json.RawMessage, error) {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		var message struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(line, &message) != nil || message.ID == nil || *message.ID != id {
			continue
		}
		if len(message.Error) > 0 && string(message.Error) != "null" {
			return nil, fmt.Errorf("codex app-server request failed")
		}
		return message.Result, nil
	}
}

func claudeDefault(home, workspace string) (string, string, error) {
	return layeredJSONModel([]string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(workspace, ".claude", "settings.json"),
		filepath.Join(workspace, ".claude", "settings.local.json"),
	}, "model")
}

func codexDefault(home, workspace string) (string, string, error) {
	model, source := "", ""
	paths := []string{filepath.Join(home, ".codex", "config.toml")}
	if workspace != "" {
		paths = append(paths, filepath.Join(workspace, ".codex", "config.toml"))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", err
		}
		root := map[string]any{}
		if err := toml.Unmarshal(data, &root); err != nil {
			return "", "", err
		}
		if value, ok := root["model"].(string); ok && strings.TrimSpace(value) != "" {
			model, source = strings.TrimSpace(value), path
		}
	}
	return model, source, nil
}

func piDefault(home, workspace string) (string, string, error) {
	model, source, provider := "", "", ""
	paths := []string{filepath.Join(home, ".pi", "agent", "settings.json")}
	if workspace != "" {
		paths = append(paths, filepath.Join(workspace, ".pi", "settings.json"))
	}
	for _, path := range paths {
		root, err := readJSON(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", err
		}
		if value, ok := root["defaultProvider"].(string); ok && strings.TrimSpace(value) != "" {
			provider = strings.TrimSpace(value)
		}
		if value, ok := root["defaultModel"].(string); ok && strings.TrimSpace(value) != "" {
			model, source = strings.TrimSpace(value), path
		}
	}
	if model != "" && provider != "" && !strings.Contains(model, "/") {
		model = provider + "/" + model
	}
	return model, source, nil
}

func openCodeDefault(home, workspace string) (string, string, error) {
	paths := []string{
		filepath.Join(home, ".config", "opencode", "opencode.json"),
		filepath.Join(home, ".config", "opencode", "opencode.jsonc"),
	}
	if customDir := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); customDir != "" {
		paths = append(paths, filepath.Join(customDir, "opencode.json"), filepath.Join(customDir, "opencode.jsonc"))
	}
	if custom := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); custom != "" {
		paths = append(paths, custom)
	}
	if workspace != "" {
		paths = append(paths, openCodeProjectPaths(workspace)...)
	}
	model, source, err := layeredJSONModel(paths, "model")
	if err != nil {
		return "", "", err
	}
	if inline := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_CONTENT")); inline != "" {
		root := map[string]any{}
		if err := json.Unmarshal([]byte(inline), &root); err != nil {
			return "", "", err
		}
		if value, ok := root["model"].(string); ok && strings.TrimSpace(value) != "" {
			model, source = strings.TrimSpace(value), "env:OPENCODE_CONFIG_CONTENT"
		}
	}
	return model, source, nil
}

func openCodeProjectPaths(workspace string) []string {
	directories := []string{}
	current, err := filepath.Abs(workspace)
	if err != nil {
		current = workspace
	}
	for current != "" {
		directories = append(directories, current)
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	paths := []string{}
	for index := len(directories) - 1; index >= 0; index-- {
		directory := directories[index]
		paths = append(paths,
			filepath.Join(directory, "opencode.json"),
			filepath.Join(directory, "opencode.jsonc"),
			filepath.Join(directory, ".opencode", "opencode.json"),
			filepath.Join(directory, ".opencode", "opencode.jsonc"),
		)
	}
	return paths
}

func layeredJSONModel(paths []string, key string) (string, string, error) {
	model, source := "", ""
	for _, path := range paths {
		if path == "" {
			continue
		}
		root, err := readJSON(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", err
		}
		if value, ok := root[key].(string); ok && strings.TrimSpace(value) != "" {
			model, source = strings.TrimSpace(value), path
		}
	}
	return model, source, nil
}

func readJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if err := json.Unmarshal(data, &root); err != nil {
		cleaned := stripJSONComments(data)
		if retryErr := json.Unmarshal(cleaned, &root); retryErr != nil {
			return nil, err
		}
	}
	return root, nil
}

func stripJSONComments(input []byte) []byte {
	output := make([]byte, 0, len(input))
	inString, escaped := false, false
	for index := 0; index < len(input); index++ {
		character := input[index]
		if inString {
			output = append(output, character)
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			continue
		}
		if character == '"' {
			inString = true
			output = append(output, character)
			continue
		}
		if character == '/' && index+1 < len(input) && input[index+1] == '/' {
			for index < len(input) && input[index] != '\n' {
				index++
			}
			output = append(output, '\n')
			continue
		}
		if character == '/' && index+1 < len(input) && input[index+1] == '*' {
			index += 2
			for index+1 < len(input) && !(input[index] == '*' && input[index+1] == '/') {
				index++
			}
			index++
			continue
		}
		if character == ',' {
			next := index + 1
			for next < len(input) && (input[next] == ' ' || input[next] == '\t' || input[next] == '\r' || input[next] == '\n') {
				next++
			}
			if next < len(input) && (input[next] == '}' || input[next] == ']') {
				continue
			}
		}
		output = append(output, character)
	}
	return output
}
