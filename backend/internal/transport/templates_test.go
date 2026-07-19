package transport

import (
	"path/filepath"
	"testing"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
)

func TestTemplateStoreSerializesAndDeletesTemplates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library", "team-templates.json")
	store := newTemplateStore(path)
	saved, err := store.Save(contracts.TeamTemplate{
		Name:  "Delivery",
		Color: "#b7f34a",
		Nodes: []contracts.Node{{ID: "lead", Kind: "orchestrator", Provider: "codex"}},
		Edges: []contracts.Edge{{ID: "edge", Source: "lead", Target: "worker", Type: "delegates_to"}},
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if saved.ID == "" || saved.CreatedAt.IsZero() {
		t.Fatalf("Save() = %#v, want generated ID and timestamp", saved)
	}
	reloaded := newTemplateStore(path)
	items := reloaded.List()
	if len(items) != 1 || items[0].Name != "Delivery" || len(items[0].Nodes) != 1 {
		t.Fatalf("reloaded templates = %#v", items)
	}
	items[0].Nodes[0].Label = "mutated"
	if reloaded.List()[0].Nodes[0].Label == "mutated" {
		t.Fatal("List() exposed mutable template node data")
	}
	updated, err := reloaded.Update(saved.ID, contracts.TeamTemplate{
		Name: "Delivery v2", Color: "#64d8ff",
		Nodes: []contracts.Node{{ID: "lead", Kind: "orchestrator", Provider: "codex"}},
		Edges: []contracts.Edge{},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ID != saved.ID || updated.CreatedAt != saved.CreatedAt || newTemplateStore(path).List()[0].Name != "Delivery v2" {
		t.Fatalf("Update() = %#v, want identity preserved and content persisted", updated)
	}
	if err := reloaded.Delete(saved.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(newTemplateStore(path).List()) != 0 {
		t.Fatal("deleted template persisted")
	}
}
