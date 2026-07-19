package transport

import (
	"errors"
	"net/http"
	"strings"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
)

func (h *HTTP) listRoleProfiles(w http.ResponseWriter, _ *http.Request) {
	snapshot, err := h.workspace.Snapshot()
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_not_open", "No workspace is open.", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": snapshot.RoleProfiles})
}

func (h *HTTP) createRoleProfile(w http.ResponseWriter, r *http.Request) {
	var profile contracts.RoleProfile
	if !decodeBody(w, r, &profile) {
		return
	}
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Model = strings.TrimSpace(profile.Model)
	if profile.ID == "" {
		profile.ID = newID()
	}
	if profile.MCPIDs == nil {
		profile.MCPIDs = []string{}
	}
	if profile.SkillIDs == nil {
		profile.SkillIDs = []string{}
	}
	if err := h.capabilities.ValidateSelection(profile.DefaultProvider, profile.MCPIDs, profile.SkillIDs); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "capability_review_required", err.Error(), nil)
		return
	}
	if err := h.models.Validate(profile.DefaultProvider, profile.Model); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_model", err.Error(), nil)
		return
	}
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		next.RoleProfiles = append(next.RoleProfiles, profile)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "role_profile_invalid", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, profile)
}

func (h *HTTP) patchRoleProfile(w http.ResponseWriter, r *http.Request) {
	var request contracts.RoleProfile
	if !decodeBody(w, r, &request) {
		return
	}
	request.Name, request.Model = strings.TrimSpace(request.Name), strings.TrimSpace(request.Model)
	if err := h.capabilities.ValidateSelection(request.DefaultProvider, request.MCPIDs, request.SkillIDs); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "capability_review_required", err.Error(), nil)
		return
	}
	if err := h.models.Validate(request.DefaultProvider, request.Model); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_model", err.Error(), nil)
		return
	}
	var result contracts.RoleProfile
	found := false
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		for index := range next.RoleProfiles {
			if next.RoleProfiles[index].ID == r.PathValue("id") {
				found = true
				request.ID = next.RoleProfiles[index].ID
				request.Builtin = next.RoleProfiles[index].Builtin
				next.RoleProfiles[index] = request
				result = request
				return nil
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "role_profile_invalid", err.Error(), nil)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "role_profile_not_found", "The role profile does not exist.", nil)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTP) deleteRoleProfile(w http.ResponseWriter, r *http.Request) {
	found := false
	_, err := h.workspace.Update(func(next *contracts.Snapshot) error {
		for _, node := range next.Nodes {
			if node.RoleProfileID == r.PathValue("id") {
				return errors.New("role profile is used by an agent")
			}
		}
		roles := make([]contracts.RoleProfile, 0, len(next.RoleProfiles))
		for _, role := range next.RoleProfiles {
			if role.ID == r.PathValue("id") {
				found = true
				continue
			}
			roles = append(roles, role)
		}
		next.RoleProfiles = roles
		return nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, "role_profile_in_use", err.Error(), nil)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "role_profile_not_found", "The role profile does not exist.", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
