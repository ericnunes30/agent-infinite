package detector

import (
	"testing"
	"time"
)

func TestMachineCoversPublicTransitions(t *testing.T) {
	started := time.Unix(100, 0)
	now := started.Add(InitialGrace + time.Millisecond)
	machine := New()
	base := Signals{Alive: true, StartedAt: started, LastOutputAt: now.Add(-time.Second), Screen: ">"}
	if got := machine.Evaluate(started.Add(time.Second), base); got != Starting {
		t.Fatalf("grace status = %s", got)
	}
	if got := machine.Evaluate(now, base); got != Idle {
		t.Fatalf("prompt status = %s", got)
	}
	base.DispatchActive, base.LastOutputAt = true, now
	if got := machine.Evaluate(now, base); got != Working {
		t.Fatalf("dispatch status = %s", got)
	}
	base.LastOutputAt, base.Screen = now.Add(-time.Second), "Do you want to continue? (y/n)"
	if got := machine.Evaluate(now, base); got != Blocked {
		t.Fatalf("blocked status = %s", got)
	}
	base.Screen = "agent is producing output"
	if got := machine.Evaluate(now, base); got != Working {
		t.Fatalf("active output status = %s", got)
	}
	base.Screen = "❯"
	if got := machine.Evaluate(now, base); got != Done {
		t.Fatalf("done status = %s", got)
	}
	if got := machine.Evaluate(now.Add(DoneHold), base); got != Idle {
		t.Fatalf("done hold status = %s", got)
	}
	base.Alive = false
	if got := machine.Evaluate(now, base); got != Dead {
		t.Fatalf("dead status = %s", got)
	}
	if !screenPromptReady("Pi is ready\n>\nmanual mode on - ? for shortcuts") {
		t.Fatal("Pi composer followed by its footer was not recognized")
	}
}

func TestActiveDescendantsKeepWorking(t *testing.T) {
	now := time.Now()
	signals := Signals{Alive: true, ActiveDescendants: true, StartedAt: now.Add(-time.Minute), LastOutputAt: now.Add(-time.Minute), Screen: "child process running"}
	if got := New().Evaluate(now, signals); got != Working {
		t.Fatalf("status = %s", got)
	}
}

func TestLifecycleReadinessHandlesPiComposerWithoutPromptMarker(t *testing.T) {
	now := time.Now()
	signals := Signals{
		Alive: true, ActiveDescendants: true, LifecycleObserved: true, LifecycleReady: true,
		StartedAt: now.Add(-time.Minute), LastOutputAt: now.Add(-time.Second),
		Screen: "Update Available\nPackage Update Available\n----------------\nC:\\workspace (bug-fix)\n0.0%/150k (auto)  minimax-m3:cloud - high",
	}
	if got := New().Evaluate(now, signals); got != Idle {
		t.Fatalf("ready Pi lifecycle status = %s, want Idle", got)
	}
	signals.LifecycleReady = false
	if got := New().Evaluate(now, signals); got != Working {
		t.Fatalf("busy Pi lifecycle status = %s, want Working", got)
	}
	signals.LifecycleReady = true
	signals.Screen = "Allow this operation?"
	if got := New().Evaluate(now, signals); got != Blocked {
		t.Fatalf("confirmation prompt status = %s, want Blocked", got)
	}
}

func TestMachineRecognizesCurrentProviderComposerPrompts(t *testing.T) {
	now := time.Now()
	for _, screen := range []string{
		"› Find and fix a bug in @filename",
		"› Explain this codebase",
		"❯ Try fixing the failing tests",
		">",
	} {
		signals := Signals{
			Alive: true, ActiveDescendants: true, StartedAt: now.Add(-time.Minute),
			LastOutputAt: now.Add(-time.Second), Screen: screen,
		}
		if got := New().Evaluate(now, signals); got != Idle {
			t.Errorf("screen %q status = %s, want Idle", screen, got)
		}
	}
}

func TestMachineDoesNotTreatQuotedOutputAsComposer(t *testing.T) {
	now := time.Now()
	signals := Signals{
		Alive: true, ActiveDescendants: true, StartedAt: now.Add(-time.Minute),
		LastOutputAt: now.Add(-time.Second), Screen: "> ordinary command output",
	}
	if got := New().Evaluate(now, signals); got != Working {
		t.Fatalf("quoted output status = %s, want Working", got)
	}
}

func TestScreenPromptReadyUsesComposerTailInsteadOfPromptHistory(t *testing.T) {
	if screenPromptReady("› submitted task\nagent is still working") {
		t.Fatal("submitted prompt in history was treated as the active composer")
	}
	if !screenPromptReady("agent answer\n› Summarize recent commits\ngpt-5.6-sol high · ~\\project") {
		t.Fatal("Codex composer followed by its footer was not recognized")
	}
}
