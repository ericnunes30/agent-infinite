# Architectural decisions

- 2026-07-15: The local HTTP API is loopback-only and authenticated with a process-scoped bearer
  token. `/health` is intentionally unauthenticated so Electron can distinguish liveness from auth.
- 2026-07-15: The MVP API evolves additively under `/api`; a breaking contract requires a new path
  version. OpenAPI in `contracts/openapi.yaml` is normative.
- 2026-07-15: Mutations return semantic status codes (`201`, `204`, `409`, `422`) and the stable
  `{code,message,details}` error shape required by the product specification.
- 2026-07-15: The UI direction is an industrial, dark operations console with restrained lime
  telemetry accents and compact monospace metadata, optimized for long-running technical work.
- 2026-07-16: Field-test findings are recorded in
  `docs/field-test-findings-2026-07-16.md`. UI defects ship first; provider routing and native
  subagent precedence remain explicitly deferred for cross-provider design discussion.
- 2026-07-16: UI 0.1.1 was validated and packaged. ORCH-01 remains intentionally unchanged until
  the cross-provider architecture discussion recorded in the field-test report.
- 2026-07-16: Agent communication uses a hybrid contract: MCP for commands, provider hooks for
  lifecycle, PTY for interactive delivery, and the backend as source of truth. Canvas agents take
  precedence over native subagents, results are dispatch-scoped, and hooks are injected ephemerally
  per Agent Infinite session without global or versioned project changes. Specification:
  `docs/communication-architecture.md`.
- 2026-07-16: Communication contract 0.2.0 is implemented. Hook policy is persisted per workspace;
  ephemeral settings and tokens live outside the repository; Codex hook trust remains explicit;
  `auto` degrades to the terminal detector; dispatches auto-start existing connected targets,
  serialize per target, persist by workspace, and retain result ownership by `dispatch_id`.
- 2026-07-16: Codex-to-Codex canvas communication is field-approved on 0.2.3 after clean restart.
  Delegation, target execution, dispatch-scoped capture, event-driven wake-up, and final reporting
  completed without result polling. The canonical communication specification records the evidence
  and the restart-after-upgrade operational note.
- 2026-07-16: The first team must be the initial canvas selection after a workspace snapshot loads;
  this is state initialization, not a simulated click. UI-04 and the remaining MVP release checks
  are tracked in `docs/mvp-status.md`.
- 2026-07-16: The light theme is field-validated and is no longer an MVP pending item. The remaining
  functional MVP work is UI-04; automated coverage and the 0.2.4 installer are release checks.
- 2026-07-17: Release 0.3.0 adds an application header, a Workspace area, and renames the sidebar's
  user-facing "Paralelos" section to "Git Worktrees". The existing domain identifiers remain
  unchanged, and the first Git Worktree is initialized as the default canvas selection.
- 2026-07-17: Product clarification pending for the next release: in the current MVP, a new team
  creates one Git Worktree, while agents added to that team reuse it. The sidebar must make this
  relationship explicit; separating Agent Teams from Git Worktrees is deferred because it would
  require a persisted-domain migration.
- 2026-07-17: Proposed product direction for discussion: manage Agent Teams independently and allow
  multiple Git Worktrees/branches to link to one team. This is not yet an approved schema change;
  canvas scope and dispatch worktree selection must be decided before implementation.
- 2026-07-17: Release 0.4.0 approves the separated domain model. `Snapshot.worktrees` stores the
  physical Git checkouts; `Team` remains the logical grouping; `Node.worktreeId` selects the
  checkout used by that agent. The migration is additive inside schema v1: legacy team branches
  become worktrees with preserved IDs, branches, and paths. A team may have zero or more worktrees,
  while creating a team creates one initial worktree by default for backwards compatibility.
- 2026-07-17: Release 0.5.0 makes Git Worktree the sole active canvas context. Agent Teams are
  managed on their own page and are linked only when a worktree is created; selecting a Team must
  never select, filter, or otherwise change a worktree/canvas context.
