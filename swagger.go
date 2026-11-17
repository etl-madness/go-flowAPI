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

func oaiRef(target string) map[string]any {
	return map[string]any{"$ref": target}
}

func oaiProp(typ string, desc string, example any) map[string]any {
	m := map[string]any{"type": typ}
	if desc != "" {
		m["description"] = desc
	}
	if example != nil {
		m["example"] = example
	}
	return m
}

func oaiBoolProp(defaultVal bool, desc string) map[string]any {
	m := map[string]any{"type": "boolean", "default": defaultVal}
	if desc != "" {
		m["description"] = desc
	}
	return m
}

func oaiEnumProp(typ string, enum []string, defaultVal any, desc string, example any) map[string]any {
	m := oaiProp(typ, desc, example)
	m["enum"] = enum
	if defaultVal != nil {
		m["default"] = defaultVal
	}
	return m
}

func oaiQueryParam(name, typ, desc string, defVal any) map[string]any {
	schema := map[string]any{"type": typ}
	if defVal != nil {
		schema["default"] = defVal
	}
	return map[string]any{
		"name":        name,
		"in":          "query",
		"description": desc,
		"required":    false,
		"schema":      schema,
	}
}

func oaiJSONBody(schemaRef string, required bool) map[string]any {
	return map[string]any{
		"required": required,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": oaiRef(schemaRef),
			},
		},
	}
}

func oaiJSONResp(desc string, schema map[string]any) map[string]any {
	return map[string]any{
		"description": desc,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": schema,
			},
		},
	}
}

func oaiContentResp(desc, mediaType string, schema map[string]any) map[string]any {
	return map[string]any{
		"description": desc,
		"content": map[string]any{
			mediaType: map[string]any{
				"schema": schema,
			},
		},
	}
}

func oaiWithCommonResponses(base map[string]any, codes ...string) map[string]any {
	codeToRef := map[string]string{
		"400": "#/components/responses/BadRequest",
		"401": "#/components/responses/Unauthorized",
		"403": "#/components/responses/Forbidden",
		"404": "#/components/responses/NotFound",
		"409": "#/components/responses/Conflict",
		"422": "#/components/responses/UnprocessableEntity",
		"502": "#/components/responses/BadRequest",
	}
	for _, c := range codes {
		if ref, ok := codeToRef[c]; ok {
			base[c] = oaiRef(ref)
		}
	}
	return base
}

type oaiOpSpec struct {
	tag         string
	summary     string
	description string
	operationID string
	params      []map[string]any
	body        map[string]any
	responses   map[string]any
	noAuth      bool
}

func buildOAIOp(spec oaiOpSpec) map[string]any {
	op := map[string]any{
		"tags":        []string{spec.tag},
		"summary":     spec.summary,
		"description": spec.description,
		"operationId": spec.operationID,
		"responses":   spec.responses,
	}
	if len(spec.params) > 0 {
		op["parameters"] = spec.params
	}
	if spec.body != nil {
		op["requestBody"] = spec.body
	}
	if spec.noAuth {
		op["security"] = []map[string]any{}
	}
	return op
}

func buildArtifactItemPathItem(paramRef, idSuffix, targetDesc, delKey string, delExample any) map[string]any {
	baseParams := []map[string]any{
		oaiRef("#/components/parameters/ArtifactTypeParam"),
		oaiRef(paramRef),
	}
	getParams := append(append([]map[string]any{}, baseParams...),
		oaiQueryParam("decrypt", "boolean", "Decrypt encrypted payload before returning", false),
		oaiQueryParam("secure_key", "string", "Optional encryption/decryption passphrase (or pass via `X-Flow-Secure-Key` header)", nil),
	)
	errCodesGet := []string{"401", "403", "404"}
	if idSuffix != "" {
		errCodesGet = []string{"400", "401", "403", "404"}
	}
	return map[string]any{
		"get": buildOAIOp(oaiOpSpec{
			tag:         "Artifacts",
			summary:     "Get a single artifact by " + targetDesc,
			description: "Fetches an artifact record by " + targetDesc + ". Optionally decrypts encrypted content when `decrypt=true` is passed. Requires `read-only`, `write`, `upload`, or `all` permission.",
			operationID: "getArtifact" + idSuffix,
			params:      getParams,
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp("Artifact record", oaiRef("#/components/schemas/ArtifactRecord")),
			}, errCodesGet...),
		}),
		"put": buildOAIOp(oaiOpSpec{
			tag:         "Artifacts",
			summary:     "Update an existing artifact by " + targetDesc,
			description: "Updates an existing `pipeline`, `options`, or `config` artifact by " + targetDesc + ". Requires `write`, `upload`, or `all` permission.",
			operationID: "updateArtifact" + idSuffix,
			params:      baseParams,
			body:        oaiJSONBody("#/components/schemas/ArtifactPayload", true),
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp("Updated artifact record", oaiRef("#/components/schemas/ArtifactRecord")),
			}, "400", "401", "403", "404"),
		}),
		"delete": buildOAIOp(oaiOpSpec{
			tag:         "Artifacts",
			summary:     "Delete an artifact by " + targetDesc,
			description: "Deletes an artifact by " + targetDesc + ". Requires `write`, `upload`, or `all` permission.",
			operationID: "deleteArtifact" + idSuffix,
			params:      baseParams,
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp("Artifact deleted", map[string]any{
					"type": "object",
					"properties": map[string]any{
						"deleted": oaiProp("boolean", "", true),
						"type":    oaiProp("string", "", "pipeline"),
						delKey:    oaiProp( map[bool]string{true: "integer", false: "string"}[delKey == "id"], "", delExample),
					},
				}),
			}, "401", "403", "404"),
		}),
	}
}

// BuildOpenAPISpec constructs the OpenAPI 3.0.3 specification for go-flowAPI.
func (s *APIServer) BuildOpenAPISpec(r *http.Request) map[string]any {
	serverURL := "http://localhost:8080"
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
	}

	typeAndIDParams := []map[string]any{
		oaiRef("#/components/parameters/ArtifactTypeParam"),
		oaiRef("#/components/parameters/ArtifactIDParam"),
	}

	execRespMap := func(okDesc string) map[string]any {
		return oaiWithCommonResponses(map[string]any{
			"200": oaiJSONResp(okDesc, oaiRef("#/components/schemas/ExecutionResponse")),
			"422": oaiJSONResp("Pipeline execution failed", oaiRef("#/components/schemas/ExecutionResponse")),
		}, "400", "401", "403")
	}

	sseRespMap := func() map[string]any {
		return oaiWithCommonResponses(map[string]any{
			"200": oaiContentResp("SSE event stream (`event: node_event`, `event: done`)", "text/event-stream", oaiProp("string", "", nil)),
		}, "400", "401", "403")
	}

	validateRespMap := func(okDesc string) map[string]any {
		return oaiWithCommonResponses(map[string]any{
			"200": oaiJSONResp(okDesc, oaiRef("#/components/schemas/ExecutionResponse")),
		}, "400", "401", "403", "422")
	}

	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "go-flowAPI — CRUD, File Upload, Execution & Telemetry API",
			"version": "1.3.1",
			"description": "Unified REST API for managing `go-flow` database artifacts (`pipeline`, `options`, `config`), " +
				"uploading XML/JSON files to the database, executing and validating pipelines (with full CLI/Builder parity, " +
				"live SSE streaming, and `node_output` logging), and querying execution & API audit telemetry.\n\n" +
				"### Authentication, Role Permissions & Default Strict Pipeline ACLs\n" +
				"All `/api/v1/*` endpoints require HTTP Basic Authentication (or Windows/AD header resolution) against `dbo.flow_api_users` (`TableAPIUsers`). " +
				"By default (`-strict-acl=true`), **all non-admin (`non-\"all\"`) accounts have zero pipeline access unless granted via explicit `allow` rules in `dbo.flow_pipeline_acl`**.\n\n" +
				"| Permission Role | Capabilities |\n" +
				"| :--- | :--- |\n" +
				"| `scoped` | Zero global permissions — strictly governed by granular per-pipeline and per-pipeline-group ACL rules in `dbo.flow_pipeline_acl` |\n" +
				"| `execution-only` | Non-pipeline read/crypto/log access plus pipeline execution/validation when granted via `dbo.flow_pipeline_acl` (or globally if `-strict-acl=false` opt-out is set) |\n" +
				"| `read-only` | Read/list/export `config`/`options` artifacts, query run/event/audit logs, and read pipelines granted via `dbo.flow_pipeline_acl` (or all pipelines if `-strict-acl=false`) |\n" +
				"| `write` / `upload` | Upload/CRUD `config`/`options` artifacts and manage pipelines granted via `dbo.flow_pipeline_acl` (or all pipelines if `-strict-acl=false`) |\n" +
				"| `all` | Full administrative access to all endpoints including `/api/v1/users`, `/api/v1/groups`, `/api/v1/pipeline-groups`, `/api/v1/acls`, and `/api/v1/db/test` |",
		},
		"servers": []map[string]any{
			{"url": serverURL, "description": "Active go-flowAPI Server"},
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
			{"name": "Access Control & Groups", "description": "Manage API user groups, pipeline groups, granular pipeline CRUD/execute ACL rules, and inspect effective permissions"},
		},
		"paths": map[string]any{
			"/api/v1/health": map[string]any{
				"get": buildOAIOp(oaiOpSpec{
					tag:         "System",
					summary:     "Health check and active database session identity",
					description: "Returns server status, database dialect, session SPID/user/application_name, and configured table names. Publicly accessible without BasicAuth (if BasicAuth credentials are supplied, `authenticated_as` and `permission` are also populated).",
					operationID: "getHealth",
					noAuth:      true,
					responses: map[string]any{
						"200": oaiJSONResp("Server and database health details", oaiRef("#/components/schemas/HealthResponse")),
					},
				}),
			},
			"/api/v1/artifacts/{type}": map[string]any{
				"get": buildOAIOp(oaiOpSpec{
					tag:         "Artifacts",
					summary:     "List artifacts by type",
					description: "Lists stored artifacts of the specified type (`pipeline`, `options`, or `config`). For `pipeline`, results are automatically filtered to pipelines where the caller has effective `read` access, and can optionally be filtered by pipeline `group`.",
					operationID: "listArtifacts",
					params: []map[string]any{
						oaiRef("#/components/parameters/ArtifactTypeParam"),
						oaiQueryParam("include_content", "boolean", "When `true` or `1`, includes the full XML `content` in each returned record", false),
						oaiQueryParam("group", "string", "Optional pipeline group filter (applies when `type=pipeline`)", nil),
					},
					responses: oaiWithCommonResponses(map[string]any{
						"200": oaiJSONResp("List of artifacts", oaiRef("#/components/schemas/ArtifactListResponse")),
					}, "400", "401", "403"),
				}),
				"post": buildOAIOp(oaiOpSpec{
					tag:         "Artifacts",
					summary:     "Create (or upsert) an artifact via JSON payload",
					description: "Creates a new `pipeline`, `options`, or `config` artifact in the database. Set `upsert: true` (in JSON or query param) to overwrite if it already exists. Requires `write`, `upload`, or `all` permission.",
					operationID: "createArtifact",
					params: []map[string]any{
						oaiRef("#/components/parameters/ArtifactTypeParam"),
						oaiQueryParam("upsert", "boolean", "When `true`, updates the artifact if it already exists instead of returning 409 Conflict", false),
					},
					body: oaiJSONBody("#/components/schemas/ArtifactPayload", true),
					responses: oaiWithCommonResponses(map[string]any{
						"201": oaiJSONResp("Artifact created", oaiRef("#/components/schemas/ArtifactRecord")),
					}, "400", "401", "403", "409"),
				}),
			},
			"/api/v1/artifacts/{type}/upload": buildUploadPathItem("uploadArtifactViaArtifactsRoute"),
			"/api/v1/upload/{type}":           buildUploadPathItem("uploadArtifact"),
			"/api/v1/artifacts/{type}/{name}": buildArtifactItemPathItem("#/components/parameters/ArtifactNameParam", "", "name", "name", "daily_etl"),
			"/api/v1/artifacts/{type}/{name}/export": map[string]any{
				"get": buildOAIOp(oaiOpSpec{
					tag:         "Artifacts",
					summary:     "Export artifact as raw XML attachment",
					description: "Downloads the artifact XML content as an `application/xml` file attachment. Requires `read-only`, `write`, `upload`, or `all` permission.",
					operationID: "exportArtifact",
					params: []map[string]any{
						oaiRef("#/components/parameters/ArtifactTypeParam"),
						oaiRef("#/components/parameters/ArtifactNameParam"),
						oaiQueryParam("decrypt", "boolean", "Decrypt encrypted payload before exporting", false),
						oaiQueryParam("secure_key", "string", "Optional decryption passphrase", nil),
					},
					responses: oaiWithCommonResponses(map[string]any{
						"200": oaiContentResp("Raw XML document", "application/xml", oaiProp("string", "", nil)),
					}, "401", "403", "404"),
				}),
			},
			"/api/v1/artifacts/{type}/id/{id}": buildArtifactItemPathItem("#/components/parameters/ArtifactIDParam", "ByID", "database table ID", "id", 1),
			"/api/v1/execute": map[string]any{
				"post": buildOAIOp(oaiOpSpec{
					tag:         "Execution",
					summary:     "Execute a pipeline synchronously",
					description: "Executes a pipeline loaded from the database by name (`file: \"my_pipeline\"` or `db:my_pipeline`), by database table ID (`id` + `artifact_type`, or `pipeline_id` / `options_id` / `config_id`, or `file: \"id:1\"`), inline XML (`file_xml`), or local path, with optional `config`, `options`, `vars`, `node_output`, encryption, and signature verification. Requires `execution-only` or `all` permission.",
					operationID: "executePipeline",
					body:        oaiJSONBody("#/components/schemas/ExecutionRequest", true),
					responses:   execRespMap("Pipeline executed successfully"),
				}),
			},
			"/api/v1/execute/{type}/{id}": map[string]any{
				"post": buildOAIOp(oaiOpSpec{
					tag:         "Execution",
					summary:     "Execute an artifact synchronously by artifact type and database table ID",
					description: "Executes an artifact stored in `TablePipelineContent` (`type=pipeline`), `TableOptionsContent` (`type=options`), or `TableConfigContent` (`type=config`) by its database primary key `ID`. Optional overrides (`vars`, `config`, `node_output`, `format`, `preflight`, `secure_key`) can be passed via JSON request body or query parameters. Also available at `/api/v1/execution/{type}/{id}`. Requires `execution-only` or `all` permission.",
					operationID: "executeByArtifactTypeAndID",
					params: append(append([]map[string]any{}, typeAndIDParams...),
						oaiQueryParam("vars", "string", "Optional comma-separated KEY=VALUE variable overrides", nil),
						oaiQueryParam("node_output", "boolean", "Persist per-node stdout/results in `pipeline_events.node_output`", false),
						oaiQueryParam("preflight", "boolean", "Execute preflight nodes instead of flow nodes", false),
					),
					body:      oaiJSONBody("#/components/schemas/ExecutionRequest", false),
					responses: execRespMap("Artifact executed successfully"),
				}),
			},
			"/api/v1/execute/stream": map[string]any{
				"post": buildOAIOp(oaiOpSpec{
					tag:         "Execution",
					summary:     "Execute a pipeline with live Server-Sent Events (SSE) streaming",
					description: "Executes a pipeline and streams real-time `node_event` and final `done` Server-Sent Events (`text/event-stream`). Requires `execution-only` or `all` permission.",
					operationID: "executePipelineStream",
					body:        oaiJSONBody("#/components/schemas/ExecutionRequest", false),
					responses:   sseRespMap(),
				}),
			},
			"/api/v1/execute/stream/{type}/{id}": map[string]any{
				"post": buildOAIOp(oaiOpSpec{
					tag:         "Execution",
					summary:     "Stream execution (SSE) by artifact type and database table ID (POST)",
					description: "Executes an artifact stored in `TablePipelineContent` (`pipeline`), `TableOptionsContent` (`options`), or `TableConfigContent` (`config`) by its database primary key `ID` and streams real-time `node_event` and `done` Server-Sent Events (`text/event-stream`). Also available at `/api/v1/execute/{type}/{id}/stream` and `/api/v1/execution/stream/{type}/{id}`. Requires `execution-only` or `all` permission.",
					operationID: "executeStreamByArtifactTypeAndIDPost",
					params:      typeAndIDParams,
					body:        oaiJSONBody("#/components/schemas/ExecutionRequest", false),
					responses:   sseRespMap(),
				}),
				"get": buildOAIOp(oaiOpSpec{
					tag:         "Execution",
					summary:     "Stream execution (SSE) by artifact type and database table ID (GET)",
					description: "Executes an artifact by its type (`pipeline`, `options`, `config`) and database primary key `ID` over a `GET` SSE stream (`text/event-stream`). Optional parameters (`vars`, `node_output`, `preflight`, `secure_key`) can be passed in the query string. Also available at `/api/v1/execute/{type}/{id}/stream`. Requires `execution-only` or `all` permission.",
					operationID: "executeStreamByArtifactTypeAndIDGet",
					params: append(append([]map[string]any{}, typeAndIDParams...),
						oaiQueryParam("vars", "string", "Optional comma-separated KEY=VALUE variable overrides", nil),
						oaiQueryParam("node_output", "boolean", "Persist per-node stdout/results in `pipeline_events.node_output`", false),
					),
					responses: sseRespMap(),
				}),
			},
			"/api/v1/validate": map[string]any{
				"post": buildOAIOp(oaiOpSpec{
					tag:         "Execution",
					summary:     "Validate pipeline XML syntax, XSD schema, and AST structure",
					description: "Validates a pipeline without executing its nodes (`validate=true`). Requires `execution-only` or `all` permission.",
					operationID: "validatePipeline",
					body:        oaiJSONBody("#/components/schemas/ExecutionRequest", true),
					responses:   validateRespMap("Pipeline is valid"),
				}),
			},
			"/api/v1/validate/{type}/{id}": map[string]any{
				"post": buildOAIOp(oaiOpSpec{
					tag:         "Execution",
					summary:     "Validate an artifact by artifact type and database table ID",
					description: "Validates an artifact stored in `TablePipelineContent` (`pipeline`), `TableOptionsContent` (`options`), or `TableConfigContent` (`config`) by its database primary key `ID` without executing nodes. Requires `execution-only` or `all` permission.",
					operationID: "validateByArtifactTypeAndID",
					params:      typeAndIDParams,
					body:        oaiJSONBody("#/components/schemas/ExecutionRequest", false),
					responses:   validateRespMap("Artifact is valid"),
				}),
			},
			"/api/v1/runs":            buildRunsPathItem("listRuns"),
			"/api/v1/logs/runs":       buildRunsPathItem("listLogsRuns"),
			"/api/v1/events":          buildEventsPathItem("listEvents"),
			"/api/v1/logs/events":     buildEventsPathItem("listLogsEvents"),
			"/api/v1/audit":           buildAuditPathItem("listAuditEvents"),
			"/api/v1/logs/api-events": buildAuditPathItem("listLogsAPIEvents"),
			"/api/v1/databases/test":  buildDBTestPathItem("testDatabaseConnection"),
			"/api/v1/db/test":         buildDBTestPathItem("testDBConnectionAlias"),
			"/api/v1/users": map[string]any{
				"get": buildOAIOp(oaiOpSpec{
					tag:         "Users",
					summary:     "List all BasicAuth API users",
					description: "Lists all users in `TableAPIUsers` (`dbo.flow_api_users`) without password hashes. Requires `all` permission.",
					operationID: "listUsers",
					responses: oaiWithCommonResponses(map[string]any{
						"200": oaiJSONResp("List of API users", map[string]any{
							"type": "object",
							"properties": map[string]any{
								"count": oaiProp("integer", "", 1),
								"users": map[string]any{"type": "array", "items": oaiRef("#/components/schemas/APIUser")},
							},
						}),
					}, "401", "403"),
				}),
				"post": buildOAIOp(oaiOpSpec{
					tag:         "Users",
					summary:     "Create or update a BasicAuth API user",
					description: "Creates or updates a user in `TableAPIUsers` (`dbo.flow_api_users`). Plaintext passwords are automatically hashed with bcrypt before storage. Requires `all` permission.",
					operationID: "upsertUser",
					body:        oaiJSONBody("#/components/schemas/UpsertUserRequest", true),
					responses: oaiWithCommonResponses(map[string]any{
						"201": oaiJSONResp("User created or updated", oaiRef("#/components/schemas/APIUser")),
					}, "400", "401", "403"),
				}),
			},
			"/api/v1/users/{username}": map[string]any{
				"delete": buildOAIOp(oaiOpSpec{
					tag:         "Users",
					summary:     "Delete a BasicAuth API user",
					description: "Deletes a user from `TableAPIUsers` (`dbo.flow_api_users`). Requires `all` permission.",
					operationID: "deleteUser",
					params: []map[string]any{
						{
							"name":        "username",
							"in":          "path",
							"required":    true,
							"description": "Username to delete",
							"schema":      oaiProp("string", "", "runner_svc"),
						},
					},
					responses: oaiWithCommonResponses(map[string]any{
						"200": oaiJSONResp("User deleted", map[string]any{
							"type": "object",
							"properties": map[string]any{
								"deleted":  oaiProp("boolean", "", true),
								"username": oaiProp("string", "", "runner_svc"),
							},
						}),
					}, "401", "403", "404"),
				}),
			},
			"/api/v1/groups":                        buildGroupsCollectionPathItem(),
			"/api/v1/groups/{name}":                 buildGroupItemPathItem(),
			"/api/v1/groups/{name}/members":         buildGroupMembersPathItem(),
			"/api/v1/groups/{name}/members/{username}": buildGroupMemberItemPathItem(),
			"/api/v1/pipeline-groups":               buildPipelineGroupsCollectionPathItem(),
			"/api/v1/pipeline-groups/{name}":        buildPipelineGroupItemPathItem(),
			"/api/v1/pipeline-groups/{name}/pipelines": buildPipelineGroupMembersPathItem(),
			"/api/v1/pipeline-groups/{name}/pipelines/{pipeline}": buildPipelineGroupMemberItemPathItem(),
			"/api/v1/acls":                          buildACLsCollectionPathItem(),
			"/api/v1/acls/{id}":                     buildACLItemPathItem(),
			"/api/v1/permissions/effective":         buildEffectivePermissionsPathItem("getEffectivePermissions"),
			"/api/v1/me/permissions":                buildEffectivePermissionsPathItem("getMyEffectivePermissions"),
		},
		"components": buildOpenAPIComponents(),
	}
}

func buildOpenAPIComponents() map[string]any {
	errRespContent := func(desc string) map[string]any {
		return oaiJSONResp(desc, oaiRef("#/components/schemas/ErrorResponse"))
	}
	artifactTypes := []string{"pipeline", "options", "config"}

	return map[string]any{
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
				"schema":      oaiEnumProp("string", artifactTypes, nil, "", nil),
			},
			"ArtifactNameParam": map[string]any{
				"name":        "name",
				"in":          "path",
				"required":    true,
				"description": "Unique artifact name in the database",
				"schema":      oaiProp("string", "", "daily_etl"),
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
			"BadRequest":          errRespContent("Bad Request"),
			"Unauthorized":        errRespContent("Unauthorized — Missing or invalid BasicAuth credentials"),
			"Forbidden":           errRespContent("Forbidden — Authenticated user lacks the required permission scope"),
			"NotFound":            errRespContent("Resource not found"),
			"Conflict":            errRespContent("Conflict — Artifact already exists"),
			"UnprocessableEntity": errRespContent("Unprocessable Entity — Validation or execution error"),
		},
		"schemas": map[string]any{
			"ErrorResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"error":  oaiProp("string", "", "Invalid username or password"),
					"status": oaiProp("integer", "", 401),
				},
			},
			"HealthResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status":           oaiProp("string", "", "ok"),
					"dialect":          oaiProp("string", "", "sqlserver"),
					"authenticated_as": oaiProp("string", "", "admin"),
					"permission":       oaiProp("string", "", "all"),
					"groups":           map[string]any{"type": "array", "items": oaiProp("string", "", "finance_ops")},
					"db_spid":          oaiProp("integer", "", 64),
					"db_user":          oaiProp("string", "", "sa"),
					"application_name": oaiProp("string", "", "go-flowAPI"),
					"tables":           map[string]any{"type": "object", "additionalProperties": oaiProp("string", "", nil)},
				},
			},
			"ArtifactRecord": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":          oaiProp("integer", "Database table primary key `ID`", 1),
					"type":        oaiEnumProp("string", artifactTypes, nil, "", "pipeline"),
					"name":        oaiProp("string", "", "daily_etl"),
					"description": oaiProp("string", "", "Daily ETL workflow"),
					"content":     oaiProp("string", "", "<Flow><Node Name=\"Step1\">...</Node></Flow>"),
					"encrypted":   oaiProp("boolean", "", false),
					"groups":      map[string]any{"type": "array", "items": oaiProp("string", "", "finance_pipelines")},
				},
			},
			"ArtifactPayload": map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name":        oaiProp("string", "", "daily_etl"),
					"description": oaiProp("string", "", "Daily ETL workflow"),
					"content":     oaiProp("string", "", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Flow/>"),
					"xml_content": oaiProp("string", "Alias for `content`", nil),
					"encrypted":   oaiBoolProp(false, "Encrypt XML content at rest using `secure_key` or server default key"),
					"secure_key":  oaiProp("string", "Optional passphrase for encryption", nil),
					"upsert":      oaiBoolProp(false, "Overwrite existing artifact if name already exists"),
					"group":       oaiProp("string", "Optional pipeline group name (or comma-separated group names) to assign when creating/updating a `pipeline`", nil),
					"groups":      map[string]any{"type": "array", "items": oaiProp("string", "", "finance_pipelines"), "description": "Optional list of pipeline groups to assign when creating/updating a `pipeline`"},
				},
			},
			"ArtifactListResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":  oaiProp("string", "", "pipeline"),
					"count": oaiProp("integer", "", 2),
					"items": map[string]any{"type": "array", "items": oaiRef("#/components/schemas/ArtifactRecord")},
				},
			},
			"ExecutionRequest": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"artifact_type":    oaiEnumProp("string", artifactTypes, nil, "Target artifact type when executing by `id` (`pipeline`, `options`, or `config`)", nil),
					"id":               oaiProp("integer", "Database table primary key `ID` for the specified `artifact_type`", 1),
					"pipeline_id":      oaiProp("integer", "Database table primary key `ID` in `TablePipelineContent` (`dbo.flow_pipeline_content`)", nil),
					"options_id":       oaiProp("integer", "Database table primary key `ID` in `TableOptionsContent` (`dbo.flow_options_content`)", nil),
					"config_id":        oaiProp("integer", "Database table primary key `ID` in `TableConfigContent` (`dbo.flow_config_content`)", nil),
					"file":             oaiProp("string", "Database artifact name (`daily_etl`, `db:daily_etl`, `id:1`), or file path", "daily_etl"),
					"file_xml":         oaiProp("string", "Inline pipeline XML (alternative to `file` / `pipeline_id`)", nil),
					"config":           oaiProp("string", "Database config artifact name (`id:1` or name) or file path", nil),
					"config_xml":       oaiProp("string", "Inline override `<Configuration>` XML", nil),
					"options":          oaiProp("string", "Database options artifact name (`id:1` or name) or file path", nil),
					"options_xml":      oaiProp("string", "Inline `<cli_options>` XML", nil),
					"format":           oaiEnumProp("string", []string{"json", "jsonpretty", "markdown", "text", "csv"}, "json", "", nil),
					"xsd":              oaiProp("string", "Optional path to XSD schema file for validation", nil),
					"validate":         oaiBoolProp(false, "Validate only without executing nodes"),
					"preflight":        oaiBoolProp(false, "Execute preflight nodes instead of flow nodes"),
					"vars":             map[string]any{"type": "object", "additionalProperties": oaiProp("string", "", nil), "example": map[string]string{"ENV": "prod"}},
					"vars_raw":         oaiProp("string", "Comma-separated KEY=VALUE variable overrides", "ENV=prod,BATCH_SIZE=500"),
					"debug":            oaiBoolProp(false, ""),
					"gopath":           oaiProp("string", "", nil),
					"xslt":             oaiProp("string", "XSLT stylesheet artifact name or file path", nil),
					"out":              oaiProp("string", "Optional output file path for XSLT transformation", nil),
					"node_output":      oaiBoolProp(false, "Persist redacted & truncated (64 KB) per-node output into `pipeline_events.node_output`"),
					"encrypted":        oaiBoolProp(false, ""),
					"secure_key":       oaiProp("string", "", nil),
					"key_file":         oaiProp("string", "", nil),
					"verify_signature": oaiBoolProp(false, ""),
					"public_key":       oaiProp("string", "", nil),
					"cert":             oaiProp("string", "", nil),
					"ca_cert":          oaiProp("string", "", nil),
					"keyring":          oaiProp("string", "", nil),
					"signature":        oaiProp("string", "", nil),
				},
			},
			"ScriptResult": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ScriptID":      oaiProp("string", "", "HelloNode"),
					"ReturnCode":    oaiProp("integer", "", 0),
					"ResultsString": oaiProp("string", "", "hello from HelloNode"),
				},
			},
			"ExecutionResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":           oaiProp("string", "", "a1b2c3d4e5f6"),
					"status":           oaiProp("string", "", "SUCCEEDED"),
					"started_at":       map[string]any{"type": "string", "format": "date-time"},
					"finished_at":      map[string]any{"type": "string", "format": "date-time"},
					"duration_ms":      oaiProp("integer", "", 42),
					"format":           oaiProp("string", "", "json"),
					"formatted_output": oaiProp("string", "", nil),
					"results":          map[string]any{"type": "array", "items": oaiRef("#/components/schemas/ScriptResult")},
					"error_message":    oaiProp("string", "", nil),
				},
			},
			"RunSummaryRecord": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":        oaiProp("string", "", nil),
					"file_path":     oaiProp("string", "", nil),
					"config_path":   oaiProp("string", "", nil),
					"status":        oaiProp("string", "", "SUCCEEDED"),
					"started_at":    map[string]any{"type": "string", "format": "date-time"},
					"finished_at":   map[string]any{"type": "string", "format": "date-time"},
					"duration_ms":   oaiProp("integer", "", nil),
					"task_count":    oaiProp("integer", "", nil),
					"user_name":     oaiProp("string", "", nil),
					"hostname":      oaiProp("string", "", nil),
					"options_path":  oaiProp("string", "", nil),
					"error_class":   oaiProp("string", "", nil),
					"error_message": oaiProp("string", "", nil),
				},
			},
			"PipelineEventRecord": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":        oaiProp("string", "", nil),
					"execution_id":  oaiProp("string", "", nil),
					"sequence_num":  oaiProp("integer", "", nil),
					"occurred_at":   map[string]any{"type": "string", "format": "date-time"},
					"event_type":    oaiProp("string", "", "node.finished"),
					"node_kind":     oaiProp("string", "", "Script"),
					"node_id":       oaiProp("string", "", "HelloNode"),
					"status":        oaiProp("string", "", "SUCCEEDED"),
					"user_name":     oaiProp("string", "", nil),
					"db_user_name":  oaiProp("string", "", nil),
					"spid":          oaiProp("string", "", nil),
					"hostname":      oaiProp("string", "", nil),
					"error_message": oaiProp("string", "", nil),
					"rows_read":     oaiProp("integer", "", nil),
					"rows_written":  oaiProp("integer", "", nil),
					"rows_affected": oaiProp("integer", "", nil),
					"node_output":   oaiProp("string", "", nil),
				},
			},
			"APIAuditEvent": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"event_id":      oaiProp("string", "", nil),
					"occurred_at":   map[string]any{"type": "string", "format": "date-time"},
					"event_type":    oaiProp("string", "", "pipeline.execute"),
					"http_method":   oaiProp("string", "", "POST"),
					"path":          oaiProp("string", "", "/api/v1/execute"),
					"artifact_type": oaiProp("string", "", nil),
					"artifact_name": oaiProp("string", "", nil),
					"run_id":        oaiProp("string", "", nil),
					"auth_user":     oaiProp("string", "", "admin"),
					"remote_addr":   oaiProp("string", "", nil),
					"status_code":   oaiProp("integer", "", 200),
					"status":        oaiProp("string", "", "SUCCESS"),
					"duration_ms":   oaiProp("integer", "", nil),
					"error_message": oaiProp("string", "", nil),
					"details_json":  oaiProp("string", "", nil),
				},
			},
			"APIUser": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"username":    oaiProp("string", "", "admin"),
					"permission":  oaiProp("string", "Role scope: `scoped`, `execution-only`, `read-only`, `write`, `upload`, or `all` (comma-separated combinations supported)", "all"),
					"is_active":   oaiProp("boolean", "", true),
					"description": oaiProp("string", "", "Initial administrator with all permissions"),
					"groups":      map[string]any{"type": "array", "items": oaiProp("string", "", "finance_ops")},
				},
			},
			"UpsertUserRequest": map[string]any{
				"type":     "object",
				"required": []string{"username", "password", "permission"},
				"properties": map[string]any{
					"username":    oaiProp("string", "", "admin"),
					"password":    oaiProp("string", "Plaintext password (automatically bcrypt-hashed) or pre-hashed `$2a$`/`sha256:` value", "StrongAdminPass!2026"),
					"permission":  oaiEnumProp("string", []string{"all", "scoped", "execution-only", "read-only", "write", "upload"}, nil, "", "all"),
					"description": oaiProp("string", "", "Full administrator account"),
					"is_active":   oaiBoolProp(true, ""),
					"groups":      map[string]any{"type": "array", "items": oaiProp("string", "", "finance_ops"), "description": "Optional API user groups to assign"},
				},
			},
			"APIGroup":              buildOAIObjectSchema(nil, buildAPIGroupProps(true)),
			"UpsertAPIGroupRequest": buildOAIObjectSchema([]string{"name"}, buildAPIGroupProps(false)),
			"PipelineGroup":         buildOAIObjectSchema(nil, buildPipelineGroupProps(true)),
			"UpsertPipelineGroupRequest": buildOAIObjectSchema([]string{"name"}, buildPipelineGroupProps(false)),
			"PipelineACL":                buildOAIObjectSchema(nil, buildPipelineACLProps(true)),
			"UpsertPipelineACLRequest":   buildOAIObjectSchema([]string{"principal_type", "principal_name", "target_type", "permissions"}, buildPipelineACLProps(false)),
			"EffectivePipelinePermissions": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pipeline":        oaiProp("string", "", "fin_daily_gl"),
					"pipeline_name":   oaiProp("string", "", "fin_daily_gl"),
					"groups":          map[string]any{"type": "array", "items": oaiProp("string", "", "finance_pipelines")},
					"actions":         map[string]any{"type": "object", "additionalProperties": oaiProp("boolean", "", true)},
					"allowed_actions": map[string]any{"type": "array", "items": oaiProp("string", "", "read")},
					"denied_actions":  map[string]any{"type": "array", "items": oaiProp("string", "", "delete")},
				},
			},
			"EffectivePermissionsResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"username":               oaiProp("string", "", "alice"),
					"global_permission":      oaiProp("string", "", "scoped"),
					"groups":                 map[string]any{"type": "array", "items": oaiProp("string", "", "finance_ops")},
					"strict_acl":             oaiProp("boolean", "", true),
					"can_create_ungrouped":   oaiProp("boolean", "", false),
					"creatable_groups":       map[string]any{"type": "array", "items": oaiProp("string", "", "finance_pipelines")},
					"can_execute_inline_xml": oaiProp("boolean", "", false),
					"pipelines":              map[string]any{"type": "array", "items": oaiRef("#/components/schemas/EffectivePipelinePermissions")},
				},
			},
			"DatabaseTestRequest": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"dsn": oaiProp("string", "Optional target database connection string to test. Leave empty (`\"\"`) to test the server's configured startup database (`StartupConnectionString` / `FLOW_API_DSN` / `-dsn`).", ""),
				},
			},
			"DatabaseTestResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"connected":        oaiProp("boolean", "", true),
					"dialect":          oaiProp("string", "", "sqlserver"),
					"spid":             oaiProp("integer", "", 55),
					"db_user":          oaiProp("string", "", "sa"),
					"application_name": oaiProp("string", "", "go-flowAPI"),
				},
			},
		},
	}
}

func buildUploadPathItem(operationID string) map[string]any {
	return map[string]any{
		"post": buildOAIOp(oaiOpSpec{
			tag:         "Upload",
			summary:     "Upload an XML file into the database (multipart/form-data or raw body)",
			description: "Uploads a `pipeline`, `options`, or `config` XML file into its configured database table (`TablePipelineContent`, `TableOptionsContent`, or `TableConfigContent`), automatically handling UTF-8 BOM / UTF-16LE / UTF-16BE decoding and optional encryption. For `pipeline` uploads, enforces granular `create` (new pipeline) or `update` (existing pipeline) permissions.",
			operationID: operationID,
			params: []map[string]any{
				oaiRef("#/components/parameters/ArtifactTypeParam"),
				oaiQueryParam("name", "string", "Artifact name (for raw body uploads, or to override the uploaded filename)", nil),
				oaiQueryParam("description", "string", "Artifact description", nil),
				oaiQueryParam("group", "string", "Optional pipeline group name (or comma-separated group names) to assign when uploading a `pipeline`", nil),
				oaiQueryParam("encrypted", "boolean", "Encrypt the uploaded XML before storing in the database", false),
				oaiQueryParam("secure_key", "string", "Optional encryption key", nil),
			},
			body: map[string]any{
				"required": true,
				"content": map[string]any{
					"multipart/form-data": map[string]any{
						"schema": map[string]any{
							"type":     "object",
							"required": []string{"file"},
							"properties": map[string]any{
								"file":        map[string]any{"type": "string", "format": "binary", "description": "XML file to upload"},
								"name":        oaiProp("string", "Optional artifact name (defaults to filename without extension)", nil),
								"description": oaiProp("string", "Optional description", nil),
								"group":       oaiProp("string", "Optional pipeline group name (or comma-separated group names)", nil),
								"encrypted":   oaiBoolProp(false, ""),
								"secure_key":  oaiProp("string", "", nil),
							},
						},
					},
					"application/xml": map[string]any{
						"schema": oaiProp("string", "", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Flow/>"),
					},
				},
			},
			responses: oaiWithCommonResponses(map[string]any{
				"201": oaiJSONResp("Artifact uploaded and stored", oaiRef("#/components/schemas/ArtifactRecord")),
			}, "400", "401", "403"),
		}),
	}
}

func buildListQueryPathItem(operationID, summary, desc, respDesc, arrayKey, itemRef string, params []map[string]any) map[string]any {
	return map[string]any{
		"get": buildOAIOp(oaiOpSpec{
			tag:         "Telemetry & Audit",
			summary:     summary,
			description: desc,
			operationID: operationID,
			params:      params,
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp(respDesc, map[string]any{
					"type": "object",
					"properties": map[string]any{
						"count":  oaiProp("integer", "", nil),
						arrayKey: map[string]any{"type": "array", "items": oaiRef(itemRef)},
					},
				}),
			}, "401", "403"),
		}),
	}
}

func buildRunsPathItem(operationID string) map[string]any {
	return buildListQueryPathItem(
		operationID,
		"Query pipeline execution run summaries",
		"Queries `TablePipelineRuns` (`dbo.pipeline_runs`). Requires `read-only` or `all` permission.",
		"Pipeline run summaries",
		"runs",
		"#/components/schemas/RunSummaryRecord",
		[]map[string]any{
			oaiQueryParam("status", "string", "Optional status filter (e.g. `SUCCEEDED`, `FAILED`)", nil),
			oaiQueryParam("limit", "integer", "Maximum number of runs to return (default 100, max 1000)", 100),
		},
	)
}

func buildEventsPathItem(operationID string) map[string]any {
	return buildListQueryPathItem(
		operationID,
		"Query per-node pipeline execution events and node_output",
		"Queries `TablePipelineEvents` (`dbo.pipeline_events`), including `node_output` when `node_output=true` was enabled on execution. Requires `read-only` or `all` permission.",
		"Pipeline execution events",
		"events",
		"#/components/schemas/PipelineEventRecord",
		[]map[string]any{
			oaiQueryParam("run_id", "string", "Filter events by pipeline `run_id`", nil),
			oaiQueryParam("limit", "integer", "Maximum number of events to return (default 500, max 2000)", 500),
		},
	)
}

func buildAuditPathItem(operationID string) map[string]any {
	return buildListQueryPathItem(
		operationID,
		"Query API audit events",
		"Queries `TableAPIAuditEvents` (`dbo.flow_api_events`) for CRUD, upload, authentication, user management, and execution audit logs. Requires `read-only` or `all` permission.",
		"API audit trail events",
		"api_events",
		"#/components/schemas/APIAuditEvent",
		[]map[string]any{
			oaiQueryParam("event_type", "string", "Filter by event type (e.g. `pipeline.execute`, `artifact.upload`, `auth.forbidden`)", nil),
			oaiQueryParam("auth_user", "string", "Filter by authenticated username", nil),
			oaiQueryParam("status", "string", "Filter by status (`SUCCESS`, `ERROR`, `FAILED`, `FORBIDDEN`, `UNAUTHORIZED`)", nil),
			oaiQueryParam("limit", "integer", "Maximum number of audit events to return (default 100, max 1000)", 100),
		},
	)
}

func buildDBTestPathItem(operationID string) map[string]any {
	return map[string]any{
		"post": buildOAIOp(oaiOpSpec{
			tag:         "System",
			summary:     "Test connectivity to the startup database or an external DSN",
			description: "Opens and pings a database connection across all 5 supported drivers (`sqlserver`, `postgres`, `mysql`, `sqlite`, `oracle`) and returns session identity details. Requires `all` permission.",
			operationID: operationID,
			body:        oaiJSONBody("#/components/schemas/DatabaseTestRequest", true),
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp("Database connection succeeded", oaiRef("#/components/schemas/DatabaseTestResponse")),
			}, "400", "401", "403", "502"),
		}),
	}
}

const (
	oaiTagAccessControl       = "Access Control & Groups"
	oaiSchemaRefAPIGroup      = "#/components/schemas/APIGroup"
	oaiSchemaRefPipelineGroup = "#/components/schemas/PipelineGroup"
	oaiSchemaRefPipelineACL   = "#/components/schemas/PipelineACL"
)

func buildOAIObjectSchema(required []string, props map[string]any) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func buildAPIGroupProps(includeID bool) map[string]any {
	props := map[string]any{
		"name":        oaiProp("string", "", "finance_ops"),
		"description": oaiProp("string", "", "Finance ETL operators"),
		"members":     map[string]any{"type": "array", "items": oaiProp("string", "", "alice")},
	}
	if includeID {
		props["id"] = oaiProp("integer", "", 1)
	}
	return props
}

func buildPipelineGroupProps(includeID bool) map[string]any {
	props := map[string]any{
		"name":         oaiProp("string", "", "finance_pipelines"),
		"description":  oaiProp("string", "", "Finance ETL pipelines"),
		"name_pattern": oaiProp("string", "Optional glob pattern matching pipeline names (e.g. `fin_*`)", "fin_*"),
		"pipelines":    map[string]any{"type": "array", "items": oaiProp("string", "", "fin_daily_gl")},
	}
	if includeID {
		props["id"] = oaiProp("integer", "", 1)
	}
	return props
}

func buildPipelineACLProps(includeID bool) map[string]any {
	principalDesc := ""
	targetDesc := "Required for `pipeline` and `pipeline_group`; defaults to `*` when `target_type` is `*`"
	if includeID {
		principalDesc = "API username, API group name, or Windows/AD identity (`DOMAIN\\Group`)"
		targetDesc = "Pipeline name, pipeline group name, or `*`"
	}
	props := map[string]any{
		"principal_type": oaiEnumProp("string", []string{"user", "group"}, nil, "", "group"),
		"principal_name": oaiProp("string", principalDesc, "finance_ops"),
		"target_type":    oaiEnumProp("string", []string{"pipeline", "pipeline_group", "*"}, nil, "", "pipeline_group"),
		"target_name":    oaiProp("string", targetDesc, "finance_pipelines"),
		"permissions":    oaiProp("string", "Comma-separated actions: `create`, `read`, `update`, `delete`, `execute`, or `all`", "read,execute"),
		"effect":         oaiEnumProp("string", []string{"allow", "deny"}, "allow", "", "allow"),
		"description":    oaiProp("string", "", "Allow finance_ops to read and execute finance pipelines"),
	}
	if includeID {
		props["id"] = oaiProp("integer", "", 1)
	}
	return props
}

func oaiPathParam(name, typ, desc string, example any) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "path",
		"required":    true,
		"description": desc,
		"schema":      oaiProp(typ, "", example),
	}
}

type oaiNamedGroupDocSpec struct {
	paramDesc        string
	groupExample     string
	itemSchemaRef    string
	upsertSchemaRef  string
	collectionKey    string
	listSummary      string
	listDesc         string
	listOpID         string
	listRespDesc     string
	listErrCodes     []string
	postSummary      string
	postDesc         string
	postOpID         string
	postRespDesc     string
	getSummary       string
	getDesc          string
	getOpID          string
	getRespDesc      string
	putSummary       string
	putDesc          string
	putOpID          string
	putRespDesc      string
	delSummary       string
	delDesc          string
	delOpID          string
	delRespDesc      string
	memListSummary   string
	memListDesc      string
	memListOpID      string
	memListRespDesc  string
	memListKey       string
	memExample       string
	memAddSummary    string
	memAddDesc       string
	memAddOpID       string
	memSingleKey     string
	memPluralKey     string
	memPluralExample string
	memDelSummary    string
	memDelDesc       string
	memDelOpID       string
	memDelParamName  string
	memDelParamDesc  string
	memDelRespDesc   string
	memDelRespKey    string
}

var (
	apiGroupDocSpec = oaiNamedGroupDocSpec{
		paramDesc:        "Group name",
		groupExample:     "finance_ops",
		itemSchemaRef:    oaiSchemaRefAPIGroup,
		upsertSchemaRef:  "#/components/schemas/UpsertAPIGroupRequest",
		collectionKey:    "groups",
		listSummary:      "List all API user groups",
		listDesc:         "Lists all user groups in `TableAPIGroups` (`dbo.flow_api_groups`) and their members. Requires `all` permission.",
		listOpID:         "listGroups",
		listRespDesc:     "List of API user groups",
		listErrCodes:     []string{"401", "403"},
		postSummary:      "Create or update an API user group",
		postDesc:         "Creates or updates a user group in `TableAPIGroups` (`dbo.flow_api_groups`) and optionally assigns initial members. Requires `all` permission.",
		postOpID:         "upsertGroup",
		postRespDesc:     "Group created or updated",
		getSummary:       "Get an API user group by name",
		getDesc:          "Fetches a user group and its member list. Requires `all` permission.",
		getOpID:          "getGroup",
		getRespDesc:      "API user group",
		putSummary:       "Update an API user group",
		putDesc:          "Updates an API user group's description and optional members. Requires `all` permission.",
		putOpID:          "updateGroup",
		putRespDesc:      "Updated API user group",
		delSummary:       "Delete an API user group",
		delDesc:          "Deletes an API user group along with its user memberships and group ACL bindings. Requires `all` permission.",
		delOpID:          "deleteGroup",
		delRespDesc:      "Group deleted",
		memListSummary:   "List members of an API user group",
		memListDesc:      "Lists usernames assigned to the specified API user group. Requires `all` permission.",
		memListOpID:      "listGroupMembers",
		memListRespDesc:  "Group members",
		memListKey:       "members",
		memExample:       "alice",
		memAddSummary:    "Add member(s) to an API user group",
		memAddDesc:       "Adds `username` and/or `usernames` (`members`) to `TableAPIUserGroups`. Requires `all` permission.",
		memAddOpID:       "addGroupMembers",
		memSingleKey:     "username",
		memPluralKey:     "usernames",
		memPluralExample: "bob",
		memDelSummary:    "Remove a user from an API user group",
		memDelDesc:       "Removes a username from `TableAPIUserGroups`. Requires `all` permission.",
		memDelOpID:       "removeGroupMember",
		memDelParamName:  "username",
		memDelParamDesc:  "Username to remove",
		memDelRespDesc:   "Member removed",
		memDelRespKey:    "username",
	}

	pipelineGroupDocSpec = oaiNamedGroupDocSpec{
		paramDesc:        "Pipeline group name",
		groupExample:     "finance_pipelines",
		itemSchemaRef:    oaiSchemaRefPipelineGroup,
		upsertSchemaRef:  "#/components/schemas/UpsertPipelineGroupRequest",
		collectionKey:    "pipeline_groups",
		listSummary:      "List pipeline groups",
		listDesc:         "Lists pipeline groups in `TablePipelineGroups` (`dbo.flow_pipeline_groups`). Non-admin users receive groups containing at least one readable pipeline or creatable group.",
		listOpID:         "listPipelineGroups",
		listRespDesc:     "List of pipeline groups",
		listErrCodes:     []string{"401"},
		postSummary:      "Create or update a pipeline group",
		postDesc:         "Creates or updates a pipeline group in `TablePipelineGroups` with optional `name_pattern` glob (e.g. `fin_*`) and explicit `pipelines` list. Requires `all` permission.",
		postOpID:         "upsertPipelineGroup",
		postRespDesc:     "Pipeline group created or updated",
		getSummary:       "Get a pipeline group by name",
		getDesc:          "Fetches a pipeline group and its explicit/pattern-matched pipelines.",
		getOpID:          "getPipelineGroup",
		getRespDesc:      "Pipeline group",
		putSummary:       "Update a pipeline group",
		putDesc:          "Updates a pipeline group's description, `name_pattern`, and optional member pipelines. Requires `all` permission.",
		putOpID:          "updatePipelineGroup",
		putRespDesc:      "Updated pipeline group",
		delSummary:       "Delete a pipeline group",
		delDesc:          "Deletes a pipeline group along with its explicit pipeline memberships and ACL bindings. Requires `all` permission.",
		delOpID:          "deletePipelineGroup",
		delRespDesc:      "Pipeline group deleted",
		memListSummary:   "List pipelines in a pipeline group",
		memListDesc:      "Lists pipelines explicitly assigned to or matching `name_pattern` of the pipeline group.",
		memListOpID:      "listPipelineGroupMembers",
		memListRespDesc:  "Pipelines in group",
		memListKey:       "pipelines",
		memExample:       "fin_daily_gl",
		memAddSummary:    "Assign pipeline(s) to a pipeline group",
		memAddDesc:       "Adds `pipeline_name` (`pipeline`) and/or `pipelines` to `TablePipelineGroupMembers`. Requires `all` permission.",
		memAddOpID:       "addPipelineGroupMembers",
		memSingleKey:     "pipeline_name",
		memPluralKey:     "pipelines",
		memPluralExample: "fin_ap_export",
		memDelSummary:    "Remove a pipeline from a pipeline group",
		memDelDesc:       "Removes an explicit pipeline membership from `TablePipelineGroupMembers`. Requires `all` permission.",
		memDelOpID:       "removePipelineGroupMember",
		memDelParamName:  "pipeline",
		memDelParamDesc:  "Pipeline name to remove",
		memDelRespDesc:   "Pipeline removed from group",
		memDelRespKey:    "pipeline_name",
	}
)

func buildNamedGroupCollectionDoc(spec oaiNamedGroupDocSpec) map[string]any {
	return map[string]any{
		"get": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     spec.listSummary,
			description: spec.listDesc,
			operationID: spec.listOpID,
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp(spec.listRespDesc, buildOAIObjectSchema(nil, map[string]any{
					"count":            oaiProp("integer", "", 1),
					spec.collectionKey: map[string]any{"type": "array", "items": oaiRef(spec.itemSchemaRef)},
				})),
			}, spec.listErrCodes...),
		}),
		"post": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     spec.postSummary,
			description: spec.postDesc,
			operationID: spec.postOpID,
			body:        oaiJSONBody(spec.upsertSchemaRef, true),
			responses: oaiWithCommonResponses(map[string]any{
				"201": oaiJSONResp(spec.postRespDesc, oaiRef(spec.itemSchemaRef)),
			}, "400", "401", "403"),
		}),
	}
}

func buildNamedGroupItemDoc(spec oaiNamedGroupDocSpec) map[string]any {
	p := []map[string]any{oaiPathParam("name", "string", spec.paramDesc, spec.groupExample)}
	return map[string]any{
		"get": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     spec.getSummary,
			description: spec.getDesc,
			operationID: spec.getOpID,
			params:      p,
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp(spec.getRespDesc, oaiRef(spec.itemSchemaRef)),
			}, "401", "403", "404"),
		}),
		"put": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     spec.putSummary,
			description: spec.putDesc,
			operationID: spec.putOpID,
			params:      p,
			body:        oaiJSONBody(spec.upsertSchemaRef, true),
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp(spec.putRespDesc, oaiRef(spec.itemSchemaRef)),
			}, "400", "401", "403"),
		}),
		"delete": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     spec.delSummary,
			description: spec.delDesc,
			operationID: spec.delOpID,
			params:      p,
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp(spec.delRespDesc, buildOAIObjectSchema(nil, map[string]any{
					"deleted": oaiProp("boolean", "", true),
					"name":    oaiProp("string", "", spec.groupExample),
				})),
			}, "401", "403", "404"),
		}),
	}
}

func buildNamedGroupMembersDoc(spec oaiNamedGroupDocSpec) map[string]any {
	p := []map[string]any{oaiPathParam("name", "string", spec.paramDesc, spec.groupExample)}
	return map[string]any{
		"get": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     spec.memListSummary,
			description: spec.memListDesc,
			operationID: spec.memListOpID,
			params:      p,
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp(spec.memListRespDesc, buildOAIObjectSchema(nil, map[string]any{
					"group":         oaiProp("string", "", spec.groupExample),
					"count":         oaiProp("integer", "", 1),
					spec.memListKey: map[string]any{"type": "array", "items": oaiProp("string", "", spec.memExample)},
				})),
			}, "401", "403", "404"),
		}),
		"post": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     spec.memAddSummary,
			description: spec.memAddDesc,
			operationID: spec.memAddOpID,
			params:      p,
			body: map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": buildOAIObjectSchema(nil, map[string]any{
							spec.memSingleKey: oaiProp("string", "", spec.memExample),
							spec.memPluralKey: map[string]any{"type": "array", "items": oaiProp("string", "", spec.memPluralExample)},
						}),
					},
				},
			},
			responses: oaiWithCommonResponses(map[string]any{
				"201": oaiJSONResp(spec.putRespDesc, oaiRef(spec.itemSchemaRef)),
			}, "400", "401", "403", "404"),
		}),
	}
}

func buildNamedGroupMemberDeleteDoc(spec oaiNamedGroupDocSpec) map[string]any {
	return map[string]any{
		"delete": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     spec.memDelSummary,
			description: spec.memDelDesc,
			operationID: spec.memDelOpID,
			params: []map[string]any{
				oaiPathParam("name", "string", spec.paramDesc, spec.groupExample),
				oaiPathParam(spec.memDelParamName, "string", spec.memDelParamDesc, spec.memExample),
			},
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp(spec.memDelRespDesc, buildOAIObjectSchema(nil, map[string]any{
					"removed":          oaiProp("boolean", "", true),
					"group":            oaiProp("string", "", spec.groupExample),
					spec.memDelRespKey: oaiProp("string", "", spec.memExample),
				})),
			}, "401", "403", "404"),
		}),
	}
}

func buildGroupsCollectionPathItem() map[string]any {
	return buildNamedGroupCollectionDoc(apiGroupDocSpec)
}

func buildGroupItemPathItem() map[string]any {
	return buildNamedGroupItemDoc(apiGroupDocSpec)
}

func buildGroupMembersPathItem() map[string]any {
	return buildNamedGroupMembersDoc(apiGroupDocSpec)
}

func buildGroupMemberItemPathItem() map[string]any {
	return buildNamedGroupMemberDeleteDoc(apiGroupDocSpec)
}

func buildPipelineGroupsCollectionPathItem() map[string]any {
	return buildNamedGroupCollectionDoc(pipelineGroupDocSpec)
}

func buildPipelineGroupItemPathItem() map[string]any {
	return buildNamedGroupItemDoc(pipelineGroupDocSpec)
}

func buildPipelineGroupMembersPathItem() map[string]any {
	return buildNamedGroupMembersDoc(pipelineGroupDocSpec)
}

func buildPipelineGroupMemberItemPathItem() map[string]any {
	return buildNamedGroupMemberDeleteDoc(pipelineGroupDocSpec)
}

func buildACLsCollectionPathItem() map[string]any {
	return map[string]any{
		"get": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     "List granular pipeline ACL rules",
			description: "Lists ACL rules in `TablePipelineACL` (`dbo.flow_pipeline_acl`) with optional filtering by principal or target. Requires `all` permission.",
			operationID: "listPipelineACLs",
			params: []map[string]any{
				oaiQueryParam("principal_type", "string", "Filter by `user` or `group`", nil),
				oaiQueryParam("principal_name", "string", "Filter by username or group name", nil),
				oaiQueryParam("target_type", "string", "Filter by `pipeline`, `pipeline_group`, or `*`", nil),
				oaiQueryParam("target_name", "string", "Filter by pipeline name, pipeline group name, or `*`", nil),
			},
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp("List of pipeline ACL rules", buildOAIObjectSchema(nil, map[string]any{
					"count": oaiProp("integer", "", 1),
					"acls":  map[string]any{"type": "array", "items": oaiRef(oaiSchemaRefPipelineACL)},
				})),
			}, "401", "403"),
		}),
		"post": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     "Create or upsert a granular pipeline ACL rule",
			description: "Creates or updates a pipeline ACL rule in `TablePipelineACL` (`dbo.flow_pipeline_acl`) granting or denying `create`, `read`, `update`, `delete`, `execute`, or `all` on a `pipeline`, `pipeline_group`, or `*`. Requires `all` permission.",
			operationID: "upsertPipelineACL",
			body:        oaiJSONBody("#/components/schemas/UpsertPipelineACLRequest", true),
			responses: oaiWithCommonResponses(map[string]any{
				"201": oaiJSONResp("ACL rule created or updated", oaiRef(oaiSchemaRefPipelineACL)),
			}, "400", "401", "403"),
		}),
	}
}

func buildACLItemPathItem() map[string]any {
	p := []map[string]any{
		{
			"name":        "id",
			"in":          "path",
			"required":    true,
			"description": "ACL rule ID",
			"schema":      map[string]any{"type": "integer", "minimum": 1, "example": 1},
		},
	}
	return map[string]any{
		"put": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     "Update a granular pipeline ACL rule by ID",
			description: "Updates an existing ACL rule in `TablePipelineACL` by its primary key `id`. Requires `all` permission.",
			operationID: "updatePipelineACL",
			params:      p,
			body:        oaiJSONBody("#/components/schemas/UpsertPipelineACLRequest", true),
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp("Updated ACL rule", oaiRef(oaiSchemaRefPipelineACL)),
			}, "400", "401", "403", "404"),
		}),
		"delete": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     "Delete a granular pipeline ACL rule by ID",
			description: "Deletes an ACL rule from `TablePipelineACL` by its primary key `id`. Requires `all` permission.",
			operationID: "deletePipelineACL",
			params:      p,
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp("ACL rule deleted", buildOAIObjectSchema(nil, map[string]any{
					"deleted": oaiProp("boolean", "", true),
					"id":      oaiProp("integer", "", 1),
				})),
			}, "401", "403", "404"),
		}),
	}
}

func buildEffectivePermissionsPathItem(operationID string) map[string]any {
	return map[string]any{
		"get": buildOAIOp(oaiOpSpec{
			tag:         oaiTagAccessControl,
			summary:     "Inspect effective per-pipeline CRUD & execution permissions",
			description: "Computes effective `create`, `read`, `update`, `delete`, and `execute` permissions across pipelines and pipeline groups. Any authenticated user can inspect their own effective permissions; administrators (`all`) may pass `?username=...` and optional `?groups=...` to preview another user's access.",
			operationID: operationID,
			params: []map[string]any{
				oaiQueryParam("username", "string", "Optional target username to inspect (requires `all` permission when inspecting another user)", nil),
				oaiQueryParam("groups", "string", "Optional comma-separated simulated AD/external groups (requires `all` permission)", nil),
			},
			responses: oaiWithCommonResponses(map[string]any{
				"200": oaiJSONResp("Effective permissions matrix", oaiRef("#/components/schemas/EffectivePermissionsResponse")),
			}, "401", "403", "404"),
		}),
	}
}
