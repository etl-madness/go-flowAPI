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
			writeJSONError(w, http.StatusMethodNotAllowed, errMsgMethodNotAllowed)
			return false
		}
	} else if r.Method != http.MethodGet && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		writeJSONError(w, http.StatusMethodNotAllowed, errMsgMethodNotAllowed)
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

const (
	errMsgMethodNotAllowed = "Method not allowed"
	fmtMsgInvalidJSON      = "invalid JSON: %v"
	auditStatusSuccess     = "SUCCESS"
	auditStatusError       = "ERROR"
)

func canViewTelemetryForPipelinePath(snap *aclSnapshot, user *APIUser, filePath string, hasGlobalRead, strict bool) bool {
	pName := ExtractPipelineNameFromRunFilePath(filePath)
	if pName == "" {
		return hasGlobalRead
	}
	canRead, _ := snap.evaluate(user, pName, nil, ActionRead, strict)
	if canRead {
		return true
	}
	canExec, _ := snap.evaluate(user, pName, nil, ActionExecute, strict)
	return canExec
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
		if canViewTelemetryForPipelinePath(snap, user, run.FilePath, hasGlobalRead, strict) {
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
		if (!knownRun && hasGlobalRead) || (knownRun && canViewTelemetryForPipelinePath(snap, user, fp, hasGlobalRead, strict)) {
			filtered = append(filtered, ev)
		}
	}
	return filtered, nil
}

// =============================================================================
// Shared Named Group HTTP Handler Machinery (API Groups & Pipeline Groups)
// =============================================================================

type namedGroupHandlerSpec struct {
	pathPrefix         string
	memberSubPath      string
	auditPrefix        string
	auditType          string
	collectionKey      string
	memberListKey      string
	memberQueryKey     string
	removedGroupKey    string
	removedMemberKey   string
	missingMemberMsg   string
	missingDeleteMsg   string
	badSubPathMsg      string
	listGroups         func(ctx context.Context) (int, any, error)
	getGroup           func(ctx context.Context, name string) (string, []string, any, error)
	upsertFromBody     func(ctx context.Context, body io.Reader, overrideName string) (string, string, any, bool, error)
	deleteGroup        func(ctx context.Context, name string) error
	parseMemberTargets func(r *http.Request) ([]string, error)
	addMember          func(ctx context.Context, groupName, member string) error
	removeMember       func(ctx context.Context, groupName, member string) error
}

func decodeOptionalJSON(r *http.Request, dst any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func extractDeleteMemberTarget(queryVal string, extraParts []string) string {
	target := strings.TrimSpace(queryVal)
	if target != "" || len(extraParts) == 0 {
		return target
	}
	raw := strings.Join(extraParts, "/")
	if unescaped, err := url.PathUnescape(raw); err == nil {
		return strings.TrimSpace(unescaped)
	}
	return strings.TrimSpace(raw)
}

func (s *APIServer) serveNamedGroupCollection(w http.ResponseWriter, r *http.Request, spec namedGroupHandlerSpec) {
	start := time.Now().UTC()
	actor := authUsernameFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		count, items, err := spec.listGroups(r.Context())
		if err != nil {
			s.recordArtifactAudit(r, actor, spec.auditPrefix+".list", spec.auditType, "", http.StatusInternalServerError, auditStatusError, start, err.Error())
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, spec.auditPrefix+".list", spec.auditType, "", http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, map[string]any{"count": count, spec.collectionKey: items})

	case http.MethodPost:
		attemptedName, canonicalName, grpObj, badJSON, err := spec.upsertFromBody(r.Context(), r.Body, "")
		if badJSON {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf(fmtMsgInvalidJSON, err))
			return
		}
		if err != nil {
			s.recordArtifactAudit(r, actor, spec.auditPrefix+".upsert", spec.auditType, attemptedName, http.StatusBadRequest, auditStatusError, start, err.Error())
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, spec.auditPrefix+".upsert", spec.auditType, canonicalName, http.StatusCreated, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusCreated, grpObj)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, errMsgMethodNotAllowed)
	}
}

func (s *APIServer) serveNamedGroupMembers(w http.ResponseWriter, r *http.Request, groupName string, extraParts []string, start time.Time, spec namedGroupHandlerSpec) {
	actor := authUsernameFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		canonicalName, members, _, err := spec.getGroup(r.Context(), groupName)
		if err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, actor, spec.auditPrefix+".member.list", spec.auditType, groupName, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, spec.auditPrefix+".member.list", spec.auditType, canonicalName, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, map[string]any{"group": canonicalName, "count": len(members), spec.memberListKey: members})

	case http.MethodPost:
		targets, err := spec.parseMemberTargets(r)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf(fmtMsgInvalidJSON, err))
			return
		}
		if len(targets) == 0 {
			writeJSONError(w, http.StatusBadRequest, spec.missingMemberMsg)
			return
		}
		if err := s.addMembersWithAudit(w, r, groupName, targets, start, spec); err != nil {
			return
		}
		_, _, grpObj, _ := spec.getGroup(r.Context(), groupName)
		s.recordArtifactAudit(r, actor, spec.auditPrefix+".member.add", spec.auditType, groupName, http.StatusCreated, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusCreated, grpObj)

	case http.MethodDelete:
		target := extractDeleteMemberTarget(r.URL.Query().Get(spec.memberQueryKey), extraParts)
		if target == "" {
			writeJSONError(w, http.StatusBadRequest, spec.missingDeleteMsg)
			return
		}
		if err := spec.removeMember(r.Context(), groupName, target); err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, actor, spec.auditPrefix+".member.remove", spec.auditType, groupName, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, spec.auditPrefix+".member.remove", spec.auditType, groupName, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, map[string]any{"removed": true, spec.removedGroupKey: groupName, spec.removedMemberKey: target})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, errMsgMethodNotAllowed)
	}
}

func (s *APIServer) addMembersWithAudit(w http.ResponseWriter, r *http.Request, groupName string, targets []string, start time.Time, spec namedGroupHandlerSpec) error {
	actor := authUsernameFromRequest(r)
	for _, m := range targets {
		if err := spec.addMember(r.Context(), groupName, m); err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, ErrArtifactNotFound) {
				code = http.StatusNotFound
			}
			s.recordArtifactAudit(r, actor, spec.auditPrefix+".member.add", spec.auditType, groupName, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return err
		}
	}
	return nil
}

func (s *APIServer) serveNamedGroupByName(w http.ResponseWriter, r *http.Request, spec namedGroupHandlerSpec) {
	start := time.Now().UTC()
	actor := authUsernameFromRequest(r)

	rel := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, spec.pathPrefix), "/")
	if rel == "" {
		s.serveNamedGroupCollection(w, r, spec)
		return
	}

	parts := strings.Split(rel, "/")
	groupName, _ := url.PathUnescape(parts[0])
	groupName = strings.TrimSpace(groupName)

	if len(parts) >= 2 && strings.EqualFold(parts[1], spec.memberSubPath) {
		s.serveNamedGroupMembers(w, r, groupName, parts[2:], start, spec)
		return
	}
	if len(parts) != 1 {
		writeJSONError(w, http.StatusBadRequest, spec.badSubPathMsg)
		return
	}

	switch r.Method {
	case http.MethodGet:
		canonicalName, _, grpObj, err := spec.getGroup(r.Context(), groupName)
		if err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, actor, spec.auditPrefix+".read", spec.auditType, groupName, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, spec.auditPrefix+".read", spec.auditType, canonicalName, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, grpObj)

	case http.MethodPut:
		_, canonicalName, grpObj, badJSON, err := spec.upsertFromBody(r.Context(), r.Body, groupName)
		if badJSON {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf(fmtMsgInvalidJSON, err))
			return
		}
		if err != nil {
			s.recordArtifactAudit(r, actor, spec.auditPrefix+".update", spec.auditType, groupName, http.StatusBadRequest, auditStatusError, start, err.Error())
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, spec.auditPrefix+".update", spec.auditType, canonicalName, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, grpObj)

	case http.MethodDelete:
		if err := spec.deleteGroup(r.Context(), groupName); err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, actor, spec.auditPrefix+".delete", spec.auditType, groupName, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, spec.auditPrefix+".delete", spec.auditType, groupName, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "name": groupName})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, errMsgMethodNotAllowed)
	}
}

func (s *APIServer) apiGroupSpec() namedGroupHandlerSpec {
	return namedGroupHandlerSpec{
		pathPrefix:       "/api/v1/groups/",
		memberSubPath:    "members",
		auditPrefix:      "group",
		auditType:        "group",
		collectionKey:    "groups",
		memberListKey:    "members",
		memberQueryKey:   "username",
		removedGroupKey:  "group",
		removedMemberKey: "username",
		missingMemberMsg: "username is required",
		missingDeleteMsg: "username is required in path or ?username= query parameter",
		badSubPathMsg:    "expected /api/v1/groups/{name} or /api/v1/groups/{name}/members",
		listGroups: func(ctx context.Context) (int, any, error) {
			groups, err := s.ListAPIGroups(ctx)
			return len(groups), groups, err
		},
		getGroup: func(ctx context.Context, name string) (string, []string, any, error) {
			grp, err := s.GetAPIGroup(ctx, name)
			if err != nil {
				return "", nil, nil, err
			}
			return grp.Name, grp.Members, grp, nil
		},
		upsertFromBody: func(ctx context.Context, body io.Reader, overrideName string) (string, string, any, bool, error) {
			var payload struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				Members     []string `json:"members"`
			}
			if err := json.NewDecoder(body).Decode(&payload); err != nil {
				return "", "", nil, true, err
			}
			targetName := payload.Name
			if overrideName != "" {
				targetName = overrideName
			}
			grp, err := s.UpsertAPIGroup(ctx, targetName, payload.Description, payload.Members)
			if err != nil {
				return targetName, "", nil, false, err
			}
			return targetName, grp.Name, grp, false, nil
		},
		deleteGroup: s.DeleteAPIGroup,
		parseMemberTargets: func(r *http.Request) ([]string, error) {
			var body struct {
				Username  string   `json:"username"`
				Usernames []string `json:"usernames"`
				Members   []string `json:"members"`
			}
			if err := decodeOptionalJSON(r, &body); err != nil {
				return nil, err
			}
			combined := append(append([]string{}, body.Usernames...), body.Members...)
			return parseGroupListValues(body.Username, combined, r.URL.Query().Get("username")), nil
		},
		addMember:    s.AddUserToGroup,
		removeMember: s.RemoveUserFromGroup,
	}
}

func (s *APIServer) pipelineGroupSpec() namedGroupHandlerSpec {
	return namedGroupHandlerSpec{
		pathPrefix:       "/api/v1/pipeline-groups/",
		memberSubPath:    "pipelines",
		auditPrefix:      "pipeline_group",
		auditType:        "pipeline_group",
		collectionKey:    "pipeline_groups",
		memberListKey:    "pipelines",
		memberQueryKey:   "pipeline",
		removedGroupKey:  "pipeline_group",
		removedMemberKey: "pipeline",
		missingMemberMsg: "pipeline name is required",
		missingDeleteMsg: "pipeline name is required in path or ?pipeline= query parameter",
		badSubPathMsg:    "expected /api/v1/pipeline-groups/{name} or /api/v1/pipeline-groups/{name}/pipelines",
		listGroups: func(ctx context.Context) (int, any, error) {
			groups, err := s.ListPipelineGroups(ctx)
			return len(groups), groups, err
		},
		getGroup: func(ctx context.Context, name string) (string, []string, any, error) {
			grp, err := s.GetPipelineGroup(ctx, name)
			if err != nil {
				return "", nil, nil, err
			}
			return grp.Name, grp.Pipelines, grp, nil
		},
		upsertFromBody: func(ctx context.Context, body io.Reader, overrideName string) (string, string, any, bool, error) {
			var payload struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				NamePattern string   `json:"name_pattern"`
				Pipelines   []string `json:"pipelines"`
			}
			if err := json.NewDecoder(body).Decode(&payload); err != nil {
				return "", "", nil, true, err
			}
			targetName := payload.Name
			if overrideName != "" {
				targetName = overrideName
			}
			grp, err := s.UpsertPipelineGroup(ctx, targetName, payload.Description, payload.NamePattern, payload.Pipelines)
			if err != nil {
				return targetName, "", nil, false, err
			}
			return targetName, grp.Name, grp, false, nil
		},
		deleteGroup: s.DeletePipelineGroup,
		parseMemberTargets: func(r *http.Request) ([]string, error) {
			var body struct {
				Pipeline     string   `json:"pipeline"`
				PipelineName string   `json:"pipeline_name"`
				Pipelines    []string `json:"pipelines"`
			}
			if err := decodeOptionalJSON(r, &body); err != nil {
				return nil, err
			}
			single := strings.TrimSpace(body.Pipeline)
			if single == "" {
				single = strings.TrimSpace(body.PipelineName)
			}
			return parseGroupListValues(single, body.Pipelines, r.URL.Query().Get("pipeline")), nil
		},
		addMember:    s.AddPipelineToGroup,
		removeMember: s.RemovePipelineFromGroup,
	}
}

// =============================================================================
// 1. User Groups Endpoints (/api/v1/groups[/{name}[/members[/{username}]]])
// =============================================================================

func (s *APIServer) handleAPIGroups(w http.ResponseWriter, r *http.Request) {
	s.serveNamedGroupCollection(w, r, s.apiGroupSpec())
}

func (s *APIServer) handleAPIGroupByName(w http.ResponseWriter, r *http.Request) {
	s.serveNamedGroupByName(w, r, s.apiGroupSpec())
}

// =============================================================================
// 2. Pipeline Groups Endpoints (/api/v1/pipeline-groups[/{name}[/pipelines[/{pipeline}]]])
// =============================================================================

func (s *APIServer) handlePipelineGroups(w http.ResponseWriter, r *http.Request) {
	s.serveNamedGroupCollection(w, r, s.pipelineGroupSpec())
}

func (s *APIServer) handlePipelineGroupByName(w http.ResponseWriter, r *http.Request) {
	s.serveNamedGroupByName(w, r, s.pipelineGroupSpec())
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
			s.recordArtifactAudit(r, actor, "acl.list", "acl", "", http.StatusInternalServerError, auditStatusError, start, err.Error())
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "acl.list", "acl", "", http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, map[string]any{"count": len(rules), "acls": rules})

	case http.MethodPost:
		var rule PipelineACL
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf(fmtMsgInvalidJSON, err))
			return
		}
		saved, err := s.UpsertPipelineACL(r.Context(), rule)
		if err != nil {
			s.recordArtifactAudit(r, actor, "acl.upsert", "acl", rule.TargetName, http.StatusBadRequest, auditStatusError, start, err.Error())
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		auditLabel := fmt.Sprintf("%s:%s->%s:%s(%s)", saved.PrincipalType, saved.PrincipalName, saved.TargetType, saved.TargetName, saved.Effect)
		s.recordArtifactAudit(r, actor, "acl.upsert", "acl", auditLabel, http.StatusCreated, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusCreated, saved)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, errMsgMethodNotAllowed)
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
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf(fmtMsgInvalidJSON, err))
			return
		}
		updated, err := s.UpdatePipelineACLByID(r.Context(), idVal, rule)
		if err != nil {
			code := artifactErrorStatusCode(err)
			if code == http.StatusInternalServerError {
				code = http.StatusBadRequest
			}
			s.recordArtifactAudit(r, actor, "acl.update", "acl", idLabel, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "acl.update", "acl", idLabel, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, updated)

	case http.MethodDelete:
		if err := s.DeletePipelineACLByID(r.Context(), idVal); err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, actor, "acl.delete", "acl", idLabel, code, auditStatusError, start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, actor, "acl.delete", "acl", idLabel, http.StatusOK, auditStatusSuccess, start, "")
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": idVal})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, errMsgMethodNotAllowed)
	}
}

func (s *APIServer) handleEffectivePermissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, errMsgMethodNotAllowed)
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
