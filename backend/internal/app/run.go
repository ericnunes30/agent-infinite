package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/detector"
	"github.com/agent-infinite/agent-infinite/backend/internal/eventbus"
	"github.com/agent-infinite/agent-infinite/backend/internal/hookbridge"
	"github.com/agent-infinite/agent-infinite/backend/internal/mcpserver"
	"github.com/agent-infinite/agent-infinite/backend/internal/orchestration"
	"github.com/agent-infinite/agent-infinite/backend/internal/terminal"
	"github.com/agent-infinite/agent-infinite/backend/internal/transport"
	"github.com/agent-infinite/agent-infinite/backend/internal/workspace"
	"github.com/agent-infinite/agent-infinite/backend/internal/worktree"
)

func Run(parent context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, stdin)
		cancel()
	}()

	token, err := randomToken()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	port, err := listenerPort(listener)
	if err != nil {
		return err
	}

	logger := log.New(stderr, "agent-infinite: ", log.LstdFlags|log.Lmicroseconds)
	terminalManager := terminal.NewManager(ctx)
	events := eventbus.New()
	cacheRoot, cacheErr := os.UserCacheDir()
	if cacheErr != nil {
		cacheRoot = os.TempDir()
	}
	dataRoot := filepath.Join(cacheRoot, "AgentInfinite")
	runtimeRoot := filepath.Join(dataRoot, "runtime")
	// A new backend process cannot own provider sessions from a previous run;
	// removing this directory reconciles temporary MCP/settings residues without
	// touching the repository or global provider configuration.
	_ = os.RemoveAll(runtimeRoot)
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	if err := events.SetJournal(filepath.Join(dataRoot, "activity.jsonl")); err != nil {
		logger.Printf("activity journal unavailable: %v", err)
	}
	terminalManager.SetStatusObserver(func(nodeID string, status detector.Status) {
		events.Emit("agent.status_changed", nodeID, map[string]any{"status": status})
	})
	terminalManager.SetOutputObserver(func(nodeID, preview string, sequence uint64) {
		events.Emit("agent.output_preview", nodeID, map[string]any{"text": preview, "sequence": sequence})
	})
	terminalManager.SetLifecycleObserver(func(eventType, nodeID, sessionID string) {
		events.Emit(eventType, nodeID, map[string]any{"sessionId": sessionID})
	})
	worktreeManager := worktree.NewManager()
	workspaceService := workspace.NewService()
	orchestrationService := orchestration.New(ctx, workspaceService, terminalManager)
	orchestrationService.SetEmitter(events.Emit)
	if err := orchestrationService.SetStorageRoot(filepath.Join(dataRoot, "dispatches")); err != nil {
		logger.Printf("dispatch persistence unavailable: %v", err)
	}
	hookService := hookbridge.New()
	hookService.SetObserver(func(event hookbridge.Event) {
		events.Emit("integration.hook_event", event.Session.NodeID, map[string]any{
			"hookSessionId": event.Session.ID, "provider": event.Session.Provider, "event": event.Name, "mode": event.Session.Mode,
		})
		switch event.Name {
		case "UserPromptSubmit":
			orchestrationService.ConfirmPromptFromLifecycle(event.Session.NodeID, event.Raw)
		case "Stop":
			orchestrationService.CompleteFromLifecycle(event.Session.NodeID, detector.Done)
		case "SubagentStart":
			events.Emit("integration.native_subagent_started", event.Session.NodeID, map[string]any{"provider": event.Session.Provider, "hookSessionId": event.Session.ID, "details": event.Raw})
		case "SubagentStop":
			events.Emit("integration.native_subagent_stopped", event.Session.NodeID, map[string]any{"provider": event.Session.Provider, "hookSessionId": event.Session.ID, "details": event.Raw})
		case "SessionEnd":
			hookService.Close(event.Session.ID)
		}
	})
	mcpHandler := mcpserver.NewHandler(orchestrationService, workspaceService)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	httpTransport := transport.NewHTTP(token, Version, baseURL, runtimeRoot, workspaceService, terminalManager, worktreeManager, mcpHandler, events, hookService, orchestrationService)
	orchestrationService.SetStarter(httpTransport.StartNodeByID)
	orchestrationService.SetRestarter(httpTransport.RestartNodeByID)
	defer terminalManager.CloseAll()
	server := &http.Server{
		Handler:           httpTransport,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		ErrorLog:          logger,
	}

	if err := json.NewEncoder(stdout).Encode(contracts.Ready{Type: "ready", Port: port, Token: token, Version: Version}); err != nil {
		return fmt.Errorf("write readiness: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	}
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate backend token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func listenerPort(listener net.Listener) (int, error) {
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return 0, fmt.Errorf("parse listener address: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return 0, fmt.Errorf("parse listener port: %w", err)
	}
	return port, nil
}
