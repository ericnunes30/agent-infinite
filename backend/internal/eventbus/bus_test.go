package eventbus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEmitAppendsDurableStructuredJournal(t *testing.T) {
	bus := New()
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	if err := bus.SetJournal(path); err != nil {
		t.Fatal(err)
	}
	bus.Emit("dispatch.failed", "dispatch-id", map[string]any{"code": "provider_missing"})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("journal is not JSONL: %v; %q", err, data)
	}
	if event.Type != "dispatch.failed" || event.EntityID != "dispatch-id" {
		t.Fatalf("journal event = %#v", event)
	}
	status, healthError := bus.JournalHealth()
	if status != "healthy" || healthError != "" {
		t.Fatalf("journal health = %q, %q", status, healthError)
	}
}
