package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const auditStatusDenied = "DENIED"

func decodeJSONBody(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

type templateUpsertPayload struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Overwrite   bool   `json:"overwrite"`
}

type templateInstantiatePayload struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Content     string   `json:"content"`
	Groups      []string `json:"groups"`
	Overwrite   bool     `json:"overwrite"`
}

func canAccessTemplate(user *APIUser, tmpl *TemplateRecord, requireWrite bool) bool {
	if user == nil || tmpl == nil {
		return false
	}
	isOwner := strings.EqualFold(strings.TrimSpace(tmpl.CreatedBy), strings.TrimSpace(user.Username))
	if requireWrite {
		if user.CanManageAllTemplates() {
			return true
		}
		return isOwner && user.CanWriteOwnTemplates()
	}
	if user.CanViewAllTemplates() {
		return true
	}
	return isOwner
}

func (s *APIServer) handleTemplatesCollection(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	authUser := authUsernameFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))
		includeContent := r.URL.Query().Get("include_content") == "true" || r.URL.Query().Get("include_content") == "1"
		ownerFilter := authUser
		if user != nil && user.CanViewAllTemplates() {
			ownerFilter = strings.TrimSpace(r.URL.Query().Get("owner"))
		}
		items, err := s.ListTemplates(r.Context(), typeFilter, ownerFilter, includeContent)
		if err != nil {
			code := http.StatusInternalServerError
			if strings.Contains(err.Error(), "unsupported template type") {
				code = http.StatusBadRequest
			}
			s.recordArtifactAudit(r, authUser, "template.list", "template", typeFilter, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUser, "template.list", "template", typeFilter, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, map[string]any{
			"count":     len(items),
			"templates": items,
		})

	case http.MethodPost:
		if user == nil || !user.CanWriteOwnTemplates() {
			msg := "Forbidden: creating templates requires 'write', 'upload', 'template-admin', or 'all' permission"
			s.recordArtifactAudit(r, authUser, "template.create", "template", "", http.StatusForbidden, auditStatusDenied, start, msg)
			writeJSONError(w, http.StatusForbidden, msg)
			return
		}
		var payload templateUpsertPayload
		if err := decodeJSONBody(r, &payload); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
			return
		}
		rec, err := s.CreateOrUpsertTemplate(r.Context(), TemplateRecord{
			Type:        payload.Type,
			Name:        payload.Name,
			Description: payload.Description,
			Content:     payload.Content,
			CreatedBy:   authUser,
			UpdatedBy:   authUser,
		}, payload.Overwrite)
		if err != nil {
			code := http.StatusInternalServerError
			switch {
			case errors.Is(err, ErrTemplateExists):
				code = http.StatusConflict
			case strings.Contains(err.Error(), "unsupported template type"), strings.Contains(err.Error(), "required"):
				code = http.StatusBadRequest
			}
			s.recordArtifactAudit(r, authUser, "template.create", "template", payload.Name, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUser, "template.create", "template", rec.Name, http.StatusCreated, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusCreated, rec)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, errMsgMethodNotAllowed)
	}
}

func (s *APIServer) handleTemplateByID(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	authUser := authUsernameFromRequest(r)

	rel := strings.TrimPrefix(r.URL.Path, "/api/v1/templates/")
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		s.handleTemplatesCollection(w, r)
		return
	}

	parts := strings.Split(rel, "/")
	id, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "valid numeric template ID is required")
		return
	}

	// Sub-route: POST /api/v1/templates/{id}/instantiate
	if len(parts) == 2 && strings.EqualFold(parts[1], "instantiate") {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed; use POST to instantiate a template")
			return
		}
		s.handleInstantiateTemplate(w, r, user, authUser, id, start)
		return
	}
	if len(parts) > 1 {
		writeJSONError(w, http.StatusNotFound, "unknown template endpoint")
		return
	}

	existing, err := s.GetTemplateByID(r.Context(), id)
	if errors.Is(err, ErrTemplateNotFound) {
		writeJSONError(w, http.StatusNotFound, "template not found")
		return
	}
	if err != nil {
		s.recordArtifactAudit(r, authUser, "template.get", "template", strconv.FormatInt(id, 10), http.StatusInternalServerError, auditStatusError, start, err.Error())
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		if !canAccessTemplate(user, existing, false) {
			msg := "Forbidden: you can only view your own templates unless granted 'template-viewer', 'template-admin', or 'all'"
			s.recordArtifactAudit(r, authUser, "template.get", "template", existing.Name, http.StatusForbidden, auditStatusDenied, start, msg)
			writeJSONError(w, http.StatusForbidden, msg)
			return
		}
		s.recordArtifactAudit(r, authUser, "template.get", "template", existing.Name, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, existing)

	case http.MethodPut:
		if !canAccessTemplate(user, existing, true) {
			msg := "Forbidden: you can only edit your own templates unless granted 'template-admin' or 'all'"
			s.recordArtifactAudit(r, authUser, "template.update", "template", existing.Name, http.StatusForbidden, auditStatusDenied, start, msg)
			writeJSONError(w, http.StatusForbidden, msg)
			return
		}
		var payload templateUpsertPayload
		if err := decodeJSONBody(r, &payload); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
			return
		}
		updated, err := s.UpdateTemplateByID(r.Context(), id, TemplateRecord{
			Type:        payload.Type,
			Name:        payload.Name,
			Description: payload.Description,
			Content:     payload.Content,
			UpdatedBy:   authUser,
		})
		if err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrTemplateExists) {
				code = http.StatusConflict
			} else if strings.Contains(err.Error(), "unsupported template type") {
				code = http.StatusBadRequest
			}
			s.recordArtifactAudit(r, authUser, "template.update", "template", existing.Name, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUser, "template.update", "template", updated.Name, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, updated)

	case http.MethodDelete:
		if !canAccessTemplate(user, existing, true) {
			msg := "Forbidden: you can only delete your own templates unless granted 'template-admin' or 'all'"
			s.recordArtifactAudit(r, authUser, "template.delete", "template", existing.Name, http.StatusForbidden, auditStatusDenied, start, msg)
			writeJSONError(w, http.StatusForbidden, msg)
			return
		}
		if err := s.DeleteTemplateByID(r.Context(), id); err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrTemplateNotFound) {
				code = http.StatusNotFound
			}
			s.recordArtifactAudit(r, authUser, "template.delete", "template", existing.Name, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUser, "template.delete", "template", existing.Name, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, map[string]any{
			"deleted": true,
			"id":      id,
			"name":    existing.Name,
		})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, errMsgMethodNotAllowed)
	}
}

func (s *APIServer) handleInstantiateTemplate(w http.ResponseWriter, r *http.Request, user *APIUser, authUser string, id int64, start time.Time) {
	tmpl, err := s.GetTemplateByID(r.Context(), id)
	if errors.Is(err, ErrTemplateNotFound) {
		writeJSONError(w, http.StatusNotFound, "template not found")
		return
	}
	if err != nil {
		s.recordArtifactAudit(r, authUser, "template.instantiate", "template", strconv.FormatInt(id, 10), http.StatusInternalServerError, auditStatusError, start, err.Error())
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !canAccessTemplate(user, tmpl, false) {
		msg := "Forbidden: you do not have permission to view or instantiate this template"
		s.recordArtifactAudit(r, authUser, "template.instantiate", "template", tmpl.Name, http.StatusForbidden, auditStatusDenied, start, msg)
		writeJSONError(w, http.StatusForbidden, msg)
		return
	}

	var payload templateInstantiatePayload
	if err := decodeJSONBody(r, &payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}
	targetName := strings.TrimSpace(payload.Name)
	if targetName == "" {
		writeJSONError(w, http.StatusBadRequest, "target artifact name is required")
		return
	}
	desc := payload.Description
	if strings.TrimSpace(desc) == "" {
		desc = tmpl.Description
	}
	content := payload.Content
	if strings.TrimSpace(content) == "" {
		content = tmpl.Content
	}

	// Enforce live artifact write permissions / pipeline ACLs
	if tmpl.Type == TargetPipeline {
		if !s.authorizePipelineWrite(w, r, pipelineWriteAuth{
			name:        targetName,
			groups:      payload.Groups,
			allowUpdate: payload.Overwrite,
			auditEvent:  "template.instantiate",
			startedAt:   start,
		}) {
			return
		}
	} else {
		if !s.authorizeNonPipelineArtifact(w, r, user, tmpl.Type, targetName, PermWrite, start) {
			return
		}
	}

	var created *ArtifactRecord
	if payload.Overwrite {
		created, err = s.UpsertArtifact(r.Context(), tmpl.Type, targetName, desc, content, false, "")
	} else {
		created, err = s.CreateArtifact(r.Context(), tmpl.Type, targetName, desc, content, false, "")
	}
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrArtifactExists) {
			code = http.StatusConflict
		}
		s.recordArtifactAudit(r, authUser, "template.instantiate", tmpl.Type, targetName, code, auditStatusError, start, err.Error())
		writeJSONError(w, code, err.Error())
		return
	}
	if tmpl.Type == TargetPipeline && len(payload.Groups) > 0 {
		if grpErr := s.assignAndPopulatePipelineGroups(r.Context(), created, payload.Groups); grpErr != nil {
			s.recordArtifactAudit(r, authUser, "template.instantiate", tmpl.Type, targetName, http.StatusInternalServerError, auditStatusError, start, grpErr.Error())
			writeJSONError(w, http.StatusInternalServerError, grpErr.Error())
			return
		}
	}
	s.recordArtifactAudit(r, authUser, "template.instantiate", tmpl.Type, created.Name, http.StatusCreated, auditStatusSuccess, start, "")
	writeJSON(w, http.StatusCreated, created)
}
