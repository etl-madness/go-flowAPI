package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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


