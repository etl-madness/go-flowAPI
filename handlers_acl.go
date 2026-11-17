package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *APIServer) writeForbiddenAudit(w http.ResponseWriter, r *http.Request, authUser, artifactType, artifactName, errMsg string, start time.Time) {
	s.logAPIEvent(r.Context(), APIAuditEvent{
		OccurredAt:   start,
		EventType:    "auth.forbidden",
		HTTPMethod:   r.Method,
		Path:         r.URL.Path,
		ArtifactType: artifactType,
		ArtifactName: artifactName,
		AuthUser:     authUser,
		RemoteAddr:   r.RemoteAddr,
		StatusCode:   http.StatusForbidden,
		Status:       "FORBIDDEN",
		DurationMs:   time.Since(start).Milliseconds(),
		ErrorMessage: errMsg,
	})
	writeJSONError(w, http.StatusForbidden, errMsg)
}

func (s *APIServer) authorizeNonPipelineArtifact(w http.ResponseWriter, r *http.Request, user *APIUser, artifactType, artifactName, requiredPerm string, start time.Time) bool {
	if user != nil && user.HasPermission(requiredPerm) {
		return true
	}
	uname := ""
	uPerm := ""
	if user != nil {
		uname = user.Username
		uPerm = user.Permission
	}
	msg := fmt.Sprintf("Insufficient permissions: %s endpoint requires %q permission (user %q has %q)", artifactType, requiredPerm, uname, uPerm)
	s.writeForbiddenAudit(w, r, uname, artifactType, artifactName, msg, start)
	return false
}

func (s *APIServer) authorizePipelineOrWrite403(w http.ResponseWriter, r *http.Request, user *APIUser, pipelineName string, candidateGroups []string, action string, start time.Time) bool {
	allowed, reason, err := s.EvaluatePipelineAccess(r.Context(), user, pipelineName, candidateGroups, action)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !allowed {
		uname := ""
		if user != nil {
			uname = user.Username
		}
		s.writeForbiddenAudit(w, r, uname, "pipeline", pipelineName, fmt.Sprintf("Forbidden (%s): %s", action, reason), start)
		return false
	}
	return true
}

func parseGroupListValues(singleVal string, listVals []string, queryVals ...string) []string {
	seen := make(map[string]struct{})
	var out []string
	addSplit := func(raw string) {
		for _, part := range strings.Split(raw, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			k := strings.ToLower(trimmed)
			if _, exists := seen[k]; !exists {
				seen[k] = struct{}{}
				out = append(out, trimmed)
			}
		}
	}
	addSplit(singleVal)
	for _, v := range listVals {
		addSplit(v)
	}
	for _, q := range queryVals {
		addSplit(q)
	}
	return out
}

func (s *APIServer) validatePipelineGroupsExist(ctx context.Context, groups []string) error {
	for _, g := range groups {
		if _, err := s.GetPipelineGroup(ctx, g); err != nil {
			if errors.Is(err, ErrArtifactNotFound) {
				return fmt.Errorf("pipeline group %q does not exist", g)
			}
			return err
		}
	}
	return nil
}

func (s *APIServer) assignPipelineToGroups(ctx context.Context, pipelineName string, groups []string) error {
	for _, g := range groups {
		if err := s.AddPipelineToGroup(ctx, g, pipelineName); err != nil {
			return err
		}
	}
	return nil
}

func (s *APIServer) authorizeExistingPipelineItem(
	w http.ResponseWriter,
	r *http.Request,
	user *APIUser,
	byID bool,
	idVal int64,
	name string,
	isExport bool,
	start time.Time,
) bool {
	if isExport {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return false
		}
	} else if r.Method != http.MethodGet && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return false
	}

	reqAction := ActionRead
	auditEvt := "artifact.read"
	if isExport {
		auditEvt = "artifact.export"
	} else if r.Method == http.MethodPut {
		reqAction = ActionUpdate
		auditEvt = "artifact.update"
	} else if r.Method == http.MethodDelete {
		reqAction = ActionDelete
		auditEvt = "artifact.delete"
	}

	authUser := ""
	if user != nil {
		authUser = user.Username
	}
	auditTarget := name
	var existing *ArtifactRecord
	var err error
	if byID {
		auditTarget = fmt.Sprintf("id:%d", idVal)
		existing, err = s.GetArtifactByID(r.Context(), "pipeline", idVal, false, "")
	} else {
		existing, err = s.GetArtifact(r.Context(), "pipeline", name, false, "")
	}

	if errors.Is(err, ErrArtifactNotFound) {
		probeName := name
		if byID {
			probeName = ""
		}
		allowedIfExisted, reason, _ := s.EvaluatePipelineAccess(r.Context(), user, probeName, nil, reqAction)
		if allowedIfExisted {
			s.recordArtifactAudit(r, authUser, auditEvt, "pipeline", auditTarget, http.StatusNotFound, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusNotFound, err.Error())
			return false
		}
		s.writeForbiddenAudit(w, r, authUser, "pipeline", auditTarget, fmt.Sprintf("Forbidden (%s): %s", reqAction, reason), start)
		return false
	}
	if err != nil {
		s.recordArtifactAudit(r, authUser, auditEvt, "pipeline", auditTarget, http.StatusInternalServerError, "ERROR", start, err.Error())
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return false
	}

	return s.authorizePipelineOrWrite403(w, r, user, existing.Name, nil, reqAction, start)
}

func (s *APIServer) filterRunsForUser(ctx context.Context, user *APIUser, runs []RunSummaryRecord) ([]RunSummaryRecord, error) {
	if user == nil || !user.IsActive {
		return []RunSummaryRecord{}, nil
	}
	if user.HasPermission(PermAll) {
		return runs, nil
	}
	snap, err := s.loadACLSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	strict := s.effectiveStrictForSnapshot(snap)
	hasGlobalRead := !strict && user.HasPermission(PermReadOnly)
	if hasGlobalRead && len(snap.acls) == 0 {
		return runs, nil
	}

	filtered := make([]RunSummaryRecord, 0, len(runs))
	for _, run := range runs {
		pName := ExtractPipelineNameFromRunFilePath(run.FilePath)
		if pName == "" {
			if hasGlobalRead {
				filtered = append(filtered, run)
			}
			continue
		}
		canRead, _ := snap.evaluate(user, pName, nil, ActionRead, strict)
		canExec, _ := snap.evaluate(user, pName, nil, ActionExecute, strict)
		if canRead || canExec {
			filtered = append(filtered, run)
		}
	}
	return filtered, nil
}

func (s *APIServer) filterEventsForUser(ctx context.Context, user *APIUser, events []PipelineEventRecord) ([]PipelineEventRecord, error) {
	if user == nil || !user.IsActive {
		return []PipelineEventRecord{}, nil
	}
	if user.HasPermission(PermAll) || len(events) == 0 {
		return events, nil
	}
	snap, err := s.loadACLSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	strict := s.effectiveStrictForSnapshot(snap)
	hasGlobalRead := !strict && user.HasPermission(PermReadOnly)
	if hasGlobalRead && len(snap.acls) == 0 {
		return events, nil
	}

	runs, runErr := s.QueryPipelineRuns(ctx, "", 1000)
	if runErr != nil {
		if hasGlobalRead {
			return events, nil
		}
		return []PipelineEventRecord{}, nil
	}
	filePathByRunID := make(map[string]string, len(runs))
	for _, r := range runs {
		filePathByRunID[r.RunID] = r.FilePath
	}

	filtered := make([]PipelineEventRecord, 0, len(events))
	for _, ev := range events {
		fp, knownRun := filePathByRunID[ev.RunID]
		if !knownRun {
			if hasGlobalRead {
				filtered = append(filtered, ev)
			}
			continue
		}
		pName := ExtractPipelineNameFromRunFilePath(fp)
		if pName == "" {
			if hasGlobalRead {
				filtered = append(filtered, ev)
			}
			continue
		}
		canRead, _ := snap.evaluate(user, pName, nil, ActionRead, strict)
		canExec, _ := snap.evaluate(user, pName, nil, ActionExecute, strict)
		if canRead || canExec {
			filtered = append(filtered, ev)
		}
	}
	return filtered, nil
}

// =============================================================================
// 1. User Groups Endpoints (/api/v1/groups[/{name}[/members[/{username}]]])
// =============================================================================

func (s *APIServer) handleAPIGroups(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	actor := authUsernameFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		groups, err := s.ListAPIGroups(r.Context())
		if err != nil {
			s.recordArtifactAudit(r, actor, "group.list", "group", "", http.StatusInternalServerError, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "group.list", "group", "", http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, map[string]any{"count": len(groups), "groups": groups})

	case http.MethodPost:
		var body struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Members     []string `json:"members"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		grp, err := s.UpsertAPIGroup(r.Context(), body.Name, body.Description, body.Members)
		if err != nil {
			s.recordArtifactAudit(r, actor, "group.upsert", "group", body.Name, http.StatusBadRequest, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "group.upsert", "group", grp.Name, http.StatusCreated, "SUCCESS", start, "")
		writeJSON(w, http.StatusCreated, grp)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *APIServer) handleAPIGroupByName(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	actor := authUsernameFromRequest(r)

	rel := strings.TrimPrefix(r.URL.Path, "/api/v1/groups/")
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		s.handleAPIGroups(w, r)
		return
	}

	parts := strings.Split(rel, "/")
	groupName, _ := url.PathUnescape(parts[0])
	groupName = strings.TrimSpace(groupName)

	// Sub-route: /api/v1/groups/{name}/members[/{username}]
	if len(parts) >= 2 && strings.EqualFold(parts[1], "members") {
		switch r.Method {
		case http.MethodGet:
			grp, err := s.GetAPIGroup(r.Context(), groupName)
			if err != nil {
				code := artifactErrorStatusCode(err)
				s.recordArtifactAudit(r, actor, "group.member.list", "group", groupName, code, "ERROR", start, err.Error())
				writeJSONError(w, code, err.Error())
				return
			}
			s.recordArtifactAudit(r, actor, "group.member.list", "group", grp.Name, http.StatusOK, "SUCCESS", start, "")
			writeJSON(w, http.StatusOK, map[string]any{"group": grp.Name, "count": len(grp.Members), "members": grp.Members})

		case http.MethodPost:
			var body struct {
				Username  string   `json:"username"`
				Usernames []string `json:"usernames"`
				Members   []string `json:"members"`
			}
			if r.Body != nil && r.ContentLength != 0 {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
					writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
					return
				}
			}
			combinedList := append([]string{}, body.Usernames...)
			combinedList = append(combinedList, body.Members...)
			targets := parseGroupListValues(body.Username, combinedList, r.URL.Query().Get("username"))
			if len(targets) == 0 {
				writeJSONError(w, http.StatusBadRequest, "username is required")
				return
			}
			for _, u := range targets {
				if err := s.AddUserToGroup(r.Context(), groupName, u); err != nil {
					code := http.StatusBadRequest
					if errors.Is(err, ErrArtifactNotFound) {
						code = http.StatusNotFound
					}
					s.recordArtifactAudit(r, actor, "group.member.add", "group", groupName, code, "ERROR", start, err.Error())
					writeJSONError(w, code, err.Error())
					return
				}
			}
			grp, _ := s.GetAPIGroup(r.Context(), groupName)
			s.recordArtifactAudit(r, actor, "group.member.add", "group", groupName, http.StatusCreated, "SUCCESS", start, "")
			writeJSON(w, http.StatusCreated, grp)

		case http.MethodDelete:
			targetUser := strings.TrimSpace(r.URL.Query().Get("username"))
			if targetUser == "" && len(parts) >= 3 {
				rawMember := strings.Join(parts[2:], "/")
				if unescaped, err := url.PathUnescape(rawMember); err == nil {
					targetUser = strings.TrimSpace(unescaped)
				} else {
					targetUser = strings.TrimSpace(rawMember)
				}
			}
			if targetUser == "" {
				writeJSONError(w, http.StatusBadRequest, "username is required in path or ?username= query parameter")
				return
			}
			if err := s.RemoveUserFromGroup(r.Context(), groupName, targetUser); err != nil {
				code := artifactErrorStatusCode(err)
				s.recordArtifactAudit(r, actor, "group.member.remove", "group", groupName, code, "ERROR", start, err.Error())
				writeJSONError(w, code, err.Error())
				return
			}
			s.recordArtifactAudit(r, actor, "group.member.remove", "group", groupName, http.StatusOK, "SUCCESS", start, "")
			writeJSON(w, http.StatusOK, map[string]any{"removed": true, "group": groupName, "username": targetUser})

		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	if len(parts) != 1 {
		writeJSONError(w, http.StatusBadRequest, "expected /api/v1/groups/{name} or /api/v1/groups/{name}/members")
		return
	}

	switch r.Method {
	case http.MethodGet:
		grp, err := s.GetAPIGroup(r.Context(), groupName)
		if err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, actor, "group.read", "group", groupName, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "group.read", "group", grp.Name, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, grp)

	case http.MethodPut:
		var body struct {
			Description string   `json:"description"`
			Members     []string `json:"members"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		grp, err := s.UpsertAPIGroup(r.Context(), groupName, body.Description, body.Members)
		if err != nil {
			s.recordArtifactAudit(r, actor, "group.update", "group", groupName, http.StatusBadRequest, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "group.update", "group", grp.Name, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, grp)

	case http.MethodDelete:
		if err := s.DeleteAPIGroup(r.Context(), groupName); err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, actor, "group.delete", "group", groupName, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "group.delete", "group", groupName, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "name": groupName})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// =============================================================================
// 2. Pipeline Groups Endpoints (/api/v1/pipeline-groups[/{name}[/pipelines[/{pipeline}]]])
// =============================================================================

func (s *APIServer) handlePipelineGroups(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	actor := authUsernameFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		groups, err := s.ListPipelineGroups(r.Context())
		if err != nil {
			s.recordArtifactAudit(r, actor, "pipeline_group.list", "pipeline_group", "", http.StatusInternalServerError, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "pipeline_group.list", "pipeline_group", "", http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, map[string]any{"count": len(groups), "pipeline_groups": groups})

	case http.MethodPost:
		var body struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			NamePattern string   `json:"name_pattern"`
			Pipelines   []string `json:"pipelines"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		grp, err := s.UpsertPipelineGroup(r.Context(), body.Name, body.Description, body.NamePattern, body.Pipelines)
		if err != nil {
			s.recordArtifactAudit(r, actor, "pipeline_group.upsert", "pipeline_group", body.Name, http.StatusBadRequest, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "pipeline_group.upsert", "pipeline_group", grp.Name, http.StatusCreated, "SUCCESS", start, "")
		writeJSON(w, http.StatusCreated, grp)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *APIServer) handlePipelineGroupByName(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	actor := authUsernameFromRequest(r)

	rel := strings.TrimPrefix(r.URL.Path, "/api/v1/pipeline-groups/")
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		s.handlePipelineGroups(w, r)
		return
	}

	parts := strings.Split(rel, "/")
	groupName, _ := url.PathUnescape(parts[0])
	groupName = strings.TrimSpace(groupName)

	// Sub-route: /api/v1/pipeline-groups/{name}/pipelines[/{pipeline}]
	if len(parts) >= 2 && strings.EqualFold(parts[1], "pipelines") {
		switch r.Method {
		case http.MethodGet:
			grp, err := s.GetPipelineGroup(r.Context(), groupName)
			if err != nil {
				code := artifactErrorStatusCode(err)
				s.recordArtifactAudit(r, actor, "pipeline_group.member.list", "pipeline_group", groupName, code, "ERROR", start, err.Error())
				writeJSONError(w, code, err.Error())
				return
			}
			s.recordArtifactAudit(r, actor, "pipeline_group.member.list", "pipeline_group", grp.Name, http.StatusOK, "SUCCESS", start, "")
			writeJSON(w, http.StatusOK, map[string]any{"group": grp.Name, "count": len(grp.Pipelines), "pipelines": grp.Pipelines})

		case http.MethodPost:
			var body struct {
				Pipeline     string   `json:"pipeline"`
				PipelineName string   `json:"pipeline_name"`
				Pipelines    []string `json:"pipelines"`
			}
			if r.Body != nil && r.ContentLength != 0 {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
					writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
					return
				}
			}
			singlePipe := strings.TrimSpace(body.Pipeline)
			if singlePipe == "" {
				singlePipe = strings.TrimSpace(body.PipelineName)
			}
			targets := parseGroupListValues(singlePipe, body.Pipelines, r.URL.Query().Get("pipeline"))
			if len(targets) == 0 {
				writeJSONError(w, http.StatusBadRequest, "pipeline name is required")
				return
			}
			for _, p := range targets {
				if err := s.AddPipelineToGroup(r.Context(), groupName, p); err != nil {
					code := http.StatusBadRequest
					if errors.Is(err, ErrArtifactNotFound) {
						code = http.StatusNotFound
					}
					s.recordArtifactAudit(r, actor, "pipeline_group.member.add", "pipeline_group", groupName, code, "ERROR", start, err.Error())
					writeJSONError(w, code, err.Error())
					return
				}
			}
			grp, _ := s.GetPipelineGroup(r.Context(), groupName)
			s.recordArtifactAudit(r, actor, "pipeline_group.member.add", "pipeline_group", groupName, http.StatusCreated, "SUCCESS", start, "")
			writeJSON(w, http.StatusCreated, grp)

		case http.MethodDelete:
			targetPipe := strings.TrimSpace(r.URL.Query().Get("pipeline"))
			if targetPipe == "" && len(parts) >= 3 {
				rawPipe := strings.Join(parts[2:], "/")
				if unescaped, err := url.PathUnescape(rawPipe); err == nil {
					targetPipe = strings.TrimSpace(unescaped)
				} else {
					targetPipe = strings.TrimSpace(rawPipe)
				}
			}
			if targetPipe == "" {
				writeJSONError(w, http.StatusBadRequest, "pipeline name is required in path or ?pipeline= query parameter")
				return
			}
			if err := s.RemovePipelineFromGroup(r.Context(), groupName, targetPipe); err != nil {
				code := artifactErrorStatusCode(err)
				s.recordArtifactAudit(r, actor, "pipeline_group.member.remove", "pipeline_group", groupName, code, "ERROR", start, err.Error())
				writeJSONError(w, code, err.Error())
				return
			}
			s.recordArtifactAudit(r, actor, "pipeline_group.member.remove", "pipeline_group", groupName, http.StatusOK, "SUCCESS", start, "")
			writeJSON(w, http.StatusOK, map[string]any{"removed": true, "pipeline_group": groupName, "pipeline": targetPipe})

		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	if len(parts) != 1 {
		writeJSONError(w, http.StatusBadRequest, "expected /api/v1/pipeline-groups/{name} or /api/v1/pipeline-groups/{name}/pipelines")
		return
	}

	switch r.Method {
	case http.MethodGet:
		grp, err := s.GetPipelineGroup(r.Context(), groupName)
		if err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, actor, "pipeline_group.read", "pipeline_group", groupName, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "pipeline_group.read", "pipeline_group", grp.Name, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, grp)

	case http.MethodPut:
		var body struct {
			Description string   `json:"description"`
			NamePattern string   `json:"name_pattern"`
			Pipelines   []string `json:"pipelines"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		grp, err := s.UpsertPipelineGroup(r.Context(), groupName, body.Description, body.NamePattern, body.Pipelines)
		if err != nil {
			s.recordArtifactAudit(r, actor, "pipeline_group.update", "pipeline_group", groupName, http.StatusBadRequest, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "pipeline_group.update", "pipeline_group", grp.Name, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, grp)

	case http.MethodDelete:
		if err := s.DeletePipelineGroup(r.Context(), groupName); err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, actor, "pipeline_group.delete", "pipeline_group", groupName, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "pipeline_group.delete", "pipeline_group", groupName, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "name": groupName})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// =============================================================================
// 3. Pipeline ACLs Endpoints (/api/v1/acls[/{id}]) & Effective Permissions
// =============================================================================

func (s *APIServer) handlePipelineACLs(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	actor := authUsernameFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		rules, err := s.ListPipelineACLs(
			r.Context(),
			q.Get("principal_type"),
			q.Get("principal_name"),
			q.Get("target_type"),
			q.Get("target_name"),
		)
		if err != nil {
			s.recordArtifactAudit(r, actor, "acl.list", "acl", "", http.StatusInternalServerError, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "acl.list", "acl", "", http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, map[string]any{"count": len(rules), "acls": rules})

	case http.MethodPost:
		var rule PipelineACL
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		saved, err := s.UpsertPipelineACL(r.Context(), rule)
		if err != nil {
			s.recordArtifactAudit(r, actor, "acl.upsert", "acl", rule.TargetName, http.StatusBadRequest, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		auditLabel := fmt.Sprintf("%s:%s->%s:%s(%s)", saved.PrincipalType, saved.PrincipalName, saved.TargetType, saved.TargetName, saved.Effect)
		s.recordArtifactAudit(r, actor, "acl.upsert", "acl", auditLabel, http.StatusCreated, "SUCCESS", start, "")
		writeJSON(w, http.StatusCreated, saved)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *APIServer) handlePipelineACLByID(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	actor := authUsernameFromRequest(r)

	rel := strings.TrimPrefix(r.URL.Path, "/api/v1/acls/")
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		s.handlePipelineACLs(w, r)
		return
	}

	idVal, err := strconv.ParseInt(strings.TrimSpace(rel), 10, 64)
	if err != nil || idVal <= 0 {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid ACL ID %q: must be a positive integer", rel))
		return
	}
	idLabel := fmt.Sprintf("id:%d", idVal)

	switch r.Method {
	case http.MethodPut:
		var rule PipelineACL
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		updated, err := s.UpdatePipelineACLByID(r.Context(), idVal, rule)
		if err != nil {
			code := artifactErrorStatusCode(err)
			if code == http.StatusInternalServerError {
				code = http.StatusBadRequest
			}
			s.recordArtifactAudit(r, actor, "acl.update", "acl", idLabel, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "acl.update", "acl", idLabel, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, updated)

	case http.MethodDelete:
		if err := s.DeletePipelineACLByID(r.Context(), idVal); err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, actor, "acl.delete", "acl", idLabel, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "acl.delete", "acl", idLabel, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": idVal})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *APIServer) handleEffectivePermissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	start := time.Now().UTC()
	caller := UserFromContext(r.Context())
	if caller == nil {
		writeJSONError(w, http.StatusUnauthorized, "Authentication credentials required")
		return
	}

	targetUser := caller
	qUser := strings.TrimSpace(r.URL.Query().Get("username"))
	qGroupsRaw := strings.TrimSpace(r.URL.Query().Get("groups"))

	if (qUser != "" && !strings.EqualFold(qUser, caller.Username)) || qGroupsRaw != "" {
		if !caller.HasPermission(PermAll) {
			s.writeForbiddenAudit(w, r, caller.Username, "acl", qUser, "Inspecting another principal's effective permissions requires 'all' permission", start)
			return
		}
		inspectName := qUser
		if inspectName == "" {
			inspectName = caller.Username
		}
		simPerm := PermScoped
		simActive := true
		if users, err := s.ListAPIUsers(r.Context()); err == nil {
			for _, u := range users {
				if strings.EqualFold(u.Username, inspectName) {
					simPerm = u.Permission
					simActive = u.IsActive
					break
				}
			}
		}
		var mergedGroups []string
		if dbGrps, err := s.ListGroupsForUser(r.Context(), inspectName); err == nil {
			mergedGroups = append(mergedGroups, dbGrps...)
		}
		mergedGroups = parseGroupListValues("", mergedGroups, qGroupsRaw)
		targetUser = &APIUser{
			Username:   inspectName,
			Permission: simPerm,
			Groups:     mergedGroups,
			IsActive:   simActive,
		}
	}

	includeAllPipelines := caller.HasPermission(PermAll)
	summary, err := s.ComputeEffectivePermissionsSummary(r.Context(), targetUser, includeAllPipelines)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.recordArtifactAudit(r, caller.Username, "acl.effective", "acl", targetUser.Username, http.StatusOK, "SUCCESS", start, "")
	writeJSON(w, http.StatusOK, summary)
}
