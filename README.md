# Agent Infinite

Agent Infinite is a Windows-first desktop control plane for running Claude Code and Codex in real,
isolated Git worktrees. Agent Teams are logical groups and may be linked to multiple Git Worktrees;
each agent selects the checkout where it works. Orchestrators delegate asynchronously over MCP; the
operator keeps every live terminal and relationship visible on a persistent canvas.

## Requirements

- Windows 10 version 1809 or newer
- Git 2.40+
- Go 1.25+
- Node.js 24+ and pnpm 10+
- Claude Code and Codex installed and authenticated for real-provider operation

The application never performs provider login and never stores provider credentials.

## Development

```powershell
pnpm install
pnpm dev
```

Releases seguem a convenção [`0.X.Y`](docs/versioning.md): `X` aumenta para funcionalidades e `Y`
aumenta para correções compatíveis.

Useful checks:

```powershell
pnpm lint
pnpm test
pnpm build
pnpm test:e2e
```

`pnpm build` compiles the Go backend into `apps/desktop/resources` before bundling the Electron
application. Auto-update and code signing remain outside the MVP.

To generate the Windows x64 NSIS installer:

```powershell
pnpm dist:win
```

The unsigned installer is written to `apps/desktop/release`. Windows SmartScreen may warn until a
trusted code-signing certificate is configured.

## Architecture

- `apps/desktop`: Electron main/preload plus a React renderer
- `backend`: Go runtime for workspaces, Git, ConPTY, detection, orchestration, and MCP
- `contracts`: documented local HTTP/WebSocket protocol and fixtures
- `.agent-infinite`: per-workspace JSON persistence created inside the opened Git repository
- `%LOCALAPPDATA%/AgentInfinite/worktrees`: durable Git worktrees linked to Agent Teams

The renderer never receives Node.js access. Electron exposes only native folder selection and the
ephemeral backend connection. Go is the source of truth for domain and runtime state.

## MVP walkthrough

1. Run `pnpm dev` and open any existing Git repository.
2. Create an Agent Team. Agent Infinite creates an initial dedicated branch and durable worktree under
   `%LOCALAPPDATA%/AgentInfinite/worktrees` plus one Claude Code or Codex orchestrator node.
3. Create additional Git Worktrees from the worktree section and link them to the team when another
   branch needs an independent checkout. When creating an agent, choose its team and worktree.
4. Create additional agents, then drag from an orchestrator's right handle to a target node. Cycles
   and edges from ordinary agents are rejected.
5. Double-click nodes to launch their real CLI in ConPTY. Selecting a running node opens its live
   xterm terminal; reconnecting reconstructs it from the backend ring buffer.
6. Ask an orchestrator to delegate using the visible agent label or role. Its scoped MCP server
   exposes `list_connected_agents`, `delegate_task`, and `get_dispatch_result`; only targets joined
   by an outgoing canvas edge are accepted. An existing offline target is started automatically,
   each result remains bound to its `dispatch_id`, and the inspector shows the full dispatch state.
7. Reload or restart. Canvas JSON and worktrees persist, and nodes marked `autoStart` are restored.

## Runtime and safety model

- The Go backend listens only on a random `127.0.0.1` port. Every API, WebSocket, and MCP request
  except `/health` requires a fresh process-scoped bearer token.
- The Electron renderer uses context isolation, sandboxing, and a narrow preload bridge. It has no
  Node.js access.
- Provider credentials remain in the provider CLIs. Agent Infinite does not log in, copy auth
  files, or overwrite Claude/Codex user configuration.
- Provider hooks are session-scoped and controlled by `integration.hooks` in the workspace canvas:
  `auto` (default), `off`, or `required`. Temporary settings live under the user cache, are removed
  on session close/startup reconciliation, and never modify provider globals or versioned project
  files. Refusing Codex hook trust in `auto` keeps the terminal active in detector mode.
- Hook callbacks use per-session tokens bound to workspace, node, and provider. Dispatch and
  lifecycle events are appended to `%LOCALAPPDATA%/AgentInfinite/activity.jsonl` for diagnosis.
- Team deletion refuses dirty worktrees. The associated branch is preserved even after a clean
  worktree is removed.
- Full xterm/WebGL rendering is demand-driven (one selected terminal in the MVP, below the hard
  eight-view budget); all other running nodes receive text previews capped at 10 Hz. Xterm
  scrollback is capped at 1,000 lines and the backend raw ring at 2 MiB.

## Tests

`pnpm test` runs Go tests plus Vitest/React Testing Library. `pnpm test:e2e` runs the built Electron
application with the deterministic `mock` provider, which is rejected unless
`AGENT_INFINITE_TEST_MODE=1`. The E2E creates a real temporary Git repository and worktrees,
dispatches through MCP, observes `Working → Done`, and verifies reload persistence.

Real-provider acceptance is opt-in because it consumes the installed providers' credentials and
quota:

```powershell
$env:AGENT_INFINITE_REAL_PROVIDERS = '1'
go -C backend test ./internal/app -run TestRealBidirectionalProviderFlow -v -count=1
```

That test proves both Claude Code → Codex and Codex → Claude dispatch-and-return flows.

## Field-test reports

- [Agent Infinite 0.1.0 — Windows field test, UI findings, and deferred orchestration questions](docs/field-test-findings-2026-07-16.md)
- [Status do MVP e pendências atuais](docs/mvp-status.md)
- [Inventário da barra lateral do OmniRift](docs/omnirift-sidebar-inventory-2026-07-16.md)

## Architecture

- [Agent communication, dispatch lifecycle, and ephemeral provider hooks](docs/communication-architecture.md)
