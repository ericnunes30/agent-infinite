# WebSocket contracts

## `/ws/terminals/{sessionId}?token=...`

The browser cannot set the HTTP Authorization header during a WebSocket upgrade, so the same
process-scoped token is carried in the query string. Unknown sessions return HTTP 404 before the
upgrade.

- Backend to renderer binary frames: raw ConPTY bytes. The first frame is the current 2 MiB ring
  buffer snapshot; subsequent frames are live output.
- Renderer to backend binary frames: raw UTF-8 terminal input.
- Text control frame: `{"type":"resize","cols":120,"rows":32}`.
- Text liveness frame: `{"type":"ping"}`; backend replies `{"type":"pong"}`.

PTY reads never wait for a browser write. Each client receives a bounded queue; a client that fills
it is disconnected and can reconstruct the terminal from the ring buffer on reconnect.

## `/ws/events?token=...`

Authenticated JSON event stream for `terminal.started`, `terminal.exited`,
`agent.status_changed`, `agent.output_preview`, every `dispatch.*` state transition,
`integration.hook_event`, `integration.degraded`, `integration.required_failed`, observed native
subagent lifecycle, and `backend.error`. Output previews are coalesced to at most ten emissions per
second per node. The renderer reconnects after loss and calls `GET /api/runtime` plus
`GET /api/dispatches` to recover session, integration, and dispatch state.
