# Pi and OpenCode providers (0.6.0)

Agent Infinite treats an orchestrator as a canvas role, not a provider. Pi and OpenCode nodes can
therefore originate or receive the same edge-authorized dispatches as Claude Code and Codex.

## Installation

Pi is used at the version already installed on the machine. Agent Infinite does not update it.

OpenCode must be current before creating a node. Update it from a terminal:

```powershell
npm install -g opencode-ai@latest
opencode --version
```

The runtime deliberately resolves the npm installation before older standalone `opencode.exe`
copies found on PATH.

## Runtime isolation

For Pi, Agent Infinite creates one temporary TypeScript extension per node and passes it through
`--extension`. The extension supplies the canvas tools and reports session lifecycle events.

For OpenCode, Agent Infinite creates a temporary `OPENCODE_CONFIG_DIR` containing a local plugin
and passes its MCP configuration through `OPENCODE_CONFIG_CONTENT`. Its bearer token is inherited
from the terminal environment, never written to the generated plugin/configuration.

All runtime artifacts are removed when the terminal closes. Neither provider's global config nor
the workspace's `.pi`, `.opencode`, `opencode.json`, or versioned files are modified.

## Dispatch lifecycle

Both connectors activate the existing hook session at provider session start. For Pi,
`session_start` marks the composer ready, `before_agent_start` marks it busy, and `agent_settled`
marks it ready again. This lifecycle state is authoritative because current Pi layouts may not show
a textual prompt marker. OpenCode marks completion at `session.idle`; `session.error` marks the
active dispatch blocked. The terminal detector remains the fallback in `auto` mode; `required`
stops a node whose connector does not activate within the existing timeout.
