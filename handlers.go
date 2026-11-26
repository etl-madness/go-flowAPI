package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/etl-madness/flow"
)

// APIServer manages the HTTP router, database connection pool, authentication, CRUD store, and executor.
type APIServer struct {
	cfg     *ServerConfig
	db      *sql.DB
	dialect string
}

// NewAPIServer creates a new APIServer backed by the provided database connection and configuration.
func NewAPIServer(cfg *ServerConfig, db *sql.DB, dialect string) *APIServer {
	return &APIServer{
		cfg:     cfg,
		db:      db,
		dialect: dialect,
	}
}

// Router is an alias for Handler().
func (s *APIServer) Router() http.Handler {
	return s.Handler()
}

// Handler builds the HTTP router with BasicAuth and role-based permissioning on all API routes.
func (s *APIServer) Handler() http.Handler {
	mux := http.NewServeMux()

	// Swagger UI & OpenAPI 3.0 Spec (publicly accessible so users can authorize via BasicAuth in the UI)
	mux.HandleFunc("/swagger", s.handleSwaggerUI)
	mux.HandleFunc("/swagger/", s.handleSwaggerUI)
	mux.HandleFunc("/docs", s.handleSwaggerUI)
	mux.HandleFunc("/docs/", s.handleSwaggerUI)
	mux.HandleFunc("/api/v1/openapi.json", s.handleOpenAPISpec)

	// Health check (publicly accessible; does not require BasicAuth)
	mux.HandleFunc("/api/v1/health", s.handleHealth)

	// Artifact CRUD & File Upload Routes (/api/v1/artifacts/{type} and /api/v1/upload/{type})
	// Authenticated via RequireAuth; granular per-pipeline/pipeline-group ACLs are enforced inside handlers.
	mux.HandleFunc("/api/v1/artifacts/", s.handleArtifactsRouter)
	mux.HandleFunc("/api/v1/upload/", s.RequireAuth(s.handleUploadRouter))

	// Execution & Validation Routes (granularity enforced per resolved target pipeline via AuthorizeExecutionRequest)
	mux.HandleFunc("/api/v1/execute", s.RequireAuth(s.handleExecuteSync))
	mux.HandleFunc("/api/v1/execute/", s.RequireAuth(s.handleExecuteRouter))
	mux.HandleFunc("/api/v1/execution", s.RequireAuth(s.handleExecuteSync))
	mux.HandleFunc("/api/v1/execution/", s.RequireAuth(s.handleExecuteRouter))
	mux.HandleFunc("/api/v1/validate", s.RequireAuth(s.handleValidate))
	mux.HandleFunc("/api/v1/validate/", s.RequireAuth(s.handleValidateRouter))

	// Telemetry & Audit Event Log Queries
	mux.HandleFunc("/api/v1/logs/runs", s.RequireAuth(s.handleListRuns))
	mux.HandleFunc("/api/v1/runs", s.RequireAuth(s.handleListRuns))
	mux.HandleFunc("/api/v1/logs/events", s.RequireAuth(s.handleListEvents))
	mux.HandleFunc("/api/v1/events", s.RequireAuth(s.handleListEvents))
	mux.HandleFunc("/api/v1/logs/api-events", s.RequirePermission(PermReadOnly, s.handleListAPIEvents))
	mux.HandleFunc("/api/v1/audit", s.RequirePermission(PermReadOnly, s.handleListAPIEvents))

	// Database Connectivity Test, User Management, Groups & Granular Pipeline ACLs (requires "all")
	mux.HandleFunc("/api/v1/databases/test", s.RequirePermission(PermAll, s.handleTestDatabase))
	mux.HandleFunc("/api/v1/db/test", s.RequirePermission(PermAll, s.handleTestDatabase))
	mux.HandleFunc("/api/v1/users", s.RequirePermission(PermAll, s.handleUsers))
	mux.HandleFunc("/api/v1/users/", s.RequirePermission(PermAll, s.handleUserByName))
	mux.HandleFunc("/api/v1/groups", s.RequirePermission(PermAll, s.handleAPIGroups))
	mux.HandleFunc("/api/v1/groups/", s.RequirePermission(PermAll, s.handleAPIGroupByName))
	mux.HandleFunc("/api/v1/pipeline-groups", s.RequirePermission(PermAll, s.handlePipelineGroups))
	mux.HandleFunc("/api/v1/pipeline-groups/", s.RequirePermission(PermAll, s.handlePipelineGroupByName))
	mux.HandleFunc("/api/v1/acls", s.RequirePermission(PermAll, s.handlePipelineACLs))
	mux.HandleFunc("/api/v1/acls/", s.RequirePermission(PermAll, s.handlePipelineACLByID))
	mux.HandleFunc("/api/v1/permissions/effective", s.RequireAuth(s.handleEffectivePermissions))
	mux.HandleFunc("/api/v1/me/permissions", s.RequireAuth(s.handleEffectivePermissions))

	// Cryptographic & Digital Signature Utility Routes (requires "read-only" or higher)
	mux.HandleFunc("/api/v1/crypto/", s.RequirePermission(PermReadOnly, s.handleCryptoRouter))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
}

func (s *APIServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	if user == nil {
		if authUser, _, hasCreds, err := s.AuthenticateRequest(r); hasCreds && err == nil {
			user = authUser
		}
	}
	username := ""
	perm := ""
	var userGroups []string
	if user != nil {
		username = user.Username
		perm = user.Permission
		userGroups = user.Groups
	}

	spid, dbUser, appName, dbServer, _ := FetchDatabaseSessionIdentityWithServer(r.Context(), s.db, s.dialect)
	s.logAPIEvent(r.Context(), APIAuditEvent{
		OccurredAt: start,
		EventType:  "system.health",
		HTTPMethod: r.Method,
		Path:       r.URL.Path,
		AuthUser:   username,
		RemoteAddr: r.RemoteAddr,
		StatusCode: http.StatusOK,
		Status:     "SUCCESS",
		DurationMs: time.Since(start).Milliseconds(),
	})

	respMap := map[string]any{
		"status":           "ok",
		"dialect":          s.dialect,
		"authenticated_as": username,
		"permission":       perm,
		"db_spid":          spid,
		"db_user":          dbUser,
		"db_server":        dbServer,
		"application_name": appName,
		"tables": map[string]string{
			"pipeline_content":       TablePipelineContent,
			"options_content":        TableOptionsContent,
			"config_content":         TableConfigContent,
			"pipeline_runs":          TablePipelineRuns,
			"pipeline_events":        TablePipelineEvents,
			"api_events":             TableAPIAuditEvents,
			"api_users":              TableAPIUsers,
			"api_groups":             TableAPIGroups,
			"api_user_groups":        TableAPIUserGroups,
			"pipeline_groups":        TablePipelineGroups,
			"pipeline_group_members": TablePipelineGroupMembers,
			"pipeline_acl":           TablePipelineACL,
		},
	}
	if len(userGroups) > 0 {
		respMap["groups"] = userGroups
	}
	writeJSON(w, http.StatusOK, respMap)
}

// handleArtifactsRouter authenticates the caller and dispatches /api/v1/artifacts/{type}[/{name}[/export]],
// enforcing granular user/group & pipeline/pipeline-group ACLs for pipelines and global RBAC for options/configs.
func (s *APIServer) handleArtifactsRouter(w http.ResponseWriter, r *http.Request) {
	s.RequireAuth(s.dispatchArtifactRequest)(w, r)
}

func (s *APIServer) handleUploadRouter(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	authUsername := ""
	if user != nil {
		authUsername = user.Username
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed for upload; use POST or PUT")
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/api/v1/upload/")
	rel = strings.TrimSuffix(rel, "/")
	parts := strings.Split(rel, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		writeJSONError(w, http.StatusBadRequest, "Artifact type is required (pipeline, options, or config)")
		return
	}
	_, _, canonicalType, err := ResolveArtifactTarget(parts[0])
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if canonicalType != "pipeline" {
		if !s.authorizeNonPipelineArtifact(w, r, user, canonicalType, "", PermUpload, start) {
			return
		}
	}
	s.handleArtifactUpload(w, r, canonicalType, authUsername, start)
}

type artifactPayload struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Group       string   `json:"group"`
	Groups      []string `json:"groups"`
	Content     string   `json:"content"`
	XMLContent  string   `json:"xml_content"`
	Encrypted   bool     `json:"encrypted"`
	Encrypt     bool     `json:"encrypt"`
	SecureKey   string   `json:"secure_key"`
	Upsert      bool     `json:"upsert"`
}

func (p *artifactPayload) resolvedContent() string {
	if p.Content != "" {
		return p.Content
	}
	return p.XMLContent
}

func (p *artifactPayload) resolvedEncrypted() bool {
	return p.Encrypted || p.Encrypt
}

func extractArtifactQuerySecret(r *http.Request, bodyKey string) (decrypt bool, secKey string) {
	q := r.URL.Query()
	decrypt = q.Get("decrypt") == "true" || q.Get("decrypt") == "1"
	secKey = strings.TrimSpace(bodyKey)
	if secKey == "" {
		secKey = strings.TrimSpace(q.Get("secure_key"))
		if secKey == "" {
			secKey = strings.TrimSpace(r.Header.Get("X-Flow-Secure-Key"))
		}
	}
	return decrypt, secKey
}

func artifactErrorStatusCode(err error) int {
	if errors.Is(err, ErrPipelineForbidden) {
		return http.StatusForbidden
	}
	if errors.Is(err, ErrArtifactNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, ErrArtifactExists) {
		return http.StatusConflict
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "pipeline group ") && strings.Contains(strings.ToLower(err.Error()), "does not exist") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func (s *APIServer) serveArtifactExport(w http.ResponseWriter, r *http.Request, authUsername, artifactType, auditTarget string, start time.Time, fetch func(decrypt bool, secKey string) (*ArtifactRecord, error)) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	decrypt, secKey := extractArtifactQuerySecret(r, "")
	rec, err := fetch(decrypt, secKey)
	if err != nil {
		code := artifactErrorStatusCode(err)
		s.recordArtifactAudit(r, authUsername, "artifact.export", artifactType, auditTarget, code, "ERROR", start, err.Error())
		writeJSONError(w, code, err.Error())
		return
	}
	s.recordArtifactAudit(r, authUsername, "artifact.export", artifactType, rec.Name, http.StatusOK, "SUCCESS", start, "")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rec.Name+".xml"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(rec.Content))
}

func (s *APIServer) serveArtifactItemCRUD(
	w http.ResponseWriter,
	r *http.Request,
	authUsername, artifactType, auditTarget string,
	start time.Time,
	getFn func(decrypt bool, secKey string) (*ArtifactRecord, error),
	updateFn func(desc, content string, enc bool, secKey string, groups []string) (*ArtifactRecord, error),
	deleteFn func() error,
	deleteResp map[string]any,
) {
	switch r.Method {
	case http.MethodGet:
		decrypt, secKey := extractArtifactQuerySecret(r, "")
		rec, err := getFn(decrypt, secKey)
		if err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, authUsername, "artifact.read", artifactType, auditTarget, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUsername, "artifact.read", artifactType, rec.Name, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, rec)

	case http.MethodPut:
		var body artifactPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
			return
		}
		requestedGroups := parseGroupListValues(body.Group, body.Groups, r.URL.Query().Get("group"), strings.Join(r.URL.Query()["groups"], ","))
		_, secKey := extractArtifactQuerySecret(r, body.SecureKey)
		rec, err := updateFn(body.Description, body.resolvedContent(), body.resolvedEncrypted(), secKey, requestedGroups)
		if err != nil {
			code := artifactErrorStatusCode(err)
			if errors.Is(err, ErrPipelineForbidden) {
				s.writeForbiddenAudit(w, r, authUsername, artifactType, auditTarget, err.Error(), start)
				return
			}
			s.recordArtifactAudit(r, authUsername, "artifact.update", artifactType, auditTarget, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUsername, "artifact.update", artifactType, rec.Name, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, rec)

	case http.MethodDelete:
		if err := deleteFn(); err != nil {
			code := artifactErrorStatusCode(err)
			s.recordArtifactAudit(r, authUsername, "artifact.delete", artifactType, auditTarget, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUsername, "artifact.delete", artifactType, auditTarget, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, deleteResp)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

type pipelineWriteAuth struct {
	auditEvent    string
	name          string
	allowUpdate   bool
	groups        []string
	startedAt     time.Time
}

func (s *APIServer) authorizePipelineWrite(w http.ResponseWriter, r *http.Request, opts pipelineWriteAuth) bool {
	user := UserFromContext(r.Context())
	authUsername := authUsernameFromRequest(r)
	trimmedName := strings.TrimSpace(opts.name)
	reqAction := ActionCreate
	if opts.allowUpdate {
		if existing, getErr := s.GetArtifact(r.Context(), TargetPipeline, trimmedName, false, ""); getErr == nil && existing != nil {
			reqAction = ActionUpdate
		}
	}
	if !s.authorizePipelineOrWrite403(w, r, user, trimmedName, opts.groups, reqAction, opts.startedAt) {
		return false
	}
	if len(opts.groups) > 0 {
		if err := s.validatePipelineGroupsExist(r.Context(), opts.groups); err != nil {
			s.recordArtifactAudit(r, authUsername, opts.auditEvent, TargetPipeline, trimmedName, http.StatusBadRequest, auditStatusError, opts.startedAt, err.Error())
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return false
		}
	}
	return true
}

func (s *APIServer) assignAndPopulatePipelineGroups(ctx context.Context, rec *ArtifactRecord, groups []string) error {
	if len(groups) == 0 {
		return nil
	}
	if err := s.assignPipelineToGroups(ctx, rec.Name, groups); err != nil {
		return err
	}
	if snap, snapErr := s.loadACLSnapshot(ctx); snapErr == nil {
		rec.Groups = snap.groupsForPipeline(rec.Name, nil)
	}
	return nil
}

func (s *APIServer) updateArtifactItemWithGroups(
	ctx context.Context,
	user *APIUser,
	artifactType string,
	groups []string,
	resolveName func() (string, error),
	doUpdate func() (*ArtifactRecord, error),
) (*ArtifactRecord, error) {
	if artifactType == TargetPipeline && len(groups) > 0 {
		pName, err := resolveName()
		if err != nil {
			return nil, err
		}
		allowed, reason, aclErr := s.EvaluatePipelineAccess(ctx, user, pName, groups, ActionUpdate)
		if aclErr != nil {
			return nil, aclErr
		}
		if !allowed {
			return nil, fmt.Errorf("%w: %s", ErrPipelineForbidden, reason)
		}
		if err := s.validatePipelineGroupsExist(ctx, groups); err != nil {
			return nil, err
		}
	}
	rec, err := doUpdate()
	if err != nil {
		return nil, err
	}
	if artifactType == TargetPipeline {
		if err := s.assignAndPopulatePipelineGroups(ctx, rec, groups); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

func (s *APIServer) dispatchArtifactRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	authUsername := ""
	if user != nil {
		authUsername = user.Username
	}

	rel := strings.TrimPrefix(r.URL.Path, "/api/v1/artifacts/")
	rel = strings.TrimSuffix(rel, "/")
	parts := strings.Split(rel, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		writeJSONError(w, http.StatusBadRequest, "Artifact type is required (pipeline, options, or config)")
		return
	}

	artifactType := parts[0]
	if _, _, canonicalType, err := ResolveArtifactTarget(artifactType); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	} else {
		artifactType = canonicalType
	}

	// For non-pipeline artifacts (options, config), enforce global RBAC permissions directly
	if artifactType != "pipeline" {
		reqPerm := PermWrite
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			reqPerm = PermReadOnly
		} else if len(parts) == 2 && strings.EqualFold(parts[1], "upload") {
			reqPerm = PermUpload
		}
		if !s.authorizeNonPipelineArtifact(w, r, user, artifactType, "", reqPerm, start) {
			return
		}
	}

	// Route 1: /api/v1/artifacts/{type}/upload (POST multipart file or raw body upload)
	if len(parts) == 2 && strings.ToLower(parts[1]) == "upload" {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed for upload; use POST or PUT")
			return
		}
		s.handleArtifactUpload(w, r, artifactType, authUsername, start)
		return
	}

	// Route 2: /api/v1/artifacts/{type} (GET list or POST create)
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			includeContent := r.URL.Query().Get("include_content") == "true" || r.URL.Query().Get("include_content") == "1"
			items, err := s.ListArtifacts(r.Context(), artifactType, includeContent)
			if err != nil {
				s.recordArtifactAudit(r, authUsername, "artifact.list", artifactType, "", http.StatusInternalServerError, "ERROR", start, err.Error())
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if artifactType == "pipeline" {
				items, err = s.FilterAccessiblePipelines(r.Context(), user, items, ActionRead)
				if err != nil {
					s.recordArtifactAudit(r, authUsername, "artifact.list", artifactType, "", http.StatusInternalServerError, "ERROR", start, err.Error())
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
				if groupFilter := strings.TrimSpace(r.URL.Query().Get("group")); groupFilter != "" {
					filtered := make([]ArtifactRecord, 0, len(items))
					for _, rec := range items {
						for _, g := range rec.Groups {
							if strings.EqualFold(g, groupFilter) {
								filtered = append(filtered, rec)
								break
							}
						}
					}
					items = filtered
				}
			}
			s.recordArtifactAudit(r, authUsername, "artifact.list", artifactType, "", http.StatusOK, "SUCCESS", start, "")
			writeJSON(w, http.StatusOK, map[string]any{
				"type":  artifactType,
				"count": len(items),
				"items": items,
			})
		case http.MethodPost:
			var body artifactPayload
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
				return
			}
			isUpsert := body.Upsert || r.URL.Query().Get("upsert") == "true"
			requestedGroups := parseGroupListValues(body.Group, body.Groups, r.URL.Query().Get("group"), strings.Join(r.URL.Query()["groups"], ","))

			if artifactType == TargetPipeline && !s.authorizePipelineWrite(w, r, pipelineWriteAuth{
				auditEvent:  "artifact.create",
				name:        body.Name,
				allowUpdate: isUpsert,
				groups:      requestedGroups,
				startedAt:   start,
			}) {
				return
			}

			_, secKey := extractArtifactQuerySecret(r, body.SecureKey)
			var rec *ArtifactRecord
			var err error
			if isUpsert {
				rec, err = s.UpsertArtifact(r.Context(), artifactType, body.Name, body.Description, body.resolvedContent(), body.resolvedEncrypted(), secKey)
			} else {
				rec, err = s.CreateArtifact(r.Context(), artifactType, body.Name, body.Description, body.resolvedContent(), body.resolvedEncrypted(), secKey)
			}
			if err != nil {
				code := artifactErrorStatusCode(err)
				s.recordArtifactAudit(r, authUsername, "artifact.create", artifactType, body.Name, code, "ERROR", start, err.Error())
				writeJSONError(w, code, err.Error())
				return
			}
			if artifactType == TargetPipeline {
				if err := s.assignAndPopulatePipelineGroups(r.Context(), rec, requestedGroups); err != nil {
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			s.recordArtifactAudit(r, authUsername, "artifact.create", artifactType, rec.Name, http.StatusCreated, "SUCCESS", start, "")
			writeJSON(w, http.StatusCreated, rec)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	// Route 2b: /api/v1/artifacts/{type}/id/{id}[/export] (GET, PUT, DELETE, export by database table ID)
	if len(parts) >= 3 && strings.ToLower(parts[1]) == "id" {
		idVal, idErr := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		if idErr != nil || idVal <= 0 {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid artifact ID %q: must be a positive integer", parts[2]))
			return
		}
		idLabel := fmt.Sprintf("id:%d", idVal)
		isExport := len(parts) == 4 && strings.ToLower(parts[3]) == "export"

		if artifactType == TargetPipeline {
			if ok := s.authorizeExistingPipelineItem(w, r, user, true, idVal, "", isExport, start); !ok {
				return
			}
		}

		getByID := func(decrypt bool, secKey string) (*ArtifactRecord, error) {
			return s.GetArtifactByID(r.Context(), artifactType, idVal, decrypt, secKey)
		}
		if isExport {
			s.serveArtifactExport(w, r, authUsername, artifactType, idLabel, start, getByID)
			return
		}
		s.serveArtifactItemCRUD(
			w, r, authUsername, artifactType, idLabel, start,
			getByID,
			func(desc, content string, enc bool, secKey string, groups []string) (*ArtifactRecord, error) {
				return s.updateArtifactItemWithGroups(
					r.Context(), user, artifactType, groups,
					func() (string, error) {
						existing, err := s.GetArtifactByID(r.Context(), TargetPipeline, idVal, false, "")
						if err != nil {
							return "", err
						}
						return existing.Name, nil
					},
					func() (*ArtifactRecord, error) {
						return s.UpdateArtifactByID(r.Context(), artifactType, idVal, desc, content, enc, secKey)
					},
				)
			},
			func() error {
				return s.DeleteArtifactByID(r.Context(), artifactType, idVal)
			},
			map[string]any{"deleted": true, "type": artifactType, "id": idVal},
		)
		return
	}

	name := parts[1]
	isExport := len(parts) == 3 && strings.ToLower(parts[2]) == "export"
	if artifactType == TargetPipeline {
		if ok := s.authorizeExistingPipelineItem(w, r, user, false, 0, name, isExport, start); !ok {
			return
		}
	}

	getByName := func(decrypt bool, secKey string) (*ArtifactRecord, error) {
		return s.GetArtifact(r.Context(), artifactType, name, decrypt, secKey)
	}

	// Route 3: /api/v1/artifacts/{type}/{name}/export (GET raw XML file download)
	if isExport {
		s.serveArtifactExport(w, r, authUsername, artifactType, name, start, getByName)
		return
	}

	// Route 4: /api/v1/artifacts/{type}/{name} (GET, PUT, DELETE)
	s.serveArtifactItemCRUD(
		w, r, authUsername, artifactType, name, start,
		getByName,
		func(desc, content string, enc bool, secKey string, groups []string) (*ArtifactRecord, error) {
			return s.updateArtifactItemWithGroups(
				r.Context(), user, artifactType, groups,
				func() (string, error) { return name, nil },
				func() (*ArtifactRecord, error) {
					return s.UpdateArtifact(r.Context(), artifactType, name, desc, content, enc, secKey)
				},
			)
		},
		func() error {
			return s.DeleteArtifact(r.Context(), artifactType, name)
		},
		map[string]any{"deleted": true, "type": artifactType, "name": name},
	)
}

func (s *APIServer) handleArtifactUpload(w http.ResponseWriter, r *http.Request, artifactType, authUsername string, start time.Time) {
	contentType := r.Header.Get("Content-Type")
	var name, desc, secureKey, singleGroup string
	var formGroups []string
	var encrypt bool
	var rawData []byte

	if strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("failed parsing multipart form: %v", err))
			return
		}
		name = strings.TrimSpace(r.FormValue("name"))
		desc = strings.TrimSpace(r.FormValue("description"))
		if desc == "" {
			desc = strings.TrimSpace(r.FormValue("desc"))
		}
		singleGroup = strings.TrimSpace(r.FormValue("group"))
		formGroups = r.MultipartForm.Value["groups"]
		secureKey = strings.TrimSpace(r.FormValue("secure_key"))
		encrypt = r.FormValue("encrypted") == "true" || r.FormValue("encrypted") == "1" || r.FormValue("encrypt") == "true" || r.FormValue("encrypt") == "1"

		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("missing 'file' in multipart upload: %v", err))
			return
		}
		defer file.Close()

		if name == "" && header != nil {
			base := filepath.Base(header.Filename)
			name = strings.TrimSuffix(base, filepath.Ext(base))
		}
		data, err := io.ReadAll(file)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("failed reading uploaded file: %v", err))
			return
		}
		rawData = data
	} else {
		// Raw XML/file body upload with query params (?name=...&description=...&encrypted=true)
		name = strings.TrimSpace(r.URL.Query().Get("name"))
		desc = strings.TrimSpace(r.URL.Query().Get("description"))
		if desc == "" {
			desc = "Uploaded via go-flowAPI"
		}
		secureKey = strings.TrimSpace(r.URL.Query().Get("secure_key"))
		if secureKey == "" {
			secureKey = r.Header.Get("X-Flow-Secure-Key")
		}
		encrypt = r.URL.Query().Get("encrypted") == "true" || r.URL.Query().Get("encrypted") == "1" || r.URL.Query().Get("encrypt") == "true" || r.URL.Query().Get("encrypt") == "1"
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("failed reading request body: %v", err))
			return
		}
		rawData = data
	}

	requestedGroups := parseGroupListValues(singleGroup, formGroups, r.URL.Query().Get("group"), strings.Join(r.URL.Query()["groups"], ","))

	if artifactType == TargetPipeline && !s.authorizePipelineWrite(w, r, pipelineWriteAuth{
		auditEvent:  "artifact.upload",
		name:        name,
		allowUpdate: true,
		groups:      requestedGroups,
		startedAt:   start,
	}) {
		return
	}

	if desc == "" {
		desc = "Uploaded via go-flowAPI"
	}
	decodedContent := DecodeUploadedBytes(rawData)

	rec, err := s.UpsertArtifact(r.Context(), artifactType, name, desc, decodedContent, encrypt, secureKey)
	if err != nil {
		s.recordArtifactAudit(r, authUsername, "artifact.upload", artifactType, name, http.StatusBadRequest, "ERROR", start, err.Error())
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if artifactType == TargetPipeline {
		if err := s.assignAndPopulatePipelineGroups(r.Context(), rec, requestedGroups); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	s.recordArtifactAudit(r, authUsername, "artifact.upload", artifactType, rec.Name, http.StatusCreated, "SUCCESS", start, "")
	writeJSON(w, http.StatusCreated, rec)
}

func (s *APIServer) recordArtifactAudit(r *http.Request, authUser, evtType, artifactType, artifactName string, code int, status string, start time.Time, errMsg string) {
	s.logAPIEvent(r.Context(), APIAuditEvent{
		OccurredAt:   start,
		EventType:    evtType,
		HTTPMethod:   r.Method,
		Path:         r.URL.Path,
		ArtifactType: artifactType,
		ArtifactName: artifactName,
		AuthUser:     authUser,
		RemoteAddr:   r.RemoteAddr,
		StatusCode:   code,
		Status:       status,
		DurationMs:   time.Since(start).Milliseconds(),
		ErrorMessage: errMsg,
	})
}

func parsePositiveQueryID(qVal, paramName string) (int64, error) {
	v := strings.TrimSpace(qVal)
	if v == "" {
		return 0, nil
	}
	idVal, err := strconv.ParseInt(v, 10, 64)
	if err != nil || idVal <= 0 {
		return 0, fmt.Errorf("invalid %s query parameter %q: must be a positive integer", paramName, v)
	}
	return idVal, nil
}

func parseExecutionRequest(r *http.Request) (ExecutionRequest, error) {
	var req ExecutionRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			return req, err
		}
	}
	q := r.URL.Query()
	if req.ArtifactType == "" {
		req.ArtifactType = strings.TrimSpace(q.Get("artifact_type"))
		if req.ArtifactType == "" {
			req.ArtifactType = strings.TrimSpace(q.Get("type"))
		}
	}
	idTargets := []struct {
		dest *int64
		name string
	}{
		{&req.ID, "id"},
		{&req.PipelineID, "pipeline_id"},
		{&req.OptionsID, "options_id"},
		{&req.ConfigID, "config_id"},
	}
	for _, tgt := range idTargets {
		if *tgt.dest <= 0 {
			idVal, err := parsePositiveQueryID(q.Get(tgt.name), tgt.name)
			if err != nil {
				return req, err
			}
			*tgt.dest = idVal
		}
	}
	if req.File == "" {
		req.File = strings.TrimSpace(q.Get("file"))
	}
	if req.Config == "" {
		req.Config = strings.TrimSpace(q.Get("config"))
	}
	if req.Options == "" {
		req.Options = strings.TrimSpace(q.Get("options"))
	}
	if req.Format == "" {
		req.Format = strings.TrimSpace(q.Get("format"))
	}
	if !req.Preflight && (q.Get("preflight") == "true" || q.Get("preflight") == "1") {
		req.Preflight = true
	}
	if !req.Validate && (q.Get("validate") == "true" || q.Get("validate") == "1") {
		req.Validate = true
	}
	if !req.NodeOutput && (q.Get("node_output") == "true" || q.Get("node_output") == "1") {
		req.NodeOutput = true
	}
	if !req.Debug && (q.Get("debug") == "true" || q.Get("debug") == "1") {
		req.Debug = true
	}
	if req.VarsRaw == "" {
		req.VarsRaw = strings.TrimSpace(q.Get("vars"))
	}
	if req.SecureKey == "" {
		req.SecureKey = strings.TrimSpace(q.Get("secure_key"))
		if req.SecureKey == "" {
			req.SecureKey = strings.TrimSpace(r.Header.Get("X-Flow-Secure-Key"))
		}
	}
	return req, nil
}

func describeExecutionTarget(req ExecutionRequest) (artifactType, artifactName string) {
	artifactType = req.ArtifactType
	if artifactType == "" {
		if req.OptionsID > 0 {
			artifactType = "options"
		} else if req.ConfigID > 0 && req.PipelineID <= 0 && req.File == "" {
			artifactType = "config"
		} else {
			artifactType = "pipeline"
		}
	}
	switch {
	case req.ID > 0:
		artifactName = fmt.Sprintf("id:%d", req.ID)
	case req.PipelineID > 0:
		artifactName = fmt.Sprintf("id:%d", req.PipelineID)
	case req.OptionsID > 0:
		artifactName = fmt.Sprintf("id:%d", req.OptionsID)
	case req.ConfigID > 0 && req.File == "":
		artifactName = fmt.Sprintf("id:%d", req.ConfigID)
	case req.File != "":
		artifactName = req.File
	case req.Options != "":
		artifactName = req.Options
	case req.Config != "":
		artifactName = req.Config
	default:
		artifactName = "inline"
	}
	return artifactType, artifactName
}

func parseTypeAndIDPair(typeRaw, idRaw string) (string, int64, error) {
	_, _, canonicalType, err := ResolveArtifactTarget(typeRaw)
	if err != nil {
		return "", 0, err
	}
	idVal, err := strconv.ParseInt(strings.TrimSpace(idRaw), 10, 64)
	if err != nil || idVal <= 0 {
		return "", 0, fmt.Errorf("invalid artifact ID %q: must be a positive integer", idRaw)
	}
	return canonicalType, idVal, nil
}

// handleExecuteRouter dispatches:
//   - /api/v1/execute/stream (or /api/v1/execution/stream)
//   - /api/v1/execute/{type}/{id} (or /api/v1/execution/{type}/{id})
//   - /api/v1/execute/stream/{type}/{id} (or /api/v1/execution/stream/{type}/{id})
//   - /api/v1/execute/{type}/{id}/stream (or /api/v1/execution/{type}/{id}/stream)
func (s *APIServer) handleExecuteRouter(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Path
	if strings.HasPrefix(rel, "/api/v1/execution/") {
		rel = strings.TrimPrefix(rel, "/api/v1/execution/")
	} else {
		rel = strings.TrimPrefix(rel, "/api/v1/execute/")
	}
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		s.handleExecuteSync(w, r)
		return
	}
	if strings.EqualFold(rel, "stream") {
		s.handleExecuteStream(w, r)
		return
	}

	parts := strings.Split(rel, "/")
	// Pattern 1: /api/v1/execute/stream/{type}/{id}
	if len(parts) == 3 && strings.EqualFold(parts[0], "stream") {
		canonicalType, idVal, err := parseTypeAndIDPair(parts[1], parts[2])
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.executeStreamWithTarget(w, r, canonicalType, idVal)
		return
	}

	// Pattern 2: /api/v1/execute/{type}/{id}/stream
	if len(parts) == 3 && strings.EqualFold(parts[2], "stream") {
		canonicalType, idVal, err := parseTypeAndIDPair(parts[0], parts[1])
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.executeStreamWithTarget(w, r, canonicalType, idVal)
		return
	}

	// Pattern 3: /api/v1/execute/{type}/{id}
	if len(parts) == 2 {
		canonicalType, idVal, err := parseTypeAndIDPair(parts[0], parts[1])
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.executeSyncWithTarget(w, r, canonicalType, idVal)
		return
	}

	writeJSONError(w, http.StatusBadRequest, "expected /api/v1/execute/{type}/{id}, /api/v1/execute/stream/{type}/{id}, or /api/v1/execute/{type}/{id}/stream")
}

// handleValidateRouter dispatches /api/v1/validate/{type}/{id}.
func (s *APIServer) handleValidateRouter(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/v1/validate/")
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		s.handleValidate(w, r)
		return
	}
	parts := strings.Split(rel, "/")
	if len(parts) == 2 {
		canonicalType, idVal, err := parseTypeAndIDPair(parts[0], parts[1])
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.validateWithTarget(w, r, canonicalType, idVal)
		return
	}
	writeJSONError(w, http.StatusBadRequest, "expected /api/v1/validate/{type}/{id}")
}

func authUsernameFromRequest(r *http.Request) string {
	if user := UserFromContext(r.Context()); user != nil {
		return user.Username
	}
	return ""
}

func (s *APIServer) prepareExecutionRequest(w http.ResponseWriter, r *http.Request, targetType string, targetID int64, errPrefix string) (ExecutionRequest, string, string, string, time.Time, bool) {
	start := time.Now().UTC()
	authUser := authUsernameFromRequest(r)
	req, err := parseExecutionRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid %s request: %v", errPrefix, err))
		return req, "", "", "", start, false
	}
	if targetType != "" {
		req.ArtifactType = targetType
	}
	if targetID > 0 {
		req.ID = targetID
	}
	artType, artName := describeExecutionTarget(req)
	return req, artType, artName, authUser, start, true
}

func (s *APIServer) recordExecutionAudit(r *http.Request, evtType, artType, artName, runID, authUser string, statusCode int, status string, start time.Time, errMsg string) {
	s.logAPIEvent(r.Context(), APIAuditEvent{
		OccurredAt:   start,
		EventType:    evtType,
		HTTPMethod:   r.Method,
		Path:         r.URL.Path,
		ArtifactType: artType,
		ArtifactName: artName,
		RunID:        runID,
		AuthUser:     authUser,
		RemoteAddr:   r.RemoteAddr,
		StatusCode:   statusCode,
		Status:       status,
		DurationMs:   time.Since(start).Milliseconds(),
		ErrorMessage: errMsg,
	})
}

func (s *APIServer) handleExecuteSync(w http.ResponseWriter, r *http.Request) {
	s.executeSyncWithTarget(w, r, "", 0)
}

func (s *APIServer) executeSyncWithTarget(w http.ResponseWriter, r *http.Request, targetType string, targetID int64) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Use POST for synchronous execution")
		return
	}
	req, artType, artName, authUser, start, ok := s.prepareExecutionRequest(w, r, targetType, targetID, "execution")
	if !ok {
		return
	}
	if resolvedName, authErr := s.AuthorizeExecutionRequest(r.Context(), UserFromContext(r.Context()), req); authErr != nil {
		if resolvedName != "" {
			artName = resolvedName
		}
		if errors.Is(authErr, ErrPipelineForbidden) {
			s.writeForbiddenAudit(w, r, authUser, artType, artName, authErr.Error(), start)
			return
		}
	}

	resp, execErr := s.ExecutePipeline(r.Context(), req, nil)
	if resp == nil && execErr != nil {
		if errors.Is(execErr, ErrPipelineForbidden) {
			s.writeForbiddenAudit(w, r, authUser, artType, artName, execErr.Error(), start)
			return
		}
		s.recordExecutionAudit(r, "pipeline.execute", artType, artName, "", authUser, http.StatusBadRequest, "ERROR", start, execErr.Error())
		writeJSONError(w, http.StatusBadRequest, execErr.Error())
		return
	}

	statusCode := http.StatusOK
	statusLabel := "SUCCESS"
	errMsg := ""
	if execErr != nil {
		statusCode = http.StatusUnprocessableEntity
		statusLabel = "FAILED"
		errMsg = execErr.Error()
	}

	s.recordExecutionAudit(r, "pipeline.execute", artType, artName, resp.RunID, authUser, statusCode, statusLabel, start, errMsg)
	writeJSON(w, statusCode, resp)
}

func (s *APIServer) handleValidate(w http.ResponseWriter, r *http.Request) {
	s.validateWithTarget(w, r, "", 0)
}

func (s *APIServer) validateWithTarget(w http.ResponseWriter, r *http.Request, targetType string, targetID int64) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Use POST for /api/v1/validate")
		return
	}
	req, artType, artName, authUser, start, ok := s.prepareExecutionRequest(w, r, targetType, targetID, "validation")
	if !ok {
		return
	}
	req.Validate = true
	artType, artName = describeExecutionTarget(req)

	if resolvedName, authErr := s.AuthorizeExecutionRequest(r.Context(), UserFromContext(r.Context()), req); authErr != nil {
		if resolvedName != "" {
			artName = resolvedName
		}
		if errors.Is(authErr, ErrPipelineForbidden) {
			s.writeForbiddenAudit(w, r, authUser, artType, artName, authErr.Error(), start)
			return
		}
	}

	resp, valErr := s.ExecutePipeline(r.Context(), req, nil)
	if valErr != nil {
		if errors.Is(valErr, ErrPipelineForbidden) {
			s.writeForbiddenAudit(w, r, authUser, artType, artName, valErr.Error(), start)
			return
		}
		s.recordExecutionAudit(r, "pipeline.validate", artType, artName, "", authUser, http.StatusUnprocessableEntity, "INVALID", start, valErr.Error())
		writeJSONError(w, http.StatusUnprocessableEntity, valErr.Error())
		return
	}

	s.recordExecutionAudit(r, "pipeline.validate", artType, artName, resp.RunID, authUser, http.StatusOK, "VALID", start, "")
	writeJSON(w, http.StatusOK, resp)
}

func (s *APIServer) handleExecuteStream(w http.ResponseWriter, r *http.Request) {
	s.executeStreamWithTarget(w, r, "", 0)
}

func (s *APIServer) executeStreamWithTarget(w http.ResponseWriter, r *http.Request, targetType string, targetID int64) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Use POST or GET for streaming execution")
		return
	}
	req, artType, artName, authUser, start, ok := s.prepareExecutionRequest(w, r, targetType, targetID, "stream")
	if !ok {
		return
	}

	// Enforce granular pipeline authorization BEFORE writing text/event-stream headers
	if resolvedName, authErr := s.AuthorizeExecutionRequest(r.Context(), UserFromContext(r.Context()), req); authErr != nil {
		if resolvedName != "" {
			artName = resolvedName
		}
		if errors.Is(authErr, ErrPipelineForbidden) {
			s.writeForbiddenAudit(w, r, authUser, artType, artName, authErr.Error(), start)
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	sendSSE := func(event string, payload any) {
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(b))
		if flusher != nil {
			flusher.Flush()
		}
	}

	resp, execErr := s.ExecutePipeline(r.Context(), req, func(evt flow.ExecutionEvent) {
		sendSSE("node_event", map[string]any{
			"type":          "node_event",
			"timestamp":     evt.OccurredAt.UTC().Format(time.RFC3339Nano),
			"run_id":        evt.RunID,
			"execution_id":  evt.ExecutionID,
			"sequence":      evt.Sequence,
			"event_type":    string(evt.Type),
			"kind":          evt.NodeKind,
			"node_id":       evt.NodeID,
			"status":        string(evt.Status),
			"error":         evt.ErrorMessage,
			"rows_read":     evt.RowCounts.Read,
			"rows_written":  evt.RowCounts.Written,
			"rows_affected": evt.RowCounts.Affected,
		})
	})

	runID := ""
	if resp != nil {
		runID = resp.RunID
	}

	if execErr != nil {
		s.recordExecutionAudit(r, "pipeline.stream", artType, artName, runID, authUser, http.StatusUnprocessableEntity, "FAILED", start, execErr.Error())
		sendSSE("done", map[string]any{
			"type":     "done",
			"run_id":   runID,
			"status":   "FAILED",
			"error":    execErr.Error(),
			"duration": time.Since(start).String(),
		})
		return
	}

	s.recordExecutionAudit(r, "pipeline.stream", artType, artName, runID, authUser, http.StatusOK, "SUCCESS", start, "")
	sendSSE("done", map[string]any{
		"type":     "done",
		"run_id":   runID,
		"status":   "SUCCESS",
		"duration": time.Since(start).String(),
		"results":  resp.Results,
	})
}

func (s *APIServer) handleListRuns(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	authUser := authUsernameFromRequest(r)

	statusFilter := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	runs, err := s.QueryPipelineRuns(r.Context(), statusFilter, limit)
	if err != nil {
		s.recordArtifactAudit(r, authUser, "logs.runs", "", "", http.StatusInternalServerError, "ERROR", start, err.Error())
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	runs, err = s.filterRunsForUser(r.Context(), user, runs)
	if err != nil {
		s.recordArtifactAudit(r, authUser, "logs.runs", "", "", http.StatusInternalServerError, "ERROR", start, err.Error())
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordArtifactAudit(r, authUser, "logs.runs", "", "", http.StatusOK, "SUCCESS", start, "")
	writeJSON(w, http.StatusOK, map[string]any{"count": len(runs), "runs": runs})
}

func (s *APIServer) handleListEvents(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	authUser := authUsernameFromRequest(r)

	runID := r.URL.Query().Get("run_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	events, err := s.QueryPipelineEvents(r.Context(), runID, limit)
	if err != nil {
		s.recordArtifactAudit(r, authUser, "logs.events", "", runID, http.StatusInternalServerError, "ERROR", start, err.Error())
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	events, err = s.filterEventsForUser(r.Context(), user, events)
	if err != nil {
		s.recordArtifactAudit(r, authUser, "logs.events", "", runID, http.StatusInternalServerError, "ERROR", start, err.Error())
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordArtifactAudit(r, authUser, "logs.events", "", runID, http.StatusOK, "SUCCESS", start, "")
	writeJSON(w, http.StatusOK, map[string]any{"count": len(events), "events": events})
}

func (s *APIServer) handleListAPIEvents(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	authUser := authUsernameFromRequest(r)

	evtType := r.URL.Query().Get("event_type")
	filterUser := r.URL.Query().Get("auth_user")
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	events, err := s.QueryAPIAuditEvents(r.Context(), evtType, filterUser, status, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordArtifactAudit(r, authUser, "logs.api_events", "", "", http.StatusOK, "SUCCESS", start, "")
	writeJSON(w, http.StatusOK, map[string]any{"count": len(events), "api_events": events})
}

func (s *APIServer) handleTestDatabase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Use POST for /api/v1/databases/test")
		return
	}
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	authUser := ""
	if user != nil {
		authUser = user.Username
	}

	var req struct {
		DSN              string `json:"dsn"`
		ConnectionString string `json:"connection_string"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
	}
	targetDSN := strings.TrimSpace(req.DSN)
	if targetDSN == "" {
		targetDSN = strings.TrimSpace(req.ConnectionString)
	}
	if targetDSN == "" {
		targetDSN = strings.TrimSpace(r.URL.Query().Get("dsn"))
	}
	if targetDSN == "" {
		targetDSN = s.cfg.ConnectionString
	}

	db, dialect, err := OpenDatabase(targetDSN)
	if err != nil {
		s.recordArtifactAudit(r, authUser, "db.test", "", dialect, http.StatusBadRequest, "ERROR", start, err.Error())
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		s.recordArtifactAudit(r, authUser, "db.test", "", dialect, http.StatusBadGateway, "ERROR", start, err.Error())
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("ping failed: %v", err))
		return
	}

	spid, dbUser, appName, dbServer, _ := FetchDatabaseSessionIdentityWithServer(ctx, db, dialect)
	s.recordArtifactAudit(r, authUser, "db.test", "", dialect, http.StatusOK, "SUCCESS", start, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"connected":        true,
		"dialect":          dialect,
		"spid":             spid,
		"db_user":          dbUser,
		"db_server":        dbServer,
		"application_name": appName,
	})
}

func (s *APIServer) handleUsers(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	actor := UserFromContext(r.Context())
	actorName := ""
	if actor != nil {
		actorName = actor.Username
	}

	switch r.Method {
	case http.MethodGet:
		users, err := s.ListAPIUsers(r.Context())
		if err != nil {
			s.recordArtifactAudit(r, actorName, "user.list", "user", "", http.StatusInternalServerError, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.recordArtifactAudit(r, actorName, "user.list", "user", "", http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, map[string]any{"count": len(users), "users": users})

	case http.MethodPost:
		var body struct {
			Username    string   `json:"username"`
			Password    string   `json:"password"`
			Permission  string   `json:"permission"`
			Description string   `json:"description"`
			IsActive    *bool    `json:"is_active"`
			Groups      []string `json:"groups"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		active := true
		if body.IsActive != nil {
			active = *body.IsActive
		}
		u, err := s.UpsertAPIUser(r.Context(), body.Username, body.Password, body.Permission, body.Description, active)
		if err != nil {
			s.recordArtifactAudit(r, actorName, "user.upsert", "user", body.Username, http.StatusBadRequest, "ERROR", start, err.Error())
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		for _, g := range body.Groups {
			g = strings.TrimSpace(g)
			if g == "" {
				continue
			}
			if gErr := s.AddUserToGroup(r.Context(), g, u.Username); gErr != nil {
				s.recordArtifactAudit(r, actorName, "user.upsert", "user", u.Username, http.StatusBadRequest, "ERROR", start, gErr.Error())
				writeJSONError(w, http.StatusBadRequest, gErr.Error())
				return
			}
		}
		if dbGroups, gErr := s.ListGroupsForUser(r.Context(), u.Username); gErr == nil {
			u.Groups = dbGroups
		}
		s.recordArtifactAudit(r, actorName, "user.upsert", "user", u.Username, http.StatusCreated, "SUCCESS", start, "")
		writeJSON(w, http.StatusCreated, u)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *APIServer) handleUserByName(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	actor := UserFromContext(r.Context())
	actorName := ""
	if actor != nil {
		actorName = actor.Username
	}

	username := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	username = strings.TrimSuffix(username, "/")
	if username == "" {
		writeJSONError(w, http.StatusBadRequest, "Username is required")
		return
	}

	if r.Method != http.MethodDelete {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if err := s.DeleteAPIUser(r.Context(), username); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrArtifactNotFound) {
			code = http.StatusNotFound
		}
		s.recordArtifactAudit(r, actorName, "user.delete", "user", username, code, "ERROR", start, err.Error())
		writeJSONError(w, code, err.Error())
		return
	}
	s.recordArtifactAudit(r, actorName, "user.delete", "user", username, http.StatusOK, "SUCCESS", start, "")
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "username": username})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error":  message,
		"status": status,
	})
}
