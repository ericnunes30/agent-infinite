package transport

import (
	"net/http"
	"strings"
)

func (h *HTTP) modelInventory(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.models.Inventory())
}

func (h *HTTP) scanModels(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Provider string `json:"provider"`
	}
	if r.ContentLength != 0 && !decodeBody(w, r, &request) {
		return
	}
	request.Provider = strings.TrimSpace(request.Provider)
	if request.Provider != "" && request.Provider != "claude" && request.Provider != "codex" && request.Provider != "pi" && request.Provider != "opencode" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_provider", "Provider must be claude, codex, pi, or opencode.", nil)
		return
	}
	workspacePath := ""
	if snapshot, err := h.workspace.Snapshot(); err == nil {
		workspacePath = snapshot.WorkspacePath
	}
	writeJSON(w, http.StatusOK, h.models.Scan(r.Context(), workspacePath, request.Provider))
}
