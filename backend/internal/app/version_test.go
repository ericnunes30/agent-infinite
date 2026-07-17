package app

import "testing"

func TestVersionMetadata(t *testing.T) {
	if Name == "" || Version == "" {
		t.Fatal("application metadata must not be empty")
	}
}
