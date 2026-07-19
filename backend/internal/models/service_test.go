package models

import (
	"errors"
	"testing"
	"time"
)

func TestResolveUsesDefaultAndBlocksKnownMissingModel(t *testing.T) {
	service := New(t.TempDir())
	service.data.Providers = []ProviderCatalog{{
		Provider: "codex", DefaultModel: "gpt-5.4", Status: ScanOK, ScannedAt: time.Now(),
		Models: []Model{{ID: "gpt-5.3", Status: StatusMissing}},
	}}
	resolution, err := service.Resolve("codex", "")
	if err != nil || resolution.Model != "gpt-5.4" {
		t.Fatalf("default resolution = %#v, %v", resolution, err)
	}
	if _, err := service.Resolve("codex", "gpt-5.3"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	custom, err := service.Resolve("codex", "vendor/custom-model")
	if err != nil || custom.Model != "vendor/custom-model" {
		t.Fatalf("custom resolution = %#v, %v", custom, err)
	}
}

func TestValidateRejectsWhitespaceButAllowsBlankDefault(t *testing.T) {
	service := New(t.TempDir())
	if err := service.Validate("claude", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Validate("claude", "bad model"); err == nil {
		t.Fatal("expected whitespace to be rejected")
	}
	if err := service.Validate("pi", "gpt-5.4"); err == nil {
		t.Fatal("expected Pi provider/model format to be required")
	}
}

func TestCatalogPersistsAndCanBeReplaced(t *testing.T) {
	root := t.TempDir()
	service := New(root)
	service.data.Providers = []ProviderCatalog{{Provider: "claude", Status: ScanOK, ScannedAt: time.Now(), Models: []Model{}}}
	if err := service.persistLocked(); err != nil {
		t.Fatal(err)
	}
	service.data.Providers[0].DefaultModel = "sonnet"
	if err := service.persistLocked(); err != nil {
		t.Fatal(err)
	}
	reloaded := New(root).Inventory()
	if len(reloaded.Providers) != 1 || reloaded.Providers[0].DefaultModel != "sonnet" {
		t.Fatalf("catalog was not replaced: %#v", reloaded)
	}
}
