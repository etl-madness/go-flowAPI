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

	// Health check (BasicAuth protected with any valid permission)
	mux.HandleFunc("/api/v1/health", s.RequirePermission(PermReadOnly, s.handleHealth))

	// Artifact CRUD & File Upload Routes (/api/v1/artifacts/{type} and /api/v1/upload/{type})
	mux.HandleFunc("/api/v1/artifacts/", s.handleArtifactsRouter)
	mux.HandleFunc("/api/v1/upload/", s.RequirePermission(PermUpload, s.handleUploadRouter))

	// Execution & Validation Routes (requires "execution-only" or "all")
	mux.HandleFunc("/api/v1/execute", s.RequirePermission(PermExecutionOnly, s.handleExecuteSync))
	mux.HandleFunc("/api/v1/execute/stream", s.RequirePermission(PermExecutionOnly, s.handleExecuteStream))
	mux.HandleFunc("/api/v1/validate", s.RequirePermission(PermExecutionOnly, s.handleValidate))

	// Telemetry & Audit Event Log Queries (requires "read-only" or "all")
	mux.HandleFunc("/api/v1/logs/runs", s.RequirePermission(PermReadOnly, s.handleListRuns))
	mux.HandleFunc("/api/v1/runs", s.RequirePermission(PermReadOnly, s.handleListRuns))
	mux.HandleFunc("/api/v1/logs/events", s.RequirePermission(PermReadOnly, s.handleListEvents))
	mux.HandleFunc("/api/v1/events", s.RequirePermission(PermReadOnly, s.handleListEvents))
	mux.HandleFunc("/api/v1/logs/api-events", s.RequirePermission(PermReadOnly, s.handleListAPIEvents))
	mux.HandleFunc("/api/v1/audit", s.RequirePermission(PermReadOnly, s.handleListAPIEvents))

	// Database Connectivity Test & User Management (requires "all")
	mux.HandleFunc("/api/v1/databases/test", s.RequirePermission(PermAll, s.handleTestDatabase))
	mux.HandleFunc("/api/v1/db/test", s.RequirePermission(PermAll, s.handleTestDatabase))
	mux.HandleFunc("/api/v1/users", s.RequirePermission(PermAll, s.handleUsers))
	mux.HandleFunc("/api/v1/users/", s.RequirePermission(PermAll, s.handleUserByName))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
}

func (s *APIServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	username := ""
	perm := ""
	if user != nil {
		username = user.Username
		perm = user.Permission
	}

	spid, dbUser, appName, _ := FetchDatabaseSessionIdentity(r.Context(), s.db, s.dialect)
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

	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "ok",
		"dialect":          s.dialect,
		"authenticated_as": username,
		"permission":       perm,
		"db_spid":          spid,
		"db_user":          dbUser,
		"application_name": appName,
		"tables": map[string]string{
			"pipeline_content": TablePipelineContent,
			"options_content":  TableOptionsContent,
			"config_content":   TableConfigContent,
			"pipeline_runs":    TablePipelineRuns,
			"pipeline_events":  TablePipelineEvents,
			"api_events":       TableAPIAuditEvents,
			"api_users":        TableAPIUsers,
		},
	})
}

// handleArtifactsRouter dispatches /api/v1/artifacts/{type}[/{name}[/export]] with appropriate RBAC checks:
//   - GET requests require PermReadOnly (which also allows PermWrite, PermUpload, PermAll)
//   - POST, PUT, DELETE requests require PermWrite (which allows PermWrite, PermUpload, PermAll)
func (s *APIServer) handleArtifactsRouter(w http.ResponseWriter, r *http.Request) {
	requiredPerm := PermWrite
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		requiredPerm = PermReadOnly
	}
	s.RequirePermission(requiredPerm, s.dispatchArtifactRequest)(w, r)
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
	s.handleArtifactUpload(w, r, canonicalType, authUsername, start)
}

type artifactPayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	XMLContent  string `json:"xml_content"`
	Encrypted   bool   `json:"encrypted"`
	Encrypt     bool   `json:"encrypt"`
	SecureKey   string `json:"secure_key"`
	Upsert      bool   `json:"upsert"`
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
			var rec *ArtifactRecord
			var err error
			if body.Upsert || r.URL.Query().Get("upsert") == "true" {
				rec, err = s.UpsertArtifact(r.Context(), artifactType, body.Name, body.Description, body.resolvedContent(), body.resolvedEncrypted(), body.SecureKey)
			} else {
				rec, err = s.CreateArtifact(r.Context(), artifactType, body.Name, body.Description, body.resolvedContent(), body.resolvedEncrypted(), body.SecureKey)
			}
			if err != nil {
				code := http.StatusInternalServerError
				if errors.Is(err, ErrArtifactExists) {
					code = http.StatusConflict
				}
				s.recordArtifactAudit(r, authUsername, "artifact.create", artifactType, body.Name, code, "ERROR", start, err.Error())
				writeJSONError(w, code, err.Error())
				return
			}
			s.recordArtifactAudit(r, authUsername, "artifact.create", artifactType, rec.Name, http.StatusCreated, "SUCCESS", start, "")
			writeJSON(w, http.StatusCreated, rec)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	name := parts[1]

	// Route 3: /api/v1/artifacts/{type}/{name}/export (GET raw XML file download)
	if len(parts) == 3 && strings.ToLower(parts[2]) == "export" {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		decrypt := r.URL.Query().Get("decrypt") == "true" || r.URL.Query().Get("decrypt") == "1"
		secKey := r.URL.Query().Get("secure_key")
		if secKey == "" {
			secKey = r.Header.Get("X-Flow-Secure-Key")
		}
		rec, err := s.GetArtifact(r.Context(), artifactType, name, decrypt, secKey)
		if err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrArtifactNotFound) {
				code = http.StatusNotFound
			}
			s.recordArtifactAudit(r, authUsername, "artifact.export", artifactType, name, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUsername, "artifact.export", artifactType, name, http.StatusOK, "SUCCESS", start, "")
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".xml"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rec.Content))
		return
	}

	// Route 4: /api/v1/artifacts/{type}/{name} (GET, PUT, DELETE)
	switch r.Method {
	case http.MethodGet:
		decrypt := r.URL.Query().Get("decrypt") == "true" || r.URL.Query().Get("decrypt") == "1"
		secKey := r.URL.Query().Get("secure_key")
		if secKey == "" {
			secKey = r.Header.Get("X-Flow-Secure-Key")
		}
		rec, err := s.GetArtifact(r.Context(), artifactType, name, decrypt, secKey)
		if err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrArtifactNotFound) {
				code = http.StatusNotFound
			}
			s.recordArtifactAudit(r, authUsername, "artifact.read", artifactType, name, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUsername, "artifact.read", artifactType, name, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, rec)

	case http.MethodPut:
		var body artifactPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
			return
		}
		rec, err := s.UpdateArtifact(r.Context(), artifactType, name, body.Description, body.resolvedContent(), body.resolvedEncrypted(), body.SecureKey)
		if err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrArtifactNotFound) {
				code = http.StatusNotFound
			}
			s.recordArtifactAudit(r, authUsername, "artifact.update", artifactType, name, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUsername, "artifact.update", artifactType, name, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, rec)

	case http.MethodDelete:
		if err := s.DeleteArtifact(r.Context(), artifactType, name); err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrArtifactNotFound) {
				code = http.StatusNotFound
			}
			s.recordArtifactAudit(r, authUsername, "artifact.delete", artifactType, name, code, "ERROR", start, err.Error())
			writeJSONError(w, code, err.Error())
			return
		}
		s.recordArtifactAudit(r, authUsername, "artifact.delete", artifactType, name, http.StatusOK, "SUCCESS", start, "")
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "type": artifactType, "name": name})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *APIServer) handleArtifactUpload(w http.ResponseWriter, r *http.Request, artifactType, authUsername string, start time.Time) {
	contentType := r.Header.Get("Content-Type")
	var name, desc, secureKey string
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

func parseExecutionRequest(r *http.Request) (ExecutionRequest, error) {
	var req ExecutionRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			return req, err
		}
	}
	q := r.URL.Query()
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
	return req, nil
}

func (s *APIServer) handleExecuteSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Use POST for /api/v1/execute")
		return
	}
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	authUser := ""
	if user != nil {
		authUser = user.Username
	}

	req, err := parseExecutionRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid execution request: %v", err))
		return
	}

	resp, execErr := s.ExecutePipeline(r.Context(), req, nil)
	if resp == nil && execErr != nil {
		s.logAPIEvent(r.Context(), APIAuditEvent{
			OccurredAt:   start,
			EventType:    "pipeline.execute",
			HTTPMethod:   r.Method,
			Path:         r.URL.Path,
			ArtifactType: "pipeline",
			ArtifactName: req.File,
			AuthUser:     authUser,
			RemoteAddr:   r.RemoteAddr,
			StatusCode:   http.StatusBadRequest,
			Status:       "ERROR",
			DurationMs:   time.Since(start).Milliseconds(),
			ErrorMessage: execErr.Error(),
		})
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

	s.logAPIEvent(r.Context(), APIAuditEvent{
		OccurredAt:   start,
		EventType:    "pipeline.execute",
		HTTPMethod:   r.Method,
		Path:         r.URL.Path,
		ArtifactType: "pipeline",
		ArtifactName: req.File,
		RunID:        resp.RunID,
		AuthUser:     authUser,
		RemoteAddr:   r.RemoteAddr,
		StatusCode:   statusCode,
		Status:       statusLabel,
		DurationMs:   time.Since(start).Milliseconds(),
		ErrorMessage: errMsg,
	})

	writeJSON(w, statusCode, resp)
}

func (s *APIServer) handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Use POST for /api/v1/validate")
		return
	}
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	authUser := ""
	if user != nil {
		authUser = user.Username
	}

	req, err := parseExecutionRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid validation request: %v", err))
		return
	}
	req.Validate = true

	resp, valErr := s.ExecutePipeline(r.Context(), req, nil)
	if valErr != nil {
		s.logAPIEvent(r.Context(), APIAuditEvent{
			OccurredAt:   start,
			EventType:    "pipeline.validate",
			HTTPMethod:   r.Method,
			Path:         r.URL.Path,
			ArtifactType: "pipeline",
			ArtifactName: req.File,
			AuthUser:     authUser,
			RemoteAddr:   r.RemoteAddr,
			StatusCode:   http.StatusUnprocessableEntity,
			Status:       "INVALID",
			DurationMs:   time.Since(start).Milliseconds(),
			ErrorMessage: valErr.Error(),
		})
		writeJSONError(w, http.StatusUnprocessableEntity, valErr.Error())
		return
	}

	s.logAPIEvent(r.Context(), APIAuditEvent{
		OccurredAt:   start,
		EventType:    "pipeline.validate",
		HTTPMethod:   r.Method,
		Path:         r.URL.Path,
		ArtifactType: "pipeline",
		ArtifactName: req.File,
		RunID:        resp.RunID,
		AuthUser:     authUser,
		RemoteAddr:   r.RemoteAddr,
		StatusCode:   http.StatusOK,
		Status:       "VALID",
		DurationMs:   time.Since(start).Milliseconds(),
	})
	writeJSON(w, http.StatusOK, resp)
}

func (s *APIServer) handleExecuteStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Use POST or GET for /api/v1/execute/stream")
		return
	}
	start := time.Now().UTC()
	user := UserFromContext(r.Context())
	authUser := ""
	if user != nil {
		authUser = user.Username
	}

	req, err := parseExecutionRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid stream request: %v", err))
		return
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
		s.logAPIEvent(r.Context(), APIAuditEvent{
			OccurredAt:   start,
			EventType:    "pipeline.stream",
			HTTPMethod:   r.Method,
			Path:         r.URL.Path,
			ArtifactType: "pipeline",
			ArtifactName: req.File,
			RunID:        runID,
			AuthUser:     authUser,
			RemoteAddr:   r.RemoteAddr,
			StatusCode:   http.StatusUnprocessableEntity,
			Status:       "FAILED",
			DurationMs:   time.Since(start).Milliseconds(),
			ErrorMessage: execErr.Error(),
		})
		sendSSE("done", map[string]any{
			"type":     "done",
			"run_id":   runID,
			"status":   "FAILED",
			"error":    execErr.Error(),
			"duration": time.Since(start).String(),
		})
		return
	}

	s.logAPIEvent(r.Context(), APIAuditEvent{
		OccurredAt:   start,
		EventType:    "pipeline.stream",
		HTTPMethod:   r.Method,
		Path:         r.URL.Path,
		ArtifactType: "pipeline",
		ArtifactName: req.File,
		RunID:        runID,
		AuthUser:     authUser,
		RemoteAddr:   r.RemoteAddr,
		StatusCode:   http.StatusOK,
		Status:       "SUCCESS",
		DurationMs:   time.Since(start).Milliseconds(),
	})
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
	authUser := ""
	if user != nil {
		authUser = user.Username
	}

	statusFilter := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	runs, err := s.QueryPipelineRuns(r.Context(), statusFilter, limit)
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
	authUser := ""
	if user != nil {
		authUser = user.Username
	}

	runID := r.URL.Query().Get("run_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	events, err := s.QueryPipelineEvents(r.Context(), runID, limit)
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
	user := UserFromContext(r.Context())
	authUser := ""
	if user != nil {
		authUser = user.Username
	}

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
		DSN string `json:"dsn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	targetDSN := strings.TrimSpace(req.DSN)
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

	spid, dbUser, appName, _ := FetchDatabaseSessionIdentity(ctx, db, dialect)
	s.recordArtifactAudit(r, authUser, "db.test", "", dialect, http.StatusOK, "SUCCESS", start, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"connected":        true,
		"dialect":          dialect,
		"spid":             spid,
		"db_user":          dbUser,
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
			Username    string `json:"username"`
			Password    string `json:"password"`
			Permission  string `json:"permission"`
			Description string `json:"description"`
			IsActive    *bool  `json:"is_active"`
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
