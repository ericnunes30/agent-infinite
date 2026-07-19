# Agent Infinite

Agent Infinite is a Windows-first desktop control plane for coordinating Claude Code, Codex, Pi,
and OpenCode agents in real Git worktrees. It keeps teams, roles, terminals, delegation edges,
models, MCP servers, and skills visible on one persistent canvas while provider credentials and
global CLI configuration remain under the user's control.

Current version: **0.15.5**.

## What it provides

- **Agent Teams:** reusable workflow definitions with an orchestrator, agents, roles, providers,
  models, positions, and delegation edges.
- **Team Templates:** a machine-local library for reusing complete team compositions across
  workspaces.
- **Git Worktrees:** independent operational checkouts created from a new or existing branch and
  optionally linked to a Team.
- **Real terminals:** provider CLIs run in ConPTY sessions with reconnectable xterm terminals,
  lightweight previews for background agents, batch activation, and fullscreen expansion.
- **Asynchronous delegation:** orchestrators discover only connected canvas targets and delegate
  through the internal MCP server. Dispatches remain bound to an ID and results return to the
  originating terminal.
- **Roles:** provider, model, MCP, and skill selections act as presets when creating agents. Existing
  agents keep their copied configuration when a Role changes.
- **Capability governance:** discover provider MCPs and skills, preserve them, curate them per agent,
  or block them only inside sessions launched by Agent Infinite.
- **Model selection:** discover provider defaults and available models, or pin an exact model ID per
  Role, agent, orchestrator, or template.

## Installation requirements

- Windows 10 version 1809 or newer
- Git 2.40+
- At least one supported CLI installed and authenticated: Claude Code, Codex, Pi, or OpenCode

Agent Infinite does not perform provider login and does not store provider credentials. OpenCode
must be current before use:

```powershell
npm install -g opencode-ai@latest
```

Install a published Windows release with its x64 NSIS installer, then open an existing Git
repository from the application.

## Typical workflow

1. Open an existing Git repository.
2. Create an Agent Team or apply a Team Template. A Team is a reusable workflow definition and does
   not permanently own a checkout.
3. Create a Git Worktree from a new or existing branch. Link it to a Team when that workflow will run
   there.
4. Add agents manually, import a Team Template into the worktree canvas, or run a Team against the
   selected worktree.
5. Connect the orchestrator's output handle to the agents it may delegate to. Cycles and outgoing
   edges from ordinary agents are rejected.
6. Use **Ativar terminais** to start every offline agent in the active worktree. Background terminals
   remain lightweight previews; select one for interactive xterm and use its expand control for
   fullscreen mode.
7. Ask the orchestrator to delegate using a visible agent label or role. Offline targets start on
   demand, queued work is serialized per target, and completion returns to the source terminal.
8. Save or restart freely. The canvas, Team definitions, templates, worktrees, capability policies,
   Role presets, and explicit model selections persist.

Deleting a clean worktree automatically stops and removes its runtime nodes, including its
orchestrator, while preserving the reusable Team definition. A worktree with uncommitted changes is
never removed automatically.

## MCPs, skills, and models

Open **Ferramentas → MCPs & Skills** to scan Claude Code, Codex, Pi, and OpenCode. External
capabilities start in **Preservar no provider**, so their existing behavior is unchanged. A
capability can then be changed to:

- **Curado por agente:** available for explicit selection in Roles and agents;
- **Bloqueado no Agent Infinite:** excluded from processes launched by the application;
- **Preservar no provider:** inherited according to the provider's existing configuration.

Bulk controls apply the same policy to all matching capabilities. Managed MCP secrets are protected
with Windows DPAPI and are never returned in full by the API. External secrets are not copied into
the Agent Infinite catalog.

The **Models** tab shows each CLI version, detected default, discovery status, and available or
unverified model IDs. Leaving the selection empty follows the provider default. An explicit model is
passed only to the CLI process launched for that node.

See [Capability governance](docs/capability-governance.md) for the policy and isolation model.

## Delegation and provider lifecycle

Each orchestrator receives a node-scoped MCP server exposing:

- `list_connected_agents`
- `delegate_task`
- `get_dispatch_result`

Only targets connected by an outgoing canvas edge are accepted. Claude Code, Codex, Pi, and
OpenCode report session lifecycle through temporary integrations. Pi readiness is driven by
`session_start`, `before_agent_start`, and `agent_settled`, so delegation does not depend on the
visual shape of its terminal prompt. If hooks are unavailable in `auto` mode, the terminal detector
acts as fallback; `required` mode refuses a session whose integration cannot activate.
Large completion messages sent back to Codex are submitted only after its lifecycle confirms the
prompt; collapsed `[Pasted Content]` blocks receive an automatic second confirmation when needed.

See [Communication architecture](docs/communication-architecture.md) and
[Pi and OpenCode integration](docs/providers-pi-opencode.md) for provider-specific behavior.

## Runtime and safety

- The backend listens only on a random `127.0.0.1` port. All API, WebSocket, and MCP requests except
  health checks require a process-scoped bearer token.
- The Electron renderer is sandboxed and context-isolated, with no direct Node.js access.
- Provider credentials remain in their CLIs. Agent Infinite does not overwrite provider user
  configuration or versioned project files.
- Governance overlays, hook settings, extensions, MCP configuration, and skill materializations are
  temporary and apply only to processes launched by Agent Infinite.
- Dispatch and lifecycle events are recorded in the local Agent Infinite activity journal for
  diagnosis. Tokens and managed secret values are excluded.
- Only the selected terminal mounts a full xterm/WebGL renderer. Other live nodes receive capped text
  previews to control CPU and memory usage.

## Development

Development additionally requires Go 1.25+, Node.js 24+, and pnpm 10+.

```powershell
pnpm install
pnpm dev
```

Useful checks:

```powershell
pnpm lint
pnpm test
pnpm build
pnpm test:e2e
```

Generate the Windows x64 NSIS installer with:

```powershell
pnpm dist:win
```

The installer is unsigned until a trusted code-signing certificate is configured, so Windows
SmartScreen may display a warning. Releases follow the [`0.X.Y`](docs/versioning.md) convention:
`X` adds functionality and `Y` contains compatible fixes.

## Tests

`pnpm test` runs the Go backend tests and the desktop Vitest/React Testing Library suite.
`pnpm test:e2e` builds and runs Electron with a deterministic mock provider against a temporary Git
repository and real worktrees.

Real-provider acceptance is opt-in because it uses installed provider credentials and quota:

```powershell
$env:AGENT_INFINITE_REAL_PROVIDERS = '1'
go -C backend test ./internal/app -run TestRealBidirectionalProviderFlow -v -count=1
```

## Further documentation

- [Team Templates and independent Team canvas](docs/team-templates.md)
- [Team, worktree, and Role management](docs/team-worktree-role-management.md)
- [Capability governance](docs/capability-governance.md)
- [Pi and OpenCode provider integration](docs/providers-pi-opencode.md)
- [MVP status and historical delivery notes](docs/mvp-status.md)
