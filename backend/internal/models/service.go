package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const staleAfter = 10 * time.Minute

type Service struct {
	path string
	mu   sync.RWMutex
	data catalogFile
}

func New(root string) *Service {
	service := &Service{path: filepath.Join(root, "models.json"), data: catalogFile{Version: 1, Providers: []ProviderCatalog{}}}
	if data, err := os.ReadFile(service.path); err == nil {
		var stored catalogFile
		if json.Unmarshal(data, &stored) == nil && stored.Version == 1 {
			service.data = stored
		}
	}
	return service
}

func (s *Service) Inventory() Inventory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	providers := cloneCatalogs(s.data.Providers)
	sort.Slice(providers, func(i, j int) bool { return providers[i].Provider < providers[j].Provider })
	var scannedAt time.Time
	for _, provider := range providers {
		if provider.ScannedAt.After(scannedAt) {
			scannedAt = provider.ScannedAt
		}
	}
	return Inventory{Providers: providers, ScannedAt: scannedAt}
}

func (s *Service) Scan(ctx context.Context, workspacePath, onlyProvider string) Inventory {
	providers := []string{"claude", "codex", "pi", "opencode"}
	if onlyProvider != "" {
		providers = []string{onlyProvider}
	}
	type result struct {
		catalog ProviderCatalog
		err     error
	}
	results := make(chan result, len(providers))
	for _, provider := range providers {
		provider := provider
		go func() {
			scanCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			catalog, err := scanProvider(scanCtx, provider, workspacePath)
			results <- result{catalog: catalog, err: err}
		}()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for range providers {
		result := <-results
		provider := result.catalog.Provider
		previous, found := findCatalog(s.data.Providers, provider)
		if result.err != nil {
			if found {
				previous.Status = ScanStale
				previous.Error = safeError(result.err)
				setCatalog(&s.data.Providers, previous)
			} else {
				result.catalog.Status = ScanError
				result.catalog.Error = safeError(result.err)
				result.catalog.Models = []Model{}
				setCatalog(&s.data.Providers, result.catalog)
			}
			continue
		}
		if found {
			seen := map[string]bool{}
			for _, model := range result.catalog.Models {
				seen[model.ID] = true
			}
			for _, model := range previous.Models {
				if !seen[model.ID] && model.Status == StatusAvailable {
					model.Status = StatusMissing
					result.catalog.Models = append(result.catalog.Models, model)
				}
			}
		}
		setCatalog(&s.data.Providers, result.catalog)
	}
	_ = s.persistLocked()
	return inventoryLocked(s.data.Providers)
}

func (s *Service) RefreshIfStale(ctx context.Context, workspacePath, provider string) Inventory {
	s.mu.RLock()
	catalog, found := findCatalog(s.data.Providers, provider)
	s.mu.RUnlock()
	if !found || time.Since(catalog.ScannedAt) > staleAfter {
		return s.Scan(ctx, workspacePath, provider)
	}
	return s.Inventory()
}

func (s *Service) Resolve(provider, requested string) (Resolution, error) {
	requested = strings.TrimSpace(requested)
	s.mu.RLock()
	catalog, found := findCatalog(s.data.Providers, provider)
	s.mu.RUnlock()
	if requested == "" {
		if found {
			return Resolution{Model: catalog.DefaultModel}, nil
		}
		return Resolution{}, nil
	}
	if found && catalog.Status == ScanOK {
		for _, model := range catalog.Models {
			if model.ID == requested && model.Status == StatusMissing {
				return Resolution{}, fmt.Errorf("%w: %s", ErrUnavailable, requested)
			}
		}
	}
	resolution := Resolution{Model: requested}
	if !found || catalog.Status == ScanError || catalog.Status == ScanStale {
		resolution.Warning = "Model catalog is stale; the provider will validate the saved model."
	}
	return resolution, nil
}

func (s *Service) Validate(provider, requested string) error {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return nil
	}
	if len(requested) > 240 || strings.IndexFunc(requested, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0 {
		return errors.New("model id is invalid")
	}
	if (provider == "pi" || provider == "opencode") && !strings.Contains(requested, "/") {
		return errors.New("model id must use provider/model format")
	}
	if provider != "claude" && provider != "codex" && provider != "pi" && provider != "opencode" && provider != "mock" {
		return errors.New("provider is invalid")
	}
	return nil
}

func (s *Service) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}

func safeError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "model discovery timed out"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "provider CLI is not installed"
	}
	return "model discovery failed"
}

func findCatalog(catalogs []ProviderCatalog, provider string) (ProviderCatalog, bool) {
	for _, catalog := range catalogs {
		if catalog.Provider == provider {
			return catalog, true
		}
	}
	return ProviderCatalog{}, false
}

func setCatalog(catalogs *[]ProviderCatalog, catalog ProviderCatalog) {
	for index := range *catalogs {
		if (*catalogs)[index].Provider == catalog.Provider {
			(*catalogs)[index] = catalog
			return
		}
	}
	*catalogs = append(*catalogs, catalog)
}

func inventoryLocked(catalogs []ProviderCatalog) Inventory {
	providers := cloneCatalogs(catalogs)
	sort.Slice(providers, func(i, j int) bool { return providers[i].Provider < providers[j].Provider })
	var scannedAt time.Time
	for _, provider := range providers {
		if provider.ScannedAt.After(scannedAt) {
			scannedAt = provider.ScannedAt
		}
	}
	return Inventory{Providers: providers, ScannedAt: scannedAt}
}

func cloneCatalogs(input []ProviderCatalog) []ProviderCatalog {
	result := make([]ProviderCatalog, len(input))
	for index, catalog := range input {
		result[index] = catalog
		result[index].Models = append([]Model(nil), catalog.Models...)
	}
	return result
}
