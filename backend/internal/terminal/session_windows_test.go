package terminal

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/agent"
)

func TestPowerShellConPTYInputResizeAndInterrupt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := startPowerShell(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("startPowerShell() error = %v", err)
	}
	defer session.Close()
	initial, output, detach := session.Attach()
	defer detach()

	if err := session.Resize(100, 28); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	if err := session.Write([]byte("Write-Output \"agent-infinite-hello\"\r")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	readUntil(t, ctx, initial, output, []byte("agent-infinite-hello"))
	readUntil(t, ctx, nil, output, []byte("> "))

	if err := session.Write([]byte("Start-Sleep -Seconds 10\r")); err != nil {
		t.Fatalf("Write(sleep) error = %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := session.Write([]byte{3}); err != nil {
		t.Fatalf("Write(Ctrl+C) error = %v", err)
	}
	readUntil(t, ctx, nil, output, []byte("> "))
	if err := session.Write([]byte("Write-Output \"after-interrupt\"\r")); err != nil {
		t.Fatalf("Write(after interrupt) error = %v", err)
	}
	readUntil(t, ctx, nil, output, []byte("after-interrupt"))
}

func TestTenMockConPTYSessionsRemainResponsive(t *testing.T) {
	t.Setenv("AGENT_INFINITE_TEST_MODE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manager := NewManager(ctx)
	defer manager.CloseAll()
	workDir, runtimeDir := t.TempDir(), t.TempDir()
	for index := range 10 {
		nodeID := fmt.Sprintf("mock-%02d", index)
		spec, err := agent.BuildLaunch(agent.LaunchOptions{Provider: "mock", WorkDir: workDir, RuntimeDir: runtimeDir, NodeID: nodeID, MCPBaseURL: "http://127.0.0.1", MCPToken: "token"})
		if err != nil {
			t.Fatal(err)
		}
		session, err := manager.StartNode(nodeID, spec)
		if err != nil {
			t.Fatalf("StartNode(%s): %v", nodeID, err)
		}
		if err := session.Write([]byte("load-" + nodeID + "\r")); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(manager.Runtimes()); got != 10 {
		t.Fatalf("Runtimes() length = %d, want 10", got)
	}
	for index := range 10 {
		nodeID := fmt.Sprintf("mock-%02d", index)
		session, err := manager.GetByNode(nodeID)
		if err != nil {
			t.Fatal(err)
		}
		for !strings.Contains(session.CleanText(), "MOCK_DONE: load-"+nodeID) {
			select {
			case <-ctx.Done():
				t.Fatalf("%s did not respond; screen = %q", nodeID, session.CleanText())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

func readUntil(t *testing.T, ctx context.Context, initial []byte, output <-chan []byte, needle []byte) {
	t.Helper()
	buffer := append([]byte(nil), initial...)
	for !bytes.Contains(buffer, needle) {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %q; output tail = %q", needle, buffer)
		case chunk, ok := <-output:
			if !ok {
				t.Fatalf("terminal ended while waiting for %q; output tail = %q", needle, buffer)
			}
			buffer = append(buffer, chunk...)
			if len(buffer) > 64*1024 {
				buffer = buffer[len(buffer)-64*1024:]
			}
		}
	}
}
