# Local protocol contracts

The renderer is the only HTTP/WebSocket caller. The backend listens on a random loopback port and
requires the per-process bearer token. The HTTP surface evolves additively under `/api`; a breaking
change requires a new path version. `openapi.yaml` is the normative HTTP contract. WebSocket and MCP
frames are documented alongside it as they are implemented.

Errors have one stable shape: `code`, `message`, and `details`. HTTP status codes remain semantic;
an error is never returned with status 200.
