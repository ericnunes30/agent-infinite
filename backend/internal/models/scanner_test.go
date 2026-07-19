package models

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstalledProviderModelDiscovery(t *testing.T) {
	if os.Getenv("AGENT_INFINITE_REAL_PROVIDERS") != "1" {
		t.Skip("set AGENT_INFINITE_REAL_PROVIDERS=1 to discover installed provider models without starting a paid session")
	}
	for _, provider := range []string{"claude", "codex", "pi", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			catalog, err := scanProvider(ctx, provider, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if catalog.Provider != provider || catalog.CLIVersion == "" {
				t.Fatalf("incomplete catalog: %#v", catalog)
			}
			t.Logf("discovered %d %s model(s), default %q", len(catalog.Models), provider, catalog.DefaultModel)
		})
	}
}

func TestParsePiModels(t *testing.T) {
	models := parsePiModels("provider model context\nopenai gpt-5.4 128k\nanthropic claude-sonnet-4 200k\n")
	if len(models) != 2 || models[0].ID != "openai/gpt-5.4" || models[1].ID != "anthropic/claude-sonnet-4" {
		t.Fatalf("unexpected Pi models: %#v", models)
	}
}

func TestParseOpenCodeModelsIgnoresNoiseAndDuplicates(t *testing.T) {
	models := parseOpenCodeModels("openai/gpt-5.4\nloading models\nopenai/gpt-5.4\nanthropic/claude-opus-4\n")
	if len(models) != 2 || models[0].ID != "openai/gpt-5.4" || models[1].ID != "anthropic/claude-opus-4" {
		t.Fatalf("unexpected OpenCode models: %#v", models)
	}
}

func TestProviderDefaultsRespectProjectPrecedence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	writeFixture(t, filepath.Join(home, ".claude", "settings.json"), `{"model":"sonnet"}`)
	writeFixture(t, filepath.Join(workspace, ".claude", "settings.local.json"), `{"model":"opus"}`)
	if model, source, err := claudeDefault(home, workspace); err != nil || model != "opus" || source == "" {
		t.Fatalf("Claude default = %q from %q", model, source)
	}

	writeFixture(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"defaultProvider":"openai","defaultModel":"gpt-5.4"}`)
	if model, _, err := piDefault(home, workspace); err != nil || model != "openai/gpt-5.4" {
		t.Fatalf("Pi default = %q", model)
	}

	writeFixture(t, filepath.Join(home, ".codex", "config.toml"), "model = \"gpt-5.3\"\n")
	writeFixture(t, filepath.Join(workspace, ".codex", "config.toml"), "model = \"gpt-5.4\"\n")
	if model, _, err := codexDefault(home, workspace); err != nil || model != "gpt-5.4" {
		t.Fatalf("Codex default = %q", model)
	}
}

func TestOpenCodeDefaultFollowsParentChainAndJSONC(t *testing.T) {
	home := t.TempDir()
	repository := t.TempDir()
	workspace := filepath.Join(repository, "packages", "app")
	if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repository, "opencode.json"), `{"model":"openai/base"}`)
	writeFixture(t, filepath.Join(workspace, "opencode.jsonc"), "{// local\n\"model\":\"openai/project\",\n}")
	model, source, err := openCodeDefault(home, workspace)
	if err != nil || model != "openai/project" || !strings.HasSuffix(source, "opencode.jsonc") {
		t.Fatalf("OpenCode default = %q from %q: %v", model, source, err)
	}
}

func TestOpenCodeDefaultReportsParseErrors(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	writeFixture(t, filepath.Join(workspace, "opencode.json"), `{"model":`)
	if _, _, err := openCodeDefault(home, workspace); err == nil {
		t.Fatal("expected invalid provider configuration to fail discovery")
	}
}

func TestDefaultDiscoveryDoesNotModifyProviderFiles(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	path := filepath.Join(home, ".claude", "settings.json")
	content := []byte(`{"model":"sonnet","apiKey":"must-stay-external"}`)
	writeFixture(t, path, string(content))
	model, source, err := claudeDefault(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(content) {
		t.Fatal("provider settings changed during discovery")
	}
	catalog, err := json.Marshal(ProviderCatalog{Provider: "claude", DefaultModel: model, DefaultSource: source})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(catalog), "must-stay-external") {
		t.Fatal("provider credential leaked into model catalog")
	}
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
