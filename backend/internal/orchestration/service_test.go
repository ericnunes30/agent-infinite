package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/terminal"
)

type fakeWorkspace struct{ snapshot contracts.Snapshot }

func (w fakeWorkspace) Snapshot() (contracts.Snapshot, error) { return w.snapshot, nil }

type fakeRuntime struct{}

func (fakeRuntime) GetByNode(string) (*terminal.Session, error) {
	return nil, terminal.ErrSessionNotFound
}

func TestReconcileMarksInterruptedDispatchFailed(t *testing.T) {
	root := t.TempDir()
	created := time.Now().Add(-time.Minute).UTC()
	interrupted := Dispatch{
		ID: "dispatch", WorkspaceID: "workspace", SourceNodeID: "source", TargetNodeID: "target",
		Status: "running", CreatedAt: created, UpdatedAt: created, Result: Result{Status: "running"},
	}
	data, err := json.Marshal([]Dispatch{interrupted})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(context.Background(), fakeWorkspace{}, fakeRuntime{})
	if err := service.SetStorageRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile("workspace"); err != nil {
		t.Fatal(err)
	}
	result, err := service.GetDispatchResult("source", "dispatch", 120)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || !strings.Contains(result.Result.Error, "backend restarted") || result.CompletedAt == nil {
		t.Fatalf("reconciled dispatch = %#v", result)
	}
}

func TestUserPromptHookConfirmsDispatchEnvelope(t *testing.T) {
	service := New(context.Background(), fakeWorkspace{}, fakeRuntime{})
	service.dispatches["dispatch-id"] = &Dispatch{
		ID: "dispatch-id", WorkspaceID: "workspace", TargetNodeID: "target",
		Status: "delivered", Result: Result{Status: "delivered"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if !service.ConfirmPromptFromLifecycle("target", []byte(`{"hook_event_name":"UserPromptSubmit","prompt":"Agent Infinite dispatch dispatch-id. Task: review"}`)) {
		t.Fatal("hook did not confirm the matching dispatch envelope")
	}
	result := service.dispatch("dispatch-id")
	if result.Status != "running" || result.DeliveryConfirmedBy != "hook" {
		t.Fatalf("confirmed dispatch = %#v", result)
	}
	if service.ConfirmPromptFromLifecycle("other", []byte(`{"prompt":"dispatch-id"}`)) {
		t.Fatal("hook from a different node confirmed the dispatch")
	}
}

func TestDispatchValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := contracts.Snapshot{
		Nodes: []contracts.Node{{ID: "source", Kind: "orchestrator"}, {ID: "target", Kind: "agent"}},
		Edges: []contracts.Edge{{ID: "edge", Source: "source", Target: "target", Type: "delegates_to"}},
	}
	tests := []struct {
		name, source, target, task string
		snapshot                   contracts.Snapshot
		want                       error
	}{
		{name: "empty task", source: "source", target: "target", task: "", snapshot: base, want: ErrInvalidTask},
		{name: "invalid source", source: "target", target: "source", task: "work", snapshot: base, want: ErrInvalidSource},
		{name: "missing edge", source: "source", target: "other", task: "work", snapshot: base, want: ErrTargetDenied},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := New(ctx, fakeWorkspace{snapshot: test.snapshot}, fakeRuntime{})
			_, err := service.DispatchTask(test.source, test.target, test.task)
			if !errors.Is(err, test.want) {
				t.Fatalf("DispatchTask() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestOfflineTargetIsQueuedThenFailsExplicitlyWithoutStarter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workspace := fakeWorkspace{snapshot: contracts.Snapshot{
		Nodes: []contracts.Node{{ID: "source", Kind: "orchestrator", Label: "Lead"}, {ID: "target", Kind: "agent", Label: "Reviewer"}},
		Edges: []contracts.Edge{{ID: "edge", Source: "source", Target: "target", Type: "delegates_to"}},
	}}
	service := New(ctx, workspace, fakeRuntime{})
	dispatch, err := service.DelegateTask("source", "Reviewer", "review this")
	if err != nil {
		t.Fatalf("DelegateTask() error = %v", err)
	}
	if dispatch.Status != "queued" {
		t.Fatalf("status = %q, want queued", dispatch.Status)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result, resultErr := service.GetDispatchResult("source", dispatch.ID, 120)
		if resultErr != nil {
			t.Fatal(resultErr)
		}
		if result.Status == "failed" {
			if !strings.Contains(result.Result.Error, "no node starter") {
				t.Fatalf("failure = %q", result.Result.Error)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("dispatch did not fail explicitly")
}

func TestTargetResolutionAcceptsUniqueLabelAndRejectsAmbiguousRole(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workspace := fakeWorkspace{snapshot: contracts.Snapshot{
		Nodes: []contracts.Node{
			{ID: "source", Kind: "orchestrator", Label: "Lead"},
			{ID: "one", Kind: "agent", Label: "Reviewer", Role: "review"},
			{ID: "two", Kind: "agent", Label: "Security", Role: "review"},
		},
		Edges: []contracts.Edge{
			{ID: "one-edge", Source: "source", Target: "one", Type: "delegates_to"},
			{ID: "two-edge", Source: "source", Target: "two", Type: "delegates_to"},
		},
	}}
	service := New(ctx, workspace, fakeRuntime{})
	dispatch, err := service.DelegateTask("source", "reviewer", "check")
	if err != nil || dispatch.TargetNodeID != "one" {
		t.Fatalf("unique label resolution = %#v, %v", dispatch, err)
	}
	_, err = service.DelegateTask("source", "review", "check")
	var ambiguity *AmbiguousTargetError
	if !errors.As(err, &ambiguity) || len(ambiguity.Candidates) != 2 {
		t.Fatalf("ambiguous role error = %#v", err)
	}
}
