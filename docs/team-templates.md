# Team Templates and independent Team canvas (0.7.0)

## Model

- A **Team Template** is a reusable composition stored in the local Agent Infinite library.
- A **Team** is an independent workflow canvas. It contains roles, providers, node positions and
  delegation edges, but has no permanent Git Worktree association.
- A **Git Worktree** is an operational context. It is selected only when a Team is executed.

Templates are shared by projects on the same machine and are stored at:

`%LOCALAPPDATA%\AgentInfinite\team-templates.json`

The file contains only Team composition: names, color, nodes, roles, providers, auto-start flags,
positions and edges. It never stores worktree paths, branches, terminals, dispatches or credentials.

## Execution

`Execute Team` requires a Git Worktree belonging to the selected Team. When none exists, the UI only
offers `Create Git Worktree`; after it is created, the user confirms execution against it.

The selected worktree becomes a temporary runtime context for the Team. The backend keeps it in
memory while one or more definition nodes have live terminal sessions, then removes it. The Team
canvas, global worktree selection and persisted data are not changed by execution.

The orchestrator and nodes marked `autoStart` launch at execution. Other definition nodes start only
when they are delegated work, preserving the existing on-demand behavior.

## Existing canvases

Existing worktree-bound canvases remain untouched. Select their Git Worktree to operate them, then
use **Extrair workflow para Team** to clone that canvas into the independent Team definition. The
source operational canvas is kept as-is.
