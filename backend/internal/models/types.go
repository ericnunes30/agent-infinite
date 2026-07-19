package models

import (
	"errors"
	"time"
)

const (
	StatusAvailable  = "available"
	StatusMissing    = "missing"
	StatusUnverified = "unverified"

	ScanOK    = "ok"
	ScanStale = "stale"
	ScanError = "scan_error"
)

var ErrUnavailable = errors.New("model is unavailable")

type Model struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Source      string `json:"source"`
	Status      string `json:"status"`
	IsDefault   bool   `json:"isDefault,omitempty"`
}

type ProviderCatalog struct {
	Provider      string    `json:"provider"`
	CLIVersion    string    `json:"cliVersion,omitempty"`
	DefaultModel  string    `json:"defaultModel,omitempty"`
	DefaultSource string    `json:"defaultSource,omitempty"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	ScannedAt     time.Time `json:"scannedAt"`
	Models        []Model   `json:"models"`
}

type Inventory struct {
	Providers []ProviderCatalog `json:"providers"`
	ScannedAt time.Time         `json:"scannedAt"`
}

type catalogFile struct {
	Version   int               `json:"version"`
	Providers []ProviderCatalog `json:"providers"`
}

type Resolution struct {
	Model   string
	Warning string
}
