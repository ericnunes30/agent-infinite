package transport

import (
	"net/http"
	"strings"

	"github.com/agent-infinite/agent-infinite/backend/internal/capabilities"
)

func (h *HTTP) capabilityInventory(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": h.capabilities.List()})
}

func (h *HTTP) scanCapabilities(w http.ResponseWriter, _ *http.Request) {
	path := ""
	if snapshot, err := h.workspace.Snapshot(); err == nil {
		path = snapshot.WorkspacePath
	}
	writeJSON(w, http.StatusOK, h.capabilities.Scan(path))
}

func (h *HTTP) patchCapabilityPolicy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Policy string `json:"policy"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	item, err := h.capabilities.SetPolicy(r.PathValue("id"), strings.TrimSpace(request.Policy))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "capability_policy_invalid", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *HTTP) patchCapabilityPolicies(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IDs    []string `json:"ids"`
		Policy string   `json:"policy"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	items, err := h.capabilities.SetPolicies(request.IDs, strings.TrimSpace(request.Policy))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "capability_bulk_policy_invalid", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *HTTP) upsertManagedMCP(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Provider    string            `json:"provider"`
		Spec        map[string]any    `json:"spec"`
		Secrets     map[string]string `json:"secrets"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	if pathID := r.PathValue("id"); pathID != "" {
		request.ID = pathID
	}
	item, err := h.capabilities.UpsertManaged(capabilities.Item{ID: request.ID, Kind: capabilities.KindMCP, Name: request.Name, Description: request.Description, Provider: request.Provider, Spec: request.Spec}, "", request.Secrets)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "capability_invalid", err.Error(), nil)
		return
	}
	status := http.StatusCreated
	if r.PathValue("id") != "" {
		status = http.StatusOK
	}
	writeJSON(w, status, item)
}

func (h *HTTP) upsertManagedSkill(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Provider    string `json:"provider"`
		Markdown    string `json:"markdown"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	if pathID := r.PathValue("id"); pathID != "" {
		request.ID = pathID
	}
	item, err := h.capabilities.UpsertManaged(capabilities.Item{ID: request.ID, Kind: capabilities.KindSkill, Name: request.Name, Description: request.Description, Provider: request.Provider}, request.Markdown, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "capability_invalid", err.Error(), nil)
		return
	}
	status := http.StatusCreated
	if r.PathValue("id") != "" {
		status = http.StatusOK
	}
	writeJSON(w, status, item)
}

func (h *HTTP) managedSkillContent(w http.ResponseWriter, r *http.Request) {
	markdown, err := h.capabilities.SkillMarkdown(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "skill_not_found", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"markdown": markdown})
}

func (h *HTTP) archiveCapability(w http.ResponseWriter, r *http.Request) {
	if err := h.capabilities.Archive(r.PathValue("id")); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "capability_archive_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) promoteCapability(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Secrets map[string]string `json:"secrets"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	item, err := h.capabilities.Promote(r.PathValue("id"), request.Secrets)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "capability_promotion_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *HTTP) testMCP(w http.ResponseWriter, r *http.Request) {
	item, err := h.capabilities.ItemForTest(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "mcp_not_found", err.Error(), nil)
		return
	}
	result, err := capabilities.TestMCP(r.Context(), item)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "mcp_test_failed", err.Error(), nil)
		return
	}
	h.capabilities.RecordToolCount(item.ID, result.ToolCount)
	writeJSON(w, http.StatusOK, result)
}
