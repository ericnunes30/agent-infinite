package orchestration

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/agent"
	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/terminal"
)

func TestDispatchWritesTaskToTargetConPTY(t *testing.T) {
	t.Setenv("AGENT_INFINITE_TEST_MODE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime := terminal.NewManager(ctx)
	defer runtime.CloseAll()
	workDir := t.TempDir()
	spec, err := agent.BuildLaunch(agent.LaunchOptions{
		Provider: "mock", WorkDir: workDir, RuntimeDir: t.TempDir(), NodeID: "target",
		MCPBaseURL: "http://127.0.0.1", MCPToken: "token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartNode("target", spec); err != nil {
		t.Fatalf("StartNode(target) error = %v", err)
	}
	workspace := fakeWorkspace{snapshot: contracts.Snapshot{
		Nodes: []contracts.Node{{ID: "source", Kind: "orchestrator"}, {ID: "target", Kind: "agent", Provider: "mock"}},
		Edges: []contracts.Edge{{ID: "edge", Source: "source", Target: "target", Type: "delegates_to"}},
	}}
	service := New(ctx, workspace, runtime)
	dispatch, err := service.DispatchTask("source", "target", "delegated-through-pty")
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	if dispatch.Status != "queued" {
		t.Fatalf("dispatch status = %q", dispatch.Status)
	}
	for {
		output, outputErr := service.GetOutput("target", 120)
		if outputErr != nil {
			t.Fatal(outputErr)
		}
		if strings.Contains(output.Text, "delegated") {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out; output = %q", output.Text)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestOfflineMockTargetAutoStartsAndSerializesDispatchResults(t *testing.T) {
	t.Setenv("AGENT_INFINITE_TEST_MODE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runtime := terminal.NewManager(ctx)
	defer runtime.CloseAll()
	workDir := t.TempDir()
	workspace := fakeWorkspace{snapshot: contracts.Snapshot{
		Nodes: []contracts.Node{
			{ID: "source", Kind: "orchestrator", Label: "Lead"},
			{ID: "target", Kind: "agent", Label: "Reviewer", Role: "review changes", Provider: "mock"},
		},
		Edges: []contracts.Edge{{ID: "edge", Source: "source", Target: "target", Type: "delegates_to"}},
	}}
	service := New(ctx, workspace, runtime)
	var starts atomic.Int32
	service.SetStarter(func(_ context.Context, nodeID string) (*terminal.Session, error) {
		starts.Add(1)
		spec, err := agent.BuildLaunch(agent.LaunchOptions{
			Provider: "mock", WorkDir: workDir, RuntimeDir: t.TempDir(), NodeID: nodeID,
			MCPBaseURL: "http://127.0.0.1", MCPToken: "token",
		})
		if err != nil {
			return nil, err
		}
		return runtime.StartNode(nodeID, spec)
	})
	first, err := service.DelegateTask("source", "Reviewer", "FIRST_UNIQUE_RESULT")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.DelegateTask("source", "review changes", "SECOND_UNIQUE_RESULT")
	if err != nil {
		t.Fatal(err)
	}
	firstResult := waitDispatch(t, ctx, service, first.ID)
	secondResult := waitDispatch(t, ctx, service, second.ID)
	if starts.Load() != 1 {
		t.Fatalf("target starts = %d, want 1", starts.Load())
	}
	if !strings.Contains(firstResult.Result.Output, "FIRST_UNIQUE_RESULT") {
		t.Fatalf("first output = %q", firstResult.Result.Output)
	}
	if !strings.Contains(secondResult.Result.Output, "SECOND_UNIQUE_RESULT") {
		t.Fatalf("second output = %q", secondResult.Result.Output)
	}
	if strings.Contains(secondResult.Result.Output, "FIRST_UNIQUE_RESULT") {
		t.Fatalf("second result contains first dispatch output: %q", secondResult.Result.Output)
	}
}

func TestDispatchWaitsForTargetReadinessBeforeTyping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runtime := terminal.NewManager(ctx)
	defer runtime.CloseAll()
	workDir := t.TempDir()
	script := `[Console]::WriteLine('1. Update now'); [Console]::Write('Press enter to continue'); $line = [Console]::ReadLine(); [Console]::WriteLine('RECEIVED:' + $line); Start-Sleep -Seconds 30`
	session, err := runtime.StartNode("target", agent.LaunchSpec{
		CommandLine: agent.WindowsCommandLine("powershell.exe", []string{"-NoLogo", "-NoProfile", "-Command", script}),
		WorkDir:     workDir, Cleanup: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(session.CleanText(), "Press enter to continue") {
		select {
		case <-ctx.Done():
			t.Fatalf("startup menu did not appear: %q", session.CleanText())
		case <-time.After(20 * time.Millisecond):
		}
	}
	workspace := fakeWorkspace{snapshot: contracts.Snapshot{
		Nodes: []contracts.Node{
			{ID: "source", Kind: "orchestrator", Label: "Lead"},
			{ID: "target", Kind: "agent", Label: "Coder", Provider: "codex"},
		},
		Edges: []contracts.Edge{{ID: "edge", Source: "source", Target: "target", Type: "delegates_to"}},
	}}
	service := New(ctx, workspace, runtime)
	dispatch, err := service.DelegateTask("source", "Coder", "DO_NOT_TYPE_IN_STARTUP_MENU")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if text := session.CleanText(); strings.Contains(text, "RECEIVED:") {
		t.Fatalf("dispatch was typed into a startup menu: %q", text)
	}
	if result := service.dispatch(dispatch.ID); result.Status != "queued" {
		t.Fatalf("dispatch status = %q, want queued while target is blocked", result.Status)
	}
}

func TestDispatchDeliversToCurrentCodexComposerPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime := terminal.NewManager(ctx)
	defer runtime.CloseAll()
	workDir := t.TempDir()
	script := `$OutputEncoding = [Console]::OutputEncoding = [Text.UTF8Encoding]::new(); [Console]::Write('› Find and fix a bug in @filename'); $line = [Console]::ReadLine(); [Console]::WriteLine(); [Console]::WriteLine('RECEIVED:' + $line); Start-Sleep -Milliseconds 1200; [Console]::WriteLine('AGENT_RESPONSE:210'); [Console]::Write('› Explain this codebase'); [Console]::ReadLine() | Out-Null`
	session, err := runtime.StartNode("target", agent.LaunchSpec{
		CommandLine: agent.WindowsCommandLine("powershell.exe", []string{"-NoLogo", "-NoProfile", "-Command", script}),
		WorkDir:     workDir, Cleanup: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(session.CleanText(), "Find and fix a bug") {
		select {
		case <-ctx.Done():
			t.Fatalf("Codex composer did not appear: %q", session.CleanText())
		case <-time.After(20 * time.Millisecond):
		}
	}
	workspace := fakeWorkspace{snapshot: contracts.Snapshot{
		Nodes: []contracts.Node{
			{ID: "source", Kind: "orchestrator", Label: "Lead"},
			{ID: "target", Kind: "agent", Label: "Coder", Provider: "codex"},
		},
		Edges: []contracts.Edge{{ID: "edge", Source: "source", Target: "target", Type: "delegates_to"}},
	}}
	service := New(ctx, workspace, runtime)
	dispatch, err := service.DelegateTask("source", "Coder", "CURRENT_CODEX_PROMPT_TASK")
	if err != nil {
		t.Fatal(err)
	}
	result := waitDispatch(t, ctx, service, dispatch.ID)
	if !strings.Contains(result.Result.Output, "AGENT_RESPONSE:210") {
		t.Fatalf("dispatch output = %q", result.Result.Output)
	}
}

func TestCodexUpdateCompletionRestartsThenDelivers(t *testing.T) {
	t.Setenv("AGENT_INFINITE_TEST_MODE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime := terminal.NewManager(ctx)
	defer runtime.CloseAll()
	workDir := t.TempDir()
	updateScript := `[Console]::Write('Update ran successfully! Please restart Codex.'); [Console]::ReadLine() | Out-Null`
	firstSession, err := runtime.StartNode("target", agent.LaunchSpec{
		CommandLine: agent.WindowsCommandLine("powershell.exe", []string{"-NoLogo", "-NoProfile", "-Command", updateScript}),
		WorkDir:     workDir, Cleanup: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(firstSession.CleanText(), "Please restart Codex") {
		select {
		case <-ctx.Done():
			t.Fatalf("restart marker did not appear: %q", firstSession.CleanText())
		case <-time.After(20 * time.Millisecond):
		}
	}
	workspace := fakeWorkspace{snapshot: contracts.Snapshot{
		Nodes: []contracts.Node{
			{ID: "source", Kind: "orchestrator", Label: "Lead"},
			{ID: "target", Kind: "agent", Label: "Coder", Provider: "codex"},
		},
		Edges: []contracts.Edge{{ID: "edge", Source: "source", Target: "target", Type: "delegates_to"}},
	}}
	service := New(ctx, workspace, runtime)
	var restarts atomic.Int32
	service.SetRestarter(func(_ context.Context, nodeID string) (*terminal.Session, error) {
		restarts.Add(1)
		if err := runtime.StopNode(nodeID); err != nil {
			return nil, err
		}
		spec, err := agent.BuildLaunch(agent.LaunchOptions{
			Provider: "mock", WorkDir: workDir, RuntimeDir: t.TempDir(), NodeID: nodeID,
			MCPBaseURL: "http://127.0.0.1", MCPToken: "token",
		})
		if err != nil {
			return nil, err
		}
		return runtime.StartNode(nodeID, spec)
	})
	dispatch, err := service.DelegateTask("source", "Coder", "AFTER_CODEX_RESTART")
	if err != nil {
		t.Fatal(err)
	}
	result := waitDispatch(t, ctx, service, dispatch.ID)
	if restarts.Load() != 1 {
		t.Fatalf("restarts = %d, want 1", restarts.Load())
	}
	if !strings.Contains(result.Result.Output, "AFTER_CODEX_RESTART") {
		t.Fatalf("result after restart = %q", result.Result.Output)
	}
}

func TestCompletionWakesIdleOrchestratorWithResult(t *testing.T) {
	t.Setenv("AGENT_INFINITE_TEST_MODE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime := terminal.NewManager(ctx)
	defer runtime.CloseAll()
	workDir := t.TempDir()
	for _, nodeID := range []string{"source", "target"} {
		spec, err := agent.BuildLaunch(agent.LaunchOptions{
			Provider: "mock", WorkDir: workDir, RuntimeDir: t.TempDir(), NodeID: nodeID,
			MCPBaseURL: "http://127.0.0.1", MCPToken: "token",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.StartNode(nodeID, spec); err != nil {
			t.Fatal(err)
		}
	}
	workspace := fakeWorkspace{snapshot: contracts.Snapshot{
		Nodes: []contracts.Node{
			{ID: "source", Kind: "orchestrator", Label: "Lead", Provider: "mock"},
			{ID: "target", Kind: "agent", Label: "Worker", Provider: "mock"},
		},
		Edges: []contracts.Edge{{ID: "edge", Source: "source", Target: "target", Type: "delegates_to"}},
	}}
	service := New(ctx, workspace, runtime)
	dispatch, err := service.DelegateTask("source", "Worker", "EVENT_DRIVEN_RESULT")
	if err != nil {
		t.Fatal(err)
	}
	source, err := runtime.GetByNode("source")
	if err != nil {
		t.Fatal(err)
	}
	for {
		text := source.CleanText()
		current := service.dispatch(dispatch.ID)
		if strings.Contains(text, "[Agent Infinite completion]") && strings.Contains(text, "EVENT_DRIVEN_RESULT") && current.Notified {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("source was not woken with result: %q", text)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if result := service.dispatch(dispatch.ID); result.Status != "done" {
		t.Fatalf("notified dispatch = %#v", result)
	}
}

func waitDispatch(t *testing.T, ctx context.Context, service *Service, dispatchID string) Dispatch {
	t.Helper()
	for {
		result, err := service.GetDispatchResult("source", dispatchID, 500)
		if err != nil {
			t.Fatal(err)
		}
		if terminalDispatchState(result.Status) {
			return result
		}
		select {
		case <-ctx.Done():
			t.Fatalf("dispatch %s timed out in %s", dispatchID, result.Status)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
