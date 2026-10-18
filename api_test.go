package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestServer(t *testing.T) (*APIServer, *httptest.Server, *sql.DB) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_flow_api.db")
	dsn := "sqlite://" + filepath.ToSlash(dbPath)

	cfg := &ServerConfig{
		ConnectionString: dsn,
		ListenAddr:       ":0",
		DefaultSecureKey: "0123456789abcdef0123456789abcdef",
	}

	db, dialect, err := OpenDatabase(cfg.ConnectionString)
	if err != nil {
		t.Fatalf("OpenDatabase failed: %v", err)
	}

	// Provision tables using the SQLite DDL from SCHEMA.md
	ddlStatements := []string{
		`CREATE TABLE IF NOT EXISTS flow_pipeline_content (
			ID          INTEGER PRIMARY KEY AUTOINCREMENT,
			Name        TEXT NOT NULL UNIQUE,
			Description TEXT,
			PipelineXML TEXT,
			CreatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UpdatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS flow_options_content (
			ID          INTEGER PRIMARY KEY AUTOINCREMENT,
			Name        TEXT NOT NULL UNIQUE,
			Description TEXT,
			OptionsXML  TEXT,
			CreatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UpdatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS flow_config_content (
			ID          INTEGER PRIMARY KEY AUTOINCREMENT,
			Name        TEXT NOT NULL UNIQUE,
			Description TEXT,
			ConfigXML   TEXT,
			CreatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UpdatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS pipeline_runs (
			run_id           TEXT PRIMARY KEY,
			file_path        TEXT,
			config_path      TEXT,
			status           TEXT NOT NULL,
			started_at       DATETIME NOT NULL,
			finished_at      DATETIME NOT NULL,
			duration_ms      INTEGER NOT NULL,
			task_count       INTEGER NOT NULL,
			user_name        TEXT,
			os_user_name     TEXT,
			db_user_name     TEXT,
			spid             INTEGER,
			application_name TEXT,
			hostname         TEXT,
			options_path     TEXT,
			error_class      TEXT,
			error_message    TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS pipeline_events (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id           TEXT NOT NULL,
			execution_id     TEXT,
			sequence_num     INTEGER NOT NULL,
			occurred_at      DATETIME NOT NULL,
			event_type       TEXT NOT NULL,
			node_kind        TEXT,
			node_id          TEXT,
			status           TEXT,
			user_name        TEXT,
			os_user_name     TEXT,
			db_user_name     TEXT,
			spid             TEXT,
			application_name TEXT,
			hostname         TEXT,
			options_path     TEXT,
			error_message    TEXT,
			rows_read        INTEGER,
			rows_written     INTEGER,
			rows_affected    INTEGER,
			node_output      TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS flow_api_events (
			event_id      TEXT PRIMARY KEY,
			occurred_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			event_type    TEXT NOT NULL,
			http_method   TEXT NOT NULL,
			path          TEXT NOT NULL,
			artifact_type TEXT,
			artifact_name TEXT,
			run_id        TEXT,
			auth_user     TEXT,
			remote_addr   TEXT,
			status_code   INTEGER NOT NULL,
			status        TEXT NOT NULL,
			duration_ms   INTEGER NOT NULL,
			error_message TEXT,
			details_json  TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS flow_api_users (
			username      TEXT PRIMARY KEY,
			password_hash TEXT NOT NULL,
			permission    TEXT NOT NULL,
			is_active     INTEGER NOT NULL DEFAULT 1,
			description   TEXT,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, stmt := range ddlStatements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("failed to create table: %v\nSQL: %s", err, stmt)
		}
	}

	apiSrv := NewAPIServer(cfg, db, dialect)

	// Seed users for each role
	ctx := context.Background()
	users := []struct {
		user string
		pass string
		perm string
	}{
		{"admin_user", "admin_pass", PermAll},
		{"exec_user", "exec_pass", PermExecutionOnly},
		{"read_user", "read_pass", PermReadOnly},
		{"upload_user", "upload_pass", PermUpload},
		{"write_user", "write_pass", PermWrite},
	}
	for _, u := range users {
		if _, err := apiSrv.UpsertAPIUser(ctx, u.user, u.pass, u.perm, "test user "+u.user, true); err != nil {
			t.Fatalf("failed to seed user %s: %v", u.user, err)
		}
	}

	ts := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(func() {
		ts.Close()
		db.Close()
	})
	return apiSrv, ts, db
}

const samplePipelineXML = `<?xml version="1.0" encoding="UTF-8"?>
<Flow>
  <Node Name="HelloNode">
    <Script Language="go"><![CDATA[
package main
import "fmt"
func main() {
    fmt.Print("hello from HelloNode")
}
]]></Script>
  </Node>
</Flow>`

func TestRoleBasedPermissions(t *testing.T) {
	_, ts, _ := setupTestServer(t)
	client := ts.Client()

	// 1. Unauthenticated request -> 401
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/health", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/health failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", resp.StatusCode)
	}

	// 2. read_user (read-only) cannot upload or execute, but can GET artifacts/runs/events
	createBody := `{"name":"test_pipe","content":` + mustJSONString(samplePipelineXML) + `}`
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/artifacts/pipeline", strings.NewReader(createBody))
	req.SetBasicAuth("read_user", "read_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/artifacts/pipeline failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when read-only user attempts write, got %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/execute", strings.NewReader(`{"file_xml":`+mustJSONString(samplePipelineXML)+`}`))
	req.SetBasicAuth("read_user", "read_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/execute failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when read-only user attempts execution, got %d", resp.StatusCode)
	}

	// 3. upload_user (upload) can upload & write artifacts, cannot execute
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/artifacts/pipeline", strings.NewReader(createBody))
	req.SetBasicAuth("upload_user", "upload_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/artifacts/pipeline failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 when upload_user creates artifact, got %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/execute", strings.NewReader(`{"file":"test_pipe"}`))
	req.SetBasicAuth("upload_user", "upload_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/execute failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when upload_user attempts execute, got %d", resp.StatusCode)
	}

	// 4. exec_user (execution-only) CANNOT read artifacts directly or write, CAN execute
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/artifacts/pipeline/test_pipe", nil)
	req.SetBasicAuth("exec_user", "exec_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/artifacts/pipeline/test_pipe failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when exec_user attempts artifact read, got %d", resp.StatusCode)
	}

	// exec_user executes test_pipe with node_output enabled
	execPayload := `{"file":"test_pipe","node_output":true,"format":"json"}`
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/execute", strings.NewReader(execPayload))
	req.SetBasicAuth("exec_user", "exec_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/execute by exec_user failed: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when exec_user executes pipeline, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var execResp ExecutionResponse
	if err := json.Unmarshal(bodyBytes, &execResp); err != nil {
		t.Fatalf("failed to unmarshal ExecutionResponse: %v", err)
	}
	if len(execResp.Results) != 1 || !strings.Contains(execResp.Results[0].ResultsString, "hello from HelloNode") {
		t.Fatalf("unexpected node output in ExecutionResponse: %+v", execResp.Results)
	}

	// 5. read_user verifies telemetry events (including node_output) and API audit trail
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/logs/events?run_id="+execResp.RunID, nil)
	req.SetBasicAuth("read_user", "read_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/logs/events failed: %v", err)
	}
	eventsBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for GET /api/v1/logs/events, got %d: %s", resp.StatusCode, string(eventsBody))
	}
	if !strings.Contains(string(eventsBody), "hello from HelloNode") {
		t.Fatalf("expected pipeline_events to contain node_output 'hello from HelloNode', got: %s", string(eventsBody))
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/logs/api-events", nil)
	req.SetBasicAuth("read_user", "read_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/logs/api-events failed: %v", err)
	}
	auditBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for GET /api/v1/logs/api-events, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(auditBody), "pipeline.execute") || !strings.Contains(string(auditBody), "auth.forbidden") {
		t.Fatalf("expected audit events to contain pipeline.execute and auth.forbidden, got: %s", string(auditBody))
	}
}

func TestMultipartUploadAndEncryptionCRUD(t *testing.T) {
	_, ts, _ := setupTestServer(t)
	client := ts.Client()

	// Upload multipart file with encryption enabled using admin_user ('all' permission)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "uploaded_flow.xml")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := fw.Write([]byte(samplePipelineXML)); err != nil {
		t.Fatalf("Write form file failed: %v", err)
	}
	_ = mw.WriteField("encrypted", "true")
	_ = mw.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/artifacts/pipeline/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetBasicAuth("admin_user", "admin_pass")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("multipart upload failed: %v", err)
	}
	respBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from multipart upload, got %d: %s", resp.StatusCode, string(respBytes))
	}

	// Fetch decrypted artifact via ?decrypt=true
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/artifacts/pipeline/uploaded_flow?decrypt=true", nil)
	req.SetBasicAuth("admin_user", "admin_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET decrypted artifact failed: %v", err)
	}
	artBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for GET artifact, got %d: %s", resp.StatusCode, string(artBytes))
	}
	if !strings.Contains(string(artBytes), "HelloNode") {
		t.Fatalf("expected decrypted content to contain HelloNode, got: %s", string(artBytes))
	}

	// Execute encrypted pipeline from DB
	execReq := `{"file":"uploaded_flow","encrypted":true,"format":"json"}`
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/execute", strings.NewReader(execReq))
	req.SetBasicAuth("admin_user", "admin_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/execute encrypted failed: %v", err)
	}
	execBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 executing encrypted pipeline, got %d: %s", resp.StatusCode, string(execBytes))
	}
}

func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
