//go:build windows

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/coder/websocket"
)

// TestRealBidirectionalProviderFlow is intentionally opt-in: it consumes real provider
// credentials and proves the complete Claude Code <-> Codex MCP path on Windows.
func TestRealBidirectionalProviderFlow(t *testing.T) {
	if os.Getenv("AGENT_INFINITE_REAL_PROVIDERS") != "1" {
		t.Skip("set AGENT_INFINITE_REAL_PROVIDERS=1 to exercise installed Claude Code and Codex")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("Claude Code is not installed")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("Codex is not installed")
	}

	repository := initAcceptanceRepository(t)
	baseURL, token, shutdown := startAcceptanceBackend(t)
	defer shutdown()
	requestJSON[contracts.Snapshot](t, http.MethodPost, baseURL+"/api/workspaces/open", token, map[string]string{"path": repository})

	claudeTeam := createAcceptanceTeam(t, baseURL, token, "Claude Acceptance", "claude")
	codexTeam := createAcceptanceTeam(t, baseURL, token, "Codex Acceptance", "codex")
	claude, codex := claudeTeam.Orchestrator, codexTeam.Orchestrator
	defer deleteAcceptanceTeam(baseURL, token, claudeTeam.Team.ID)
	defer deleteAcceptanceTeam(baseURL, token, codexTeam.Team.ID)
	snapshot := requestJSON[contracts.Snapshot](t, http.MethodGet, baseURL+"/api/snapshot", token, nil)

	claudeTerminal := startAcceptanceNode(t, baseURL, token, claude.ID)
	defer stopAcceptanceNode(baseURL, token, claude.ID)
	codexTerminal := startAcceptanceNode(t, baseURL, token, codex.ID)
	defer stopAcceptanceNode(baseURL, token, codex.ID)

	claudeScreen := connectAcceptanceTerminal(t, baseURL, token, claudeTerminal)
	defer claudeScreen.close()
	codexScreen := connectAcceptanceTerminal(t, baseURL, token, codexTerminal)
	defer codexScreen.close()
	prepareProvider(t, claudeScreen, "claude")
	prepareProvider(t, codexScreen, "codex")

	setAcceptanceEdge(t, baseURL, token, snapshot, claude.ID, codex.ID)
	assertProviderFlow(t, "claude-to-codex", claudeScreen, codexScreen, codex.Label)

	setAcceptanceEdge(t, baseURL, token, snapshot, codex.ID, claude.ID)
	assertProviderFlow(t, "codex-to-claude", codexScreen, claudeScreen, claude.Label)
}

type acceptanceTeamResponse struct {
	Team         contracts.Team `json:"team"`
	Orchestrator contracts.Node `json:"orchestrator"`
}

func createAcceptanceTeam(t *testing.T, baseURL, token, name, provider string) acceptanceTeamResponse {
	t.Helper()
	response := requestJSON[acceptanceTeamResponse](t, http.MethodPost, baseURL+"/api/teams", token, map[string]string{
		"name": name, "color": "#d4ff63", "orchestratorProvider": provider,
	})
	return response
}

func initAcceptanceRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "acceptance@agent-infinite.local"},
		{"config", "user.name", "Agent Infinite Acceptance"},
	}
	for _, args := range commands {
		command := exec.Command("git", append([]string{"-C", repository}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("# acceptance\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "initial"}} {
		command := exec.Command("git", append([]string{"-C", repository}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
	return repository
}

func startAcceptanceBackend(t *testing.T) (string, string, func()) {
	t.Helper()
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, stdinReader, stdoutWriter, &stderr)
	}()
	var ready contracts.Ready
	if err := json.NewDecoder(stdoutReader).Decode(&ready); err != nil {
		cancel()
		t.Fatalf("read backend handshake: %v; stderr: %s", err, stderr.String())
	}
	shutdown := func() {
		_ = stdinWriter.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("backend shutdown: %v; stderr: %s", err, stderr.String())
			}
		case <-time.After(8 * time.Second):
			cancel()
			t.Errorf("backend did not shut down; stderr: %s", stderr.String())
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", ready.Port), ready.Token, shutdown
}

func requestJSON[T any](t *testing.T, method, url, token string, body any) T {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("%s %s: status %d: %s", method, url, response.StatusCode, data)
	}
	var value T
	if len(data) > 0 {
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatalf("decode %s: %v: %s", url, err, data)
		}
	}
	return value
}

func startAcceptanceNode(t *testing.T, baseURL, token, nodeID string) string {
	t.Helper()
	response := requestJSON[map[string]any](t, http.MethodPost, baseURL+"/api/nodes/"+nodeID+"/start", token, map[string]any{})
	sessionID, ok := response["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("node %s returned no terminal session: %#v", nodeID, response)
	}
	return sessionID
}

func stopAcceptanceNode(baseURL, token, nodeID string) {
	request, _ := http.NewRequest(http.MethodPost, baseURL+"/api/nodes/"+nodeID+"/stop", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err == nil {
		_ = response.Body.Close()
	}
}

func deleteAcceptanceTeam(baseURL, token, teamID string) {
	request, _ := http.NewRequest(http.MethodDelete, baseURL+"/api/teams/"+teamID, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err == nil {
		_ = response.Body.Close()
	}
}

type acceptanceScreen struct {
	connection *websocket.Conn
	mu         sync.Mutex
	output     bytes.Buffer
}

func connectAcceptanceTerminal(t *testing.T, baseURL, token, sessionID string) *acceptanceScreen {
	t.Helper()
	url := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws/terminals/" + sessionID + "?token=" + token
	connection, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("connect terminal %s: %v", sessionID, err)
	}
	screen := &acceptanceScreen{connection: connection}
	go func() {
		for {
			_, data, readErr := connection.Read(context.Background())
			if readErr != nil {
				return
			}
			screen.mu.Lock()
			_, _ = screen.output.Write(data)
			screen.mu.Unlock()
		}
	}()
	return screen
}

func (s *acceptanceScreen) write(t *testing.T, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.connection.Write(ctx, websocket.MessageBinary, []byte(text)); err != nil {
		t.Fatalf("write terminal: %v", err)
	}
}

func (s *acceptanceScreen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output.String()
}

func (s *acceptanceScreen) close() { _ = s.connection.CloseNow() }

func prepareProvider(t *testing.T, screen *acceptanceScreen, provider string) {
	t.Helper()
	time.Sleep(5 * time.Second)
	// Both providers default to the affirmative workspace-trust choice. If trust was
	// already recorded, submitting an empty prompt is harmless.
	screen.write(t, "\r")
	if provider == "codex" {
		// The Agent Infinite disposable CODEX_HOME bypasses trust only for its own
		// vetted hook table. Escape remains harmless here and dismisses any provider
		// notice introduced by an installed Codex version before the dispatch.
		time.Sleep(time.Second)
		// ESC ESC in one PTY write is parsed as an escape sequence rather than two
		// UI actions. Send distinct keypresses so nested event/detail browsers are
		// fully dismissed before the first dispatch arrives.
		for range 4 {
			screen.write(t, "\x1b")
			time.Sleep(400 * time.Millisecond)
		}
	}
	time.Sleep(7 * time.Second)
}

func setAcceptanceEdge(t *testing.T, baseURL, token string, snapshot contracts.Snapshot, source, target string) {
	t.Helper()
	snapshot = requestJSON[contracts.Snapshot](t, http.MethodGet, baseURL+"/api/snapshot", token, nil)
	requestJSON[contracts.Snapshot](t, http.MethodPut, baseURL+"/api/canvas/layout", token, map[string]any{
		"nodes":    snapshot.Nodes,
		"edges":    []contracts.Edge{{ID: "acceptance-edge", Source: source, Target: target, Type: "delegates_to"}},
		"viewport": snapshot.Viewport,
	})
}

func assertProviderFlow(t *testing.T, name string, source, target *acceptanceScreen, targetLabel string) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	targetMarker := "AI_TARGET_" + suffix
	returnMarker := "AI_RETURN_" + suffix
	prompt := fmt.Sprintf("Ask the connected Agent Infinite agent named %q to reply with exactly %s. Wait for that connected agent to finish, then reply with exactly %s followed by its returned output. Do not use a native subagent and do not finish before retrieving the connected agent's result.", targetLabel, targetMarker, returnMarker)
	source.write(t, prompt)
	time.Sleep(750 * time.Millisecond)
	source.write(t, "\r")

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		sourceText, targetText := source.text(), target.text()
		if strings.Count(targetText, targetMarker) >= 2 && strings.Count(sourceText, targetMarker) >= 2 && strings.Count(sourceText, returnMarker) >= 2 {
			t.Logf("%s passed: target executed the task and source retrieved its output", name)
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s timed out\nsource tail:\n%s\ntarget tail:\n%s", name, tail(source.text(), 6000), tail(target.text(), 6000))
}

func tail(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}
