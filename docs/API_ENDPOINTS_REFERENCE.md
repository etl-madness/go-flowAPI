---
title: go-flowAPI Complete Endpoints & RBAC Reference
description: Comprehensive overview of all go-flowAPI REST and Server-Sent Events (SSE) endpoints, RBAC permissions, request/response schemas, and telemetry behavior
---

# `go-flowAPI` — Complete API Endpoints & RBAC Reference

> **Unified REST & Server-Sent Events (SSE) API Reference**  
> **OpenAPI 3.0.3 Spec:** `/api/v1/openapi.json` | **Interactive Swagger UI:** `/swagger/` (or `/docs`)  
> **Authentication:** Database-Backed HTTP BasicAuth & Windows/AD Group Resolution (`dbo.flow_api_users`)

This document provides a complete architectural and operational overview of every endpoint exposed by **`go-flowAPI`**, including Role-Based Access Control (RBAC) rules, artifact addressing (by unique `name` or database `id`), synchronous and live SSE pipeline execution, cryptographic utilities, and per-node database session telemetry.

---

## Table of Contents

1. [Endpoint Architecture & Request Lifecycle](#1-endpoint-architecture--request-lifecycle)
2. [Authentication & Role-Based Permission Matrix](#2-authentication--role-based-permission-matrix)
3. [Complete Summary Table of All Endpoints](#3-complete-summary-table-of-all-endpoints)
4. [Category 1: Public Documentation, OpenAPI & Health Endpoints](#4-category-1-public-documentation-openapi--health-endpoints)
5. [Category 2: File Upload & Artifact CRUD (`pipelines`, `options`, `configs`)](#5-category-2-file-upload--artifact-crud-pipelines-options-configs)
6. [Category 3: Pipeline Execution, Live SSE Streaming & Validation](#6-category-3-pipeline-execution-live-sse-streaming--validation)
7. [Category 4: Cryptographic & Digital Signature Endpoints (`/api/v1/crypto/*`)](#7-category-4-cryptographic--digital-signature-endpoints-apiv1crypto)
8. [Category 5: Telemetry, Execution History & Audit Log Endpoints](#8-category-5-telemetry-execution-history--audit-log-endpoints)
9. [Category 6: Database Connectivity Testing & User Management](#9-category-6-database-connectivity-testing--user-management)

---

## 1. Endpoint Architecture & Request Lifecycle

Every incoming HTTP request passes through security headers middleware, RBAC authentication (except public `/swagger/*` and `/api/v1/health` routes), the target handler, and automated audit logging into `dbo.flow_api_events`.

```mermaid
flowchart TD
    CLIENT["HTTP Client / flowUI / CI-CD Runner"] --> MW["Security Middleware<br/>(X-Content-Type-Options: nosniff, X-Frame-Options: DENY)"]

    MW --> PUBLIC{"Public Route?<br/>(/swagger/*, /docs/*,<br/>/api/v1/openapi.json,<br/>/api/v1/health)"}

    PUBLIC -->|"Yes"| H_PUB["Serve Swagger UI, OpenAPI Spec,<br/>or Health Check (Optional BasicAuth)"]
    PUBLIC -->|"No"| AUTH["AuthenticateRequest (auth.go)<br/>1. HTTP BasicAuth against flow_api_users<br/>2. Windows/AD User &amp; Group Fallback"]

    AUTH -->|"401 Unauthorized"| ERR401["Log auth.failed / auth.missing<br/>to flow_api_events &amp; return 401"]
    AUTH -->|"Authenticated"| RBAC{"HasPerm(user.Permission,<br/>requiredPerm)?"}

    RBAC -->|"403 Forbidden"| ERR403["Log auth.forbidden<br/>to flow_api_events &amp; return 403"]
    RBAC -->|"Authorized"| ROUTER["Dispatch to Domain Handler"]

    subgraph Handlers["Domain Route Groups"]
        R_ART["Artifact CRUD &amp; Upload<br/>/api/v1/artifacts/*<br/>/api/v1/upload/*"]
        R_EXEC["Execution, SSE &amp; Validate<br/>/api/v1/execute/*<br/>/api/v1/validate/*"]
        R_CRYPTO["Cryptography &amp; Signatures<br/>/api/v1/crypto/*"]
        R_LOGS["Telemetry &amp; Audit Queries<br/>/api/v1/runs, /api/v1/events, /api/v1/audit"]
        R_ADMIN["Admin &amp; DB Diagnostics<br/>/api/v1/users/*, /api/v1/db/test"]
    end

    ROUTER --> R_ART & R_EXEC & R_CRYPTO & R_LOGS & R_ADMIN
    R_ART & R_EXEC & R_CRYPTO & R_LOGS & R_ADMIN --> AUDIT[("Audit &amp; Telemetry Sink<br/>dbo.flow_api_events<br/>dbo.pipeline_runs<br/>dbo.pipeline_events")]
```

---

## 2. Authentication & Role-Based Permission Matrix

`go-flowAPI` authenticates requests against `TableAPIUsers` (`dbo.flow_api_users`) and supports both **HTTP Basic Authentication** (bcrypt `$2a$`/`$2b$`, `sha256:<hex>`, or constant-time digest comparison) and **Windows / Active Directory Group Resolution** (`DOMAIN\username` or `DOMAIN\groupname`).

Permissions can be assigned as a single role or a comma-separated list (e.g., `"read-only,execution-only"`):

```mermaid
flowchart LR
    subgraph Roles["Configured Permission in flow_api_users"]
        P_ALL["all<br/>(Administrator)"]
        P_WRITE["write / upload<br/>(Publisher)"]
        P_EXEC["execution-only<br/>(Runner Service)"]
        P_READ["read-only<br/>(Auditor / Viewer)"]
    end

    subgraph Capabilities["Granted API Capabilities"]
        C_ADMIN["User Management &amp; DB Test<br/>(/api/v1/users, /api/v1/db/test)"]
        C_MUTATE["Create / Update / Delete / Upload Artifacts<br/>(POST, PUT, DELETE /api/v1/artifacts/*, /api/v1/upload/*)"]
        C_RUN["Execute, Stream &amp; Validate Pipelines<br/>(/api/v1/execute/*, /api/v1/validate/*)"]
        C_VIEW["Read Artifacts, Export XML, Query Logs &amp; Crypto Tools<br/>(GET /api/v1/artifacts/*, /runs, /events, /audit, /crypto/*)"]
    end

    P_ALL --> C_ADMIN & C_MUTATE & C_RUN & C_VIEW
    P_WRITE --> C_MUTATE & C_VIEW
    P_EXEC --> C_RUN & C_VIEW
    P_READ --> C_VIEW
```

| Role Name | Read / Export Artifacts & Logs | Crypto Utilities (`/api/v1/crypto/*`) | Execute, Stream & Validate Pipelines | Upload, Create, Update & Delete Artifacts | Manage Users & Test DBs |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **`read-only`** | ✅ | ✅ | ❌ | ❌ | ❌ |
| **`execution-only`** | ✅ *(Required to load artifacts)* | ✅ | ✅ | ❌ | ❌ |
| **`write`** / **`upload`** | ✅ | ✅ | ❌ *(Unless combined)* | ✅ | ❌ |
| **`all`** | ✅ | ✅ | ✅ | ✅ | ✅ |

---

## 3. Complete Summary Table of All Endpoints

| Category | HTTP Method | Endpoint Path (and Aliases) | Required Permission | Summary |
| :--- | :--- | :--- | :--- | :--- |
| **Docs & OpenAPI** | `GET` | `/swagger/` (or `/swagger`, `/docs`, `/docs/`) | Public | Interactive Swagger UI with BasicAuth **Authorize** modal. |
| **Docs & OpenAPI** | `GET` | `/api/v1/openapi.json` (or `/swagger/doc.json`, `/swagger/openapi.json`) | Public | OpenAPI 3.0.3 JSON specification. |
| **System Health** | `GET` | `/api/v1/health` | Public *(Optional BasicAuth)* | Returns service health, active DB dialect, session identity (`db_spid`, `db_user`, `application_name`), and configured table names. |
| **File Upload** | `POST`, `PUT` | `/api/v1/upload/{type}` (or `/api/v1/artifacts/{type}/upload`) | `write`, `upload`, or `all` | Uploads `.xml`/`.json` files (`multipart/form-data` or raw body) into `pipelines`, `options`, or `configs` (auto-transcodes UTF-16 BOM to UTF-8). |
| **Artifact CRUD** | `GET` | `/api/v1/artifacts/{type}` | `read-only`, `execution-only`, `write`, `upload`, or `all` | Lists all artifacts of `{type}` (`pipeline`, `options`, `config`). Supports `?include_content=true`. |
| **Artifact CRUD** | `POST` | `/api/v1/artifacts/{type}` | `write`, `upload`, or `all` | Creates a new artifact in `{type}` (or upserts when `upsert=true`). |
| **Artifact CRUD** | `GET` | `/api/v1/artifacts/{type}/{name}` | `read-only`, `execution-only`, `write`, `upload`, or `all` | Fetches a single artifact by unique `name`. Supports `?decrypt=true` and `X-Flow-Secure-Key`. |
| **Artifact CRUD** | `PUT` | `/api/v1/artifacts/{type}/{name}` | `write`, `upload`, or `all` | Updates an existing artifact by `name` (description, content, optional encryption). |
| **Artifact CRUD** | `DELETE` | `/api/v1/artifacts/{type}/{name}` | `write`, `upload`, or `all` | Deletes an artifact by `name`. |
| **Artifact CRUD** | `GET` | `/api/v1/artifacts/{type}/{name}/export` | `read-only`, `execution-only`, `write`, `upload`, or `all` | Downloads the artifact as a raw `.xml` attachment (`Content-Disposition: attachment`). |
| **Artifact by ID** | `GET` | `/api/v1/artifacts/{type}/id/{id}` | `read-only`, `execution-only`, `write`, `upload`, or `all` | Fetches a single artifact by its integer database table `ID` (`?decrypt=true` supported). |
| **Artifact by ID** | `PUT` | `/api/v1/artifacts/{type}/id/{id}` | `write`, `upload`, or `all` | Updates an existing artifact by its integer database table `ID`. |
| **Artifact by ID** | `DELETE` | `/api/v1/artifacts/{type}/id/{id}` | `write`, `upload`, or `all` | Deletes an artifact by its integer database table `ID`. |
| **Artifact by ID** | `GET` | `/api/v1/artifacts/{type}/id/{id}/export` | `read-only`, `execution-only`, `write`, `upload`, or `all` | Exports an artifact by database table `ID` as a raw `.xml` attachment. |
| **Execution** | `POST` | `/api/v1/execute` (or `/api/v1/execution`) | `execution-only` or `all` | Executes a pipeline synchronously with full CLI/Builder parity. |
| **Execution** | `POST` | `/api/v1/execute/{type}/{id}` (or `/api/v1/execution/{type}/{id}`) | `execution-only` or `all` | Executes a stored `pipeline`, `options`, or `config` artifact synchronously by database table `ID`. |
| **Live SSE Stream** | `POST`, `GET` | `/api/v1/execute/stream` (or `/api/v1/execution/stream`) | `execution-only` or `all` | Executes a pipeline and streams real-time Server-Sent Events (`event: node_event`, `event: done`). |
| **Live SSE Stream** | `POST`, `GET` | `/api/v1/execute/stream/{type}/{id}` (or `/api/v1/execute/{type}/{id}/stream`) | `execution-only` or `all` | Streams live SSE execution for a stored artifact by `{type}` and database table `{id}`. |
| **Validation** | `POST` | `/api/v1/validate` | `execution-only` or `all` | Validates pipeline XML against XSD and semantic AST rules without executing `<flow>` steps. |
| **Validation** | `POST` | `/api/v1/validate/{type}/{id}` | `execution-only` or `all` | Validates a stored artifact by `{type}` and database table `{id}`. |
| **Cryptography** | `POST` | `/api/v1/crypto/keygen` | `read-only` or higher | Generates AES-256 keys, RSA-2048/4096, ECDSA-P256/P384, Ed25519 keypairs (+ X.509 certs), or OpenPGP keypairs. |
| **Cryptography** | `POST` | `/api/v1/crypto/encrypt` | `read-only` or higher | Encrypts content into a `FLOWENC:v1:` AES-256-GCM armored envelope. |
| **Cryptography** | `POST` | `/api/v1/crypto/decrypt` | `read-only` or higher | Decrypts a `FLOWENC:v1:` payload and inspects inner signature status. |
| **Cryptography** | `POST` | `/api/v1/crypto/sign` | `read-only` or higher | Signs content using `OPENSSL`, `PKCS7`, or `PGP` schemes (`none`, `sign-then-encrypt`, or `encrypt-then-sign`). |
| **Cryptography** | `POST` | `/api/v1/crypto/verify` | `read-only` or higher | Verifies signed/encrypted payloads (`FLOWSIGNED:v1:` or detached signatures) and returns certificate/signer metadata. |
| **Telemetry Logs** | `GET` | `/api/v1/runs` (or `/api/v1/logs/runs`) | `read-only` or higher | Queries pipeline execution summaries from `dbo.pipeline_runs`. |
| **Telemetry Logs** | `GET` | `/api/v1/events` (or `/api/v1/logs/events`) | `read-only` or higher | Queries step-level execution events and `node_output` from `dbo.pipeline_events`. |
| **Audit Logs** | `GET` | `/api/v1/audit` (or `/api/v1/logs/api-events`) | `read-only` or higher | Queries API request and security audit records from `dbo.flow_api_events`. |
| **DB Diagnostics** | `POST` | `/api/v1/db/test` (or `/api/v1/databases/test`) | `all` | Tests connectivity and fetches session identity (`spid`, `db_user`, `application_name`) for the startup DB or a custom DSN. |
| **User Admin** | `GET` | `/api/v1/users` | `all` | Lists all registered API users and their permission scopes (`password_hash` omitted). |
| **User Admin** | `POST` | `/api/v1/users` | `all` | Creates or updates an API user (automatically bcrypt-hashes plaintext passwords). |
| **User Admin** | `DELETE` | `/api/v1/users/{username}` | `all` | Deletes an API user account by username. |

---

## 4. Category 1: Public Documentation, OpenAPI & Health Endpoints

### 4.1 `GET /swagger/` (Aliases: `/swagger`, `/docs`, `/docs/`)
- **Authentication**: Public (No BasicAuth required to load the UI; click **Authorize** inside Swagger UI to supply credentials for protected endpoints).
- **Description**: Serves the interactive Swagger UI preloaded with `/api/v1/openapi.json`.

### 4.2 `GET /api/v1/openapi.json` (Aliases: `/swagger/doc.json`, `/swagger/openapi.json`)
- **Authentication**: Public.
- **Description**: Returns the complete OpenAPI 3.0.3 JSON specification describing all routes, parameters, schemas, and security schemes.

### 4.3 `GET /api/v1/health`
- **Authentication**: Public (No BasicAuth required; if BasicAuth credentials are provided, `authenticated_as` and `permission` are populated in the response).
- **Description**: Verifies API readiness, queries live database session identity from the startup `log_db` connection, and returns the resolved table mapping.

**Example Request & Response:**
```bash
curl http://localhost:5180/api/v1/health
```
```json
{
  "application_name": "go-flowAPI",
  "authenticated_as": "",
  "db_spid": 58,
  "db_user": "dbo",
  "dialect": "sqlserver",
  "permission": "",
  "status": "ok",
  "tables": {
    "api_events": "dbo.flow_api_events",
    "api_users": "dbo.flow_api_users",
    "config_content": "dbo.flow_config_content",
    "options_content": "dbo.flow_options_content",
    "pipeline_content": "dbo.flow_pipeline_content",
    "pipeline_events": "dbo.pipeline_events",
    "pipeline_runs": "dbo.pipeline_runs"
  }
}
```

---

## 5. Category 2: File Upload & Artifact CRUD (`pipelines`, `options`, `configs`)

All artifact endpoints accept `{type}` values mapping to the three repository tables:
- **`pipeline`** (or `pipelines`) → `dbo.flow_pipeline_content` (`PipelineName`, `PipelineXML`)
- **`options`** (or `option`) → `dbo.flow_options_content` (`OptionsName`, `OptionsXML`)
- **`config`** (or `configs`) → `dbo.flow_config_content` (`ConfigName`, `ConfigXML`)

### 5.1 `POST | PUT /api/v1/upload/{type}` (Alias: `/api/v1/artifacts/{type}/upload`)
- **Required Permission**: `write`, `upload`, or `all`.
- **Supported Payload Modes**:
  1. **`multipart/form-data`**: Form fields `file` (required), `name` (optional; defaults to filename without extension), `description` (or `desc`), `encrypted` (`true`/`false`), and `secure_key`.
  2. **Raw Body (`application/xml` or `text/plain`)**: Query parameters `?name=<name>&description=<desc>&encrypted=true&secure_key=<key>` (or `X-Flow-Secure-Key` header).
- **Encoding & Encryption**: Automatically detects and transcodes UTF-16LE/BE BOM files to UTF-8. If `encrypted=true`, wraps the payload in `FLOWENC:v1:` using AES-256-GCM before storing in the database.

```bash
# Multipart file upload
curl -u publisher:PubPass123! \
  -F "file=@daily_orders.xml" \
  -F "name=daily_orders" \
  -F "description=Daily orders synchronization pipeline" \
  http://localhost:5180/api/v1/upload/pipelines
```

### 5.2 `GET /api/v1/artifacts/{type}`
- **Required Permission**: `read-only`, `execution-only`, `write`, `upload`, or `all`.
- **Query Parameters**:
  - `include_content=true` (optional): Includes the full XML/encrypted payload (`content`) and security inspection flags (`encrypted`, `signed`, `signature_format`) in each item.

```bash
curl -u auditor:AuditPass123! \
  "http://localhost:5180/api/v1/artifacts/pipelines?include_content=false"
```

### 5.3 `POST /api/v1/artifacts/{type}`
- **Required Permission**: `write`, `upload`, or `all`.
- **Description**: Creates a new artifact from a JSON payload. Returns `409 Conflict` if an artifact with the same `name` already exists unless `"upsert": true` (or `?upsert=true`) is set.

```bash
curl -u publisher:PubPass123! \
  -X POST http://localhost:5180/api/v1/artifacts/pipelines \
  -H "Content-Type: application/json" \
  -d '{
    "name": "hourly_sync",
    "description": "Hourly incremental sync",
    "content": "<pipeline><flow></flow></pipeline>",
    "encrypted": false,
    "upsert": true
  }'
```

### 5.4 `GET | PUT | DELETE /api/v1/artifacts/{type}/{name}` & `/api/v1/artifacts/{type}/id/{id}`
Artifacts can be retrieved, updated, or deleted either by **unique name** (`/api/v1/artifacts/{type}/{name}`) or by **database table ID** (`/api/v1/artifacts/{type}/id/{id}`):

- **`GET`**: Fetches metadata, content, and cryptographic status (`encrypted`, `signed`, `signature_format`). Pass `?decrypt=true` along with `X-Flow-Secure-Key` (or `?secure_key=...` or server default key) to decrypt `FLOWENC:v1:` payloads on the fly.
- **`PUT`**: Updates `description` and `content` (and optionally re-encrypts when `"encrypted": true`).
- **`DELETE`**: Permanently removes the artifact row from the database table.

```bash
# Fetch pipeline by database ID=4 and decrypt on the fly
curl -u runner_svc:RunPass123! \
  -H "X-Flow-Secure-Key: my-secret-passphrase" \
  "http://localhost:5180/api/v1/artifacts/pipeline/id/4?decrypt=true"
```

### 5.5 `GET /api/v1/artifacts/{type}/{name}/export` & `/api/v1/artifacts/{type}/id/{id}/export`
- **Required Permission**: `read-only`, `execution-only`, `write`, `upload`, or `all`.
- **Description**: Downloads the raw XML content directly with `Content-Type: application/xml; charset=utf-8` and `Content-Disposition: attachment; filename="<name>.xml"`. Supports `?decrypt=true`.

---

## 6. Category 3: Pipeline Execution, Live SSE Streaming & Validation

### 6.1 Execution Lifecycle & Per-Node Database Session Identity

When `go-flowAPI` executes a pipeline, it merges the configuration hierarchy, verifies signatures/encryption, registers all AST nodes with `DatabaseSink`, and logs per-node telemetry to `dbo.pipeline_events` using the exact session identity rules matching the `go-flow` CLI:

```mermaid
flowchart TD
    REQ["Execution Request<br/>POST /api/v1/execute or /api/v1/execute/stream"] --> RESOLVE["1. Resolve Artifacts (Name, ID, db:name, or Inline XML)<br/>Base Pipeline &lt; Options &lt; Config &lt; Request Vars"]

    RESOLVE --> SEC["2. Cryptographic Gate<br/>Verify Signature (FLOWSIGNED:v1 / .sig / .p7s / .asc)<br/>Decrypt Payload (FLOWENC:v1 / AES-256-GCM)"]

    SEC --> VAL["3. Validate XSD &amp; Semantic AST<br/>(If validate=true, return validation result immediately)"]

    VAL --> EXEC["4. Execute Preflight &amp; Flow Nodes<br/>Emit ExecutionEvents to MultiSink (DatabaseSink + SSE CallbackSink)"]

    subgraph IdentityRules["5. DatabaseSink Per-Node Session Identity Resolution"]
        KIND{"Event NodeKind?"}
        KIND -->|"sql"| ID_SQL["Query Connecting DB (node.SQL.DBName)<br/>Log connecting DB application_name, spid, db_user"]
        KIND -->|"sql_bulk (source != target)"| ID_CROSS["Query Source DB &amp; Target DB<br/>application_name = Target DB app name<br/>spid = src->tgt, db_user = src->tgt"]
        KIND -->|"sql_bulk (source == target)"| ID_SAME["Query Connecting DB (node.SQLBulk.DBName)<br/>Log connecting DB application_name, spid, db_user"]
        KIND -->|"All other events<br/>(script, group, kv, run.started, etc.)"| ID_LOG["Use log_db Connection Identity<br/>Log log_db application_name, spid, db_user"]
    end

    EXEC --> KIND
    ID_SQL & ID_CROSS & ID_SAME & ID_LOG --> DB_TABLES[("dbo.pipeline_runs &amp;<br/>dbo.pipeline_events")]
```

### 6.2 `POST /api/v1/execute` & `POST /api/v1/execute/{type}/{id}`
- **Aliases**: `/api/v1/execution`, `/api/v1/execution/{type}/{id}`.
- **Required Permission**: `execution-only` or `all`.
- **Description**: Executes a pipeline synchronously and returns an `ExecutionResponse` JSON object containing `run_id`, `status`, `started_at`, `finished_at`, `duration_ms`, `formatted_output`, and `results`.

#### Supported `ExecutionRequest` JSON Fields & Query Parameters:

| JSON Field | Query Parameter | Type | Description |
| :--- | :--- | :--- | :--- |
| `artifact_type` | `artifact_type` / `type` | `string` | Target artifact type when executing by `id`: `"pipeline"`, `"options"`, or `"config"`. |
| `id` | `id` | `int64` | Database table primary key `ID` for the specified `artifact_type`. |
| `pipeline_id` | `pipeline_id` | `int64` | Explicit `dbo.flow_pipeline_content.ID` to execute. |
| `options_id` | `options_id` | `int64` | Explicit `dbo.flow_options_content.ID` to load CLI options from. |
| `config_id` | `config_id` | `int64` | Explicit `dbo.flow_config_content.ID` to merge environment overrides from. |
| `file` | `file` | `string` | Pipeline reference (`db:<name>`, `id:<id>`, `sql://...`, or file path). |
| `file_xml` | — | `string` | Inline pipeline XML string (used when not referencing a stored pipeline). |
| `options` | `options` | `string` | Options artifact reference (`db:<name>`, `id:<id>`, or path). |
| `options_xml` | — | `string` | Inline `<cli_options>` XML string. |
| `config` | `config` | `string` | Config override artifact reference (`db:<name>`, `id:<id>`, or path). |
| `config_xml` | — | `string` | Inline override `<pipeline>` config XML string. |
| `vars` | — | `map[string]string` | Key-value map of runtime variable overrides (`{"ENV": "prod"}`). |
| `vars_raw` | `vars` | `string` | Comma/semicolon-separated variable overrides (`"ENV=prod,BATCH=500"`). |
| `format` | `format` | `string` | Output format (`json`, `jsonpretty`, `text`, `csv`, `markdown`). |
| `node_output` | `node_output` | `bool` | Captures per-node output into `dbo.pipeline_events.node_output` (64 KB UTF-8 safe truncation + secret redaction). |
| `preflight` | `preflight` | `bool` | Executes only the `<preflight>` section and halts before `<flow>`. |
| `validate` | `validate` | `bool` | Validates XSD & AST without executing pipeline steps. |
| `debug` | `debug` | `bool` | Enables verbose step-level debug logging. |
| `xslt` | `xslt` | `string` | Applies an XSLT 3.0 transformation (`autodoc_html.xslt`, `autodoc_md.xslt`, or custom path) and returns the rendered document. |
| `encrypted` | — | `bool` | Indicates the input artifact is encrypted. |
| `secure_key` | `secure_key` | `string` | Decryption passphrase (or pass via `X-Flow-Secure-Key` header). |
| `verify_signature` | — | `bool` | Enforces digital signature verification before execution. |
| `public_key` / `cert` / `ca_cert` / `keyring` / `signature` | — | `string` | Inline PEM/armored key material or file path for signature verification. |

**Example Synchronous Execution Request:**
```bash
# Execute by artifact name with runtime variable overrides and node_output logging
curl -u runner_svc:RunPass123! \
  -X POST http://localhost:5180/api/v1/execute \
  -H "Content-Type: application/json" \
  -d '{
    "file": "db:daily_orders",
    "config": "db:prod_config",
    "format": "json",
    "node_output": true,
    "vars": {
      "BATCH_SIZE": "5000",
      "REGION": "US-EAST"
    }
  }'

# Execute directly by artifact type and database table ID
curl -u runner_svc:RunPass123! \
  -X POST "http://localhost:5180/api/v1/execute/pipeline/1?node_output=true&format=json"
```

---

### 6.3 `POST | GET /api/v1/execute/stream` & `/api/v1/execute/stream/{type}/{id}`
- **Aliases**: `/api/v1/execute/{type}/{id}/stream`, `/api/v1/execution/stream`, `/api/v1/execution/stream/{type}/{id}`.
- **Required Permission**: `execution-only` or `all`.
- **Description**: Executes a pipeline and streams real-time **Server-Sent Events (`text/event-stream`)** as each node starts and finishes.

```mermaid
sequenceDiagram
    participant Client as Client / flowUI
    participant API as go-flowAPI (/api/v1/execute/stream)
    participant Engine as flow Executor
    participant DB as Audit DB (pipeline_runs / pipeline_events)

    Client->>API: POST /api/v1/execute/stream/pipeline/1
    API-->>Client: HTTP 200 (Content-Type: text/event-stream)
    API->>Engine: ExecutePipeline(ctx, req, onEvent)
    Engine->>DB: INSERT run.started (pipeline_runs & pipeline_events)
    Engine-->>API: EventRunStarted
    API-->>Client: event: node_event (event_type: "run.started")

    loop For Each Pipeline Node
        Engine-->>API: EventNodeStarted
        API-->>Client: event: node_event (event_type: "node.started", node_id: "...")
        Engine->>DB: INSERT node.finished (with resolved DB application_name & node_output)
        Engine-->>API: EventNodeFinished
        API-->>Client: event: node_event (event_type: "node.finished", rows_read, rows_written)
    end

    Engine->>DB: UPDATE pipeline_runs (status = SUCCEEDED)
    API-->>Client: event: done (run_id, status: "SUCCESS", duration, results)
```

**Sample SSE Stream Output:**
```text
event: node_event
data: {"event_type":"run.started","execution_id":"exec-1","kind":"","node_id":"","rows_affected":0,"rows_read":0,"rows_written":0,"run_id":"run-1730462000","sequence":1,"status":"RUNNING","timestamp":"2026-11-01T12:00:00.102Z","type":"node_event"}

event: node_event
data: {"event_type":"node.finished","execution_id":"exec-1","kind":"sql","node_id":"fetch_customers","rows_affected":0,"rows_read":150,"rows_written":0,"run_id":"run-1730462000","sequence":3,"status":"SUCCEEDED","timestamp":"2026-11-01T12:00:00.245Z","type":"node_event"}

event: done
data: {"duration":"250.4ms","results":[...],"run_id":"run-1730462000","status":"SUCCESS","type":"done"}
```

---

### 6.4 `POST /api/v1/validate` & `POST /api/v1/validate/{type}/{id}`
- **Required Permission**: `execution-only` or `all`.
- **Description**: Forces `validate=true` on the execution request, verifying XML well-formedness, XSD compliance (`pipeline.xsd` / `options.xsd`), and AST semantic rules without running database queries or scripts. Returns `200 OK` if valid or `422 Unprocessable Entity` with detailed validation errors.

---

## 7. Category 4: Cryptographic & Digital Signature Endpoints (`/api/v1/crypto/*`)

All `/api/v1/crypto/*` endpoints require `read-only` (or higher) permission and expose pure-Go cryptographic operations from `github.com/etl-madness/flow/flowcrypto`.

```mermaid
flowchart LR
    subgraph Keygen["POST /api/v1/crypto/keygen"]
        KG_SYM["AES-256-GCM<br/>(32-byte Base64 SecureKey)"]
        KG_ASYM["RSA-2048/4096, ECDSA-P256/P384, Ed25519<br/>(Keypair + Optional X.509 Cert)"]
        KG_PGP["OpenPGP / GPG<br/>(Armored Private &amp; Public Keyring)"]
    end

    subgraph Envelopes["Envelope Operations"]
        SIGN["POST /api/v1/crypto/sign<br/>Schemes: OPENSSL, PKCS7, PGP<br/>Modes: none, sign-then-encrypt, encrypt-then-sign"]
        ENC["POST /api/v1/crypto/encrypt<br/>FLOWENC:v1: (AES-256-GCM)"]
        VER["POST /api/v1/crypto/verify<br/>Auto-unwraps &amp; validates signature + CA chain"]
        DEC["POST /api/v1/crypto/decrypt<br/>Decrypts FLOWENC:v1: &amp; inspects inner signature"]
    end

    Keygen --> SIGN & ENC
    SIGN & ENC --> VER & DEC
```

### 7.1 `POST /api/v1/crypto/keygen`
Generates symmetric keys, asymmetric keypairs, self-signed X.509 code-signing certificates, or OpenPGP keypairs.
- **Request Body**:
  ```json
  {
    "algorithm": "RSA-2048",
    "common_name": "Flow Production Signer",
    "email": "security@example.com",
    "validity_days": 365,
    "generate_cert": true
  }
  ```
  *(Supported `algorithm` values: `"AES-256"`, `"RSA-2048"`, `"RSA-4096"`, `"ECDSA-P256"`, `"ECDSA-P384"`, `"ED25519"`, `"PGP"`.)*

### 7.2 `POST /api/v1/crypto/encrypt` & `POST /api/v1/crypto/decrypt`
- **`/api/v1/crypto/encrypt`**: Accepts `{"content": "<xml...>", "secure_key": "..."}` and returns `{"content": "FLOWENC:v1:...", "encrypted": true}`.
- **`/api/v1/crypto/decrypt`**: Accepts `{"content": "FLOWENC:v1:...", "secure_key": "..."}` and returns the plaintext `content` plus `encrypted`, `signed`, and `signature_format` flags.

### 7.3 `POST /api/v1/crypto/sign`
Signs pipeline, options, or config XML content using pure-Go `OPENSSL` (RSA/ECDSA/Ed25519), `PKCS7` (CMS `.p7s`), or `PGP` (OpenPGP) and optionally combines signing with `FLOWENC:v1:` encryption.
- **Request Body**:
  ```json
  {
    "content": "<pipeline><flow></flow></pipeline>",
    "scheme": "OPENSSL",
    "private_key_pem": "-----BEGIN PRIVATE KEY-----\n...",
    "cert_pem": "-----BEGIN CERTIFICATE-----\n...",
    "wrap": true,
    "encrypt_mode": "sign-then-encrypt",
    "secure_key": "my-production-passphrase"
  }
  ```
- **Response**: Returns `format`, `detached_signature` (`FLOWSIG:v1:...`), `signed_payload` (`FLOWSIGNED:v1:...` or outer `FLOWENC:v1:...`), `encrypted`, and `signed`.

### 7.4 `POST /api/v1/crypto/verify`
Verifies `FLOWSIGNED:v1:` wrapped payloads or detached signatures (`signature`), automatically decrypting outer/inner `FLOWENC:v1:` layers when `secure_key` is provided, and validates X.509 certificate chains when `ca_cert_pem` is supplied.
- **Response**: Returns `valid`, `format`, `algorithm`, `signer_info`, X.509 certificate details (`subject`, `issuer`, `serial_number`, `not_before`, `not_after`, `fingerprint`), and `unwrapped_content`.

---

## 8. Category 5: Telemetry, Execution History & Audit Log Endpoints

### 8.1 `GET /api/v1/runs` (Alias: `/api/v1/logs/runs`)
- **Required Permission**: `read-only`, `execution-only`, `write`, `upload`, or `all`.
- **Query Parameters**:
  - `status` (optional): Filter by run status (e.g., `SUCCEEDED`, `FAILED`, `RUNNING`).
  - `limit` (optional, default `100`, max `1000`): Maximum number of rows returned, ordered by `started_at DESC`.
- **Description**: Queries `dbo.pipeline_runs` for high-level pipeline execution summaries (`run_id`, `status`, `started_at`, `finished_at`, `duration_ms`, `spid`, `error_message`).

### 8.2 `GET /api/v1/events` (Alias: `/api/v1/logs/events`)
- **Required Permission**: `read-only`, `execution-only`, `write`, `upload`, or `all`.
- **Query Parameters**:
  - `run_id` (optional): Filter step events for a specific pipeline run (`run_id`).
  - `limit` (optional, default `200`, max `2000`): Maximum number of rows returned.
- **Description**: Queries `dbo.pipeline_events` for step-by-step execution metrics, including `sequence_num`, `event_type`, `node_kind`, `node_id`, `node_path`, `status`, `duration_ms`, `rows_read`, `rows_written`, `rows_affected`, `db_user_name`, `spid`, `application_name`, and `node_output`.

### 8.3 `GET /api/v1/audit` (Alias: `/api/v1/logs/api-events`)
- **Required Permission**: `read-only`, `execution-only`, `write`, `upload`, or `all`.
- **Query Parameters**:
  - `event_type` (optional): Filter by audit event type (e.g., `pipeline.execute`, `artifact.upload`, `auth.failed`, `user.upsert`).
  - `auth_user` (optional): Filter by authenticated username.
  - `status` (optional): Filter by status (`SUCCESS`, `FAILED`, `ERROR`, `UNAUTHORIZED`, `FORBIDDEN`).
  - `limit` (optional, default `200`, max `2000`).
- **Description**: Queries `dbo.flow_api_events` for the complete security and operational audit trail.

---

## 9. Category 6: Database Connectivity Testing & User Management

### 9.1 `POST /api/v1/db/test` (Alias: `/api/v1/databases/test`)
- **Required Permission**: `all`.
- **Description**: Opens and pings either the server's default startup connection (when body is `{}`) or an explicit target `dsn` / `connection_string`, and returns the live database session identity (`dialect`, `spid`, `db_user`, `application_name`).

```bash
curl -u admin:Admin123! \
  -X POST http://localhost:5180/api/v1/db/test \
  -H "Content-Type: application/json" \
  -d '{"dsn": "postgres://flow:flowpass@localhost:5432/flowdb?sslmode=disable"}'
```

### 9.2 `GET /api/v1/users`
- **Required Permission**: `all`.
- **Description**: Returns all accounts in `dbo.flow_api_users` (`id`, `username`, `permission`, `is_active`, `description`, `created_at`, `updated_at`). Password hashes are never returned.

### 9.3 `POST /api/v1/users`
- **Required Permission**: `all`.
- **Description**: Creates or updates (upserts) an API user account in `dbo.flow_api_users`. Plaintext passwords supplied in `"password"` are automatically hashed with **bcrypt** before storage. Supports Windows domain accounts (`DOMAIN\username`) and Active Directory groups (`DOMAIN\GroupName`).

```bash
curl -u admin:Admin123! \
  -X POST http://localhost:5180/api/v1/users \
  -H "Content-Type: application/json" \
  -d '{
    "username": "runner_svc",
    "password": "StrongRunnerPassword!2026",
    "permission": "execution-only",
    "description": "Automated ETL runner service account",
    "is_active": true
  }'
```

### 9.4 `DELETE /api/v1/users/{username}`
- **Required Permission**: `all`.
- **Description**: Deletes the specified user from `dbo.flow_api_users`.

```bash
curl -u admin:Admin123! \
  -X DELETE http://localhost:5180/api/v1/users/runner_svc
```

---

## Related Documentation

- [**`go-flowAPI` Documentation Hub**](index.md)
- [**Cross-Platform Compilation & Build Guide**](COMPILATION_AND_BUILD_GUIDE.md)
- [**Detailed Docker & Docker Compose Guide**](DOCKER_DEPLOYMENT_GUIDE.md)
