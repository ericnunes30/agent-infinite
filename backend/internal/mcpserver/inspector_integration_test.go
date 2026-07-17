package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/orchestration"
)

// This opt-in protocol check uses the official MCP Inspector CLI in addition
// to the SDK client tests. It may download the current Inspector package.
func TestOfficialMCPInspectorListsTools(t *testing.T) {
	if os.Getenv("AGENT_INFINITE_MCP_INSPECTOR") != "1" {
		t.Skip("set AGENT_INFINITE_MCP_INSPECTOR=1 to run the official MCP Inspector")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	workspace := testWorkspace{snapshot: contracts.Snapshot{
		WorkspaceID: "workspace",
		Nodes:       []contracts.Node{{ID: "source", Kind: "orchestrator", Label: "Lead"}},
	}}
	service := orchestration.New(ctx, workspace, emptyRuntime{})
	mux := http.NewServeMux()
	mux.Handle("/mcp/{sourceNodeId}", NewHandler(service, workspace))
	server := httptest.NewServer(mux)
	defer server.Close()
	command := exec.CommandContext(ctx, "npx.cmd", "-y", "@modelcontextprotocol/inspector@latest", "--cli", server.URL+"/mcp/source", "--transport", "http", "--method", "tools/list")
	output, err := command.CombinedOutput()
	text := string(output)
	for _, tool := range []string{"list_connected_agents", "delegate_task", "get_dispatch_result"} {
		if !strings.Contains(text, tool) {
			t.Fatalf("Inspector output missing %q (process error %v):\n%s", tool, err, text)
		}
	}
	if err != nil {
		// Inspector 0.18.x currently triggers a libuv assertion during Windows
		// teardown after printing a valid tools/list response. The protocol check
		// above is authoritative; keep the teardown defect visible in test logs.
		t.Logf("Inspector returned the complete response before Windows teardown error: %v", err)
	}
}
