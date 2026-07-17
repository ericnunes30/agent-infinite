package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
	"github.com/agent-infinite/agent-infinite/backend/internal/orchestration"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Workspace interface {
	Snapshot() (contracts.Snapshot, error)
}

type Handler struct {
	orchestration *orchestration.Service
	workspace     Workspace
	http          *mcp.StreamableHTTPHandler
}

func NewHandler(orchestrator *orchestration.Service, workspace Workspace) *Handler {
	handler := &Handler{orchestration: orchestrator, workspace: workspace}
	handler.http = mcp.NewStreamableHTTPHandler(handler.serverForRequest, &mcp.StreamableHTTPOptions{JSONResponse: true})
	return handler
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.http.ServeHTTP(w, r) }

func (h *Handler) serverForRequest(request *http.Request) *mcp.Server {
	source := request.PathValue("sourceNodeId")
	server := mcp.NewServer(
		&mcp.Implementation{Name: "agent-infinite", Title: "Agent Infinite connected agents", Version: "0.5.0"},
		&mcp.ServerOptions{Instructions: h.instructions(source)},
	)

	type listInput struct{}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_connected_agents",
		Description: "List this canvas orchestrator and only the existing Agent Infinite nodes it is authorized to contact. Use this before delegation when the user names a visible label or role. An offline node is still a valid target and will be started by delegate_task. Never replace a missing, ambiguous, offline, or failed connected target with a provider-native subagent.",
		Annotations: &mcp.ToolAnnotations{Title: "List connected canvas agents", ReadOnlyHint: true, OpenWorldHint: boolPointer(false)},
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ listInput) (*mcp.CallToolResult, orchestration.ConnectedAgents, error) {
		connected, err := h.orchestration.ListConnectedAgents(source)
		return nil, connected, err
	})

	type delegateInput struct {
		Target string `json:"target" jsonschema:"Exact connected agent ID, visible label, or visible role. Labels and roles must resolve uniquely among this orchestrator's outgoing canvas connections."`
		Task   string `json:"task" jsonschema:"Complete non-empty task for one request and one response, at most 32 KiB."`
	}
	type delegateOutput struct {
		DispatchID  string `json:"dispatch_id"`
		TargetID    string `json:"target_id"`
		TargetLabel string `json:"target_label"`
		Status      string `json:"status"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delegate_task",
		Description: "Create a traceable asynchronous dispatch to one authorized Agent Infinite canvas node. The target may be named by unique label, unique role, or ID. Existing offline nodes are automatically started and the dispatch remains queued until the provider is ready. After success, end the current turn and wait: Agent Infinite will wake this orchestrator once with the isolated result. Do not poll get_dispatch_result. Ambiguity and routing failures are explicit and never authorize native-subagent fallback.",
		Annotations: &mcp.ToolAnnotations{Title: "Delegate to connected canvas agent", DestructiveHint: boolPointer(false), OpenWorldHint: boolPointer(false)},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input delegateInput) (*mcp.CallToolResult, delegateOutput, error) {
		dispatch, err := h.orchestration.DelegateTask(source, input.Target, input.Task)
		if err != nil {
			return nil, delegateOutput{}, err
		}
		return nil, delegateOutput{DispatchID: dispatch.ID, TargetID: dispatch.TargetNodeID, TargetLabel: dispatch.TargetLabel, Status: dispatch.Status}, nil
	})

	type resultInput struct {
		DispatchID string `json:"dispatch_id" jsonschema:"dispatch_id returned by delegate_task. Results are authorized and isolated by dispatch, never by agent alone."`
		MaxLines   int    `json:"max_lines,omitempty" jsonschema:"Maximum output lines from 1 to 500; defaults to 120."`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_dispatch_result",
		Description: "Recovery path for one dispatch created by this orchestrator. Call only when the user explicitly requests the result or automatic notification was interrupted. If the dispatch is active, this single call waits inside Agent Infinite until completion without repeated model turns. Never poll it and never call it after an Agent Infinite completion notification.",
		Annotations: &mcp.ToolAnnotations{Title: "Read dispatch result", ReadOnlyHint: true, OpenWorldHint: boolPointer(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input resultInput) (*mcp.CallToolResult, orchestration.Dispatch, error) {
		dispatch, err := h.orchestration.WaitDispatchResult(ctx, source, input.DispatchID, input.MaxLines)
		return nil, dispatch, err
	})
	return server
}

func boolPointer(value bool) *bool { return &value }

func (h *Handler) instructions(source string) string {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		return "Agent Infinite connected-agent orchestration is unavailable until a workspace is open."
	}
	var identity contracts.Node
	targets := make([]string, 0)
	for _, node := range snapshot.Nodes {
		if node.ID == source {
			identity = node
		}
	}
	for _, edge := range snapshot.Edges {
		if edge.Source != source {
			continue
		}
		for _, node := range snapshot.Nodes {
			if node.ID == edge.Target {
				targets = append(targets, fmt.Sprintf("%s (role: %s, provider: %s)", node.Label, node.Role, node.Provider))
			}
		}
	}
	return fmt.Sprintf(`AGENT INFINITE PRECEDENCE: when the user asks for an agent, app agent, connected agent, or names a visible label/role, use these Agent Infinite tools before any provider-native subagent mechanism. Native subagents are allowed only when the user explicitly asks for one. A missing edge, ambiguous name, offline provider, startup failure, or tool error is never permission to silently fall back to a native subagent.

You are the canvas node %q, role %q, in team %q. Authorized connected targets: %s.

Workflow: (1) use list_connected_agents when target identity is not already unambiguous; (2) call delegate_task with the visible label/role and complete task; (3) retain dispatch_id, stop using tools, and end the current turn so this node becomes idle; (4) Agent Infinite will wake this node once with a completion JSON containing the isolated result; (5) report that result without calling get_dispatch_result. get_dispatch_result is a blocking recovery path only when automatic notification was interrupted or the user explicitly asks for it. Never poll. One dispatch carries one task and one response.`, identity.Label, identity.Role, identity.TeamID, strings.Join(targets, "; "))
}
