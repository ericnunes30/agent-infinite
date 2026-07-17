package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/orchestration"
	"github.com/agent-infinite/agent-infinite/backend/internal/terminal"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testWorkspace struct{ snapshot contracts.Snapshot }

func (w testWorkspace) Snapshot() (contracts.Snapshot, error) { return w.snapshot, nil }

type emptyRuntime struct{}

func (emptyRuntime) GetByNode(string) (*terminal.Session, error) {
	return nil, terminal.ErrSessionNotFound
}

func TestStreamableHTTPResolvesLabelAndReturnsDispatchScopedResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workspace := testWorkspace{snapshot: contracts.Snapshot{
		WorkspaceID: "workspace",
		Nodes: []contracts.Node{
			{ID: "source", Kind: "orchestrator", Label: "Lead", Role: "coordinate"},
			{ID: "target", Kind: "agent", Label: "Reviewer", Role: "review changes", Provider: "codex"},
		},
		Edges: []contracts.Edge{{ID: "edge", Source: "source", Target: "target", Type: "delegates_to"}},
	}}
	service := orchestration.New(ctx, workspace, emptyRuntime{})
	service.SetStarter(func(_ context.Context, _ string) (*terminal.Session, error) {
		time.Sleep(150 * time.Millisecond)
		return nil, terminal.ErrSessionNotFound
	})
	handler := NewHandler(service, workspace)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("sourceNodeId", "source")
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_connected_agents", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	listedJSON, _ := json.Marshal(listed.StructuredContent)
	if !strings.Contains(string(listedJSON), "Reviewer") {
		t.Fatalf("connected agents = %s", listedJSON)
	}
	delegated, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "delegate_task", Arguments: map[string]any{"target": "Reviewer", "task": "review this"}})
	if err != nil {
		t.Fatal(err)
	}
	delegatedJSON, _ := json.Marshal(delegated.StructuredContent)
	var dispatchID string
	var delegatedValue map[string]any
	if err := json.Unmarshal(delegatedJSON, &delegatedValue); err == nil {
		dispatchID, _ = delegatedValue["dispatch_id"].(string)
	}
	if dispatchID == "" {
		t.Fatalf("delegate result has no dispatch_id: %s", delegatedJSON)
	}
	started := time.Now()
	result, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_dispatch_result", Arguments: map[string]any{"dispatch_id": dispatchID}})
	if callErr != nil {
		t.Fatal(callErr)
	}
	resultJSON, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(resultJSON), `"status":"failed"`) {
		t.Fatalf("dispatch result = %s, want terminal failure", resultJSON)
	}
	if elapsed := time.Since(started); elapsed < 100*time.Millisecond {
		t.Fatalf("get_dispatch_result returned after %s; want one server-side wait", elapsed)
	}
}

func TestStreamableHTTPListsOrchestrationTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workspace := testWorkspace{snapshot: contracts.Snapshot{Nodes: []contracts.Node{{ID: "source", Kind: "orchestrator"}}, Edges: []contracts.Edge{}}}
	service := orchestration.New(ctx, workspace, emptyRuntime{})
	mux := http.NewServeMux()
	mux.Handle("/mcp/{sourceNodeId}", NewHandler(service, workspace))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp/source", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	want := map[string]bool{"list_connected_agents": false, "delegate_task": false, "get_dispatch_result": false}
	for _, tool := range tools.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q was not advertised", name)
		}
	}
}
