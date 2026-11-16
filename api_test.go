package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl-madness/flow"
	"github.com/microsoft/go-mssqldb/msdsn"
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
		`CREATE TABLE IF NOT EXISTS flow_api_groups (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
			description TEXT,
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS flow_api_user_groups (
			group_name TEXT NOT NULL COLLATE NOCASE,
			username   TEXT NOT NULL COLLATE NOCASE,
			added_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (group_name, username)
		)`,
		`CREATE TABLE IF NOT EXISTS flow_pipeline_groups (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			name         TEXT NOT NULL UNIQUE COLLATE NOCASE,
			description  TEXT,
			name_pattern TEXT,
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS flow_pipeline_group_members (
			group_name    TEXT NOT NULL COLLATE NOCASE,
			pipeline_name TEXT NOT NULL COLLATE NOCASE,
			added_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (group_name, pipeline_name)
		)`,
		`CREATE TABLE IF NOT EXISTS flow_pipeline_acl (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			principal_type TEXT NOT NULL COLLATE NOCASE,
			principal_name TEXT NOT NULL COLLATE NOCASE,
			target_type    TEXT NOT NULL COLLATE NOCASE,
			target_name    TEXT NOT NULL COLLATE NOCASE,
			permissions    TEXT NOT NULL,
			effect         TEXT NOT NULL DEFAULT 'allow' COLLATE NOCASE,
			description    TEXT,
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (principal_type, principal_name, target_type, target_name, effect)
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

	// 1. Unauthenticated GET /api/v1/health -> 200 (public), while protected route -> 401
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/health", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/health failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for unauthenticated GET /api/v1/health, got %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/artifacts/pipeline", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/artifacts/pipeline failed: %v", err)
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

	// 4. exec_user (execution-only) CAN read artifacts and execute, CANNOT write/upload
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/artifacts/pipeline/test_pipe", nil)
	req.SetBasicAuth("exec_user", "exec_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/artifacts/pipeline/test_pipe failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when exec_user reads artifact, got %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/artifacts/pipeline", strings.NewReader(createBody))
	req.SetBasicAuth("exec_user", "exec_pass")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/artifacts/pipeline by exec_user failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when exec_user attempts artifact write, got %d", resp.StatusCode)
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

func TestSwaggerAndBootstrap(t *testing.T) {
	_, ts, _ := setupTestServer(t)
	client := ts.Client()

	// 1. Verify /swagger/ serves Swagger UI HTML without requiring prior BasicAuth
	resp, err := client.Get(ts.URL + "/swagger/")
	if err != nil {
		t.Fatalf("GET /swagger/ failed: %v", err)
	}
	htmlBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for GET /swagger/, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(htmlBytes), "SwaggerUIBundle") || !strings.Contains(string(htmlBytes), "/swagger/doc.json") {
		t.Fatalf("expected Swagger UI HTML to reference SwaggerUIBundle and /swagger/doc.json")
	}

	// 2. Verify /swagger/doc.json and /api/v1/openapi.json return valid OpenAPI 3.0.3 JSON
	for _, specPath := range []string{"/swagger/doc.json", "/swagger/openapi.json", "/api/v1/openapi.json"} {
		specResp, err := client.Get(ts.URL + specPath)
		if err != nil {
			t.Fatalf("GET %s failed: %v", specPath, err)
		}
		specBytes, _ := io.ReadAll(specResp.Body)
		specResp.Body.Close()
		if specResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for GET %s, got %d", specPath, specResp.StatusCode)
		}
		var parsed map[string]any
		if err := json.Unmarshal(specBytes, &parsed); err != nil {
			t.Fatalf("failed unmarshaling OpenAPI JSON from %s: %v", specPath, err)
		}
		if parsed["openapi"] != "3.0.3" {
			t.Fatalf("expected openapi 3.0.3, got %v", parsed["openapi"])
		}
		paths, ok := parsed["paths"].(map[string]any)
		if !ok || paths["/api/v1/execute"] == nil || paths["/api/v1/users"] == nil || paths["/api/v1/upload/{type}"] == nil {
			t.Fatalf("expected OpenAPI paths to include /api/v1/execute, /api/v1/users, and /api/v1/upload/{type}")
		}
	}

	// 3. Verify LoadStartupConfig parses -bootstrap-user, -bootstrap-pass, and defaults -bootstrap-perm to "all"
	cfg, err := LoadStartupConfig([]string{
		"-bootstrap-user", "initial_admin",
		"-bootstrap-pass", "StrongBootstrapPass!123",
	})
	if err != nil {
		t.Fatalf("LoadStartupConfig failed: %v", err)
	}
	if cfg.BootstrapUser != "initial_admin" || cfg.BootstrapPass != "StrongBootstrapPass!123" || cfg.BootstrapPerm != PermAll {
		t.Fatalf("unexpected bootstrap config: %+v", cfg)
	}
}

func TestExecuteAndStreamByArtifactTypeAndTableID(t *testing.T) {
	_, ts, _ := setupTestServer(t)
	client := ts.Client()

	createArtifact := func(artType, name, content string) ArtifactRecord {
		t.Helper()
		payload := fmt.Sprintf(`{"name":%s,"content":%s,"encrypted":false,"upsert":true}`, mustJSONString(name), mustJSONString(content))
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/artifacts/"+artType, strings.NewReader(payload))
		req.SetBasicAuth("admin_user", "admin_pass")
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/v1/artifacts/%s failed: %v", artType, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 creating %s artifact, got %d: %s", artType, resp.StatusCode, string(body))
		}
		var rec ArtifactRecord
		if err := json.Unmarshal(body, &rec); err != nil {
			t.Fatalf("unmarshal artifact record failed: %v", err)
		}
		if rec.ID <= 0 {
			t.Fatalf("expected positive database table ID for %s artifact, got %d", artType, rec.ID)
		}
		return rec
	}

	// 1. Create pipeline, config, and options artifacts and capture their database table IDs
	pipeRec := createArtifact("pipeline", "id_test_pipe", samplePipelineXML)
	cfgRec := createArtifact("config", "id_test_cfg", samplePipelineXML)
	optionsXML := fmt.Sprintf(
		`<cli_options><file>id:%d</file><format>json</format><node_output>true</node_output></cli_options>`,
		pipeRec.ID,
	)
	optRec := createArtifact("options", "id_test_opt", optionsXML)

	// 2. Verify GET /api/v1/artifacts/{type}/id/{id} returns the record by table ID
	getReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/artifacts/pipeline/id/%d", ts.URL, pipeRec.ID), nil)
	getReq.SetBasicAuth("read_user", "read_pass")
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("GET artifact by ID failed: %v", err)
	}
	getBytes, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK || !strings.Contains(string(getBytes), "id_test_pipe") {
		t.Fatalf("expected 200 and id_test_pipe from GET by ID, got %d: %s", getResp.StatusCode, string(getBytes))
	}

	// 3. Execute synchronously via POST /api/v1/execute/pipeline/{id}
	execPipeReq, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/execute/pipeline/%d?format=json&node_output=true", ts.URL, pipeRec.ID), nil)
	execPipeReq.SetBasicAuth("exec_user", "exec_pass")
	execPipeResp, err := client.Do(execPipeReq)
	if err != nil {
		t.Fatalf("POST /api/v1/execute/pipeline/%d failed: %v", pipeRec.ID, err)
	}
	execPipeBytes, _ := io.ReadAll(execPipeResp.Body)
	execPipeResp.Body.Close()
	if execPipeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /api/v1/execute/pipeline/%d, got %d: %s", pipeRec.ID, execPipeResp.StatusCode, string(execPipeBytes))
	}
	var execResult ExecutionResponse
	if err := json.Unmarshal(execPipeBytes, &execResult); err != nil {
		t.Fatalf("unmarshal ExecutionResponse failed: %v", err)
	}
	if execResult.Status != "succeeded" {
		t.Fatalf("expected succeeded executing pipeline by ID, got %+v", execResult)
	}

	// 4. Execute synchronously via POST /api/v1/execute/options/{id}
	execOptReq, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/execute/options/%d", ts.URL, optRec.ID), nil)
	execOptReq.SetBasicAuth("exec_user", "exec_pass")
	execOptResp, err := client.Do(execOptReq)
	if err != nil {
		t.Fatalf("POST /api/v1/execute/options/%d failed: %v", optRec.ID, err)
	}
	execOptBytes, _ := io.ReadAll(execOptResp.Body)
	execOptResp.Body.Close()
	if execOptResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /api/v1/execute/options/%d, got %d: %s", optRec.ID, execOptResp.StatusCode, string(execOptBytes))
	}

	// 5. Execute synchronously via POST /api/v1/execute/config/{id}
	execCfgReq, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/execute/config/%d?format=json", ts.URL, cfgRec.ID), nil)
	execCfgReq.SetBasicAuth("exec_user", "exec_pass")
	execCfgResp, err := client.Do(execCfgReq)
	if err != nil {
		t.Fatalf("POST /api/v1/execute/config/%d failed: %v", cfgRec.ID, err)
	}
	execCfgBytes, _ := io.ReadAll(execCfgResp.Body)
	execCfgResp.Body.Close()
	if execCfgResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /api/v1/execute/config/%d, got %d: %s", cfgRec.ID, execCfgResp.StatusCode, string(execCfgBytes))
	}

	// 6. Stream SSE events via POST /api/v1/execute/stream/pipeline/{id} and GET /api/v1/execute/options/{id}/stream
	streamURLs := []string{
		fmt.Sprintf("%s/api/v1/execute/stream/pipeline/%d", ts.URL, pipeRec.ID),
		fmt.Sprintf("%s/api/v1/execute/options/%d/stream", ts.URL, optRec.ID),
		fmt.Sprintf("%s/api/v1/execution/stream/pipeline/%d", ts.URL, pipeRec.ID),
	}
	for _, sURL := range streamURLs {
		sReq, _ := http.NewRequest(http.MethodPost, sURL, strings.NewReader(`{"node_output":true}`))
		sReq.SetBasicAuth("exec_user", "exec_pass")
		sReq.Header.Set("Content-Type", "application/json")
		sResp, err := client.Do(sReq)
		if err != nil {
			t.Fatalf("stream request %s failed: %v", sURL, err)
		}
		sBytes, _ := io.ReadAll(sResp.Body)
		sResp.Body.Close()
		if sResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 from stream %s, got %d: %s", sURL, sResp.StatusCode, string(sBytes))
		}
		sseText := string(sBytes)
		if !strings.Contains(sseText, "event: node_event") || !strings.Contains(sseText, "event: done") {
			t.Fatalf("expected SSE stream from %s to contain node_event and done, got:\n%s", sURL, sseText)
		}
	}
}

func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestDatabaseSink_PerNodeSessionIdentityAndCrossDB(t *testing.T) {
	_, _, db := setupTestServer(t)
	ctx := context.Background()

	sink := NewDatabaseSink(db, "sqlite", false, false)
	sink.SetSessionIdentity(1000, "log_user", "log_app")
	sink.SetDBSessionIdentity("source_db", 2000, "src_user", "src_app")
	sink.SetDBSessionIdentity("target_db", 3000, "tgt_user", "tgt_app")

	nodes := []flow.PipelineNode{
		{
			Kind: flow.NodeSQL,
			SQL: &flow.SQLElement{
				ID:     "fetch_step",
				DBName: "source_db",
			},
		},
		{
			Kind: flow.NodeSQL,
			SQL: &flow.SQLElement{
				ID:     "insert_step",
				DBName: "target_db",
			},
		},
		{
			Kind: flow.NodeSQLBulk,
			SQLBulk: &flow.SQLBulkElement{
				ID:       "cross_db_bulk",
				DBName:   "source_db",
				TargetDB: "target_db",
			},
		},
		{
			Kind: flow.NodeSQLBulk,
			SQLBulk: &flow.SQLBulkElement{
				ID:       "same_db_bulk",
				DBName:   "source_db",
				TargetDB: "source_db",
			},
		},
		{
			Kind: flow.NodeScript,
			Script: &flow.ScriptItem{
				ID: "transform_script",
			},
		},
		{
			Kind:        flow.NodeGroup,
			GroupID:     "tx_group",
			Transaction: true,
			DBName:      "source_db",
		},
	}
	sink.RegisterNodes(nodes)

	now := time.Now().UTC()
	events := []flow.ExecutionEvent{
		{
			RunID:      "run-ident-1",
			Sequence:   1,
			OccurredAt: now,
			Type:       flow.EventRunStarted,
		},
		{
			RunID:      "run-ident-1",
			Sequence:   2,
			OccurredAt: now,
			Type:       flow.EventNodeFinished,
			NodeKind:   "sql",
			NodeID:     "fetch_step",
			NodePath:   "sql[0]",
			Status:     flow.RunStatusSucceeded,
		},
		{
			RunID:      "run-ident-1",
			Sequence:   3,
			OccurredAt: now,
			Type:       flow.EventNodeFinished,
			NodeKind:   "sql",
			NodeID:     "insert_step",
			NodePath:   "sql[1]",
			Status:     flow.RunStatusSucceeded,
		},
		{
			RunID:      "run-ident-1",
			Sequence:   4,
			OccurredAt: now,
			Type:       flow.EventNodeFinished,
			NodeKind:   "sql_bulk",
			NodeID:     "cross_db_bulk",
			NodePath:   "sql_bulk[2]",
			Status:     flow.RunStatusSucceeded,
		},
		{
			RunID:      "run-ident-1",
			Sequence:   5,
			OccurredAt: now,
			Type:       flow.EventNodeFinished,
			NodeKind:   "sql_bulk",
			NodeID:     "same_db_bulk",
			NodePath:   "sql_bulk[3]",
			Status:     flow.RunStatusSucceeded,
		},
		{
			RunID:      "run-ident-1",
			Sequence:   6,
			OccurredAt: now,
			Type:       flow.EventNodeFinished,
			NodeKind:   "script",
			NodeID:     "transform_script",
			NodePath:   "script[4]",
			Status:     flow.RunStatusSucceeded,
		},
		{
			RunID:      "run-ident-1",
			Sequence:   7,
			OccurredAt: now,
			Type:       flow.EventNodeFinished,
			NodeKind:   "group",
			NodeID:     "tx_group",
			NodePath:   "group[5]",
			Status:     flow.RunStatusSucceeded,
		},
	}

	for _, ev := range events {
		if err := sink.Emit(ctx, ev); err != nil {
			t.Fatalf("Emit failed for seq %d: %v", ev.Sequence, err)
		}
	}

	type evtRow struct {
		seq     int
		nodeID  string
		dbUser  string
		spid    string
		appName string
	}
	rows, err := db.QueryContext(ctx, "SELECT sequence_num, node_id, db_user_name, spid, application_name FROM pipeline_events WHERE run_id = 'run-ident-1' ORDER BY sequence_num ASC")
	if err != nil {
		t.Fatalf("failed querying pipeline_events: %v", err)
	}
	defer rows.Close()

	var recorded []evtRow
	for rows.Next() {
		var r evtRow
		if err := rows.Scan(&r.seq, &r.nodeID, &r.dbUser, &r.spid, &r.appName); err != nil {
			t.Fatalf("failed scanning row: %v", err)
		}
		recorded = append(recorded, r)
	}

	if len(recorded) != 7 {
		t.Fatalf("expected 7 events, got %d", len(recorded))
	}

	if recorded[0].spid != "1000" || recorded[0].appName != "log_app" || recorded[0].dbUser != "log_user" {
		t.Errorf("event 1 (run.started) expected log_db identity, got %+v", recorded[0])
	}
	if recorded[1].spid != "2000" || recorded[1].appName != "src_app" || recorded[1].dbUser != "src_user" {
		t.Errorf("event 2 (fetch_step) expected source_db identity, got %+v", recorded[1])
	}
	if recorded[2].spid != "3000" || recorded[2].appName != "tgt_app" || recorded[2].dbUser != "tgt_user" {
		t.Errorf("event 3 (insert_step) expected target_db identity, got %+v", recorded[2])
	}
	if recorded[3].spid != "2000->3000" || recorded[3].appName != "tgt_app" || recorded[3].dbUser != "src_user->tgt_user" {
		t.Errorf("event 4 (cross_db_bulk) expected target appName ('tgt_app') and cross-db spid/user, got %+v", recorded[3])
	}
	if recorded[4].spid != "2000" || recorded[4].appName != "src_app" || recorded[4].dbUser != "src_user" {
		t.Errorf("event 5 (same_db_bulk) expected source_db identity ('src_app'), got %+v", recorded[4])
	}
	if recorded[5].spid != "1000" || recorded[5].appName != "log_app" || recorded[5].dbUser != "log_user" {
		t.Errorf("event 6 (transform_script) expected log_db fallback, got %+v", recorded[5])
	}
	if recorded[6].spid != "1000" || recorded[6].appName != "log_app" || recorded[6].dbUser != "log_user" {
		t.Errorf("event 7 (tx_group) expected log_db identity for non-sql/sql_bulk node, got %+v", recorded[6])
	}
}




func doAuthJSON(t *testing.T, client *http.Client, method, url, username, password string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		switch v := body.(type) {
		case string:
			reader = strings.NewReader(v)
		case []byte:
			reader = bytes.NewReader(v)
		default:
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("failed marshaling body: %v", err)
			}
			reader = bytes.NewReader(b)
		}
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestCryptoEndpointsComprehensive(t *testing.T) {
	_, ts, _ := setupTestServer(t)
	client := ts.Client()

	// 1. Method not allowed & unknown crypto endpoint
	code, _ := doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/crypto/keygen", "read_user", "read_pass", nil, nil)
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET crypto, got %d", code)
	}
	code, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/unknown", "read_user", "read_pass", `{}`, nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown crypto endpoint, got %d", code)
	}

	// Invalid JSON bodies across all 5 endpoints
	for _, ep := range []string{"keygen", "encrypt", "decrypt", "sign", "verify"} {
		c, _ := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/"+ep, "read_user", "read_pass", `{invalid`, nil)
		if c != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid JSON on %s, got %d", ep, c)
		}
	}

	// 2. Keygen across all algorithms (AES, PGP, RSA-2048, ECDSA-P256, ECDSA-P384, ED25519) + GenerateCert
	var aesKey string
	{
		c, b := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/keygen", "read_user", "read_pass", map[string]any{
			"algorithm":   "AES-256",
			"byte_length": 32,
		}, nil)
		if c != http.StatusOK {
			t.Fatalf("AES keygen failed (%d): %s", c, string(b))
		}
		var kr cryptoKeygenResponse
		_ = json.Unmarshal(b, &kr)
		if kr.SecureKey == "" || kr.Algorithm != "AES-256-GCM" {
			t.Fatalf("unexpected AES keygen response: %+v", kr)
		}
		aesKey = kr.SecureKey
	}

	var pgpPriv, pgpPub string
	{
		c, b := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/keygen", "read_user", "read_pass", map[string]any{
			"algorithm": "PGP",
		}, nil)
		if c != http.StatusOK {
			t.Fatalf("PGP keygen failed (%d): %s", c, string(b))
		}
		var kr cryptoKeygenResponse
		_ = json.Unmarshal(b, &kr)
		pgpPriv, pgpPub = kr.PrivateKeyPEM, kr.PublicKeyPEM
		if pgpPriv == "" || pgpPub == "" || kr.Fingerprint == "" {
			t.Fatalf("unexpected PGP keygen response: %+v", kr)
		}
	}

	var rsaPriv, rsaPub, rsaCert string
	for _, alg := range []string{"", "ECDSA-P256", "ECDSA-P384", "ED25519"} {
		c, b := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/keygen", "read_user", "read_pass", map[string]any{
			"algorithm":     alg,
			"common_name":   "Test Signer",
			"validity_days": 30,
			"generate_cert": true,
		}, nil)
		if c != http.StatusOK {
			t.Fatalf("keygen(%q) failed (%d): %s", alg, c, string(b))
		}
		var kr cryptoKeygenResponse
		_ = json.Unmarshal(b, &kr)
		if kr.PrivateKeyPEM == "" || kr.PublicKeyPEM == "" || kr.CertificatePEM == "" {
			t.Fatalf("expected keys and cert for alg %q", alg)
		}
		if alg == "" {
			rsaPriv, rsaPub, rsaCert = kr.PrivateKeyPEM, kr.PublicKeyPEM, kr.CertificatePEM
		}
	}

	// 3. Encrypt & Decrypt
	c, encBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/encrypt", "read_user", "read_pass", map[string]any{
		"content":    samplePipelineXML,
		"secure_key": aesKey,
	}, nil)
	if c != http.StatusOK {
		t.Fatalf("encrypt failed (%d): %s", c, string(encBytes))
	}
	var encMap map[string]any
	_ = json.Unmarshal(encBytes, &encMap)
	cipherText, _ := encMap["content"].(string)

	c, decBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/decrypt", "read_user", "read_pass", map[string]any{
		"content": cipherText,
	}, map[string]string{"X-Flow-Secure-Key": aesKey})
	if c != http.StatusOK {
		t.Fatalf("decrypt failed (%d): %s", c, string(decBytes))
	}
	if !strings.Contains(string(decBytes), "HelloNode") {
		t.Fatalf("decrypted text missing HelloNode: %s", string(decBytes))
	}

	// Decrypt error with bad key
	c, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/decrypt", "read_user", "read_pass", map[string]any{
		"content":    cipherText,
		"secure_key": "wrong-key-value",
	}, nil)
	if c != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad decrypt key, got %d", c)
	}

	// 4. Sign & Verify: OPENSSL (wrapped, detached, encrypt-then-sign, sign-then-encrypt), PKCS7, PGP
	// Missing content or private key
	c, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/sign", "read_user", "read_pass", map[string]any{"content": ""}, nil)
	if c != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty sign content, got %d", c)
	}
	c, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/sign", "read_user", "read_pass", map[string]any{"content": "abc"}, nil)
	if c != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing private key, got %d", c)
	}

	// 4a. OPENSSL sign with encrypt-then-sign & wrap=true
	c, signRespBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/sign", "read_user", "read_pass", map[string]any{
		"content":         samplePipelineXML,
		"scheme":          "OPENSSL",
		"private_key_pem": rsaPriv,
		"wrap":            true,
		"encrypt_mode":    "encrypt-then-sign",
		"secure_key":      aesKey,
	}, nil)
	if c != http.StatusOK {
		t.Fatalf("OPENSSL encrypt-then-sign failed (%d): %s", c, string(signRespBytes))
	}
	var srOpenSSL cryptoSignResponse
	_ = json.Unmarshal(signRespBytes, &srOpenSSL)

	// Verify wrapped without public key -> returns Valid:false with message
	c, verNoKeyBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/verify", "read_user", "read_pass", map[string]any{
		"content":    srOpenSSL.SignedPayload,
		"secure_key": aesKey,
	}, nil)
	if c != http.StatusOK || !strings.Contains(string(verNoKeyBytes), "no public_key_pem") {
		t.Fatalf("expected unwrapped message when no key provided, got (%d): %s", c, string(verNoKeyBytes))
	}

	// Verify wrapped with public key
	c, verOpenSSLBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/verify", "read_user", "read_pass", map[string]any{
		"content":        srOpenSSL.SignedPayload,
		"public_key_pem": rsaPub,
		"secure_key":     aesKey,
	}, nil)
	if c != http.StatusOK {
		t.Fatalf("verify OPENSSL failed (%d): %s", c, string(verOpenSSLBytes))
	}
	var vrOpenSSL cryptoVerifyResponse
	_ = json.Unmarshal(verOpenSSLBytes, &vrOpenSSL)
	if !vrOpenSSL.Valid || !vrOpenSSL.Encrypted || !strings.Contains(vrOpenSSL.UnwrappedContent, "HelloNode") {
		t.Fatalf("unexpected verify response: %+v", vrOpenSSL)
	}

	// 4b. PKCS7 sign-then-encrypt & verify with cert_pem + ca_cert_pem
	// First check PKCS7 missing cert_pem returns 400
	c, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/sign", "read_user", "read_pass", map[string]any{
		"content":         samplePipelineXML,
		"scheme":          "PKCS7",
		"private_key_pem": rsaPriv,
	}, nil)
	if c != http.StatusBadRequest {
		t.Fatalf("expected 400 when PKCS7 missing cert_pem, got %d", c)
	}

	c, pkcs7Bytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/sign", "read_user", "read_pass", map[string]any{
		"content":         samplePipelineXML,
		"scheme":          "PKCS7",
		"private_key_pem": rsaPriv,
		"cert_pem":        rsaCert,
		"wrap":            true,
		"encrypt_mode":    "sign-then-encrypt",
		"secure_key":      aesKey,
	}, nil)
	if c != http.StatusOK {
		t.Fatalf("PKCS7 sign-then-encrypt failed (%d): %s", c, string(pkcs7Bytes))
	}
	var srPKCS7 cryptoSignResponse
	_ = json.Unmarshal(pkcs7Bytes, &srPKCS7)

	c, verPKCS7Bytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/verify", "read_user", "read_pass", map[string]any{
		"content":     srPKCS7.SignedPayload,
		"cert_pem":    rsaCert,
		"ca_cert_pem": rsaCert,
		"secure_key":  aesKey,
	}, nil)
	if c != http.StatusOK {
		t.Fatalf("verify PKCS7 failed (%d): %s", c, string(verPKCS7Bytes))
	}
	var vrPKCS7 cryptoVerifyResponse
	_ = json.Unmarshal(verPKCS7Bytes, &vrPKCS7)
	if !vrPKCS7.Valid || vrPKCS7.Subject == "" || vrPKCS7.Fingerprint == "" {
		t.Fatalf("expected valid PKCS7 verification with certificate metadata, got: %+v", vrPKCS7)
	}

	// 4c. PGP sign (wrap=false, detached) & verify with keyring_pem
	c, pgpSignBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/sign", "read_user", "read_pass", map[string]any{
		"content":         samplePipelineXML,
		"scheme":          "PGP",
		"private_key_pem": pgpPriv,
		"wrap":            false,
	}, nil)
	if c != http.StatusOK {
		t.Fatalf("PGP sign failed (%d): %s", c, string(pgpSignBytes))
	}
	var srPGP cryptoSignResponse
	_ = json.Unmarshal(pgpSignBytes, &srPGP)

	c, verPGPBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/verify", "read_user", "read_pass", map[string]any{
		"content":     samplePipelineXML,
		"signature":   srPGP.DetachedSignature,
		"keyring_pem": pgpPub,
	}, nil)
	if c != http.StatusOK {
		t.Fatalf("PGP verify failed (%d): %s", c, string(verPGPBytes))
	}
	var vrPGP cryptoVerifyResponse
	_ = json.Unmarshal(verPGPBytes, &vrPGP)
	if !vrPGP.Valid {
		t.Fatalf("expected valid PGP detached verification, got: %+v", vrPGP)
	}

	// Verify unsigned payload without signature -> Valid:false
	c, verUnsigned := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/verify", "read_user", "read_pass", map[string]any{
		"content": "plain unsigned content",
	}, nil)
	if c != http.StatusOK || !strings.Contains(string(verUnsigned), "not wrapped") {
		t.Fatalf("unexpected unsigned verify response (%d): %s", c, string(verUnsigned))
	}

	// Verify empty content -> 400
	c, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/crypto/verify", "read_user", "read_pass", map[string]any{
		"content": "",
	}, nil)
	if c != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty verify content, got %d", c)
	}
}

func TestAuthAndWindowsIdentityComprehensive(t *testing.T) {
	apiSrv, ts, _ := setupTestServer(t)
	client := ts.Client()
	ctx := context.Background()

	// 1. Permission ranking, token normalization, validation, and VerifyPassword variants
	for _, perm := range []string{"all", "admin", "*", "full", "write", "editor", "upload", "upload-only", "execution-only", "runner", "read-only", "viewer", "read-only,execution-only"} {
		if err := ValidatePermissionString(perm); err != nil {
			t.Fatalf("expected %q to be valid, got: %v", perm, err)
		}
		r := permRank(perm)
		if r <= 0 || rankToPerm(r) == "" {
			t.Fatalf("expected positive rank for %q, got %d", perm, r)
		}
	}
	if rankToPerm(0) != PermReadOnly {
		t.Fatalf("expected default rankToPerm(0) to be read-only")
	}
	if err := ValidatePermissionString(""); err == nil {
		t.Fatalf("expected error for empty permission")
	}
	if err := ValidatePermissionString("invalid-perm"); err == nil {
		t.Fatalf("expected error for invalid-perm")
	}

	// VerifyPassword with SHA256, plaintext, bcrypt, and WINDOWS marker
	sum := sha256.Sum256([]byte("secret123"))
	shaHash := "sha256:" + hex.EncodeToString(sum[:])
	if !VerifyPassword(shaHash, "secret123") || VerifyPassword(shaHash, "wrong") {
		t.Fatalf("SHA256 VerifyPassword failed")
	}
	if !VerifyPassword("plain_secret", "plain_secret") || VerifyPassword("plain_secret", "wrong") {
		t.Fatalf("plaintext VerifyPassword failed")
	}
	if VerifyPassword("WINDOWS", "WINDOWS") || VerifyPassword(WindowsIntegratedAuthMarker, "any") || VerifyPassword("", "any") {
		t.Fatalf("expected false for WINDOWS/empty stored hash")
	}

	// parseDBBool variants
	for _, v := range []any{true, int64(1), int(1), []byte("true"), "yes", "1", "t", "y"} {
		if !parseDBBool(v) {
			t.Fatalf("expected parseDBBool(%v) == true", v)
		}
	}
	for _, v := range []any{false, int64(0), int(0), []byte("0"), "no", nil, 3.14} {
		if parseDBBool(v) {
			t.Fatalf("expected parseDBBool(%v) == false", v)
		}
	}

	// 2. Windows groups header parsing (CSV, JSON base64, raw base64)
	if g := parseWindowsGroupsHeader(""); g != nil {
		t.Fatalf("expected nil for empty groups header")
	}
	gCSV := parseWindowsGroupsHeader("CORP\\Admins, CORP\\ETL_Runners;Group3\nGroup4")
	if len(gCSV) != 4 {
		t.Fatalf("expected 4 groups from CSV header, got %v", gCSV)
	}
	b64JSON := base64.StdEncoding.EncodeToString([]byte(`["CORP\\Developers","CORP\\Admins"]`))
	if gJSON := parseWindowsGroupsHeader(b64JSON); len(gJSON) != 2 {
		t.Fatalf("expected 2 groups from base64 JSON, got %v", gJSON)
	}
	b64Raw := base64.StdEncoding.EncodeToString([]byte("CORP\\GroupA,CORP\\GroupB"))
	if gRaw := parseWindowsGroupsHeader(b64Raw); len(gRaw) != 2 {
		t.Fatalf("expected 2 groups from base64 text, got %v", gRaw)
	}

	// 3. Container/auto user detection & resolveWindowsUserAndGroups
	for _, u := range []string{"", WindowsAutoHostUserMarker, "app", "root", `container\app`, `/root`} {
		if !isContainerOrAutoUser(u) {
			t.Fatalf("expected %q to be detected as container/auto user", u)
		}
	}
	if isContainerOrAutoUser(`CORP\alice`) {
		t.Fatalf("expected CORP\\alice not to be container user")
	}

	t.Setenv("FLOW_WINDOWS_USER", `CORP\env_win_user`)
	t.Setenv("FLOW_WINDOWS_GROUPS", `RSYSLAB\Domain Users,RSYSLAB\ETL_Operators`)
	groupsFile := filepath.Join(t.TempDir(), "ad-groups.txt")
	_ = os.WriteFile(groupsFile, []byte("RSYSLAB\\AD_Sidecar_Admins\nRSYSLAB\\Data_Engineers\n"), 0600)
	t.Setenv("FLOW_WINDOWS_GROUPS_FILE", groupsFile)
	resolvedU, resolvedG := resolveWindowsUserAndGroups(WindowsAutoHostUserMarker, []string{"CORP\\Group1", "CORP\\group1", ""})
	if resolvedU != `CORP\env_win_user` || len(resolvedG) < 5 {
		t.Fatalf("unexpected resolved user/groups: %q, %v", resolvedU, resolvedG)
	}
	t.Setenv("FLOW_WINDOWS_USER", "")
	t.Setenv("FLOW_WINDOWS_GROUPS", "")
	t.Setenv("FLOW_WINDOWS_GROUPS_FILE", "")
	t.Setenv("HOST_USERNAME", "host_bob")
	t.Setenv("HOST_USERDOMAIN", "CORPDOM")
	resolvedU2, _ := resolveWindowsUserAndGroups("root", nil)
	if resolvedU2 != `CORPDOM\host_bob` {
		t.Fatalf("expected CORPDOM\\host_bob, got %q", resolvedU2)
	}
	t.Setenv("HOST_USERNAME", "")
	t.Setenv("HOST_USERDOMAIN", "")

	// Also test current OS user resolution
	if curr, err := user.Current(); err == nil && curr != nil {
		_, _ = resolveWindowsUserAndGroups(curr.Username, nil)
	}
	_ = lookupGroupSidQualified("S-1-1-0") // Everyone SID on Windows
	_ = lookupGroupSidQualified("invalid-sid")

	// 4. Register Windows user & AD group in TableAPIUsers and authenticate via headers & BasicAuth
	_, err := apiSrv.UpsertAPIUser(ctx, `CORP\ETL_Admins`, "WINDOWS", "all", "AD Group", true)
	if err != nil {
		t.Fatalf("UpsertAPIUser AD group failed: %v", err)
	}
	_, err = apiSrv.UpsertAPIUser(ctx, `CORP\disabled_win`, "WINDOWS", "read-only", "Disabled AD User", false)
	if err != nil {
		t.Fatalf("UpsertAPIUser disabled user failed: %v", err)
	}
	_, err = apiSrv.UpsertAPIUser(ctx, `local_win_pass`, "local_pass_123", "write", "Local user with password", true)
	if err != nil {
		t.Fatalf("UpsertAPIUser local_win_pass failed: %v", err)
	}

	// Authenticate via AuthenticateWindowsIdentity
	winU, err := apiSrv.AuthenticateWindowsIdentity(ctx, `CORP\jane`, []string{`ETL_Admins`})
	if err != nil || winU == nil || winU.Permission != PermAll {
		t.Fatalf("AuthenticateWindowsIdentity via group failed: %v, %+v", err, winU)
	}

	// Disabled Windows user
	if _, err := apiSrv.AuthenticateWindowsIdentity(ctx, `CORP\disabled_win`, nil); err == nil {
		t.Fatalf("expected error for disabled Windows user")
	}

	// Unregistered Windows user
	if _, err := apiSrv.AuthenticateWindowsIdentity(ctx, `CORP\stranger`, []string{`CORP\UnknownGroup`}); err == nil {
		t.Fatalf("expected error for unregistered Windows user")
	}

	// Local user with password via AuthenticateWindowsIdentityWithPassword
	if _, err := apiSrv.AuthenticateWindowsIdentityWithPassword(ctx, "local_win_pass", "wrong_pass", nil); err == nil {
		t.Fatalf("expected error for wrong password on local_win_pass")
	}
	if u, err := apiSrv.AuthenticateWindowsIdentityWithPassword(ctx, "local_win_pass", "local_pass_123", nil); err != nil || u == nil {
		t.Fatalf("expected success for local_win_pass, got %v", err)
	}

	// HTTP request with X-Flow-Windows-User and X-Flow-Windows-Groups headers
	code, healthBytes := doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/health", "", "", nil, map[string]string{
		HeaderWindowsUser:   `CORP\jane`,
		HeaderWindowsGroups: `CORP\ETL_Admins`,
	})
	if code != http.StatusOK || !strings.Contains(string(healthBytes), `CORP\\jane`) {
		t.Fatalf("expected health check to report CORP\\jane, got (%d): %s", code, string(healthBytes))
	}

	// HTTP request with BasicAuth using WindowsIntegratedAuthMarker
	code, _ = doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/users", `CORP\jane`, WindowsIntegratedAuthMarker, nil, map[string]string{
		HeaderWindowsGroups: `CORP\ETL_Admins`,
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200 for /api/v1/users with WindowsIntegratedAuthMarker, got %d", code)
	}

	// HTTP request with DOMAIN\user and regular password fallback in AuthenticateRequest
	_, _ = apiSrv.UpsertAPIUser(ctx, `CORP\direct_domain_user`, "WINDOWS", "read-only", "Direct domain user", true)
	code, _ = doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/runs", `CORP\direct_domain_user`, "any_domain_pass", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for direct_domain_user fallback, got %d", code)
	}
}

func TestStoreHandlersAndUsersCRUD(t *testing.T) {
	apiSrv, ts, _ := setupTestServer(t)
	client := ts.Client()

	// Verify Router() alias
	if apiSrv.Router() == nil {
		t.Fatalf("expected Router() to return non-nil handler")
	}

	// 1. DecodeUploadedBytes: UTF-8 BOM, UTF-16LE, UTF-16BE
	utf8BOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte("<Flow/>")...)
	if got := DecodeUploadedBytes(utf8BOM); got != "<Flow/>" {
		t.Fatalf("UTF-8 BOM decode failed: %q", got)
	}
	utf16LE := []byte{0xFF, 0xFE, '<', 0, 'F', 0, 'l', 0, 'o', 0, 'w', 0, '/', 0, '>', 0}
	if got := DecodeUploadedBytes(utf16LE); got != "<Flow/>" {
		t.Fatalf("UTF-16LE decode failed: %q", got)
	}
	utf16BE := []byte{0xFE, 0xFF, 0, '<', 0, 'F', 0, 'l', 0, 'o', 0, 'w', 0, '/', 0, '>'}
	if got := DecodeUploadedBytes(utf16BE); got != "<Flow/>" {
		t.Fatalf("UTF-16BE decode failed: %q", got)
	}

	// 2. Raw upload via /api/v1/upload/pipeline
	code, upBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/upload/pipeline?name=raw_uploaded&description=raw_desc", "upload_user", "upload_pass", samplePipelineXML, nil)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 for raw upload, got %d: %s", code, string(upBytes))
	}
	var upRec ArtifactRecord
	_ = json.Unmarshal(upBytes, &upRec)

	// Upload method not allowed & invalid type
	if c, _ := doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/upload/pipeline", "upload_user", "upload_pass", nil, nil); c != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /api/v1/upload/pipeline, got %d", c)
	}
	if c, _ := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/upload/invalid_type", "upload_user", "upload_pass", samplePipelineXML, nil); c != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid upload type, got %d", c)
	}

	// 3. ListArtifacts (with and without include_content)
	code, listBytes := doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/artifacts/pipeline?include_content=true", "read_user", "read_pass", nil, nil)
	if code != http.StatusOK || !strings.Contains(string(listBytes), "raw_uploaded") {
		t.Fatalf("list artifacts failed (%d): %s", code, string(listBytes))
	}

	// Duplicate Create (409 Conflict)
	code, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/artifacts/pipeline", "write_user", "write_pass", map[string]any{
		"name":        "raw_uploaded",
		"xml_content": samplePipelineXML,
	}, nil)
	if code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for duplicate create, got %d", code)
	}

	// 4. UpdateArtifact by name & by ID
	code, _ = doAuthJSON(t, client, http.MethodPut, ts.URL+"/api/v1/artifacts/pipeline/raw_uploaded", "write_user", "write_pass", map[string]any{
		"description": "updated_by_name",
		"xml_content": samplePipelineXML,
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 updating by name, got %d", code)
	}

	code, _ = doAuthJSON(t, client, http.MethodPut, fmt.Sprintf("%s/api/v1/artifacts/pipeline/id/%d", ts.URL, upRec.ID), "write_user", "write_pass", map[string]any{
		"description": "", // empty description preserves existing
		"content":     samplePipelineXML,
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 updating by ID, got %d", code)
	}

	// 5. Export by name & by ID
	code, expBytes := doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/artifacts/pipeline/raw_uploaded/export", "read_user", "read_pass", nil, nil)
	if code != http.StatusOK || !strings.Contains(string(expBytes), "HelloNode") {
		t.Fatalf("export by name failed (%d): %s", code, string(expBytes))
	}
	code, expIDBytes := doAuthJSON(t, client, http.MethodGet, fmt.Sprintf("%s/api/v1/artifacts/pipeline/id/%d/export", ts.URL, upRec.ID), "read_user", "read_pass", nil, nil)
	if code != http.StatusOK || !strings.Contains(string(expIDBytes), "HelloNode") {
		t.Fatalf("export by ID failed (%d): %s", code, string(expIDBytes))
	}

	// 6. Delete by ID & by name + 404 checks
	code, _ = doAuthJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/v1/artifacts/pipeline/id/%d", ts.URL, upRec.ID), "write_user", "write_pass", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("delete by ID failed: %d", code)
	}
	// Deleting again should return 404
	code, _ = doAuthJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/v1/artifacts/pipeline/id/%d", ts.URL, upRec.ID), "write_user", "write_pass", nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 deleting already deleted ID, got %d", code)
	}

	// Create another to delete by name
	_, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/artifacts/options", "write_user", "write_pass", map[string]any{
		"name":    "temp_opt",
		"content": "<cli_options/>",
	}, nil)
	code, _ = doAuthJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/artifacts/options/temp_opt", "write_user", "write_pass", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("delete by name failed: %d", code)
	}
	code, _ = doAuthJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/artifacts/options/temp_opt", "write_user", "write_pass", nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 deleting non-existent name, got %d", code)
	}

	// 7. Validate endpoints (/api/v1/validate and /api/v1/validate/{type}/{id})
	_, cfgBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/artifacts/config", "write_user", "write_pass", map[string]any{
		"name":    "val_cfg",
		"content": samplePipelineXML,
	}, nil)
	var cfgRec ArtifactRecord
	_ = json.Unmarshal(cfgBytes, &cfgRec)

	code, valBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/validate", "exec_user", "exec_pass", map[string]any{
		"file_xml": samplePipelineXML,
		"format":   "markdown",
	}, nil)
	if code != http.StatusOK || !strings.Contains(string(valBytes), "valid") {
		t.Fatalf("POST /api/v1/validate failed (%d): %s", code, string(valBytes))
	}

	code, _ = doAuthJSON(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/validate/config/%d", ts.URL, cfgRec.ID), "exec_user", "exec_pass", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("POST /api/v1/validate/config/%d failed: %d", cfgRec.ID, code)
	}

	// Invalid XML validation -> 422
	code, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/validate", "exec_user", "exec_pass", map[string]any{
		"file_xml": "<Flow><Broken",
	}, nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for broken XML validation, got %d", code)
	}

	// 8. Users CRUD (/api/v1/users and /api/v1/users/{username}) & Databases test (/api/v1/databases/test)
	activeFalse := false
	code, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/users", "admin_user", "admin_pass", map[string]any{
		"username":    "temp_svc_user",
		"password":    "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"permission":  "read-only",
		"description": "Temp user",
		"is_active":   &activeFalse,
	}, nil)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating user, got %d", code)
	}

	code, usersBytes := doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/users", "admin_user", "admin_pass", nil, nil)
	if code != http.StatusOK || !strings.Contains(string(usersBytes), "temp_svc_user") {
		t.Fatalf("GET /api/v1/users failed (%d): %s", code, string(usersBytes))
	}

	code, _ = doAuthJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/users/temp_svc_user", "admin_user", "admin_pass", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("DELETE /api/v1/users/temp_svc_user failed: %d", code)
	}
	code, _ = doAuthJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/users/temp_svc_user", "admin_user", "admin_pass", nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 deleting non-existent user, got %d", code)
	}

	// Databases test endpoint
	code, dbTestBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/databases/test", "admin_user", "admin_pass", map[string]any{
		"dsn": "", // uses startup DB
	}, nil)
	if code != http.StatusOK || !strings.Contains(string(dbTestBytes), `"connected":true`) {
		t.Fatalf("POST /api/v1/databases/test failed (%d): %s", code, string(dbTestBytes))
	}

	// Query runs endpoint (/api/v1/runs)
	code, runsBytes := doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/runs?status=succeeded&limit=10", "read_user", "read_pass", nil, nil)
	if code != http.StatusOK || !strings.Contains(string(runsBytes), `"runs"`) {
		t.Fatalf("GET /api/v1/runs failed (%d): %s", code, string(runsBytes))
	}

	// Query audit events with filters
	code, _ = doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/audit?event_type=user.upsert&auth_user=admin_user&status=SUCCESS&limit=5", "read_user", "read_pass", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/audit with filters failed: %d", code)
	}
}

func TestExecutorAdvancedFeatures(t *testing.T) {
	apiSrv, ts, _ := setupTestServer(t)
	client := ts.Client()
	ctx := context.Background()

	// 1. formatScriptResults across all formats
	sampleResults := []flow.ScriptResult{
		{ScriptID: "Step1", ReturnCode: 0, Duration: "10ms", ResultsString: "line1\nline2"},
	}
	for _, fmtName := range []string{"json", "jsonpretty", "markdown", "text", "csv", "unknown"} {
		out := formatScriptResults(fmtName, sampleResults)
		if out == "" {
			t.Fatalf("formatScriptResults(%q) returned empty string", fmtName)
		}
	}

	// 2. resolveConnectionString with {{var}} and {{.var}}
	varCfgs := []flow.VariableConfig{
		{Name: "HOST", Value: "db.internal"},
		{Name: "BASE_DSN", Value: "postgres://{{HOST}}:{{.PORT}}/{{DB}}"},
	}
	resolvedDSN := resolveConnectionString("{{BASE_DSN}}", varCfgs, "PORT=5432", map[string]string{"DB": "analytics"})
	if resolvedDSN != "postgres://db.internal:5432/analytics" {
		t.Fatalf("unexpected resolvedDSN: %q", resolvedDSN)
	}
	if resolveConnectionString("sqlite://plain.db", nil, "", nil) != "sqlite://plain.db" {
		t.Fatalf("plain DSN should remain unchanged")
	}

	// 3. loadSQLResourceURI against the test SQLite database
	_, err := apiSrv.UpsertArtifact(ctx, "pipeline", "sql_uri_pipe", "test sql uri", samplePipelineXML, false, "")
	if err != nil {
		t.Fatalf("UpsertArtifact failed: %v", err)
	}
	sqlURI := "sql://sqlite@" + strings.TrimPrefix(apiSrv.cfg.ConnectionString, "sqlite://") + "#SELECT PipelineXML FROM flow_pipeline_content WHERE Name = 'sql_uri_pipe'"
	loadedBytes, err := loadSQLResourceURI(ctx, sqlURI)
	if err != nil || !strings.Contains(string(loadedBytes), "HelloNode") {
		t.Fatalf("loadSQLResourceURI failed: %v, %s", err, string(loadedBytes))
	}
	if _, err := loadSQLResourceURI(ctx, "invalid_uri_without_hash"); err == nil {
		t.Fatalf("expected error for SQL URI without #")
	}
	if _, err := loadSQLResourceURI(ctx, apiSrv.cfg.ConnectionString+"#SELECT PipelineXML FROM flow_pipeline_content WHERE Name = 'nonexistent'"); err == nil {
		t.Fatalf("expected error for 0 rows in loadSQLResourceURI")
	}

	// 4. applyOptionsXMLToRequest with both XML elements and <option flag="..."> items
	comprehensiveOptionsXML := `<cli_options>
		<options>
			<file>sql_uri_pipe</file>
			<format>jsonpretty</format>
			<xsd>schema.xsd</xsd>
			<config>my_cfg</config>
			<validate>true</validate>
			<preflight>true</preflight>
			<vars>A=1,B=2</vars>
			<debug>true</debug>
			<gopath>/tmp/go</gopath>
			<xslt>style.xsl</xslt>
			<out>out.xml</out>
			<node-output>true</node-output>
			<encrypted>true</encrypted>
			<secure-key>my_sec_key</secure-key>
			<verify-signature>true</verify-signature>
			<public-key>pub.pem</public-key>
			<cert>cert.pem</cert>
			<ca-cert>ca.pem</ca-cert>
			<keyring>ring.gpg</keyring>
			<signature>sig.bin</signature>
		</options>
	</cli_options>`
	req1 := ExecutionRequest{OptionsXML: comprehensiveOptionsXML}
	if _, err := apiSrv.applyOptionsXMLToRequest(ctx, &req1); err != nil {
		t.Fatalf("applyOptionsXMLToRequest (elements) failed: %v", err)
	}
	if req1.File != "sql_uri_pipe" || req1.Format != "jsonpretty" || !req1.Validate || !req1.Preflight || !req1.NodeOutput || !req1.VerifySignature {
		t.Fatalf("elements were not applied properly: %+v", req1)
	}

	flagItemsOptionsXML := `<cli_options>
		<option flag="--file">sql_uri_pipe</option>
		<option flag="--format">text</option>
		<option flag="--xsd">s.xsd</option>
		<option flag="--config">c.xml</option>
		<option flag="--validate">true</option>
		<option flag="--preflight">true</option>
		<option flag="--vars">K=V</option>
		<option flag="--debug">true</option>
		<option flag="--gopath">/go</option>
		<option flag="--xslt">t.xsl</option>
		<option flag="--out">o.xml</option>
		<option flag="--node-output">true</option>
		<option flag="--encrypted">true</option>
		<option flag="--secure-key">k</option>
		<option flag="--verify-signature">true</option>
		<option flag="--public-key">pk</option>
		<option flag="--cert">ct</option>
		<option flag="--ca-cert">ca</option>
		<option flag="--keyring">kr</option>
		<option flag="--signature">sg</option>
	</cli_options>`
	req2 := ExecutionRequest{OptionsXML: flagItemsOptionsXML}
	if _, err := apiSrv.applyOptionsXMLToRequest(ctx, &req2); err != nil {
		t.Fatalf("applyOptionsXMLToRequest (flag items) failed: %v", err)
	}
	if req2.File != "sql_uri_pipe" || req2.Format != "text" || req2.Keyring != "kr" || req2.Signature != "sg" {
		t.Fatalf("flag items were not applied properly: %+v", req2)
	}

	// 5. XSLT transformation & writeTempFile
	identityXSLT := `<?xml version="1.0" encoding="UTF-8"?>
<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template match="@*|node()">
    <xsl:copy>
      <xsl:apply-templates select="@*|node()"/>
    </xsl:copy>
  </xsl:template>
</xsl:stylesheet>`
	outFile := filepath.Join(t.TempDir(), "xslt_out.xml")
	transformed, err := transformXMLWithXSLT(ctx, []byte(samplePipelineXML), []byte(identityXSLT), outFile)
	if err != nil || !strings.Contains(string(transformed), "HelloNode") {
		t.Fatalf("transformXMLWithXSLT failed: %v", err)
	}

	// Execute pipeline using SQL URI + config_xml + vars
	overrideCfgXML := `<?xml version="1.0" encoding="UTF-8"?><Flow><Variables><Variable Name="EXTRA" Value="123"/></Variables></Flow>`
	code, execBytes := doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/execute?preflight=false&debug=true&vars=FOO=bar", "exec_user", "exec_pass", map[string]any{
		"file":       sqlURI,
		"config_xml": overrideCfgXML,
		"vars":       map[string]string{"DYNAMIC": "yes"},
		"format":     "text",
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("execute with SQL URI and config_xml failed (%d): %s", code, string(execBytes))
	}
}

type mockRowQuerier struct {
	dialect string
}

func (m *mockRowQuerier) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db, _, _ := OpenDatabase("sqlite://:memory:")
	defer db.Close()
	return db.QueryRowContext(ctx, "SELECT 42, 'mock_db_user', 'mock_app'")
}

func TestDBTelemetryAndConfigDialects(t *testing.T) {
	ctx := context.Background()

	// 1. ParseDSNWithDriver & detectDriverFromDSN across all supported drivers
	dsnCases := []struct {
		raw         string
		hint        string
		wantDriver  string
		wantDialect string
	}{
		{"sql://postgres@postgres://u:p@localhost:5432/db", "", "postgres", "postgres"},
		{"db://mysql@u:p@tcp(localhost:3306)/db", "", "mysql", "mysql"},
		{"sqlserver://localhost:1433?database=master&fedauth=ActiveDirectoryDefault", "", "azuresql", "sqlserver"},
		{"sqlserver://sa:pass@localhost:1433?database=master", "mssql", "sqlserver", "sqlserver"},
		{"server=localhost;fedauth=ActiveDirectoryInteractive", "sqlserver", "azuresql", "sqlserver"},
		{"", "azuresql", "azuresql", "sqlserver"},
		{"postgres://u:p@localhost/db", "", "postgres", "postgres"},
		{"mysql://u:p@tcp(localhost:3306)/db", "", "mysql", "mysql"},
		{"sqlite3://test.db", "", "sqlite", "sqlite"},
		{":memory:", "", "sqlite", "sqlite"},
		{"oracle://u:p@localhost:1521/xe", "", "oracle", "oracle"},
		{"custom_dsn", "custom_drv", "custom_drv", "custom_drv"},
		{"unknown_unprefixed_dsn", "", "sqlserver", "sqlserver"},
	}
	for _, tc := range dsnCases {
		drv, dialect, _ := ParseDSNWithDriver(tc.raw, tc.hint)
		if drv != tc.wantDriver || dialect != tc.wantDialect {
			t.Fatalf("ParseDSNWithDriver(%q, %q) = (%q, %q), want (%q, %q)", tc.raw, tc.hint, drv, dialect, tc.wantDriver, tc.wantDialect)
		}
	}
	_, _, _ = ParseDSN("sqlite://:memory:")
	if r := RedactDSN("sqlserver://sa:password=Secret123@localhost"); !strings.Contains(r, "[REDACTED]") {
		t.Fatalf("expected RedactDSN to redact password, got %q", r)
	}

	// 2. Placeholder, resolveTableName, buildArtifactUpsertQuery, buildUpdateNodeFinishedOutputQuery across dialects
	for _, d := range []string{"postgres", "sqlserver", "azuresql", "oracle", "mysql", "sqlite"} {
		_ = Placeholder(d, 1)
		_ = resolveTableName(d, TablePipelineContent)
		_ = buildArtifactUpsertQuery(d, "flow_pipeline_content", "PipelineXML")
		_ = buildUpdateNodeFinishedOutputQuery(d, TablePipelineEvents)
	}

	// 3. FetchDatabaseSessionIdentity across all dialects using mockRowQuerier
	_, _, _, _ = FetchDatabaseSessionIdentity(ctx, nil, "sqlserver")
	mq := &mockRowQuerier{}
	for _, d := range []string{"sqlserver", "postgres", "mysql", "oracle", "sqlite", "other"} {
		spid, u, app, err := FetchDatabaseSessionIdentity(ctx, mq, d)
		if err != nil || spid == 0 || app == "" {
			t.Fatalf("FetchDatabaseSessionIdentity(%q) failed: spid=%d, user=%q, app=%q, err=%v", d, spid, u, app, err)
		}
	}

	// 4. LoadStartupConfig & resolveKey with env vars and key-file
	t.Setenv("KRB5CCNAME", "FILE:/krb5cache/krb5cc_1000")
	t.Setenv("KRB5_KTNAME", "FILE:/etc/krb5.keytab")
	var krbCfg msdsn.Config
	normalizeKrb5Config(&krbCfg)
	if krbCfg.Parameters["krb5-credcachefile"] != "/krb5cache/krb5cc_1000" || krbCfg.Parameters["krb5-keytabfile"] != "/etc/krb5.keytab" {
		t.Fatalf("unexpected normalized krb5 parameters: %+v", krbCfg.Parameters)
	}
	normalizeKrb5Config(nil)
	t.Setenv("KRB5CCNAME", "")
	t.Setenv("KRB5_KTNAME", "")

	keyFile := filepath.Join(t.TempDir(), "secret.key")
	_ = os.WriteFile(keyFile, []byte("  file-secret-key-123  \n"), 0600)
	t.Setenv("PORT", "9091")
	t.Setenv("FLOW_API_DEBUG", "true")
	t.Setenv("FLOW_API_DSN", "sqlite://:memory:")
	cfg, err := LoadStartupConfig([]string{"-key-file", keyFile})
	if err != nil || cfg.DefaultSecureKey != "file-secret-key-123" || cfg.ListenAddr != ":9091" || !cfg.Debug {
		t.Fatalf("LoadStartupConfig with env/keyfile failed: %+v, err=%v", cfg, err)
	}
	t.Setenv("FLOW_SECURE_KEY", "env-sec-key")
	if k := resolveKey("", ""); k != "env-sec-key" {
		t.Fatalf("expected env-sec-key, got %q", k)
	}

	// 5. Telemetry helpers: truncateNodeOutput, nodeIdentityFromAST, getters, parseDBTime
	longStr := strings.Repeat("a", 200)
	if tr := truncateNodeOutput(longStr, 50); len(tr) > 50 || !strings.HasSuffix(tr, nodeOutputTruncationSuffix) {
		t.Fatalf("truncateNodeOutput(50) failed: %q", tr)
	}
	if trShort := truncateNodeOutput("hello world", 5); len(trShort) != 5 {
		t.Fatalf("truncateNodeOutput(5) failed: %q", trShort)
	}

	allNodeKinds := []flow.PipelineNode{
		{Kind: flow.NodeKV, KV: &flow.KVElement{ID: "kv1"}},
		{Kind: flow.NodeKVBulk, KVBulk: &flow.KVBulkElement{ID: "kvb1"}},
		{Kind: flow.NodeAssert, Assert: &flow.AssertElement{ID: "a1"}},
		{Kind: flow.NodeSQL, SQL: &flow.SQLElement{ID: "s1"}},
		{Kind: flow.NodeSQLBulk, SQLBulk: &flow.SQLBulkElement{ID: "sb1"}},
		{Kind: flow.NodeScript, Script: &flow.ScriptItem{ID: "sc1"}},
		{Kind: flow.NodeHTTPClient, HTTPClient: &flow.HTTPClientElement{ID: "hc1"}},
		{Kind: flow.NodeTemplate, Template: &flow.TemplateElement{ID: "t1"}},
		{Kind: flow.NodeHtmlTemplate, HtmlTemplate: &flow.HtmlTemplateElement{ID: "ht1"}},
		{Kind: flow.NodeFileSave, FileSave: &flow.FileSaveElement{ID: "fs1"}},
		{Kind: flow.NodeFileRead, FileRead: &flow.FileReadElement{ID: "fr1"}},
		{Kind: flow.NodeExcelRead, ExcelRead: &flow.ExcelReadElement{ID: "er1"}},
		{Kind: flow.NodeExcelWrite, ExcelWrite: &flow.ExcelWriteElement{ID: "ew1"}},
		{Kind: flow.NodeXMLXPath, XmlXPath: &flow.XmlXPathElement{ID: "xp1"}},
		{Kind: flow.NodeJSONPath, JsonPath: &flow.JsonPathElement{ID: "jp1"}},
		{Kind: flow.NodeYAMLPath, YamlPath: &flow.YamlPathElement{ID: "yp1"}},
		{Kind: flow.NodeGroup, GroupID: "g1", Children: []flow.PipelineNode{{Kind: flow.NodeKV}}, ElseNodes: []flow.PipelineNode{{Kind: flow.NodeAssert}}},
		{Kind: flow.NodeParallel, GroupID: "p1"},
		{Kind: flow.NodeIf, GroupID: "if1"},
		{Kind: flow.NodeForEach, GroupID: "fe1"},
		{Kind: flow.NodeWhile, GroupID: "w1"},
		{Kind: flow.NodeKind(999)},
	}
	sink := NewDatabaseSink(nil, "postgres", true, true)
	sink.RegisterNodes(allNodeKinds)
	sink.RegisterNodeTarget("custom_node", NodeDBTarget{NodeKind: "sql", DBName: "{{DB_VAR}}"})
	reg := flow.NewRegistry()
	reg.SetVar("DB_VAR", "resolved_db")
	sink.SetRegistry(reg)
	if got := sink.resolveDBName("{{DB_VAR}}"); got != "resolved_db" {
		t.Fatalf("resolveDBName failed: %q", got)
	}
	if sink.SPID() == 0 || sink.SSPID() == 0 || sink.ApplicationName() == "" {
		t.Fatalf("unexpected sink identity getters")
	}
	_ = sink.DBUser()

	// Time & scalar conversions
	for _, tsRaw := range []any{
		time.Now(),
		[]byte("2026-11-07T19:00:00Z"),
		"2026-11-07 19:00:00",
		"invalid-time",
		nil,
	} {
		_ = parseDBTime(tsRaw)
	}
	_ = toString([]byte("bytes"))
	_ = toString(123)
	_ = toInt64(int(5))
	_ = toInt64(int32(5))
	_ = toInt64("not-an-int")
}

func TestRemainingCoverageBranches(t *testing.T) {
	apiSrv, ts, _ := setupTestServer(t)
	client := ts.Client()
	ctx := context.Background()

	// 1. QueryPipelineRuns without status filter so all columns of recorded runs are scanned
	// First run a pipeline so pipeline_runs is populated
	_, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/execute?pipeline_id=1", "exec_user", "exec_pass", map[string]any{
		"file_xml": samplePipelineXML,
	}, nil)
	code, runsBytes := doAuthJSON(t, client, http.MethodGet, ts.URL+"/api/v1/runs", "read_user", "read_pass", nil, nil)
	if code != http.StatusOK || !strings.Contains(string(runsBytes), `"runs"`) {
		t.Fatalf("GET /api/v1/runs failed (%d): %s", code, string(runsBytes))
	}

	// 2. writeTempFile & loadInlineOrFileBytes from file
	tmpPath, err := writeTempFile("test-*.xml", []byte(samplePipelineXML))
	if err != nil {
		t.Fatalf("writeTempFile failed: %v", err)
	}
	defer os.Remove(tmpPath)
	fileData, err := loadInlineOrFileBytes(tmpPath)
	if err != nil || len(fileData) == 0 {
		t.Fatalf("loadInlineOrFileBytes from file failed: %v", err)
	}
	_, _ = loadInlineOrFileBytes("")
	_, _ = loadInlineOrFileBytes("/nonexistent/path/file.pem")

	// 3. Execute pipeline from local file path & with custom log_db
	pipeWithLogDB := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Flow>
  <Databases>
    <Database Name="log_db" Driver="sqlite" ConnectionString=%q />
  </Databases>
  <Node Name="StepLog">
    <Script Language="go"><![CDATA[
package main
import "fmt"
func main() { fmt.Print("ok") }
]]></Script>
  </Node>
</Flow>`, apiSrv.cfg.ConnectionString)
	code, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/execute", "exec_user", "exec_pass", map[string]any{
		"file_xml": pipeWithLogDB,
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("execute with log_db failed: %d", code)
	}

	// Execute by local file path
	code, _ = doAuthJSON(t, client, http.MethodPost, ts.URL+"/api/v1/execute", "exec_user", "exec_pass", map[string]any{
		"file": tmpPath,
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("execute by local file path failed: %d", code)
	}

	// 4. getOrFetchSessionIdentity with active registry DB handle
	sink := NewDatabaseSink(apiSrv.db, "sqlite", true, false)
	reg := flow.NewRegistry()
	_ = reg.InitDatabases([]flow.DatabaseConfig{
		{Name: "work_db", Driver: "sqlite", ConnectionString: ":memory:"},
		{Name: "log_db", Driver: "sqlite", ConnectionString: ":memory:"},
	})
	defer reg.CloseDatabases()
	sink.SetRegistry(reg)
	if ident, err := sink.getOrFetchSessionIdentity(ctx, "work_db"); err != nil || ident.SPID == 0 {
		t.Fatalf("getOrFetchSessionIdentity(work_db) failed: %+v, %v", ident, err)
	}
	if ident, err := sink.getOrFetchSessionIdentity(ctx, "log_db"); err != nil || ident.SPID == 0 {
		t.Fatalf("getOrFetchSessionIdentity(log_db) failed: %+v, %v", ident, err)
	}

	// 5. describeExecutionTarget & parsePositiveQueryID branches
	for _, req := range []ExecutionRequest{
		{ID: 1},
		{PipelineID: 2},
		{OptionsID: 3},
		{ConfigID: 4},
		{File: "f.xml"},
		{Options: "o.xml"},
		{Config: "c.xml"},
		{},
	} {
		_, _ = describeExecutionTarget(req)
	}
	if _, err := parsePositiveQueryID("-5", "id"); err == nil {
		t.Fatalf("expected error for negative query ID")
	}

	// 6. BuildOpenAPISpec branches (nil request, custom ListenAddr, X-Forwarded-Proto, /swagger redirect & 404)
	_ = apiSrv.BuildOpenAPISpec(nil)
	srvCustom := NewAPIServer(&ServerConfig{ListenAddr: "localhost:8888"}, nil, "sqlite")
	_ = srvCustom.BuildOpenAPISpec(nil)

	httpsReq, _ := http.NewRequest(http.MethodGet, "http://example.com/api/v1/openapi.json", nil)
	httpsReq.Header.Set("X-Forwarded-Proto", "https")
	_ = apiSrv.BuildOpenAPISpec(httpsReq)

	// /swagger redirect, 405, and 404
	noRedirClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	redirResp, err := noRedirClient.Get(ts.URL + "/swagger")
	if err == nil {
		redirResp.Body.Close()
	}
	c, _ := doAuthJSON(t, client, http.MethodPost, ts.URL+"/swagger/", "", "", nil, nil)
	if c != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST /swagger/, got %d", c)
	}
	c, _ = doAuthJSON(t, client, http.MethodGet, ts.URL+"/swagger/nonexistent", "", "", nil, nil)
	if c != http.StatusNotFound {
		t.Fatalf("expected 404 for /swagger/nonexistent, got %d", c)
	}
}

func TestEnvFileLogFileAndRunAPIServer(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, "go-flow-api.env")
	dbPath := filepath.Join(tmpDir, "svc_test.db")
	logPath := filepath.Join(tmpDir, "logs", "go-flow-api.log")

	envContent := strings.Join([]string{
		"# Sample environment configuration",
		"export FLOW_API_DSN=\"" + dbPath + "\"",
		"FLOW_API_ADDR='127.0.0.1:0'",
		"FLOW_WINDOWS_USER=\"RSYSLAB\\U00001\"",
		"FLOW_API_DEBUG=true # inline comment",
		"",
	}, "\r\n")
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		t.Fatalf("failed writing temp env file: %v", err)
	}

	// Ensure env vars are clean so loadEnvFile populates them
	t.Setenv("FLOW_API_DSN", "")
	t.Setenv("FLOW_API_ADDR", "")
	t.Setenv("FLOW_WINDOWS_USER", "")
	t.Setenv("FLOW_API_DEBUG", "")

	cfg, err := LoadStartupConfig([]string{"-env-file", envPath, "-log-file", logPath, "-service-name", "GoFlowAPITest"})
	if err != nil {
		t.Fatalf("LoadStartupConfig with -env-file failed: %v", err)
	}
	if cfg.ConnectionString != dbPath || cfg.ListenAddr != "127.0.0.1:0" || !cfg.Debug || cfg.LogFilePath != logPath || cfg.ServiceName != "GoFlowAPITest" {
		t.Fatalf("unexpected parsed ServerConfig: %+v", cfg)
	}
	if gotWinUser := os.Getenv("FLOW_WINDOWS_USER"); gotWinUser != `RSYSLAB\U00001` {
		t.Fatalf("expected literal backslash in FLOW_WINDOWS_USER, got %q", gotWinUser)
	}

	// Missing env-file should return an error
	if _, err := LoadStartupConfig([]string{"-env-file", filepath.Join(tmpDir, "missing.env")}); err == nil {
		t.Fatalf("expected error for missing -env-file")
	}

	// Verify configureLogOutput creates parent directory and log file
	origOut := log.Writer()
	defer log.SetOutput(origOut)
	lf, err := configureLogOutput(logPath)
	if err != nil || lf == nil {
		t.Fatalf("configureLogOutput failed: f=%v err=%v", lf, err)
	}
	log.Printf("service log check")
	_ = lf.Close()
	log.SetOutput(origOut)
	if data, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(data), "service log check") {
		t.Fatalf("expected log entry in %s, err=%v", logPath, err)
	}

	// Verify interactive maybeRunOSService returns handled=false outside SCM
	if handled, err := maybeRunOSService(cfg); err != nil || handled {
		t.Fatalf("expected handled=false outside SCM, got handled=%v err=%v", handled, err)
	}

	// Verify normalizeKrb5Config resolves keytab principal user & realm from AD_REALM / FLOW_WINDOWS_USER
	// and isContainerOrAutoUser recognizes the 'flowapi' daemon service account
	t.Setenv("KRB5_KTNAME", "FILE:/etc/go-flow-api/flowapi.keytab")
	t.Setenv("AD_REALM", "RSYSLAB.COM")
	t.Setenv("FLOW_WINDOWS_USER", `RSYSLAB\svc_goflow`)
	var ktCfg msdsn.Config
	normalizeKrb5Config(&ktCfg)
	if ktCfg.Parameters["krb5-keytabfile"] != "/etc/go-flow-api/flowapi.keytab" ||
		ktCfg.Parameters["krb5-realm"] != "RSYSLAB.COM" ||
		ktCfg.User != "svc_goflow" {
		t.Fatalf("unexpected keytab normalized config: user=%q params=%+v", ktCfg.User, ktCfg.Parameters)
	}
	if !isContainerOrAutoUser("flowapi") || !isContainerOrAutoUser(`HOST\flowapi`) {
		t.Fatalf("expected flowapi daemon account to be recognized by isContainerOrAutoUser")
	}
	t.Setenv("KRB5_KTNAME", "")
	t.Setenv("AD_REALM", "")

	// Verify runAPIServer starts, invokes onReady, and shuts down cleanly when context cancels
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runAPIServer(ctx, cfg, func() {
			cancel()
		})
	}()
	if err := <-errCh; err != nil {
		t.Fatalf("runAPIServer failed: %v", err)
	}
}

func TestGranularACLGroupsAndPipelineCRUD(t *testing.T) {
	_, ts, _ := setupTestServer(t)
	client := ts.Client()

	doReq := func(method, path, user, pass, body string) (int, []byte) {
		t.Helper()
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, ts.URL+path, rdr)
		if err != nil {
			t.Fatalf("NewRequest(%s %s): %v", method, path, err)
		}
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do(%s %s): %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	// 1. Verify OpenAPI spec exposes the new Access Control & Groups endpoints and schemas
	code, specBytes := doReq(http.MethodGet, "/swagger/doc.json", "", "", "")
	if code != http.StatusOK {
		t.Fatalf("expected 200 from /swagger/doc.json, got %d", code)
	}
	for _, needle := range []string{"/api/v1/groups", "/api/v1/pipeline-groups", "/api/v1/acls", "/api/v1/permissions/effective", "PipelineACL"} {
		if !strings.Contains(string(specBytes), needle) {
			t.Fatalf("expected OpenAPI spec to contain %q", needle)
		}
	}

	// 2. Non-admin cannot manage groups or ACLs
	if c, _ := doReq(http.MethodPost, "/api/v1/groups", "write_user", "write_pass", `{"name":"finance_ops"}`); c != http.StatusForbidden {
		t.Fatalf("expected 403 when non-admin creates group, got %d", c)
	}

	// 3. Admin creates API user groups, adds/removes members, and creates scoped users assigned to groups
	code, grpBody := doReq(http.MethodPost, "/api/v1/groups", "admin_user", "admin_pass", `{"name":"finance_ops","description":"Finance ETL Operators"}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating finance_ops group, got %d: %s", code, string(grpBody))
	}
	code, _ = doReq(http.MethodPost, "/api/v1/groups", "admin_user", "admin_pass", `{"name":"temp_grp","description":"Temp"}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating temp_grp, got %d", code)
	}
	code, _ = doReq(http.MethodPut, "/api/v1/groups/temp_grp", "admin_user", "admin_pass", `{"description":"Updated Temp"}`)
	if code != http.StatusOK {
		t.Fatalf("expected 200 updating temp_grp, got %d", code)
	}
	code, _ = doReq(http.MethodDelete, "/api/v1/groups/temp_grp", "admin_user", "admin_pass", "")
	if code != http.StatusOK {
		t.Fatalf("expected 200 deleting temp_grp, got %d", code)
	}

	// Create scoped users: fin_reader (in finance_ops via POST /api/v1/users) and fin_author
	code, userResp := doReq(http.MethodPost, "/api/v1/users", "admin_user", "admin_pass",
		`{"username":"fin_reader","password":"fin_pass","permission":"scoped","groups":["finance_ops"]}`)
	if code != http.StatusCreated || !strings.Contains(string(userResp), "finance_ops") {
		t.Fatalf("expected 201 creating fin_reader with finance_ops group, got %d: %s", code, string(userResp))
	}
	code, _ = doReq(http.MethodPost, "/api/v1/users", "admin_user", "admin_pass",
		`{"username":"fin_author","password":"fin_pass","permission":"scoped"}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating fin_author, got %d", code)
	}
	code, _ = doReq(http.MethodPost, "/api/v1/groups/finance_ops/members", "admin_user", "admin_pass", `{"username":"fin_author"}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 adding fin_author to finance_ops, got %d", code)
	}
	code, memBody := doReq(http.MethodGet, "/api/v1/groups/finance_ops/members", "admin_user", "admin_pass", "")
	if code != http.StatusOK || !strings.Contains(string(memBody), "fin_reader") || !strings.Contains(string(memBody), "fin_author") {
		t.Fatalf("expected both members in finance_ops, got %d: %s", code, string(memBody))
	}
	// Remove fin_author from finance_ops so fin_author tests direct user-level ACLs while fin_reader tests group-level ACLs
	if c, _ := doReq(http.MethodDelete, "/api/v1/groups/finance_ops/members/fin_author", "admin_user", "admin_pass", ""); c != http.StatusOK {
		t.Fatalf("expected 200 removing fin_author from finance_ops, got %d", c)
	}

	// 4. Create pipeline groups: finance_pipelines (with glob pattern fin_*) and hr_pipelines (explicit membership)
	code, _ = doReq(http.MethodPost, "/api/v1/pipeline-groups", "admin_user", "admin_pass",
		`{"name":"finance_pipelines","description":"Finance ETL","name_pattern":"fin_*"}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating finance_pipelines, got %d", code)
	}
	code, _ = doReq(http.MethodPost, "/api/v1/pipeline-groups", "admin_user", "admin_pass",
		`{"name":"hr_pipelines","description":"HR ETL"}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating hr_pipelines, got %d", code)
	}

	// Seed initial pipelines as admin_user
	code, finPipeResp := doReq(http.MethodPost, "/api/v1/artifacts/pipeline", "admin_user", "admin_pass",
		`{"name":"fin_gl_daily","description":"GL Daily","content":`+mustJSONString(samplePipelineXML)+`}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating fin_gl_daily, got %d: %s", code, string(finPipeResp))
	}
	var finRec ArtifactRecord
	_ = json.Unmarshal(finPipeResp, &finRec)

	code, hrPipeResp := doReq(http.MethodPost, "/api/v1/artifacts/pipeline", "admin_user", "admin_pass",
		`{"name":"hr_payroll","description":"HR Payroll","groups":["hr_pipelines"],"content":`+mustJSONString(samplePipelineXML)+`}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating hr_payroll, got %d: %s", code, string(hrPipeResp))
	}
	var hrRec ArtifactRecord
	_ = json.Unmarshal(hrPipeResp, &hrRec)

	//Also test explicit pipeline addition to finance_pipelines for a non-fin_* name
	code, _ = doReq(http.MethodPost, "/api/v1/artifacts/pipeline", "admin_user", "admin_pass",
		`{"name":"shared_ledger_sync","content":`+mustJSONString(samplePipelineXML)+`}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating shared_ledger_sync, got %d", code)
	}
	code, _ = doReq(http.MethodPost, "/api/v1/pipeline-groups/finance_pipelines/pipelines", "admin_user", "admin_pass",
		`{"pipeline_name":"shared_ledger_sync"}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 assigning shared_ledger_sync to finance_pipelines, got %d", code)
	}

	// 5. Before ACL rules exist, scoped fin_reader sees 0 pipelines and gets 403 on direct read
	code, listBefore := doReq(http.MethodGet, "/api/v1/artifacts/pipeline", "fin_reader", "fin_pass", "")
	if code != http.StatusOK || !strings.Contains(string(listBefore), `"count":0`) {
		t.Fatalf("expected scoped user with no ACLs to list 0 pipelines, got %d: %s", code, string(listBefore))
	}
	if c, _ := doReq(http.MethodGet, "/api/v1/artifacts/pipeline/fin_gl_daily", "fin_reader", "fin_pass", ""); c != http.StatusForbidden {
		t.Fatalf("expected 403 for scoped user reading fin_gl_daily without ACL, got %d", c)
	}
	if c, _ := doReq(http.MethodGet, "/api/v1/artifacts/options", "fin_reader", "fin_pass", ""); c != http.StatusForbidden {
		t.Fatalf("expected 403 for scoped user listing global options directly, got %d", c)
	}

	// 6. Grant group finance_ops -> read on pipeline_group finance_pipelines
	code, aclBody := doReq(http.MethodPost, "/api/v1/acls", "admin_user", "admin_pass",
		`{"principal_type":"group","principal_name":"finance_ops","target_type":"pipeline_group","target_name":"finance_pipelines","permissions":"read","effect":"allow","description":"Finance readers"}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating group ACL, got %d: %s", code, string(aclBody))
	}
	var readACL PipelineACL
	_ = json.Unmarshal(aclBody, &readACL)

	// fin_reader can now list ONLY fin_gl_daily (via fin_* pattern) and shared_ledger_sync (via explicit group member), NOT hr_payroll!
	code, listAfter := doReq(http.MethodGet, "/api/v1/artifacts/pipeline", "fin_reader", "fin_pass", "")
	if code != http.StatusOK {
		t.Fatalf("expected 200 listing pipelines as fin_reader, got %d", code)
	}
	if !strings.Contains(string(listAfter), "fin_gl_daily") || !strings.Contains(string(listAfter), "shared_ledger_sync") || strings.Contains(string(listAfter), "hr_payroll") {
		t.Fatalf("unexpected filtered pipeline list for fin_reader: %s", string(listAfter))
	}
	// Group filter query param ?group=finance_pipelines works
	code, grpFiltered := doReq(http.MethodGet, "/api/v1/artifacts/pipeline?group=finance_pipelines", "fin_reader", "fin_pass", "")
	if code != http.StatusOK || !strings.Contains(string(grpFiltered), `"count":2`) {
		t.Fatalf("expected 2 pipelines with ?group=finance_pipelines, got %d: %s", code, string(grpFiltered))
	}
	// Direct GET by name, by ID, and /export succeed on fin_gl_daily and return 403 on hr_payroll
	if c, _ := doReq(http.MethodGet, "/api/v1/artifacts/pipeline/fin_gl_daily", "fin_reader", "fin_pass", ""); c != http.StatusOK {
		t.Fatalf("expected 200 reading fin_gl_daily by name, got %d", c)
	}
	if c, _ := doReq(http.MethodGet, fmt.Sprintf("/api/v1/artifacts/pipeline/id/%d", finRec.ID), "fin_reader", "fin_pass", ""); c != http.StatusOK {
		t.Fatalf("expected 200 reading fin_gl_daily by ID, got %d", c)
	}
	if c, _ := doReq(http.MethodGet, "/api/v1/artifacts/pipeline/fin_gl_daily/export", "fin_reader", "fin_pass", ""); c != http.StatusOK {
		t.Fatalf("expected 200 exporting fin_gl_daily, got %d", c)
	}
	if c, _ := doReq(http.MethodGet, "/api/v1/artifacts/pipeline/hr_payroll", "fin_reader", "fin_pass", ""); c != http.StatusForbidden {
		t.Fatalf("expected 403 reading hr_payroll by name, got %d", c)
	}
	if c, _ := doReq(http.MethodGet, fmt.Sprintf("/api/v1/artifacts/pipeline/id/%d", hrRec.ID), "fin_reader", "fin_pass", ""); c != http.StatusForbidden {
		t.Fatalf("expected 403 reading hr_payroll by ID, got %d", c)
	}
	// fin_reader has read only -> cannot update or delete fin_gl_daily
	if c, _ := doReq(http.MethodPut, "/api/v1/artifacts/pipeline/fin_gl_daily", "fin_reader", "fin_pass", `{"content":`+mustJSONString(samplePipelineXML)+`}`); c != http.StatusForbidden {
		t.Fatalf("expected 403 when read-only scoped user attempts PUT, got %d", c)
	}

	// 7. Grant user fin_author -> create,read,update on pipeline_group finance_pipelines
	code, authorACLBytes := doReq(http.MethodPost, "/api/v1/acls", "admin_user", "admin_pass",
		`{"principal_type":"user","principal_name":"fin_author","target_type":"pipeline_group","target_name":"finance_pipelines","permissions":"create,read,update","effect":"allow"}`)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 creating fin_author ACL, got %d: %s", code, string(authorACLBytes))
	}
	var authorACL PipelineACL
	_ = json.Unmarshal(authorACLBytes, &authorACL)

	// fin_author can create a pipeline inside finance_pipelines (either via explicit group or via fin_* name pattern!)
	if c, b := doReq(http.MethodPost, "/api/v1/artifacts/pipeline", "fin_author", "fin_pass",
		`{"name":"custom_fin_job","group":"finance_pipelines","content":`+mustJSONString(samplePipelineXML)+`}`); c != http.StatusCreated {
		t.Fatalf("expected 201 creating custom_fin_job in finance_pipelines, got %d: %s", c, string(b))
	}
	if c, b := doReq(http.MethodPost, "/api/v1/artifacts/pipeline", "fin_author", "fin_pass",
		`{"name":"fin_ar_aging","content":`+mustJSONString(samplePipelineXML)+`}`); c != http.StatusCreated {
		t.Fatalf("expected 201 creating fin_ar_aging matching fin_* pattern, got %d: %s", c, string(b))
	}
	// fin_author CANNOT create an ungrouped non-matching pipeline or smuggle into hr_pipelines!
	if c, _ := doReq(http.MethodPost, "/api/v1/artifacts/pipeline", "fin_author", "fin_pass",
		`{"name":"rogue_unscoped_job","content":`+mustJSONString(samplePipelineXML)+`}`); c != http.StatusForbidden {
		t.Fatalf("expected 403 creating ungrouped non-matching pipeline, got %d", c)
	}
	if c, _ := doReq(http.MethodPost, "/api/v1/artifacts/pipeline", "fin_author", "fin_pass",
		`{"name":"fin_smuggle","groups":["finance_pipelines","hr_pipelines"],"content":`+mustJSONString(samplePipelineXML)+`}`); c != http.StatusForbidden {
		t.Fatalf("expected 403 when attempting to assign unauthorized hr_pipelines group on create, got %d", c)
	}

	// Raw upload via POST /api/v1/upload/pipeline?name=fin_upload_job checks create on new and update on overwrite
	uploadReq := func(targetName string) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/upload/pipeline?name="+targetName+"&group=finance_pipelines", strings.NewReader(samplePipelineXML))
		req.SetBasicAuth("fin_author", "fin_pass")
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("upload request failed: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := uploadReq("fin_upload_job"); c != http.StatusCreated {
		t.Fatalf("expected 201 uploading new fin_upload_job, got %d", c)
	}
	if c := uploadReq("fin_upload_job"); c != http.StatusCreated {
		t.Fatalf("expected 201 overwriting fin_upload_job with update perm, got %d", c)
	}
	// Upload overwrite on hr_payroll must be 403
	hrUpReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/upload/pipeline?name=hr_payroll", strings.NewReader(samplePipelineXML))
	hrUpReq.SetBasicAuth("fin_author", "fin_pass")
	hrUpReq.Header.Set("Content-Type", "application/xml")
	hrUpResp, _ := client.Do(hrUpReq)
	hrUpResp.Body.Close()
	if hrUpResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 uploading over hr_payroll, got %d", hrUpResp.StatusCode)
	}

	// fin_author can PUT update custom_fin_job, but DELETE fails until delete action is granted via PUT /api/v1/acls/{id}
	if c, _ := doReq(http.MethodPut, "/api/v1/artifacts/pipeline/custom_fin_job", "fin_author", "fin_pass",
		`{"description":"Updated desc","group":"finance_pipelines","content":`+mustJSONString(samplePipelineXML)+`}`); c != http.StatusOK {
		t.Fatalf("expected 200 updating custom_fin_job, got %d", c)
	}
	if c, _ := doReq(http.MethodDelete, "/api/v1/artifacts/pipeline/custom_fin_job", "fin_author", "fin_pass", ""); c != http.StatusForbidden {
		t.Fatalf("expected 403 deleting custom_fin_job without delete ACL, got %d", c)
	}

	// Upgrade fin_author's ACL via PUT /api/v1/acls/{id} to include delete
	if c, b := doReq(http.MethodPut, fmt.Sprintf("/api/v1/acls/%d", authorACL.ID), "admin_user", "admin_pass",
		`{"principal_type":"user","principal_name":"fin_author","target_type":"pipeline_group","target_name":"finance_pipelines","permissions":"create,read,update,delete","effect":"allow"}`); c != http.StatusOK {
		t.Fatalf("expected 200 updating ACL by ID, got %d: %s", c, string(b))
	}
	if c, _ := doReq(http.MethodDelete, "/api/v1/artifacts/pipeline/custom_fin_job", "fin_author", "fin_pass", ""); c != http.StatusOK {
		t.Fatalf("expected 200 deleting custom_fin_job after granting delete ACL, got %d", c)
	}

	// 8. Inspect effective permissions: self-inspection, non-admin blocked from inspecting others, admin simulation
	code, selfPerm := doReq(http.MethodGet, "/api/v1/me/permissions", "fin_reader", "fin_pass", "")
	if code != http.StatusOK || !strings.Contains(string(selfPerm), `"username":"fin_reader"`) {
		t.Fatalf("expected 200 from /api/v1/me/permissions for fin_reader, got %d: %s", code, string(selfPerm))
	}
	if c, _ := doReq(http.MethodGet, "/api/v1/permissions/effective?username=admin_user", "fin_reader", "fin_pass", ""); c != http.StatusForbidden {
		t.Fatalf("expected 403 when non-admin inspects another user's permissions, got %d", c)
	}
	code, adminInspect := doReq(http.MethodGet, "/api/v1/permissions/effective?username=fin_author", "admin_user", "admin_pass", "")
	if code != http.StatusOK || !strings.Contains(string(adminInspect), `"finance_pipelines"`) {
		t.Fatalf("expected 200 from admin effective permissions inspection, got %d: %s", code, string(adminInspect))
	}

	// Delete ACL by ID and verify listing filter
	code, aclList := doReq(http.MethodGet, "/api/v1/acls?principal_name=finance_ops", "admin_user", "admin_pass", "")
	if code != http.StatusOK || !strings.Contains(string(aclList), `"count":1`) {
		t.Fatalf("expected 1 ACL for finance_ops, got %d: %s", code, string(aclList))
	}
	if c, _ := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/acls/%d", readACL.ID), "admin_user", "admin_pass", ""); c != http.StatusOK {
		t.Fatalf("expected 200 deleting readACL, got %d", c)
	}
}

func TestGranularACLExecutionPrecedenceWindowsAndStrictMode(t *testing.T) {
	apiSrv, ts, _ := setupTestServer(t)
	client := ts.Client()

	doReq := func(method, path, user, pass, body string, extraHeaders map[string]string) (int, []byte) {
		t.Helper()
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, ts.URL+path, rdr)
		if err != nil {
			t.Fatalf("NewRequest(%s %s): %v", method, path, err)
		}
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do(%s %s): %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	// Seed pipeline_group finance_pipelines, fin_exec_pipe, hr_secret_pipe, a shared config, and two options profiles
	doReq(http.MethodPost, "/api/v1/pipeline-groups", "admin_user", "admin_pass", `{"name":"finance_pipelines","name_pattern":"fin_*"}`, nil)

	_, finBytes := doReq(http.MethodPost, "/api/v1/artifacts/pipeline", "admin_user", "admin_pass",
		`{"name":"fin_exec_pipe","content":`+mustJSONString(samplePipelineXML)+`}`, nil)
	var finRec ArtifactRecord
	_ = json.Unmarshal(finBytes, &finRec)

	_, hrBytes := doReq(http.MethodPost, "/api/v1/artifacts/pipeline", "admin_user", "admin_pass",
		`{"name":"hr_secret_pipe","content":`+mustJSONString(samplePipelineXML)+`}`, nil)
	var hrRec ArtifactRecord
	_ = json.Unmarshal(hrBytes, &hrRec)

	cfgXML := `<?xml version="1.0" encoding="UTF-8"?><Configuration><Variables><Variable Name="ENV" Value="prod"/></Variables></Configuration>`
	_, cfgBytes := doReq(http.MethodPost, "/api/v1/artifacts/config", "admin_user", "admin_pass",
		`{"name":"shared_cfg","content":`+mustJSONString(cfgXML)+`}`, nil)
	var cfgRec ArtifactRecord
	_ = json.Unmarshal(cfgBytes, &cfgRec)

	optAllowedXML := `<cli_options><file>fin_exec_pipe</file><config>shared_cfg</config><format>json</format></cli_options>`
	_, optAllowedBytes := doReq(http.MethodPost, "/api/v1/artifacts/options", "admin_user", "admin_pass",
		`{"name":"opt_fin_allowed","content":`+mustJSONString(optAllowedXML)+`}`, nil)
	var optAllowedRec ArtifactRecord
	_ = json.Unmarshal(optAllowedBytes, &optAllowedRec)

	optSecretXML := `<cli_options><file>hr_secret_pipe</file><format>json</format></cli_options>`
	_, optSecretBytes := doReq(http.MethodPost, "/api/v1/artifacts/options", "admin_user", "admin_pass",
		`{"name":"opt_hr_secret","content":`+mustJSONString(optSecretXML)+`}`, nil)
	var optSecretRec ArtifactRecord
	_ = json.Unmarshal(optSecretBytes, &optSecretRec)

	// Create scoped runner and grant read,execute on finance_pipelines
	doReq(http.MethodPost, "/api/v1/users", "admin_user", "admin_pass",
		`{"username":"fin_runner","password":"run_pass","permission":"scoped"}`, nil)
	doReq(http.MethodPost, "/api/v1/acls", "admin_user", "admin_pass",
		`{"principal_type":"user","principal_name":"fin_runner","target_type":"pipeline_group","target_name":"finance_pipelines","permissions":"read,execute","effect":"allow"}`, nil)

	// Execute hr_secret_pipe as admin_user first so telemetry tables contain both hr_secret_pipe and fin_exec_pipe runs
	if c, b := doReq(http.MethodPost, "/api/v1/execute", "admin_user", "admin_pass", `{"file":"hr_secret_pipe","node_output":true}`, nil); c != http.StatusOK {
		t.Fatalf("admin execute hr_secret_pipe failed (%d): %s", c, string(b))
	}

	// 1. fin_runner CAN execute, validate, and SSE-stream fin_exec_pipe (including via opt_fin_allowed + shared_cfg)
	if c, b := doReq(http.MethodPost, "/api/v1/execute", "fin_runner", "run_pass", `{"file":"fin_exec_pipe","config":"shared_cfg","node_output":true}`, nil); c != http.StatusOK {
		t.Fatalf("expected 200 executing fin_exec_pipe, got %d: %s", c, string(b))
	}
	if c, b := doReq(http.MethodPost, fmt.Sprintf("/api/v1/execute/pipeline/%d", finRec.ID), "fin_runner", "run_pass", "", nil); c != http.StatusOK {
		t.Fatalf("expected 200 executing fin_exec_pipe by ID, got %d: %s", c, string(b))
	}
	if c, b := doReq(http.MethodPost, "/api/v1/validate", "fin_runner", "run_pass", `{"file":"fin_exec_pipe"}`, nil); c != http.StatusOK {
		t.Fatalf("expected 200 validating fin_exec_pipe, got %d: %s", c, string(b))
	}
	if c, b := doReq(http.MethodPost, "/api/v1/execute/stream", "fin_runner", "run_pass", `{"file":"fin_exec_pipe"}`, nil); c != http.StatusOK || !strings.Contains(string(b), "event: done") {
		t.Fatalf("expected 200 SSE stream for fin_exec_pipe, got %d: %s", c, string(b))
	}
	if c, b := doReq(http.MethodGet, fmt.Sprintf("/api/v1/execute/stream/pipeline/%d", finRec.ID), "fin_runner", "run_pass", "", nil); c != http.StatusOK || !strings.Contains(string(b), "event: done") {
		t.Fatalf("expected 200 SSE stream by ID for fin_exec_pipe, got %d: %s", c, string(b))
	}
	if c, b := doReq(http.MethodPost, fmt.Sprintf("/api/v1/execute/options/%d", optAllowedRec.ID), "fin_runner", "run_pass", "", nil); c != http.StatusOK {
		t.Fatalf("expected 200 executing opt_fin_allowed pointing to fin_exec_pipe, got %d: %s", c, string(b))
	}

	// 2. fin_runner is BLOCKED (403 Forbidden) on hr_secret_pipe, SSE stream of hr_secret_pipe, options indirection to hr_secret_pipe, inline XML, sql:// URI, local paths, and direct config execution
	deniedExecCases := []struct {
		name string
		path string
		body string
	}{
		{"direct secret name", "/api/v1/execute", `{"file":"hr_secret_pipe"}`},
		{"direct secret ID", fmt.Sprintf("/api/v1/execute/pipeline/%d", hrRec.ID), ""},
		{"SSE stream secret name", "/api/v1/execute/stream", `{"file":"hr_secret_pipe"}`},
		{"SSE stream secret ID", fmt.Sprintf("/api/v1/execute/stream/pipeline/%d", hrRec.ID), ""},
		{"options indirection to secret", "/api/v1/execute", `{"options":"opt_hr_secret"}`},
		{"options ID indirection to secret", fmt.Sprintf("/api/v1/execute/options/%d", optSecretRec.ID), ""},
		{"inline options_xml indirection to secret", "/api/v1/execute", `{"options_xml":` + mustJSONString(optSecretXML) + `}`},
		{"inline file_xml bypass attempt", "/api/v1/execute", `{"file_xml":` + mustJSONString(samplePipelineXML) + `}`},
		{"sql:// URI bypass attempt", "/api/v1/execute", `{"file":"sql://sqlserver/dbo.flow_pipeline_content/PipelineXML?id=1"}`},
		{"local file path bypass attempt", "/api/v1/execute", `{"file":"./local_pipeline.xml"}`},
		{"direct config execution by ID", fmt.Sprintf("/api/v1/execute/config/%d", cfgRec.ID), ""},
	}
	for _, tc := range deniedExecCases {
		c, b := doReq(http.MethodPost, tc.path, "fin_runner", "run_pass", tc.body, nil)
		if c != http.StatusForbidden {
			t.Fatalf("case %q: expected 403 Forbidden, got %d: %s", tc.name, c, string(b))
		}
	}

	// 3. Telemetry scoping: fin_runner sees runs & events ONLY for fin_exec_pipe, never hr_secret_pipe
	code, runsBody := doReq(http.MethodGet, "/api/v1/runs", "fin_runner", "run_pass", "", nil)
	if code != http.StatusOK || !strings.Contains(string(runsBody), "fin_exec_pipe") || strings.Contains(string(runsBody), "hr_secret_pipe") {
		t.Fatalf("expected fin_runner /api/v1/runs to include fin_exec_pipe and exclude hr_secret_pipe, got %d: %s", code, string(runsBody))
	}
	code, eventsBody := doReq(http.MethodGet, "/api/v1/events", "fin_runner", "run_pass", "", nil)
	if code != http.StatusOK || !strings.Contains(string(eventsBody), "HelloNode") {
		t.Fatalf("expected fin_runner /api/v1/events to return fin_exec_pipe node events, got %d: %s", code, string(eventsBody))
	}
	// Audit trail stays restricted to global read/admin roles
	if c, _ := doReq(http.MethodGet, "/api/v1/audit", "fin_runner", "run_pass", "", nil); c != http.StatusForbidden {
		t.Fatalf("expected 403 on /api/v1/audit for scoped user, got %d", c)
	}

	// 4. Precedence check: explicit DENY overrides both scoped allow AND legacy global exec_user, while admin_user (PermAll) is unaffected
	_, denyBytes := doReq(http.MethodPost, "/api/v1/acls", "admin_user", "admin_pass",
		`{"principal_type":"user","principal_name":"fin_runner","target_type":"pipeline","target_name":"fin_exec_pipe","permissions":"execute","effect":"deny"}`, nil)
	var denyRule PipelineACL
	_ = json.Unmarshal(denyBytes, &denyRule)

	if c, _ := doReq(http.MethodPost, "/api/v1/execute", "fin_runner", "run_pass", `{"file":"fin_exec_pipe"}`, nil); c != http.StatusForbidden {
		t.Fatalf("expected explicit user deny to override pipeline_group allow (got %d)", c)
	}
	// Deny legacy exec_user on hr_secret_pipe
	doReq(http.MethodPost, "/api/v1/acls", "admin_user", "admin_pass",
		`{"principal_type":"user","principal_name":"exec_user","target_type":"pipeline","target_name":"hr_secret_pipe","permissions":"execute","effect":"deny"}`, nil)
	if c, _ := doReq(http.MethodPost, "/api/v1/execute", "exec_user", "exec_pass", `{"file":"hr_secret_pipe"}`, nil); c != http.StatusForbidden {
		t.Fatalf("expected explicit deny to block legacy exec_user on hr_secret_pipe, got %d", c)
	}
	if c, _ := doReq(http.MethodPost, "/api/v1/execute", "admin_user", "admin_pass", `{"file":"hr_secret_pipe"}`, nil); c != http.StatusOK {
		t.Fatalf("expected admin_user (PermAll) to remain allowed, got %d", c)
	}

	// 5. Windows / Active Directory group integration via ACL rule with NO row in flow_api_users
	doReq(http.MethodPost, "/api/v1/acls", "admin_user", "admin_pass",
		`{"principal_type":"group","principal_name":"CORP\\AD_Finance_Runners","target_type":"pipeline_group","target_name":"finance_pipelines","permissions":"read,execute","effect":"allow"}`, nil)

	adHeaders := map[string]string{
		HeaderWindowsUser:   `CORP\alice_ad`,
		HeaderWindowsGroups: `CORP\Domain Users, CORP\AD_Finance_Runners`,
	}
	if c, b := doReq(http.MethodGet, "/api/v1/health", "", "", "", adHeaders); c != http.StatusOK || !strings.Contains(string(b), `"permission":"scoped"`) {
		t.Fatalf("expected Windows AD user matched via ACL group to authenticate as scoped on /health, got %d: %s", c, string(b))
	}
	if c, b := doReq(http.MethodPost, "/api/v1/execute", "", "", `{"file":"fin_exec_pipe"}`, adHeaders); c != http.StatusOK {
		t.Fatalf("expected AD group member to execute fin_exec_pipe, got %d: %s", c, string(b))
	}
	if c, _ := doReq(http.MethodPost, "/api/v1/execute", "", "", `{"file":"hr_secret_pipe"}`, adHeaders); c != http.StatusForbidden {
		t.Fatalf("expected AD group member to receive 403 on hr_secret_pipe, got %d", c)
	}

	// 6. StrictACL mode (-strict-acl / FLOW_API_STRICT_ACL=true) disables legacy global pipeline fallback for non-admin users
	strictCfg, err := LoadStartupConfig([]string{"-dsn", "sqlite://strict.db", "-strict-acl"})
	if err != nil || !strictCfg.StrictACL {
		t.Fatalf("expected -strict-acl flag to set StrictACL=true, cfg=%+v err=%v", strictCfg, err)
	}

	apiSrv.cfg.StrictACL = true
	defer func() { apiSrv.cfg.StrictACL = false }()

	// Under StrictACL, read_user has no ACL entries -> sees 0 pipelines and gets 403 on direct pipeline read
	code, strictList := doReq(http.MethodGet, "/api/v1/artifacts/pipeline", "read_user", "read_pass", "", nil)
	if code != http.StatusOK || !strings.Contains(string(strictList), `"count":0`) {
		t.Fatalf("expected read_user with no ACLs under StrictACL to list 0 pipelines, got %d: %s", code, string(strictList))
	}
	if c, _ := doReq(http.MethodPost, "/api/v1/execute", "exec_user", "exec_pass", `{"file":"fin_exec_pipe"}`, nil); c != http.StatusForbidden {
		t.Fatalf("expected exec_user without ACL under StrictACL to get 403 on execute, got %d", c)
	}
}

func TestLegacySevenTableSchemaGracefulFallback(t *testing.T) {
	tmpDir := t.TempDir()
	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(tmpDir, "legacy7.db"))
	cfg := &ServerConfig{
		ConnectionString: dsn,
		ListenAddr:       ":0",
		DefaultSecureKey: "0123456789abcdef0123456789abcdef",
	}
	db, dialect, err := OpenDatabase(cfg.ConnectionString)
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer db.Close()

	// Provision ONLY the original 7 tables (no ACL or group tables)
	legacyDDL := []string{
		`CREATE TABLE flow_pipeline_content (ID INTEGER PRIMARY KEY AUTOINCREMENT, Name TEXT NOT NULL UNIQUE, Description TEXT, PipelineXML TEXT, CreatedAt DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UpdatedAt DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE flow_options_content (ID INTEGER PRIMARY KEY AUTOINCREMENT, Name TEXT NOT NULL UNIQUE, Description TEXT, OptionsXML TEXT, CreatedAt DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UpdatedAt DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE flow_config_content (ID INTEGER PRIMARY KEY AUTOINCREMENT, Name TEXT NOT NULL UNIQUE, Description TEXT, ConfigXML TEXT, CreatedAt DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UpdatedAt DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE pipeline_runs (run_id TEXT PRIMARY KEY, file_path TEXT, config_path TEXT, status TEXT NOT NULL, started_at DATETIME NOT NULL, finished_at DATETIME NOT NULL, duration_ms INTEGER NOT NULL, task_count INTEGER NOT NULL, user_name TEXT, os_user_name TEXT, db_user_name TEXT, spid INTEGER, application_name TEXT, hostname TEXT, options_path TEXT, error_class TEXT, error_message TEXT)`,
		`CREATE TABLE pipeline_events (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, execution_id TEXT, sequence_num INTEGER NOT NULL, occurred_at DATETIME NOT NULL, event_type TEXT NOT NULL, node_kind TEXT, node_id TEXT, status TEXT, user_name TEXT, os_user_name TEXT, db_user_name TEXT, spid TEXT, application_name TEXT, hostname TEXT, options_path TEXT, error_message TEXT, rows_read INTEGER, rows_written INTEGER, rows_affected INTEGER, node_output TEXT)`,
		`CREATE TABLE flow_api_events (event_id TEXT PRIMARY KEY, occurred_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, event_type TEXT NOT NULL, http_method TEXT NOT NULL, path TEXT NOT NULL, artifact_type TEXT, artifact_name TEXT, run_id TEXT, auth_user TEXT, remote_addr TEXT, status_code INTEGER NOT NULL, status TEXT NOT NULL, duration_ms INTEGER NOT NULL, error_message TEXT, details_json TEXT)`,
		`CREATE TABLE flow_api_users (username TEXT PRIMARY KEY, password_hash TEXT NOT NULL, permission TEXT NOT NULL, is_active INTEGER NOT NULL DEFAULT 1, description TEXT, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	}
	for _, stmt := range legacyDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy DDL failed: %v", err)
		}
	}

	srv := NewAPIServer(cfg, db, dialect)
	ctx := context.Background()
	_, _ = srv.UpsertAPIUser(ctx, "write_u", "pass", PermWrite, "", true)
	_, _ = srv.UpsertAPIUser(ctx, "exec_u", "pass", PermExecutionOnly, "", true)
	_, _ = srv.UpsertAPIUser(ctx, "scoped_u", "pass", PermScoped, "", true)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	client := ts.Client()

	// Legacy write_u can create pipeline without ACL tables present
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/artifacts/pipeline", strings.NewReader(`{"name":"legacy_pipe","content":`+mustJSONString(samplePipelineXML)+`}`))
	req.SetBasicAuth("write_u", "pass")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST legacy_pipe failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 on 7-table schema for write_u, got %d", resp.StatusCode)
	}

	// Legacy exec_u can execute legacy_pipe
	execReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/execute", strings.NewReader(`{"file":"legacy_pipe"}`))
	execReq.SetBasicAuth("exec_u", "pass")
	execResp, err := client.Do(execReq)
	if err != nil {
		t.Fatalf("POST execute legacy_pipe failed: %v", err)
	}
	execResp.Body.Close()
	if execResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on 7-table schema for exec_u, got %d", execResp.StatusCode)
	}

	// Scoped user fails closed (403 Forbidden) on 7-table schema
	scopedReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/execute", strings.NewReader(`{"file":"legacy_pipe"}`))
	scopedReq.SetBasicAuth("scoped_u", "pass")
	scopedResp, err := client.Do(scopedReq)
	if err != nil {
		t.Fatalf("POST execute as scoped_u failed: %v", err)
	}
	scopedResp.Body.Close()
	if scopedResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 fail-closed on 7-table schema for scoped_u, got %d", scopedResp.StatusCode)
	}
}
