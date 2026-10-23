package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>go-flowAPI — Swagger UI</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui.css" />
  <style>
    html { box-sizing: border-box; overflow-y: scroll; }
    *, *:before, *:after { box-sizing: inherit; }
    body { margin: 0; background: #fafafa; }
    .topbar-Note {
      background: #1b1b1b;
      color: #f0f0f0;
      padding: 8px 20px;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      font-size: 13px;
      display: flex;
      justify-content: space-between;
      align-items: center;
    }
    .topbar-Note code {
      background: #333;
      padding: 2px 6px;
      border-radius: 4px;
      color: #85ea2d;
    }
  </style>
</head>
<body>
  <div class="topbar-Note">
    <div>
      <strong>go-flowAPI</strong> — Unified Artifact CRUD, File Upload, Pipeline Execution &amp; Telemetry API
    </div>
    <div>
      Authenticate via <strong>Authorize</strong> using BasicAuth credentials from <code>dbo.flow_api_users</code>
    </div>
  </div>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui-bundle.js"></script>
  <script src="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui-standalone-preset.js"></script>
  <script>
    window.onload = function() {
      window.ui = SwaggerUIBundle({
        url: "/swagger/doc.json",
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [
          SwaggerUIBundle.presets.apis,
          SwaggerUIStandalonePreset
        ],
        layout: "StandaloneLayout",
        persistAuthorization: true,
        tryItOutEnabled: true
      });
    };
  </script>
</body>
</html>`

// handleSwaggerUI serves the Swagger UI HTML and the OpenAPI 3.0 JSON specification.
func (s *APIServer) handleSwaggerUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	path := r.URL.Path
	if path == "/swagger/doc.json" || path == "/swagger/openapi.json" || path == "/api/v1/openapi.json" {
		s.handleOpenAPISpec(w, r)
		return
	}

	if path == "/swagger" {
		http.Redirect(w, r, "/swagger/", http.StatusMovedPermanently)
		return
	}

	if path == "/swagger/" || path == "/swagger/index.html" || path == "/docs" || path == "/docs/" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Allow Swagger UI assets from unpkg CDN while keeping framing disabled
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(swaggerUIHTML))
		return
	}

	writeJSONError(w, http.StatusNotFound, "Swagger resource not found")
}

// handleOpenAPISpec serves the dynamically enriched OpenAPI 3.0.3 JSON document.
func (s *APIServer) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	spec := s.BuildOpenAPISpec(r)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(spec)
}

// BuildOpenAPISpec constructs the OpenAPI 3.0.3 specification for go-flowAPI.
func (s *APIServer) BuildOpenAPISpec(r *http.Request) map[string]any {
	serverURL := ""
	if r != nil && strings.TrimSpace(r.Host) != "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		serverURL = scheme + "://" + r.Host
	} else if s != nil && s.cfg != nil && strings.TrimSpace(s.cfg.ListenAddr) != "" {
		addr := strings.TrimSpace(s.cfg.ListenAddr)
		if strings.HasPrefix(addr, ":") {
			serverURL = "http://localhost" + addr
		} else {
			serverURL = "http://" + addr
		}
	} else {
		serverURL = "http://localhost:8080"
	}

	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "go-flowAPI — CRUD, File Upload, Execution & Telemetry API",
			"version": "1.0.0",
			"description": "Unified REST API for managing `go-flow` database artifacts (`pipeline`, `options`, `config`), " +
				"uploading XML/JSON files to the database, executing and validating pipelines (with full CLI/Builder parity, " +
				"live SSE streaming, and `node_output` logging), and querying execution & API audit telemetry.\n\n" +
				"### Authentication & Role Permissions\n" +
				"All `/api/v1/*` endpoints require HTTP Basic Authentication against `dbo.flow_api_users` (`TableAPIUsers`).\n\n" +
				"| Permission Role | Capabilities |\n" +
				"| :--- | :--- |\n" +
				"| `execution-only` | Execute and validate pipelines (`/api/v1/execute`, `/api/v1/execute/stream`, `/api/v1/validate`) plus read/list/export artifacts and query logs |\n" +
				"| `read-only` | Read/list/export artifacts and query run, event, and audit logs |\n" +
				"| `write` / `upload` | Upload files and create, update, delete, or read artifacts |\n" +
				"| `all` | Full administrative access to all endpoints including `/api/v1/users` and `/api/v1/databases/test` |",
		},
		"servers": []map[string]any{
			{
				"url":         serverURL,
				"description": "Active go-flowAPI Server",
			},
		},
		"security": []map[string]any{
			{"basicAuth": []string{}},
		},
		"tags": []map[string]any{
			{"name": "System", "description": "Health check and database connectivity verification"},
			{"name": "Artifacts", "description": "CRUD operations and XML exports for pipeline, options, and config artifacts"},
			{"name": "Upload", "description": "Multipart file and raw XML upload endpoints for pipeline, options, and config tables"},
			{"name": "Execution", "description": "Synchronous execution, SSE streaming execution, and XSD/AST validation"},
			{"name": "Telemetry & Audit", "description": "Query pipeline runs, node execution events (including node_output), and API audit events"},
			{"name": "Users", "description": "Manage BasicAuth API users and role permissions in TableAPIUsers (requires 'all' permission)"},
		},
		"paths": map[string]any{
			"/api/v1/health": map[string]any{
				"get": map[string]any{
					"tags":        []string{"System"},
					"summary":     "Health check and active database session identity",
					"description": "Returns server status, database dialect, session SPID/user/application_name, and configured table names. Publicly accessible without BasicAuth (if BasicAuth credentials are supplied, `authenticated_as` and `permission` are also populated).",
					"operationId": "getHealth",
					"security":    []map[string]any{},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Server and database health details",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/HealthResponse"},
								},
							},
						},
					},
				},
			},
			"/api/v1/artifacts/{type}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"Artifacts"},
					"summary":     "List artifacts by type",
					"description": "Lists all stored artifacts of the specified type (`pipeline`, `options`, or `config`). Requires `read-only`, `write`, `upload`, or `all` permission.",
					"operationId": "listArtifacts",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{
							"name":        "include_content",
							"in":          "query",
							"description": "When `true` or `1`, includes the full XML `content` in each returned record",
							"required":    false,
							"schema":      map[string]any{"type": "boolean", "default": false},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of artifacts",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ArtifactListResponse"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
					},
				},
				"post": map[string]any{
					"tags":        []string{"Artifacts"},
					"summary":     "Create (or upsert) an artifact via JSON payload",
					"description": "Creates a new `pipeline`, `options`, or `config` artifact in the database. Set `upsert: true` (in JSON or query param) to overwrite if it already exists. Requires `write`, `upload`, or `all` permission.",
					"operationId": "createArtifact",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{
							"name":        "upsert",
							"in":          "query",
							"description": "When `true`, updates the artifact if it already exists instead of returning 409 Conflict",
							"required":    false,
							"schema":      map[string]any{"type": "boolean", "default": false},
						},
					},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/ArtifactPayload"},
							},
						},
					},
					"responses": map[string]any{
						"201": map[string]any{
							"description": "Artifact created",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ArtifactRecord"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"409": map[string]any{"$ref": "#/components/responses/Conflict"},
					},
				},
			},
			"/api/v1/artifacts/{type}/upload": buildUploadPathItem("uploadArtifactViaArtifactsRoute"),
			"/api/v1/upload/{type}":          buildUploadPathItem("uploadArtifact"),
			"/api/v1/artifacts/{type}/{name}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"Artifacts"},
					"summary":     "Get a single artifact by name",
					"description": "Fetches an artifact record by name. Optionally decrypts encrypted content when `decrypt=true` is passed. Requires `read-only`, `write`, `upload`, or `all` permission.",
					"operationId": "getArtifact",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactNameParam"},
						{
							"name":        "decrypt",
							"in":          "query",
							"description": "Decrypt encrypted payload before returning",
							"required":    false,
							"schema":      map[string]any{"type": "boolean", "default": false},
						},
						{
							"name":        "secure_key",
							"in":          "query",
							"description": "Optional encryption/decryption passphrase (or pass via `X-Flow-Secure-Key` header)",
							"required":    false,
							"schema":      map[string]any{"type": "string"},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Artifact record",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ArtifactRecord"},
								},
							},
						},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"404": map[string]any{"$ref": "#/components/responses/NotFound"},
					},
				},
				"put": map[string]any{
					"tags":        []string{"Artifacts"},
					"summary":     "Update an existing artifact by name",
					"description": "Updates an existing `pipeline`, `options`, or `config` artifact. Requires `write`, `upload`, or `all` permission.",
					"operationId": "updateArtifact",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactNameParam"},
					},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/ArtifactPayload"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Updated artifact record",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ArtifactRecord"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"404": map[string]any{"$ref": "#/components/responses/NotFound"},
					},
				},
				"delete": map[string]any{
					"tags":        []string{"Artifacts"},
					"summary":     "Delete an artifact by name",
					"description": "Deletes an artifact from the database. Requires `write`, `upload`, or `all` permission.",
					"operationId": "deleteArtifact",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactNameParam"},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Artifact deleted",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"deleted": map[string]any{"type": "boolean", "example": true},
											"type":    map[string]any{"type": "string", "example": "pipeline"},
											"name":    map[string]any{"type": "string", "example": "daily_etl"},
										},
									},
								},
							},
						},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"404": map[string]any{"$ref": "#/components/responses/NotFound"},
					},
				},
			},
			"/api/v1/artifacts/{type}/{name}/export": map[string]any{
				"get": map[string]any{
					"tags":        []string{"Artifacts"},
					"summary":     "Export artifact as raw XML attachment",
					"description": "Downloads the artifact XML content as an `application/xml` file attachment. Requires `read-only`, `write`, `upload`, or `all` permission.",
					"operationId": "exportArtifact",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactNameParam"},
						{
							"name":        "decrypt",
							"in":          "query",
							"description": "Decrypt encrypted payload before exporting",
							"required":    false,
							"schema":      map[string]any{"type": "boolean", "default": false},
						},
						{
							"name":        "secure_key",
							"in":          "query",
							"description": "Optional decryption passphrase",
							"required":    false,
							"schema":      map[string]any{"type": "string"},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Raw XML document",
							"content": map[string]any{
								"application/xml": map[string]any{
									"schema": map[string]any{"type": "string"},
								},
							},
						},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"404": map[string]any{"$ref": "#/components/responses/NotFound"},
					},
				},
			},
			"/api/v1/artifacts/{type}/id/{id}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"Artifacts"},
					"summary":     "Get a single artifact by database table ID",
					"description": "Fetches an artifact record by its database primary key `ID` (`TablePipelineContent`, `TableOptionsContent`, or `TableConfigContent`). Optionally decrypts encrypted content when `decrypt=true` is passed. Requires `read-only`, `write`, `upload`, or `all` permission.",
					"operationId": "getArtifactByID",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactIDParam"},
						{
							"name":        "decrypt",
							"in":          "query",
							"description": "Decrypt encrypted payload before returning",
							"required":    false,
							"schema":      map[string]any{"type": "boolean", "default": false},
						},
						{
							"name":        "secure_key",
							"in":          "query",
							"description": "Optional decryption passphrase (or pass via `X-Flow-Secure-Key` header)",
							"required":    false,
							"schema":      map[string]any{"type": "string"},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Artifact record",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ArtifactRecord"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"404": map[string]any{"$ref": "#/components/responses/NotFound"},
					},
				},
				"put": map[string]any{
					"tags":        []string{"Artifacts"},
					"summary":     "Update an existing artifact by database table ID",
					"description": "Updates an existing `pipeline`, `options`, or `config` artifact by its database primary key `ID`. Requires `write`, `upload`, or `all` permission.",
					"operationId": "updateArtifactByID",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactIDParam"},
					},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/ArtifactPayload"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Updated artifact record",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ArtifactRecord"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"404": map[string]any{"$ref": "#/components/responses/NotFound"},
					},
				},
				"delete": map[string]any{
					"tags":        []string{"Artifacts"},
					"summary":     "Delete an artifact by database table ID",
					"description": "Deletes an artifact by its database primary key `ID`. Requires `write`, `upload`, or `all` permission.",
					"operationId": "deleteArtifactByID",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactIDParam"},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Artifact deleted",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"deleted": map[string]any{"type": "boolean", "example": true},
											"type":    map[string]any{"type": "string", "example": "pipeline"},
											"id":      map[string]any{"type": "integer", "example": 1},
										},
									},
								},
							},
						},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"404": map[string]any{"$ref": "#/components/responses/NotFound"},
					},
				},
			},
			"/api/v1/execute": map[string]any{
				"post": map[string]any{
					"tags":        []string{"Execution"},
					"summary":     "Execute a pipeline synchronously",
					"description": "Executes a pipeline loaded from the database by name (`file: \"my_pipeline\"` or `db:my_pipeline`), by database table ID (`id` + `artifact_type`, or `pipeline_id` / `options_id` / `config_id`, or `file: \"id:1\"`), inline XML (`file_xml`), or local path, with optional `config`, `options`, `vars`, `node_output`, encryption, and signature verification. Requires `execution-only` or `all` permission.",
					"operationId": "executePipeline",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/ExecutionRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Pipeline executed successfully",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ExecutionResponse"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"422": map[string]any{
							"description": "Pipeline execution failed",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ExecutionResponse"},
								},
							},
						},
					},
				},
			},
			"/api/v1/execute/{type}/{id}": map[string]any{
				"post": map[string]any{
					"tags":        []string{"Execution"},
					"summary":     "Execute an artifact synchronously by artifact type and database table ID",
					"description": "Executes an artifact stored in `TablePipelineContent` (`type=pipeline`), `TableOptionsContent` (`type=options`), or `TableConfigContent` (`type=config`) by its database primary key `ID`. Optional overrides (`vars`, `config`, `node_output`, `format`, `preflight`, `secure_key`) can be passed via JSON request body or query parameters. Also available at `/api/v1/execution/{type}/{id}`. Requires `execution-only` or `all` permission.",
					"operationId": "executeByArtifactTypeAndID",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactIDParam"},
						{
							"name":        "vars",
							"in":          "query",
							"required":    false,
							"description": "Optional comma-separated KEY=VALUE variable overrides",
							"schema":      map[string]any{"type": "string"},
						},
						{
							"name":        "node_output",
							"in":          "query",
							"required":    false,
							"description": "Persist per-node stdout/results in `pipeline_events.node_output`",
							"schema":      map[string]any{"type": "boolean", "default": false},
						},
						{
							"name":        "preflight",
							"in":          "query",
							"required":    false,
							"description": "Execute preflight nodes instead of flow nodes",
							"schema":      map[string]any{"type": "boolean", "default": false},
						},
					},
					"requestBody": map[string]any{
						"required": false,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/ExecutionRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Artifact executed successfully",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ExecutionResponse"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"422": map[string]any{
							"description": "Pipeline execution failed",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ExecutionResponse"},
								},
							},
						},
					},
				},
			},
			"/api/v1/execute/stream": map[string]any{
				"post": map[string]any{
					"tags":        []string{"Execution"},
					"summary":     "Execute a pipeline with live Server-Sent Events (SSE) streaming",
					"description": "Executes a pipeline and streams real-time `node_event` and final `done` Server-Sent Events (`text/event-stream`). Requires `execution-only` or `all` permission.",
					"operationId": "executePipelineStream",
					"requestBody": map[string]any{
						"required": false,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/ExecutionRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "SSE event stream (`event: node_event`, `event: done`)",
							"content": map[string]any{
								"text/event-stream": map[string]any{
									"schema": map[string]any{"type": "string"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
					},
				},
			},
			"/api/v1/execute/stream/{type}/{id}": map[string]any{
				"post": map[string]any{
					"tags":        []string{"Execution"},
					"summary":     "Stream execution (SSE) by artifact type and database table ID (POST)",
					"description": "Executes an artifact stored in `TablePipelineContent` (`pipeline`), `TableOptionsContent` (`options`), or `TableConfigContent` (`config`) by its database primary key `ID` and streams real-time `node_event` and `done` Server-Sent Events (`text/event-stream`). Also available at `/api/v1/execute/{type}/{id}/stream` and `/api/v1/execution/stream/{type}/{id}`. Requires `execution-only` or `all` permission.",
					"operationId": "executeStreamByArtifactTypeAndIDPost",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactIDParam"},
					},
					"requestBody": map[string]any{
						"required": false,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/ExecutionRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "SSE event stream (`event: node_event`, `event: done`)",
							"content": map[string]any{
								"text/event-stream": map[string]any{
									"schema": map[string]any{"type": "string"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
					},
				},
				"get": map[string]any{
					"tags":        []string{"Execution"},
					"summary":     "Stream execution (SSE) by artifact type and database table ID (GET)",
					"description": "Executes an artifact by its type (`pipeline`, `options`, `config`) and database primary key `ID` over a `GET` SSE stream (`text/event-stream`). Optional parameters (`vars`, `node_output`, `preflight`, `secure_key`) can be passed in the query string. Also available at `/api/v1/execute/{type}/{id}/stream`. Requires `execution-only` or `all` permission.",
					"operationId": "executeStreamByArtifactTypeAndIDGet",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactIDParam"},
						{
							"name":        "vars",
							"in":          "query",
							"required":    false,
							"description": "Optional comma-separated KEY=VALUE variable overrides",
							"schema":      map[string]any{"type": "string"},
						},
						{
							"name":        "node_output",
							"in":          "query",
							"required":    false,
							"description": "Persist per-node stdout/results in `pipeline_events.node_output`",
							"schema":      map[string]any{"type": "boolean", "default": false},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "SSE event stream (`event: node_event`, `event: done`)",
							"content": map[string]any{
								"text/event-stream": map[string]any{
									"schema": map[string]any{"type": "string"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
					},
				},
			},
			"/api/v1/validate": map[string]any{
				"post": map[string]any{
					"tags":        []string{"Execution"},
					"summary":     "Validate pipeline XML syntax, XSD schema, and AST structure",
					"description": "Validates a pipeline without executing its nodes (`validate=true`). Requires `execution-only` or `all` permission.",
					"operationId": "validatePipeline",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/ExecutionRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Pipeline is valid",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ExecutionResponse"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"422": map[string]any{"$ref": "#/components/responses/UnprocessableEntity"},
					},
				},
			},
			"/api/v1/validate/{type}/{id}": map[string]any{
				"post": map[string]any{
					"tags":        []string{"Execution"},
					"summary":     "Validate an artifact by artifact type and database table ID",
					"description": "Validates an artifact stored in `TablePipelineContent` (`pipeline`), `TableOptionsContent` (`options`), or `TableConfigContent` (`config`) by its database primary key `ID` without executing nodes. Requires `execution-only` or `all` permission.",
					"operationId": "validateByArtifactTypeAndID",
					"parameters": []map[string]any{
						{"$ref": "#/components/parameters/ArtifactTypeParam"},
						{"$ref": "#/components/parameters/ArtifactIDParam"},
					},
					"requestBody": map[string]any{
						"required": false,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/ExecutionRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Artifact is valid",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ExecutionResponse"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"422": map[string]any{"$ref": "#/components/responses/UnprocessableEntity"},
					},
				},
			},
			"/api/v1/runs":      buildRunsPathItem("listRuns"),
			"/api/v1/logs/runs": buildRunsPathItem("listLogsRuns"),
			"/api/v1/events":      buildEventsPathItem("listEvents"),
			"/api/v1/logs/events": buildEventsPathItem("listLogsEvents"),
			"/api/v1/audit":           buildAuditPathItem("listAuditEvents"),
			"/api/v1/logs/api-events": buildAuditPathItem("listLogsAPIEvents"),
			"/api/v1/databases/test":  buildDBTestPathItem("testDatabaseConnection"),
			"/api/v1/db/test":         buildDBTestPathItem("testDBConnectionAlias"),
			"/api/v1/users": map[string]any{
				"get": map[string]any{
					"tags":        []string{"Users"},
					"summary":     "List all BasicAuth API users",
					"description": "Lists all users in `TableAPIUsers` (`dbo.flow_api_users`) without password hashes. Requires `all` permission.",
					"operationId": "listUsers",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of API users",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"count": map[string]any{"type": "integer", "example": 1},
											"users": map[string]any{
												"type":  "array",
												"items": map[string]any{"$ref": "#/components/schemas/APIUser"},
											},
										},
									},
								},
							},
						},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
					},
				},
				"post": map[string]any{
					"tags":        []string{"Users"},
					"summary":     "Create or update a BasicAuth API user",
					"description": "Creates or updates a user in `TableAPIUsers` (`dbo.flow_api_users`). Plaintext passwords are automatically hashed with bcrypt before storage. Requires `all` permission.",
					"operationId": "upsertUser",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/UpsertUserRequest"},
							},
						},
					},
					"responses": map[string]any{
						"201": map[string]any{
							"description": "User created or updated",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/APIUser"},
								},
							},
						},
						"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
					},
				},
			},
			"/api/v1/users/{username}": map[string]any{
				"delete": map[string]any{
					"tags":        []string{"Users"},
					"summary":     "Delete a BasicAuth API user",
					"description": "Deletes a user from `TableAPIUsers` (`dbo.flow_api_users`). Requires `all` permission.",
					"operationId": "deleteUser",
					"parameters": []map[string]any{
						{
							"name":        "username",
							"in":          "path",
							"required":    true,
							"description": "Username to delete",
							"schema":      map[string]any{"type": "string", "example": "runner_svc"},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "User deleted",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"deleted":  map[string]any{"type": "boolean", "example": true},
											"username": map[string]any{"type": "string", "example": "runner_svc"},
										},
									},
								},
							},
						},
						"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
						"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
						"404": map[string]any{"$ref": "#/components/responses/NotFound"},
					},
				},
			},
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"basicAuth": map[string]any{
					"type":        "http",
					"scheme":      "basic",
					"description": "HTTP Basic Authentication backed by `dbo.flow_api_users` (`TableAPIUsers`).",
				},
			},
			"parameters": map[string]any{
				"ArtifactTypeParam": map[string]any{
					"name":        "type",
					"in":          "path",
					"required":    true,
					"description": "Artifact category (`pipeline`, `options`, or `config`)",
					"schema": map[string]any{
						"type": "string",
						"enum": []string{"pipeline", "options", "config"},
					},
				},
				"ArtifactNameParam": map[string]any{
					"name":        "name",
					"in":          "path",
					"required":    true,
					"description": "Unique artifact name in the database",
					"schema":      map[string]any{"type": "string", "example": "daily_etl"},
				},
				"ArtifactIDParam": map[string]any{
					"name":        "id",
					"in":          "path",
					"required":    true,
					"description": "Database table primary key `ID` (`INT` / `BIGINT`) in `TablePipelineContent`, `TableOptionsContent`, or `TableConfigContent`",
					"schema":      map[string]any{"type": "integer", "minimum": 1, "example": 1},
				},
			},
			"responses": map[string]any{
				"BadRequest": map[string]any{
					"description": "Bad Request",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
						},
					},
				},
				"Unauthorized": map[string]any{
					"description": "Unauthorized — Missing or invalid BasicAuth credentials",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
						},
					},
				},
				"Forbidden": map[string]any{
					"description": "Forbidden — Authenticated user lacks the required permission scope",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
						},
					},
				},
				"NotFound": map[string]any{
					"description": "Resource not found",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
						},
					},
				},
				"Conflict": map[string]any{
					"description": "Conflict — Artifact already exists",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
						},
					},
				},
				"UnprocessableEntity": map[string]any{
					"description": "Unprocessable Entity — Validation or execution error",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
						},
					},
				},
			},
			"schemas": map[string]any{
				"ErrorResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"error":  map[string]any{"type": "string", "example": "Invalid username or password"},
						"status": map[string]any{"type": "integer", "example": 401},
					},
				},
				"HealthResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":           map[string]any{"type": "string", "example": "ok"},
						"dialect":          map[string]any{"type": "string", "example": "sqlserver"},
						"authenticated_as": map[string]any{"type": "string", "example": "admin"},
						"permission":       map[string]any{"type": "string", "example": "all"},
						"db_spid":          map[string]any{"type": "integer", "example": 64},
						"db_user":          map[string]any{"type": "string", "example": "sa"},
						"application_name": map[string]any{"type": "string", "example": "go-flowAPI"},
						"tables": map[string]any{
							"type":                 "object",
							"additionalProperties": map[string]any{"type": "string"},
						},
					},
				},
				"ArtifactRecord": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":          map[string]any{"type": "integer", "example": 1, "description": "Database table primary key `ID`"},
						"type":        map[string]any{"type": "string", "enum": []string{"pipeline", "options", "config"}, "example": "pipeline"},
						"name":        map[string]any{"type": "string", "example": "daily_etl"},
						"description": map[string]any{"type": "string", "example": "Daily ETL workflow"},
						"content":     map[string]any{"type": "string", "example": "<Flow><Node Name=\"Step1\">...</Node></Flow>"},
						"encrypted":   map[string]any{"type": "boolean", "example": false},
					},
				},
				"ArtifactPayload": map[string]any{
					"type":     "object",
					"required": []string{"name"},
					"properties": map[string]any{
						"name":        map[string]any{"type": "string", "example": "daily_etl"},
						"description": map[string]any{"type": "string", "example": "Daily ETL workflow"},
						"content":     map[string]any{"type": "string", "example": "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Flow/>"},
						"xml_content": map[string]any{"type": "string", "description": "Alias for `content`"},
						"encrypted":   map[string]any{"type": "boolean", "default": false, "description": "Encrypt XML content at rest using `secure_key` or server default key"},
						"secure_key":  map[string]any{"type": "string", "description": "Optional passphrase for encryption"},
						"upsert":      map[string]any{"type": "boolean", "default": false, "description": "Overwrite existing artifact if name already exists"},
					},
				},
				"ArtifactListResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":  map[string]any{"type": "string", "example": "pipeline"},
						"count": map[string]any{"type": "integer", "example": 2},
						"items": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/ArtifactRecord"},
						},
					},
				},
				"ExecutionRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"artifact_type":    map[string]any{"type": "string", "enum": []string{"pipeline", "options", "config"}, "description": "Target artifact type when executing by `id` (`pipeline`, `options`, or `config`)"},
						"id":               map[string]any{"type": "integer", "example": 1, "description": "Database table primary key `ID` for the specified `artifact_type`"},
						"pipeline_id":      map[string]any{"type": "integer", "description": "Database table primary key `ID` in `TablePipelineContent` (`dbo.flow_pipeline_content`)"},
						"options_id":       map[string]any{"type": "integer", "description": "Database table primary key `ID` in `TableOptionsContent` (`dbo.flow_options_content`)"},
						"config_id":        map[string]any{"type": "integer", "description": "Database table primary key `ID` in `TableConfigContent` (`dbo.flow_config_content`)"},
						"file":             map[string]any{"type": "string", "example": "daily_etl", "description": "Database artifact name (`daily_etl`, `db:daily_etl`, `id:1`), or file path"},
						"file_xml":         map[string]any{"type": "string", "description": "Inline pipeline XML (alternative to `file` / `pipeline_id`)"},
						"config":           map[string]any{"type": "string", "description": "Database config artifact name (`id:1` or name) or file path"},
						"config_xml":       map[string]any{"type": "string", "description": "Inline override `<Configuration>` XML"},
						"options":          map[string]any{"type": "string", "description": "Database options artifact name (`id:1` or name) or file path"},
						"options_xml":      map[string]any{"type": "string", "description": "Inline `<cli_options>` XML"},
						"format":           map[string]any{"type": "string", "enum": []string{"json", "jsonpretty", "markdown", "text", "csv"}, "default": "json"},
						"xsd":              map[string]any{"type": "string", "description": "Optional path to XSD schema file for validation"},
						"validate":         map[string]any{"type": "boolean", "default": false, "description": "Validate only without executing nodes"},
						"preflight":        map[string]any{"type": "boolean", "default": false, "description": "Execute preflight nodes instead of flow nodes"},
						"vars":             map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "example": map[string]string{"ENV": "prod"}},
						"vars_raw":         map[string]any{"type": "string", "example": "ENV=prod,BATCH_SIZE=500", "description": "Comma-separated KEY=VALUE variable overrides"},
						"debug":            map[string]any{"type": "boolean", "default": false},
						"gopath":           map[string]any{"type": "string"},
						"xslt":             map[string]any{"type": "string", "description": "XSLT stylesheet artifact name or file path"},
						"out":              map[string]any{"type": "string", "description": "Optional output file path for XSLT transformation"},
						"node_output":      map[string]any{"type": "boolean", "default": false, "description": "Persist redacted & truncated (64 KB) per-node output into `pipeline_events.node_output`"},
						"encrypted":        map[string]any{"type": "boolean", "default": false},
						"secure_key":       map[string]any{"type": "string"},
						"key_file":         map[string]any{"type": "string"},
						"verify_signature": map[string]any{"type": "boolean", "default": false},
						"public_key":       map[string]any{"type": "string"},
						"cert":             map[string]any{"type": "string"},
						"ca_cert":          map[string]any{"type": "string"},
						"keyring":          map[string]any{"type": "string"},
						"signature":        map[string]any{"type": "string"},
					},
				},
				"ScriptResult": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ScriptID":      map[string]any{"type": "string", "example": "HelloNode"},
						"ReturnCode":    map[string]any{"type": "integer", "example": 0},
						"ResultsString": map[string]any{"type": "string", "example": "hello from HelloNode"},
					},
				},
				"ExecutionResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"run_id":           map[string]any{"type": "string", "example": "a1b2c3d4e5f6"},
						"status":           map[string]any{"type": "string", "example": "SUCCEEDED"},
						"started_at":       map[string]any{"type": "string", "format": "date-time"},
						"finished_at":      map[string]any{"type": "string", "format": "date-time"},
						"duration_ms":      map[string]any{"type": "integer", "example": 42},
						"format":           map[string]any{"type": "string", "example": "json"},
						"formatted_output": map[string]any{"type": "string"},
						"results": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/ScriptResult"},
						},
						"error_message": map[string]any{"type": "string"},
					},
				},
				"RunSummaryRecord": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"run_id":           map[string]any{"type": "string"},
						"file_path":        map[string]any{"type": "string"},
						"config_path":      map[string]any{"type": "string"},
						"status":           map[string]any{"type": "string", "example": "SUCCEEDED"},
						"started_at":       map[string]any{"type": "string", "format": "date-time"},
						"finished_at":      map[string]any{"type": "string", "format": "date-time"},
						"duration_ms":      map[string]any{"type": "integer"},
						"task_count":       map[string]any{"type": "integer"},
						"user_name":        map[string]any{"type": "string"},
						"hostname":         map[string]any{"type": "string"},
						"options_path":     map[string]any{"type": "string"},
						"error_class":      map[string]any{"type": "string"},
						"error_message":    map[string]any{"type": "string"},
					},
				},
				"PipelineEventRecord": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"run_id":        map[string]any{"type": "string"},
						"execution_id":  map[string]any{"type": "string"},
						"sequence_num":  map[string]any{"type": "integer"},
						"occurred_at":   map[string]any{"type": "string", "format": "date-time"},
						"event_type":    map[string]any{"type": "string", "example": "node.finished"},
						"node_kind":     map[string]any{"type": "string", "example": "Script"},
						"node_id":       map[string]any{"type": "string", "example": "HelloNode"},
						"status":        map[string]any{"type": "string", "example": "SUCCEEDED"},
						"user_name":     map[string]any{"type": "string"},
						"db_user_name":  map[string]any{"type": "string"},
						"spid":          map[string]any{"type": "string"},
						"hostname":      map[string]any{"type": "string"},
						"error_message": map[string]any{"type": "string"},
						"rows_read":     map[string]any{"type": "integer"},
						"rows_written":  map[string]any{"type": "integer"},
						"rows_affected": map[string]any{"type": "integer"},
						"node_output":   map[string]any{"type": "string"},
					},
				},
				"APIAuditEvent": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"event_id":      map[string]any{"type": "string"},
						"occurred_at":   map[string]any{"type": "string", "format": "date-time"},
						"event_type":    map[string]any{"type": "string", "example": "pipeline.execute"},
						"http_method":   map[string]any{"type": "string", "example": "POST"},
						"path":          map[string]any{"type": "string", "example": "/api/v1/execute"},
						"artifact_type": map[string]any{"type": "string"},
						"artifact_name": map[string]any{"type": "string"},
						"run_id":        map[string]any{"type": "string"},
						"auth_user":     map[string]any{"type": "string", "example": "admin"},
						"remote_addr":   map[string]any{"type": "string"},
						"status_code":   map[string]any{"type": "integer", "example": 200},
						"status":        map[string]any{"type": "string", "example": "SUCCESS"},
						"duration_ms":   map[string]any{"type": "integer"},
						"error_message": map[string]any{"type": "string"},
						"details_json":  map[string]any{"type": "string"},
					},
				},
				"APIUser": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"username":    map[string]any{"type": "string", "example": "admin"},
						"permission":  map[string]any{"type": "string", "example": "all", "description": "Role scope: `execution-only`, `read-only`, `write`, `upload`, or `all` (comma-separated combinations supported)"},
						"is_active":   map[string]any{"type": "boolean", "example": true},
						"description": map[string]any{"type": "string", "example": "Initial administrator with all permissions"},
					},
				},
				"UpsertUserRequest": map[string]any{
					"type":     "object",
					"required": []string{"username", "password", "permission"},
					"properties": map[string]any{
						"username":    map[string]any{"type": "string", "example": "admin"},
						"password":    map[string]any{"type": "string", "example": "StrongAdminPass!2026", "description": "Plaintext password (automatically bcrypt-hashed) or pre-hashed `$2a$`/`sha256:` value"},
						"permission":  map[string]any{"type": "string", "enum": []string{"all", "execution-only", "read-only", "write", "upload"}, "example": "all"},
						"description": map[string]any{"type": "string", "example": "Full administrator account"},
						"is_active":   map[string]any{"type": "boolean", "default": true},
					},
				},
				"DatabaseTestRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"dsn": map[string]any{
							"type":        "string",
							"description": "Optional target database connection string to test. Leave empty (`\"\"`) to test the server's configured startup database (`StartupConnectionString` / `FLOW_API_DSN` / `-dsn`).",
							"example":     "",
						},
					},
				},
				"DatabaseTestResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"connected":        map[string]any{"type": "boolean", "example": true},
						"dialect":          map[string]any{"type": "string", "example": "sqlserver"},
						"spid":             map[string]any{"type": "integer", "example": 55},
						"db_user":          map[string]any{"type": "string", "example": "sa"},
						"application_name": map[string]any{"type": "string", "example": "go-flowAPI"},
					},
				},
			},
		},
	}
}

func buildUploadPathItem(operationID string) map[string]any {
	op := map[string]any{
		"tags":        []string{"Upload"},
		"summary":     "Upload an XML file into the database (multipart/form-data or raw body)",
		"description": "Uploads a `pipeline`, `options`, or `config` XML file into its configured database table (`TablePipelineContent`, `TableOptionsContent`, or `TableConfigContent`), automatically handling UTF-8 BOM / UTF-16LE / UTF-16BE decoding and optional encryption. Requires `write`, `upload`, or `all` permission.",
		"operationId": operationID,
		"parameters": []map[string]any{
			{"$ref": "#/components/parameters/ArtifactTypeParam"},
			{
				"name":        "name",
				"in":          "query",
				"description": "Artifact name (for raw body uploads, or to override the uploaded filename)",
				"required":    false,
				"schema":      map[string]any{"type": "string"},
			},
			{
				"name":        "description",
				"in":          "query",
				"description": "Artifact description",
				"required":    false,
				"schema":      map[string]any{"type": "string"},
			},
			{
				"name":        "encrypted",
				"in":          "query",
				"description": "Encrypt the uploaded XML before storing in the database",
				"required":    false,
				"schema":      map[string]any{"type": "boolean", "default": false},
			},
			{
				"name":        "secure_key",
				"in":          "query",
				"description": "Optional encryption key",
				"required":    false,
				"schema":      map[string]any{"type": "string"},
			},
		},
		"requestBody": map[string]any{
			"required": true,
			"content": map[string]any{
				"multipart/form-data": map[string]any{
					"schema": map[string]any{
						"type":     "object",
						"required": []string{"file"},
						"properties": map[string]any{
							"file":        map[string]any{"type": "string", "format": "binary", "description": "XML file to upload"},
							"name":        map[string]any{"type": "string", "description": "Optional artifact name (defaults to filename without extension)"},
							"description": map[string]any{"type": "string", "description": "Optional description"},
							"encrypted":   map[string]any{"type": "boolean", "default": false},
							"secure_key":  map[string]any{"type": "string"},
						},
					},
				},
				"application/xml": map[string]any{
					"schema": map[string]any{"type": "string", "example": "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Flow/>"},
				},
			},
		},
		"responses": map[string]any{
			"201": map[string]any{
				"description": "Artifact uploaded and stored",
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/ArtifactRecord"},
					},
				},
			},
			"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
			"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
			"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
		},
	}
	return map[string]any{
		"post": op,
	}
}

func buildRunsPathItem(operationID string) map[string]any {
	return map[string]any{
		"get": map[string]any{
			"tags":        []string{"Telemetry & Audit"},
			"summary":     "Query pipeline execution run summaries",
			"description": "Queries `TablePipelineRuns` (`dbo.pipeline_runs`). Requires `read-only` or `all` permission.",
			"operationId": operationID,
			"parameters": []map[string]any{
				{
					"name":        "status",
					"in":          "query",
					"description": "Optional status filter (e.g. `SUCCEEDED`, `FAILED`)",
					"required":    false,
					"schema":      map[string]any{"type": "string"},
				},
				{
					"name":        "limit",
					"in":          "query",
					"description": "Maximum number of runs to return (default 100, max 1000)",
					"required":    false,
					"schema":      map[string]any{"type": "integer", "default": 100},
				},
			},
			"responses": map[string]any{
				"200": map[string]any{
					"description": "Pipeline run summaries",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"count": map[string]any{"type": "integer"},
									"runs": map[string]any{
										"type":  "array",
										"items": map[string]any{"$ref": "#/components/schemas/RunSummaryRecord"},
									},
								},
							},
						},
					},
				},
				"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
				"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
			},
		},
	}
}

func buildEventsPathItem(operationID string) map[string]any {
	return map[string]any{
		"get": map[string]any{
			"tags":        []string{"Telemetry & Audit"},
			"summary":     "Query per-node pipeline execution events and node_output",
			"description": "Queries `TablePipelineEvents` (`dbo.pipeline_events`), including `node_output` when `node_output=true` was enabled on execution. Requires `read-only` or `all` permission.",
			"operationId": operationID,
			"parameters": []map[string]any{
				{
					"name":        "run_id",
					"in":          "query",
					"description": "Filter events by pipeline `run_id`",
					"required":    false,
					"schema":      map[string]any{"type": "string"},
				},
				{
					"name":        "limit",
					"in":          "query",
					"description": "Maximum number of events to return (default 500, max 2000)",
					"required":    false,
					"schema":      map[string]any{"type": "integer", "default": 500},
				},
			},
			"responses": map[string]any{
				"200": map[string]any{
					"description": "Pipeline execution events",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"count": map[string]any{"type": "integer"},
									"events": map[string]any{
										"type":  "array",
										"items": map[string]any{"$ref": "#/components/schemas/PipelineEventRecord"},
									},
								},
							},
						},
					},
				},
				"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
				"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
			},
		},
	}
}

func buildAuditPathItem(operationID string) map[string]any {
	return map[string]any{
		"get": map[string]any{
			"tags":        []string{"Telemetry & Audit"},
			"summary":     "Query API audit events",
			"description": "Queries `TableAPIAuditEvents` (`dbo.flow_api_events`) for CRUD, upload, authentication, user management, and execution audit logs. Requires `read-only` or `all` permission.",
			"operationId": operationID,
			"parameters": []map[string]any{
				{
					"name":        "event_type",
					"in":          "query",
					"description": "Filter by event type (e.g. `pipeline.execute`, `artifact.upload`, `auth.forbidden`)",
					"required":    false,
					"schema":      map[string]any{"type": "string"},
				},
				{
					"name":        "auth_user",
					"in":          "query",
					"description": "Filter by authenticated username",
					"required":    false,
					"schema":      map[string]any{"type": "string"},
				},
				{
					"name":        "status",
					"in":          "query",
					"description": "Filter by status (`SUCCESS`, `ERROR`, `FAILED`, `FORBIDDEN`, `UNAUTHORIZED`)",
					"required":    false,
					"schema":      map[string]any{"type": "string"},
				},
				{
					"name":        "limit",
					"in":          "query",
					"description": "Maximum number of audit events to return (default 100, max 1000)",
					"required":    false,
					"schema":      map[string]any{"type": "integer", "default": 100},
				},
			},
			"responses": map[string]any{
				"200": map[string]any{
					"description": "API audit trail events",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"count": map[string]any{"type": "integer"},
									"api_events": map[string]any{
										"type":  "array",
										"items": map[string]any{"$ref": "#/components/schemas/APIAuditEvent"},
									},
								},
							},
						},
					},
				},
				"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
				"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
			},
		},
	}
}

func buildDBTestPathItem(operationID string) map[string]any {
	return map[string]any{
		"post": map[string]any{
			"tags":        []string{"System"},
			"summary":     "Test connectivity to the startup database or an external DSN",
			"description": "Opens and pings a database connection across all 5 supported drivers (`sqlserver`, `postgres`, `mysql`, `sqlite`, `oracle`) and returns session identity details. Requires `all` permission.",
			"operationId": operationID,
			"requestBody": map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/DatabaseTestRequest"},
					},
				},
			},
			"responses": map[string]any{
				"200": map[string]any{
					"description": "Database connection succeeded",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"$ref": "#/components/schemas/DatabaseTestResponse"},
						},
					},
				},
				"400": map[string]any{"$ref": "#/components/responses/BadRequest"},
				"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
				"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
				"502": map[string]any{"$ref": "#/components/responses/BadRequest"},
			},
		},
	}
}
