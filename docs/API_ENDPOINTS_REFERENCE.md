---
title: go-flowAPI Complete Endpoints & RBAC Reference
description: Comprehensive overview of all go-flowAPI REST and Server-Sent Events (SSE) endpoints, RBAC & granular pipeline ACL permissions, request/response schemas, and telemetry behavior
---

# `go-flowAPI` — Complete API Endpoints, RBAC & Granular ACL Reference

> **Unified REST & Server-Sent Events (SSE) API Reference**  
> **OpenAPI 3.0.3 Spec:** `/api/v1/openapi.json` | **Interactive Swagger UI:** `/swagger/` (or `/docs`)  
> **Authentication & Authorization:** Database-Backed HTTP BasicAuth, Windows/AD Group Resolution (`dbo.flow_api_users`), API User Groups (`dbo.flow_api_groups`), Pipeline Groups (`dbo.flow_pipeline_groups`), and Granular Pipeline ACLs (`dbo.flow_pipeline_acl`)

This document provides a complete architectural and operational overview of every endpoint exposed by **`go-flowAPI`**, including global Role-Based Access Control (RBAC), granular user/group pipeline CRUD & execution ACLs (`allow` / `deny`), artifact addressing (by unique `name` or database `id`), synchronous and live SSE pipeline execution, cryptographic utilities, and per-node database session telemetry.

---

## Table of Contents

1. [Endpoint Architecture & Request Lifecycle](#1-endpoint-architecture--request-lifecycle)
2. [Authentication, Global Roles & Granular ACL Matrix](#2-authentication-global-roles--granular-acl-matrix)
3. [Complete Summary Table of All Endpoints](#3-complete-summary-table-of-all-endpoints)
4. [Category 1: Public Documentation, OpenAPI & Health Endpoints](#4-category-1-public-documentation-openapi--health-endpoints)
5. [Category 2: File Upload & Artifact CRUD (`pipelines`, `options`, `configs`)](#5-category-2-file-upload--artifact-crud-pipelines-options-configs)
6. [Category 3: Pipeline Execution, Live SSE Streaming & Validation](#6-category-3-pipeline-execution-live-sse-streaming--validation)
7. [Category 4: Cryptographic & Digital Signature Endpoints (`/api/v1/crypto/*`)](#7-category-4-cryptographic--digital-signature-endpoints-apiv1crypto)
8. [Category 5: Telemetry, Execution History & Audit Log Endpoints](#8-category-5-telemetry-execution-history--audit-log-endpoints)
9. [Category 6: Database Connectivity Testing & User Management](#9-category-6-database-connectivity-testing--user-management)
10. [Category 7: Granular Pipeline ACLs, User Groups, Pipeline Groups & Effective Permissions](#10-category-7-granular-pipeline-acls-user-groups-pipeline-groups--effective-permissions)

---

## 1. Endpoint Architecture & Request Lifecycle

Every incoming HTTP request passes through security headers middleware, authentication + group resolution (except public `/swagger/*`, `/api/v1/openapi.json`, and `/api/v1/health` routes), route/pipeline authorization (`HasPerm` + `AuthorizePipelineAction`), the target handler, and automated audit logging into `dbo.flow_api_events`.

```mermaid
flowchart TD
    CLIENT["HTTP Client / flowUI / CI-CD Runner"] --> MW["Security Middleware<br/>(X-Content-Type-Options: nosniff, X-Frame-Options: DENY)"]

    MW --> PUBLIC{"Public Route?<br/>(/swagger/*, /docs/*,<br/>/api/v1/openapi.json,<br/>/api/v1/health)"}

    PUBLIC -->|"Yes"| H_PUB["Serve Swagger UI, OpenAPI Spec,<br/>or Health Check (Optional BasicAuth)"]
    PUBLIC -->|"No"| AUTH["AuthenticateRequest (auth.go)<br/>1. HTTP BasicAuth against flow_api_users<br/>2. Windows/AD User &amp; Group Fallback<br/>3. Resolve API Groups (flow_api_user_groups)"]

    AUTH -->|"401 Unauthorized"| ERR401["Log auth.failed / auth.missing<br/>to flow_api_events &amp; return 401"]
    AUTH -->|"Authenticated"| RBAC{"Route RBAC + Pipeline ACL<br/>(all -&gt; Explicit DENY -&gt; Explicit ALLOW -&gt; Global Role)"}

    RBAC -->|"403 Forbidden"| ERR403["Log auth.forbidden<br/>to flow_api_events &amp; return 403"]
    RBAC -->|"Authorized"| ROUTER["Dispatch to Domain Handler"]

    subgraph Handlers["Domain Route Groups"]
        R_ART["Artifact CRUD &amp; Upload<br/>/api/v1/artifacts/*<br/>/api/v1/upload/*"]
        R_EXEC["Execution, SSE &amp; Validate<br/>/api/v1/execute/*<br/>/api/v1/validate/*"]
        R_CRYPTO["Cryptography &amp; Signatures<br/>/api/v1/crypto/*"]
        R_LOGS["Telemetry &amp; Audit Queries<br/>/api/v1/runs, /api/v1/events, /api/v1/audit"]
        R_ADMIN["Admin, Groups, ACLs &amp; DB Test<br/>/api/v1/users, /groups, /pipeline-groups,<br/>/acls, /permissions/effective, /db/test"]
    end

    ROUTER --> R_ART & R_EXEC & R_CRYPTO & R_LOGS & R_ADMIN
    R_ART & R_EXEC & R_CRYPTO & R_LOGS & R_ADMIN --> AUDIT[("Audit &amp; Telemetry Sink<br/>dbo.flow_api_events<br/>dbo.pipeline_runs<br/>dbo.pipeline_events")]
```

---

## 2. Authentication, Global Roles & Granular ACL Matrix

`go-flowAPI` authenticates requests against `TableAPIUsers` (`dbo.flow_api_users`) and resolves group memberships from both `TableAPIUserGroups` (`dbo.flow_api_user_groups`) and **Windows / Active Directory headers** (`X-Flow-Windows-User`, `X-Flow-Windows-Groups` Base64 JSON array of `DOMAIN\Group` names).

Password hashes support **bcrypt** (`$2a$` / `$2b$`), **`sha256:<hex>`**, or constant-time digest comparison. Global permissions can be configured as a single role or comma-separated roles (e.g., `"read-only,execution-only"`):

```mermaid
flowchart LR
    subgraph Roles["Configured Role in flow_api_users"]
        P_ALL["all<br/>(Administrator)"]
        P_WRITE["write / upload<br/>(Publisher)"]
        P_EXEC["execution-only<br/>(Runner Service)"]
        P_READ["read-only<br/>(Auditor / Viewer)"]
        P_SCOPED["scoped<br/>(Zero Global Rights;<br/>ACL-Only Principal)"]
    end

    subgraph ACL["Granular Pipeline ACLs (flow_pipeline_acl)"]
        A_RULES["User / Group Rules (allow | deny)<br/>Targets: pipeline, pipeline_group, *<br/>Actions: create, read, update, delete, execute, all"]
    end

    subgraph Capabilities["Granted API Capabilities"]
        C_ADMIN["Manage Users, Groups, Pipeline Groups,<br/>ACLs &amp; Test DBs (/api/v1/users, /groups, /acls, /db/test)"]
        C_MUTATE["Create / Update / Delete / Upload Artifacts<br/>(POST, PUT, DELETE /api/v1/artifacts/*, /api/v1/upload/*)"]
        C_RUN["Execute, Stream &amp; Validate Pipelines<br/>(/api/v1/execute/*, /api/v1/validate/*)"]
        C_VIEW["Read Artifacts, Export XML, Query Logs &amp; Crypto Tools<br/>(GET /api/v1/artifacts/*, /runs, /events, /audit, /crypto/*)"]
    end

    P_ALL --> C_ADMIN & C_MUTATE & C_RUN & C_VIEW
    P_WRITE --> C_MUTATE & C_VIEW
    P_EXEC --> C_RUN & C_VIEW
    P_READ --> C_VIEW
    P_SCOPED --> A_RULES
    A_RULES -.->|"Grants or Denies Pipeline CRUD + Execute"| C_MUTATE & C_RUN & C_VIEW
```

| Role Name | Read / Export `config` & `options`, Logs & Crypto | Read / Export `pipeline` Artifacts | Execute, Stream & Validate Pipelines | Upload, Create, Update & Delete Pipelines | Upload, Create, Update & Delete `config` / `options` | Manage Users, Groups, ACLs & Test DBs |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| **`scoped`** | ❌ | 🔒 *Per-ACL (`read`)* | 🔒 *Per-ACL (`execute`)* | 🔒 *Per-ACL (`create`, `update`, `delete`)* | ❌ | ❌ *(Self-inspect `/me/permissions` allowed)* |
| **`read-only`** | ✅ | ✅ *(Unless ACL `deny`)* | 🔒 *Only if ACL `allow`* | 🔒 *Only if ACL `allow`* | ❌ | ❌ |
| **`execution-only`** | ✅ | ✅ *(Unless ACL `deny`)* | ✅ *(Unless ACL `deny`)* | 🔒 *Only if ACL `allow`* | ❌ | ❌ |
| **`write`** / **`upload`** | ✅ | ✅ *(Unless ACL `deny`)* | 🔒 *Only if combined or ACL `allow`* | ✅ *(Unless ACL `deny`)* | ✅ | ❌ |
| **`all`** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |

> **Strict ACL Mode (`-strict-acl` / `FLOW_API_STRICT_ACL=true`):** When enabled on the server, all non-`all` roles ignore global pipeline baseline permissions and behave like `scoped` for pipeline CRUD and execution — every pipeline action requires an explicit `allow` rule in `dbo.flow_pipeline_acl` (with zero matching `deny` rules).

---

## 3. Complete Summary Table of All Endpoints

| Category | HTTP Method | Endpoint Path (and Aliases) | Required Permission | Summary |
| :--- | :--- | :--- | :--- | :--- |
| **Docs & OpenAPI** | `GET` | `/swagger/` (or `/swagger`, `/docs`, `/docs/`) | Public | Interactive Swagger UI with BasicAuth **Authorize** modal. |
| **Docs & OpenAPI** | `GET` | `/api/v1/openapi.json` (or `/swagger/doc.json`, `/swagger/openapi.json`) | Public | OpenAPI 3.0.3 JSON specification. |
| **System Health** | `GET` | `/api/v1/health` | Public *(Optional BasicAuth)* | Returns service health, active DB dialect, session identity (`db_spid`, `db_user`, `application_name`), caller `groups`, and configured table names. |
| **File Upload** | `POST`, `PUT` | `/api/v1/upload/{type}` (or `/api/v1/artifacts/{type}/upload`) | `write`, `upload`, `all`, or Pipeline ACL (`create`/`update`) | Uploads `.xml`/`.json` files (`multipart/form-data` or raw body) into `pipelines`, `options`, or `configs` (supports optional `group` assignment for pipelines). |
| **Artifact CRUD** | `GET` | `/api/v1/artifacts/{type}` | `read-only`, `execution-only`, `write`, `upload`, `all`, or Pipeline ACL (`read`) | Lists artifacts of `{type}` (`pipeline`, `options`, `config`). For `pipeline`, automatically filters out pipelines the caller is not permitted to `read`. |
| **Artifact CRUD** | `POST` | `/api/v1/artifacts/{type}` | `write`, `upload`, `all`, or Pipeline ACL (`create`/`update`) | Creates a new artifact in `{type}` (or upserts when `upsert=true`). Supports `"groups": [...]` for `pipeline` artifacts. |
| **Artifact CRUD** | `GET` | `/api/v1/artifacts/{type}/{name}` | `read-only`+ or Pipeline ACL (`read`) | Fetches a single artifact by unique `name`. Supports `?decrypt=true` and `X-Flow-Secure-Key`. |
| **Artifact CRUD** | `PUT` | `/api/v1/artifacts/{type}/{name}` | `write`, `upload`, `all`, or Pipeline ACL (`update`) | Updates an existing artifact by `name` (description, content, optional encryption, optional `groups`). |
| **Artifact CRUD** | `DELETE` | `/api/v1/artifacts/{type}/{name}` | `write`, `upload`, `all`, or Pipeline ACL (`delete`) | Deletes an artifact by `name` (cleans up pipeline group memberships and direct ACLs when deleting a `pipeline`). |
| **Artifact CRUD** | `GET` | `/api/v1/artifacts/{type}/{name}/export` | `read-only`+ or Pipeline ACL (`read`) | Downloads the artifact as a raw `.xml` attachment (`Content-Disposition: attachment`). |
| **Artifact by ID** | `GET` | `/api/v1/artifacts/{type}/id/{id}` | `read-only`+ or Pipeline ACL (`read`) | Fetches a single artifact by its integer database table `ID` (`?decrypt=true` supported). |
| **Artifact by ID** | `PUT` | `/api/v1/artifacts/{type}/id/{id}` | `write`, `upload`, `all`, or Pipeline ACL (`update`) | Updates an existing artifact by its integer database table `ID`. |
| **Artifact by ID** | `DELETE` | `/api/v1/artifacts/{type}/id/{id}` | `write`, `upload`, `all`, or Pipeline ACL (`delete`) | Deletes an artifact by its integer database table `ID`. |
| **Artifact by ID** | `GET` | `/api/v1/artifacts/{type}/id/{id}/export` | `read-only`+ or Pipeline ACL (`read`) | Exports an artifact by database table `ID` as a raw `.xml` attachment. |
| **Execution** | `POST` | `/api/v1/execute` (or `/api/v1/execution`) | `execution-only`, `all`, or Pipeline ACL (`execute`) | Executes a pipeline synchronously with full CLI/Builder parity and per-pipeline ACL enforcement. |
| **Execution** | `POST` | `/api/v1/execute/{type}/{id}` (or `/api/v1/execution/{type}/{id}`) | `execution-only`, `all`, or Pipeline ACL (`execute`) | Executes a stored `pipeline`, `options`, or `config` artifact synchronously by database table `ID`. |
| **Live SSE Stream** | `POST`, `GET` | `/api/v1/execute/stream` (or `/api/v1/execution/stream`) | `execution-only`, `all`, or Pipeline ACL (`execute`) | Executes a pipeline and streams real-time Server-Sent Events (`event: node_event`, `event: done`). |
| **Live SSE Stream** | `POST`, `GET` | `/api/v1/execute/stream/{type}/{id}` (or `/api/v1/execute/{type}/{id}/stream`) | `execution-only`, `all`, or Pipeline ACL (`execute`) | Streams live SSE execution for a stored artifact by `{type}` and database table `{id}`. |
| **Validation** | `POST` | `/api/v1/validate` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Validates pipeline XML against XSD and semantic AST rules without executing `<flow>` steps. |
| **Validation** | `POST` | `/api/v1/validate/{type}/{id}` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Validates a stored artifact by `{type}` and database table `{id}`. |
| **Cryptography** | `POST` | `/api/v1/crypto/keygen` | `read-only` or higher | Generates AES-256 keys, RSA-2048/4096, ECDSA-P256/P384, Ed25519 keypairs (+ X.509 certs), or OpenPGP keypairs. |
| **Cryptography** | `POST` | `/api/v1/crypto/encrypt` | `read-only` or higher | Encrypts content into a `FLOWENC:v1:` AES-256-GCM armored envelope. |
| **Cryptography** | `POST` | `/api/v1/crypto/decrypt` | `read-only` or higher | Decrypts a `FLOWENC:v1:` payload and inspects inner signature status. |
| **Cryptography** | `POST` | `/api/v1/crypto/sign` | `read-only` or higher | Signs content using `OPENSSL`, `PKCS7`, or `PGP` schemes (`none`, `sign-then-encrypt`, or `encrypt-then-sign`). |
| **Cryptography** | `POST` | `/api/v1/crypto/verify` | `read-only` or higher | Verifies signed/encrypted payloads (`FLOWSIGNED:v1:` or detached signatures) and returns certificate/signer metadata. |
| **Telemetry Logs** | `GET` | `/api/v1/runs` (or `/api/v1/logs/runs`) | `read-only` or higher | Queries pipeline execution summaries from `dbo.pipeline_runs`. |
| **Telemetry Logs** | `GET` | `/api/v1/events` (or `/api/v1/logs/events`) | `read-only` or higher | Queries step-level execution events and `node_output` from `dbo.pipeline_events`. |
| **Audit Logs** | `GET` | `/api/v1/audit` (or `/api/v1/logs/api-events`) | `read-only` or higher | Queries API request and security audit records from `dbo.flow_api_events`. |
| **DB Diagnostics** | `POST` | `/api/v1/db/test` (or `/api/v1/databases/test`) | `all` | Tests connectivity and fetches session identity (`spid`, `db_user`, `application_name`) for the startup DB or a custom DSN. |
| **User Admin** | `GET` | `/api/v1/users` | `all` | Lists all registered API users, their permission scopes, and assigned `groups` (`password_hash` omitted). |
| **User Admin** | `POST` | `/api/v1/users` | `all` | Creates or updates an API user (`all`, `scoped`, `write`, `upload`, `execution-only`, `read-only`) and syncs optional `groups`. |
| **User Admin** | `DELETE` | `/api/v1/users/{username}` | `all` | Deletes an API user account by username and removes its group memberships/direct ACLs. |
| **User Groups** | `GET`, `POST` | `/api/v1/groups` | `all` | Lists all API user groups or creates/upserts a group (`dbo.flow_api_groups`). |
| **User Groups** | `GET`, `PUT`, `DELETE` | `/api/v1/groups/{name}` | `all` | Inspects, updates, or deletes an API user group and cleans up memberships/ACLs. |
| **User Groups** | `GET`, `POST` | `/api/v1/groups/{name}/members` | `all` | Lists group members or adds `username` / `usernames` to `dbo.flow_api_user_groups`. |
| **User Groups** | `DELETE` | `/api/v1/groups/{name}/members/{username}` | `all` | Removes a user from `dbo.flow_api_user_groups`. |
| **Pipeline Groups** | `GET` | `/api/v1/pipeline-groups` | Any authenticated user | Lists pipeline groups (`dbo.flow_pipeline_groups`); non-admins receive groups with readable/creatable access. |
| **Pipeline Groups** | `POST` | `/api/v1/pipeline-groups` | `all` | Creates/upserts a pipeline group with optional `name_pattern` glob and explicit `pipelines`. |
| **Pipeline Groups** | `GET`, `PUT`, `DELETE` | `/api/v1/pipeline-groups/{name}` | `GET`: Auth (`read`); `PUT`, `DELETE`: `all` | Reads, updates, or deletes a pipeline group and cleans up memberships/ACLs. |
| **Pipeline Groups** | `GET`, `POST` | `/api/v1/pipeline-groups/{name}/pipelines` | `GET`: Auth (`read`); `POST`: `all` | Lists pipelines in a group or assigns `pipeline_name` / `pipelines` (`dbo.flow_pipeline_group_members`). |
| **Pipeline Groups** | `DELETE` | `/api/v1/pipeline-groups/{name}/pipelines/{pipeline}` | `all` | Removes an explicit pipeline membership from `dbo.flow_pipeline_group_members`. |
| **Pipeline ACLs** | `GET`, `POST` | `/api/v1/acls` | `all` | Lists (filterable by principal/target) or creates/upserts `allow`/`deny` pipeline ACL rules (`dbo.flow_pipeline_acl`). |
| **Pipeline ACLs** | `PUT`, `DELETE` | `/api/v1/acls/{id}` | `all` | Updates or deletes a granular pipeline ACL rule by primary key `id`. |
| **Effective Perms** | `GET` | `/api/v1/permissions/effective` (or `/api/v1/me/permissions`) | Any authenticated user (`all` to inspect others) | Computes effective `create`, `read`, `update`, `delete`, and `execute` permissions across all stored pipelines. |

---

## 4. Category 1: Public Documentation, OpenAPI & Health Endpoints

### 4.1 `GET /swagger/` (Aliases: `/swagger`, `/docs`, `/docs/`)
- **Authentication**: Public (No BasicAuth required to load the UI; click **Authorize** inside Swagger UI to supply credentials for protected endpoints).
- **Description**: Serves the interactive Swagger UI preloaded with `/api/v1/openapi.json`.

### 4.2 `GET /api/v1/openapi.json` (Aliases: `/swagger/doc.json`, `/swagger/openapi.json`)
- **Authentication**: Public.
- **Description**: Returns the complete OpenAPI 3.0.3 JSON specification describing all routes, parameters, schemas, and security schemes.

### 4.3 `GET /api/v1/health`
- **Authentication**: Public (No BasicAuth required; if BasicAuth or Windows Integrated headers are provided, `authenticated_as`, `permission`, and `groups` are populated in the response).
- **Description**: Verifies API readiness, queries live database session identity from the startup `log_db` connection, and returns the resolved 12-table schema mapping.

**Example Request & Response:**
```bash
curl -u alice:AlicePass! http://localhost:8080/api/v1/health
```
```json
{
  "application_name": "go-flowAPI",
  "authenticated_as": "alice",
  "db_spid": 58,
  "db_user": "dbo",
  "dialect": "sqlserver",
  "groups": [
    "finance_ops"
  ],
  "permission": "scoped",
  "status": "ok",
  "tables": {
    "api_events": "dbo.flow_api_events",
    "api_groups": "dbo.flow_api_groups",
    "api_user_groups": "dbo.flow_api_user_groups",
    "api_users": "dbo.flow_api_users",
    "config_content": "dbo.flow_config_content",
    "options_content": "dbo.flow_options_content",
    "pipeline_acl": "dbo.flow_pipeline_acl",
    "pipeline_content": "dbo.flow_pipeline_content",
    "pipeline_events": "dbo.pipeline_events",
    "pipeline_group_members": "dbo.flow_pipeline_group_members",
    "pipeline_groups": "dbo.flow_pipeline_groups",
    "pipeline_runs": "dbo.pipeline_runs"
  }
}
```

---

## 5. Category 2: File Upload & Artifact CRUD (`pipelines`, `options`, `configs`)

All artifact endpoints accept `{type}` values mapping to the three repository tables:
- **`pipeline`** (or `pipelines`) → `dbo.flow_pipeline_content` (`Name`, `PipelineXML`) — **supports pipeline groups (`groups`) and per-pipeline / per-pipeline-group ACLs**
- **`options`** (or `option`) → `dbo.flow_options_content` (`Name`, `OptionsXML`)
- **`config`** (or `configs`) → `dbo.flow_config_content` (`Name`, `ConfigXML`)

### 5.1 `POST | PUT /api/v1/upload/{type}` (Alias: `/api/v1/artifacts/{type}/upload`)
- **Required Permission**:
  - For `options` / `config`: `write`, `upload`, or `all`.
  - For `pipeline`: `write`, `upload`, `all`, or granular ACL `create` (when creating a new pipeline in an authorized target group/glob/`*`) or `update` (when updating an existing pipeline).
- **Supported Payload Modes**:
  1. **`multipart/form-data`**: Form fields `file` (required), `name` (optional; defaults to filename without extension), `description` (or `desc`), `group` (optional pipeline group name or comma-separated group names), `encrypted` (`true`/`false`), and `secure_key`.
  2. **Raw Body (`application/xml` or `text/plain`)**: Query parameters `?name=<name>&description=<desc>&group=<group1,group2>&encrypted=true&secure_key=<key>` (or `X-Flow-Secure-Key` header).
- **Encoding & Encryption**: Automatically detects and transcodes UTF-16LE/BE BOM files to UTF-8. If `encrypted=true`, wraps the payload in `FLOWENC:v1:` using AES-256-GCM before storing in the database.

```bash
# Multipart pipeline file upload assigned to pipeline group finance_pipelines
curl -u publisher:PubPass123! \
  -F "file=@fin_daily_orders.xml" \
  -F "name=fin_daily_orders" \
  -F "group=finance_pipelines" \
  -F "description=Daily orders synchronization pipeline" \
  http://localhost:8080/api/v1/upload/pipelines
```

### 5.2 `GET /api/v1/artifacts/{type}`
- **Required Permission**:
  - For `options` / `config`: `read-only`, `execution-only`, `write`, `upload`, or `all` (`403 Forbidden` for `scoped` users).
  - For `pipeline`: Any authenticated user with global `read-only`+ or at least one `read` ACL grant; the returned `items` array is automatically filtered to only those pipelines the caller is authorized to `read`, and each pipeline record includes its resolved `"groups": [...]` list.
- **Query Parameters**:
  - `include_content=true` (optional): Includes the full XML/encrypted payload (`content`) and security inspection flags (`encrypted`, `signed`, `signature_format`) in each item.

```bash
curl -u alice:AlicePass! \
  "http://localhost:8080/api/v1/artifacts/pipelines?include_content=false"
```

### 5.3 `POST /api/v1/artifacts/{type}`
- **Required Permission**: `write`, `upload`, `all`, or granular Pipeline ACL (`create` for new pipelines; `update` when overwriting via `upsert=true`).
- **Description**: Creates a new artifact from a JSON payload. Returns `409 Conflict` if an artifact with the same `name` already exists unless `"upsert": true` (or `?upsert=true`) is set. When `type` is `pipeline`, optional `"group"` (CSV string) or `"groups"` (string array) assigns explicit membership in `dbo.flow_pipeline_group_members`.

```bash
curl -u alice:AlicePass! \
  -X POST "http://localhost:8080/api/v1/artifacts/pipelines?upsert=true" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "fin_hourly_sync",
    "description": "Hourly incremental finance sync",
    "content": "<pipeline name=\"fin_hourly_sync\"><flow></flow></pipeline>",
    "groups": ["finance_pipelines"],
    "encrypted": false,
    "upsert": true
  }'
```

### 5.4 `GET | PUT | DELETE /api/v1/artifacts/{type}/{name}` & `/api/v1/artifacts/{type}/id/{id}`
Artifacts can be retrieved, updated, or deleted either by **unique name** (`/api/v1/artifacts/{type}/{name}`) or by **database table ID** (`/api/v1/artifacts/{type}/id/{id}`):

- **`GET`**: Enforces `read` permission (including per-pipeline `allow`/`deny` ACLs). Fetches metadata, `groups`, `content`, and cryptographic status (`encrypted`, `signed`, `signature_format`). Pass `?decrypt=true` along with `X-Flow-Secure-Key` (or `?secure_key=...` or server default key) to decrypt `FLOWENC:v1:` payloads on the fly.
- **`PUT`**: Enforces `update` permission. Updates `description`, `content`, optional encryption (`"encrypted": true`), and optional pipeline `"groups"` (when supplied on `pipeline` updates).
- **`DELETE`**: Enforces `delete` permission. Permanently removes the artifact row from the database table (and cleans up `dbo.flow_pipeline_group_members` + direct pipeline ACL entries when deleting a `pipeline`).

```bash
# Fetch pipeline by database ID=4 and decrypt on the fly
curl -u runner_svc:RunPass123! \
  -H "X-Flow-Secure-Key: my-secret-passphrase" \
  "http://localhost:8080/api/v1/artifacts/pipeline/id/4?decrypt=true"
```

### 5.5 `GET /api/v1/artifacts/{type}/{name}/export` & `/api/v1/artifacts/{type}/id/{id}/export`
- **Required Permission**: `read-only`+ or Pipeline ACL `read` on the target pipeline.
- **Description**: Downloads the raw XML content directly with `Content-Type: application/xml; charset=utf-8` and `Content-Disposition: attachment; filename="<name>.xml"`. Supports `?decrypt=true`.

---

## 6. Category 3: Pipeline Execution, Live SSE Streaming & Validation

### 6.1 Execution Lifecycle, ACL Gate & Per-Node Database Session Identity

When `go-flowAPI` executes or validates a pipeline, it resolves the target pipeline name/ID, verifies the caller's granular **`execute` permission** (`AuthorizePipelineAction`), merges the configuration hierarchy, verifies signatures/encryption, registers all AST nodes with `DatabaseSink`, and logs per-node telemetry to `dbo.pipeline_events`:

- **Stored Pipeline Execution (`file: "db:<name>"`, `"<name>"`, `pipeline_id`, or `/execute/pipeline/{id}`)**: Evaluates `execute` permission for that specific pipeline across user/group ACLs (`pipeline`, matching `pipeline_group` memberships & `name_pattern` globs, and `*`) and global role.
- **Inline / Ad-Hoc XML Execution (`file_xml` without a stored pipeline reference)**: Requires global `execution-only` / `all` (when `-strict-acl` is false) or an explicit wildcard (`target_type: "*"`) `execute` `allow` ACL rule with no wildcard `deny`.

```mermaid
flowchart TD
    REQ["Execution Request<br/>POST /api/v1/execute or /api/v1/execute/stream"] --> ACL["1. Pipeline ACL Gate<br/>Verify execute permission on resolved pipeline<br/>(or wildcard * for inline file_xml)"]

    ACL -->|"403 Forbidden"| DENY["Audit pipeline.execute FORBIDDEN<br/>&amp; return 403"]
    ACL -->|"Authorized"| RESOLVE["2. Resolve Artifacts (Name, ID, db:name, or Inline XML)<br/>Base Pipeline &lt; Options &lt; Config &lt; Request Vars"]

    RESOLVE --> SEC["3. Cryptographic Gate<br/>Verify Signature (FLOWSIGNED:v1 / .sig / .p7s / .asc)<br/>Decrypt Payload (FLOWENC:v1 / AES-256-GCM)"]

    SEC --> VAL["4. Validate XSD &amp; Semantic AST<br/>(If validate=true, return validation result immediately)"]

    VAL --> EXEC["5. Execute Preflight &amp; Flow Nodes<br/>Emit ExecutionEvents to MultiSink (DatabaseSink + SSE CallbackSink)"]

    subgraph IdentityRules["6. DatabaseSink Per-Node Session Identity Resolution"]
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
- **Required Permission**: `execution-only`, `all`, or Pipeline ACL `execute` on the target pipeline.
- **Description**: Executes a pipeline synchronously and returns an `ExecutionResponse` JSON object containing `run_id`, `status`, `started_at`, `finished_at`, `duration_ms`, `formatted_output`, and `results`.

#### Supported `ExecutionRequest` JSON Fields & Query Parameters:

| JSON Field | Query Parameter | Type | Description |
| :--- | :--- | :--- | :--- |
| `artifact_type` | `artifact_type` / `type` | `string` | Target artifact type when executing by `id`: `"pipeline"`, `"options"`, or `"config"`. |
| `id` | `id` | `int64` | Database table primary key `ID` for the specified `artifact_type`. |
| `pipeline_id` | `pipeline_id` | `int64` | Explicit `dbo.flow_pipeline_content.ID` to execute. |
| `options_id` | `options_id` | `int64` | Explicit `dbo.flow_options_content.ID` to load CLI options from. |
| `config_id` | `config_id` | `int64` | Explicit `dbo.flow_config_content.ID` to merge environment overrides from. |
| `file` | `file` | `string` | Pipeline reference (`<name>`, `db:<name>`, `id:<id>`, `sql://...`, or file path). |
| `file_xml` | — | `string` | Inline pipeline XML string (requires wildcard `*` `execute` or global `execution-only`/`all`). |
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
  -X POST http://localhost:8080/api/v1/execute \
  -H "Content-Type: application/json" \
  -d '{
    "file": "db:fin_daily_orders",
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
  -X POST "http://localhost:8080/api/v1/execute/pipeline/1?node_output=true&format=json"
```

---

### 6.3 `POST | GET /api/v1/execute/stream` & `/api/v1/execute/stream/{type}/{id}`
- **Aliases**: `/api/v1/execute/{type}/{id}/stream`, `/api/v1/execution/stream`, `/api/v1/execution/stream/{type}/{id}`.
- **Required Permission**: `execution-only`, `all`, or Pipeline ACL `execute` on the target pipeline.
- **Description**: Executes a pipeline and streams real-time **Server-Sent Events (`text/event-stream`)** as each node starts and finishes.

```mermaid
sequenceDiagram
    participant Client as Client / flowUI
    participant API as go-flowAPI (/api/v1/execute/stream)
    participant Engine as flow Executor
    participant DB as Audit DB (pipeline_runs / pipeline_events)

    Client->>API: POST /api/v1/execute/stream/pipeline/1
    API->>API: Verify execute permission (ACL + RBAC)
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
- **Required Permission**: `execution-only`, `all`, or Pipeline ACL `execute` on the target pipeline.
- **Description**: Forces `validate=true` on the execution request, verifying XML well-formedness, XSD compliance (`pipeline.xsd` / `options.xsd`), and AST semantic rules without running database queries or scripts. Returns `200 OK` if valid or `422 Unprocessable Entity` with detailed validation errors.

---

## 7. Category 4: Cryptographic & Digital Signature Endpoints (`/api/v1/crypto/*`)

All `/api/v1/crypto/*` endpoints require `read-only` (or higher) global permission and expose pure-Go cryptographic operations from `github.com/etl-madness/flow/flowcrypto`.

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
- **`/api/v1/crypto/encrypt`**: Accepts `{"payload": "<xml...>", "secure_key": "..."}` (or `"content"`) and returns `{"encrypted_payload": "FLOWENC:v1:...", "content": "FLOWENC:v1:...", "encrypted": true}`.
- **`/api/v1/crypto/decrypt`**: Accepts `{"payload": "FLOWENC:v1:...", "secure_key": "..."}` (or `"content"`) and returns the plaintext `decrypted_payload` / `content` plus `encrypted`, `signed`, and `signature_format` flags.

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
- **Response**: Returns `format`, `detached_signature` (`FLOWSIG:v1:...`), `signed_payload` / `signed_envelope` (`FLOWSIGNED:v1:...` or outer `FLOWENC:v1:...`), `encrypted`, and `signed`.

### 7.4 `POST /api/v1/crypto/verify`
Verifies `FLOWSIGNED:v1:` wrapped payloads or detached signatures (`signature`), automatically decrypting outer/inner `FLOWENC:v1:` layers when `secure_key` is provided, and validates X.509 certificate chains when `ca_cert_pem` is supplied.
- **Response**: Returns `valid` / `verified`, `format`, `algorithm`, `signer_info`, X.509 certificate details (`subject`, `issuer`, `serial_number`, `not_before`, `not_after`, `fingerprint`), and `unwrapped_content`.

---

## 8. Category 5: Telemetry, Execution History & Audit Log Endpoints

### 8.1 `GET /api/v1/runs` (Alias: `/api/v1/logs/runs`)
- **Required Permission**: `read-only`, `execution-only`, `write`, `upload`, or `all`.
- **Query Parameters**:
  - `status` (optional): Filter by run status (e.g., `SUCCEEDED`, `FAILED`, `RUNNING`).
  - `limit` (optional, default `100`, max `1000`): Maximum number of rows returned, ordered by `started_at DESC`.
- **Description**: Queries `dbo.pipeline_runs` for high-level pipeline execution summaries (`run_id`, `file_path`, `config_path`, `status`, `started_at`, `finished_at`, `duration_ms`, `spid`, `error_message`).

### 8.2 `GET /api/v1/events` (Alias: `/api/v1/logs/events`)
- **Required Permission**: `read-only`, `execution-only`, `write`, `upload`, or `all`.
- **Query Parameters**:
  - `run_id` (optional): Filter step events for a specific pipeline run (`run_id`).
  - `limit` (optional, default `200`, max `2000`): Maximum number of rows returned.
- **Description**: Queries `dbo.pipeline_events` for step-by-step execution metrics, including `sequence_num`, `event_type`, `node_kind`, `node_id`, `status`, `rows_read`, `rows_written`, `rows_affected`, `db_user_name`, `spid`, `application_name`, and `node_output`.

### 8.3 `GET /api/v1/audit` (Alias: `/api/v1/logs/api-events`)
- **Required Permission**: `read-only`, `execution-only`, `write`, `upload`, or `all`.
- **Query Parameters**:
  - `event_type` (optional): Filter by audit event type (e.g., `pipeline.execute`, `artifact.upload`, `auth.failed`, `user.upsert`, `group.upsert`, `pipeline_group.upsert`, `acl.upsert`).
  - `auth_user` (optional): Filter by authenticated username.
  - `status` (optional): Filter by status (`SUCCESS`, `FAILED`, `ERROR`, `UNAUTHORIZED`, `FORBIDDEN`).
  - `limit` (optional, default `200`, max `2000`).
- **Description**: Queries `dbo.flow_api_events` for the complete security and operational audit trail.

---

## 9. Category 6: Database Connectivity Testing & User Management

### 9.1 `POST /api/v1/db/test` (Alias: `/api/v1/databases/test`)
- **Required Permission**: `all`.
- **Description**: Opens and pings either the server's default startup connection (when body is `{}` or `"dsn": ""`) or an explicit target `dsn` / `connection_string`, and returns the live database session identity (`connected`, `dialect`, `spid`, `db_user`, `application_name`).

```bash
curl -u admin:Admin123! \
  -X POST http://localhost:8080/api/v1/db/test \
  -H "Content-Type: application/json" \
  -d '{"dsn": "postgres://flow:flowpass@localhost:5432/flowdb?sslmode=disable"}'
```

### 9.2 `GET /api/v1/users`
- **Required Permission**: `all`.
- **Description**: Returns all accounts in `dbo.flow_api_users` (`username`, `permission`, `is_active`, `description`, `groups`, `created_at`, `updated_at`), enriched with each user's assigned group memberships from `dbo.flow_api_user_groups`. Password hashes are never returned.

### 9.3 `POST /api/v1/users`
- **Required Permission**: `all`.
- **Description**: Creates or updates (upserts) an API user account in `dbo.flow_api_users`.
  - Plaintext passwords supplied in `"password"` are automatically hashed with **bcrypt** before storage (when updating an existing user or registering a Windows identity `DOMAIN\user` / `DOMAIN\Group`, `"password"` may be left empty).
  - `"permission"` accepts `"scoped"` (zero global baseline rights — ACL-only), `"read-only"`, `"execution-only"`, `"upload"`, `"write"`, `"all"`, or comma-separated combinations.
  - Optional `"groups"` (`[]string`) replaces/syncs the user's memberships in `dbo.flow_api_user_groups` (automatically creating missing group rows in `dbo.flow_api_groups` if needed).

```bash
curl -u admin:Admin123! \
  -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -d '{
    "username": "alice",
    "password": "StrongAnalystPassword!2026",
    "permission": "scoped",
    "description": "Scoped Finance Analyst (governed by finance_ops ACLs)",
    "is_active": true,
    "groups": ["finance_ops"]
  }'
```

### 9.4 `DELETE /api/v1/users/{username}`
- **Required Permission**: `all`.
- **Description**: Deletes the specified user from `dbo.flow_api_users` and removes their memberships in `dbo.flow_api_user_groups` and direct `principal_type = 'user'` entries in `dbo.flow_pipeline_acl`.

```bash
curl -u admin:Admin123! \
  -X DELETE http://localhost:8080/api/v1/users/alice
```

---

## 10. Category 7: Granular Pipeline ACLs, User Groups, Pipeline Groups & Effective Permissions

Granular Access Control Lists (`dbo.flow_pipeline_acl`) provide fine-grained **`create`**, **`read`**, **`update`**, **`delete`**, and **`execute`** (or **`all`**) authorization at the **user** (`principal_type: "user"`) or **group** (`principal_type: "group"`, matching both `dbo.flow_api_groups` and Windows/AD groups) level against:
- An individual pipeline (`target_type: "pipeline"`, `target_name: "<pipeline_name>"`)
- A pipeline group (`target_type: "pipeline_group"`, `target_name: "<group_name>"` — matching both explicit memberships in `dbo.flow_pipeline_group_members` and wildcard `name_pattern` globs such as `fin_*` in `dbo.flow_pipeline_groups`)
- All pipelines (`target_type: "*"`, `target_name: "*"`)

### 10.1 Authorization Evaluation Order

```mermaid
flowchart TD
    START["AuthorizePipelineAction(user, pipelineName, targetGroups, action)"] --> IS_ADMIN{"1. user has 'all'<br/>admin role?"}
    IS_ADMIN -->|"Yes"| ALLOW_ADMIN["ALLOW"]
    IS_ADMIN -->|"No"| RESOLVE_PG["Resolve matching Pipeline Groups<br/>(Explicit flow_pipeline_group_members +<br/>case-insensitive glob name_pattern)"]

    RESOLVE_PG --> CHECK_DENY{"2. Any matching ACL rule<br/>with effect = 'deny'<br/>for user or user.Groups?"}
    CHECK_DENY -->|"Yes"| DENY_ACL["DENY (403 Forbidden)<br/>Explicit deny overrides all allow rules &amp; global roles"]

    CHECK_DENY -->|"No"| CHECK_ALLOW{"3. Any matching ACL rule<br/>with effect = 'allow'<br/>granting action (or 'all')?"}
    CHECK_ALLOW -->|"Yes"| ALLOW_ACL["ALLOW"]

    CHECK_ALLOW -->|"No"| STRICT{"4. Server -strict-acl<br/>OR user.Permission == 'scoped'?"}
    STRICT -->|"Yes"| DENY_STRICT["DENY (403 Forbidden)"]
    STRICT -->|"No"| GLOBAL["Fallback to Global Role<br/>(read-only / execution-only / write / upload)"]
```

> **Note on `create` Authorization for `scoped` / `-strict-acl` Users:** Creating a brand-new pipeline is permitted when the user holds `create` (or `all`) via:
> 1. A wildcard rule (`target_type: "*"`, `effect: "allow"`), **or**
> 2. A `pipeline_group` rule where the new pipeline's name matches that group's `name_pattern` glob (e.g. `fin_*`) or the request explicitly assigns `"groups": ["finance_pipelines"]` (and every requested group grants `create`), **or**
> 3. A pre-authorized named `pipeline` rule (`target_type: "pipeline"`, `target_name: "<new_pipeline_name>"`).

---

### 10.2 `GET /api/v1/groups` & `POST /api/v1/groups`
- **Required Permission**: `all`.
- **`GET /api/v1/groups`**: Returns `{"count": N, "groups": [...]}` listing every API user group in `dbo.flow_api_groups` along with its sorted `members` array.
- **`POST /api/v1/groups`**: Creates or upserts an API user group and replaces its member list when `"members"` is supplied (`201 Created`).

```bash
curl -u admin:Admin123! \
  -X POST http://localhost:8080/api/v1/groups \
  -H "Content-Type: application/json" \
  -d '{
    "name": "finance_ops",
    "description": "Finance ETL Analysts & Operators",
    "members": ["alice", "CORP\\bob"]
  }'
```

### 10.3 `GET | PUT | DELETE /api/v1/groups/{name}`
- **Required Permission**: `all`.
- **`GET`**: Fetches a single `APIGroup` record (`id`, `name`, `description`, `members`, `created_at`, `updated_at`).
- **`PUT`**: Updates `description` and (if provided) `members` for `{name}`.
- **`DELETE`**: Deletes `{name}` from `dbo.flow_api_groups`, removes its user memberships from `dbo.flow_api_user_groups`, and deletes `principal_type = 'group'` ACL rules referencing `{name}`.

### 10.4 `GET | POST /api/v1/groups/{name}/members` & `DELETE /api/v1/groups/{name}/members/{username}`
- **Required Permission**: `all`.
- **`GET /api/v1/groups/{name}/members`**: Returns `{"group": "<name>", "count": N, "members": [...]}`.
- **`POST /api/v1/groups/{name}/members`**: Adds one or more usernames (`{"username": "alice"}` or `{"usernames": ["alice", "bob"]}`) idempotently to `{name}` (`201 Created`) and returns the updated `APIGroup`.
- **`DELETE /api/v1/groups/{name}/members/{username}`**: Removes `{username}` from `{name}`.

---

### 10.5 `GET /api/v1/pipeline-groups` & `POST /api/v1/pipeline-groups`
- **Required Permission**:
  - **`GET`**: Any authenticated user. Administrators (`all`) see all pipeline groups; non-admin users receive pipeline groups containing at least one readable pipeline or where the user holds `create` rights.
  - **`POST`**: `all`.
- **Description**: Manages logical pipeline groups in `dbo.flow_pipeline_groups`. Each group supports both:
  - **`name_pattern`**: Optional case-insensitive glob (`*` and `?` wildcards, e.g., `fin_*` or `*_daily`) that automatically includes any matching pipeline in `dbo.flow_pipeline_content`.
  - **`pipelines`**: Explicit pipeline names persisted in `dbo.flow_pipeline_group_members`.

```bash
curl -u admin:Admin123! \
  -X POST http://localhost:8080/api/v1/pipeline-groups \
  -H "Content-Type: application/json" \
  -d '{
    "name": "finance_pipelines",
    "description": "All finance ledger & close pipelines",
    "name_pattern": "fin_*",
    "pipelines": ["shared_gl_reconcile"]
  }'
```

### 10.6 `GET | PUT | DELETE /api/v1/pipeline-groups/{name}`
- **Required Permission**: `GET` requires any authenticated user with access to the group; `PUT` and `DELETE` require `all`.
- **`GET`**: Returns the `PipelineGroup` record with all resolved pipelines (explicit + `name_pattern` matches, filtered to readable pipelines for non-admins).
- **`PUT`**: Updates `description`, `name_pattern`, and optional `pipelines`.
- **`DELETE`**: Deletes the pipeline group, its explicit rows in `dbo.flow_pipeline_group_members`, and `target_type = 'pipeline_group'` ACL rules referencing `{name}`.

### 10.7 `GET | POST /api/v1/pipeline-groups/{name}/pipelines` & `DELETE /api/v1/pipeline-groups/{name}/pipelines/{pipeline}`
- **Required Permission**: `GET` requires authenticated read access to the group; `POST` and `DELETE` require `all`.
- **`GET`**: Returns `{"group": "<name>", "count": N, "pipelines": [...]}`.
- **`POST`**: Assigns one or more pipelines (`{"pipeline_name": "shared_gl_reconcile"}` or `{"pipelines": ["p1", "p2"]}`) to `{name}` (`201 Created`).
- **`DELETE`**: Removes `{pipeline}` from explicit membership in `dbo.flow_pipeline_group_members`.

---

### 10.8 `GET /api/v1/acls` & `POST /api/v1/acls`
- **Required Permission**: `all`.
- **`GET /api/v1/acls`**: Lists granular ACL rules from `dbo.flow_pipeline_acl`. Supports optional query filters:
  - `principal_type` (`user` | `group`)
  - `principal_name` (username, API group name, or `DOMAIN\Group`)
  - `target_type` (`pipeline` | `pipeline_group` | `*`)
  - `target_name` (pipeline name, pipeline group name, or `*`)
- **`POST /api/v1/acls`**: Upserts an ACL rule keyed by `(principal_type, principal_name, target_type, target_name, effect)` (`201 Created`).
  - `permissions`: Comma-separated list of `create`, `read`, `update`, `delete`, `execute`, or `all`.
  - `effect`: `"allow"` (default) or `"deny"`.

```bash
# 1. Allow group finance_ops to read, create, update, and execute pipelines in finance_pipelines
curl -u admin:Admin123! \
  -X POST http://localhost:8080/api/v1/acls \
  -H "Content-Type: application/json" \
  -d '{
    "principal_type": "group",
    "principal_name": "finance_ops",
    "target_type": "pipeline_group",
    "target_name": "finance_pipelines",
    "permissions": "create,read,update,execute",
    "effect": "allow",
    "description": "Finance operators manage and run finance pipelines"
  }'

# 2. Explicitly deny delete across all pipelines (*) for contractor_bob
curl -u admin:Admin123! \
  -X POST http://localhost:8080/api/v1/acls \
  -H "Content-Type: application/json" \
  -d '{
    "principal_type": "user",
    "principal_name": "contractor_bob",
    "target_type": "*",
    "target_name": "*",
    "permissions": "delete",
    "effect": "deny",
    "description": "Prevent contractor_bob from deleting any pipeline"
  }'
```

### 10.9 `PUT /api/v1/acls/{id}` & `DELETE /api/v1/acls/{id}`
- **Required Permission**: `all`.
- **`PUT /api/v1/acls/{id}`**: Updates an existing ACL rule in `dbo.flow_pipeline_acl` by integer primary key `{id}`.
- **`DELETE /api/v1/acls/{id}`**: Deletes ACL rule `#{id}` and returns `{"deleted": true, "id": <id>}`.

---

### 10.10 `GET /api/v1/permissions/effective` (Alias: `GET /api/v1/me/permissions`)
- **Required Permission**:
  - **Self-inspection** (no query parameters, or `?username=<caller>`): Any authenticated user (including `scoped` users).
  - **Admin principal inspection & group simulation** (`?username=<other>` and/or `?groups=<csv>`): Requires `all`.
- **Query Parameters**:
  - `username` (optional, admin-only when inspecting another user): Target API or Windows username to evaluate. If the user does not exist in `dbo.flow_api_users` when `groups` is provided, evaluates as a simulated `scoped` principal with those groups.
  - `groups` (optional, admin-only): Comma-separated extra group names (e.g., `finance_ops,CORP\ETL_Runners`) merged into the principal's resolved groups for "what-if" policy testing.
- **Description**: Evaluates the complete ACL + RBAC matrix across all stored pipelines and pipeline groups, returning global capabilities and per-pipeline `create`, `read`, `update`, `delete`, and `execute` booleans.

```bash
# Self-inspection by any signed-in user
curl -u alice:AlicePass! http://localhost:8080/api/v1/me/permissions

# Admin "what-if" inspection for user alice with simulated AD group
curl -u admin:Admin123! \
  "http://localhost:8080/api/v1/permissions/effective?username=alice&groups=CORP%5CProd_Approvers"
```

**Example Response (`EffectivePermissionsResponse`):**
```json
{
  "username": "alice",
  "global_permission": "scoped",
  "groups": [
    "finance_ops"
  ],
  "strict_acl": true,
  "can_create_ungrouped": false,
  "creatable_groups": [
    "finance_pipelines"
  ],
  "can_execute_inline_xml": false,
  "pipelines": [
    {
      "pipeline": "fin_daily_orders",
      "groups": [
        "finance_pipelines"
      ],
      "actions": {
        "create": true,
        "read": true,
        "update": true,
        "delete": false,
        "execute": true
      }
    }
  ]
}
```

---

## Related Documentation

- [**`go-flowAPI` Documentation Hub**](index.md)
- [**Database Schema Reference (`SCHEMA.md`)**](../SCHEMA.md)
- [**Cross-Platform Compilation & Build Guide**](API_COMPILATION_AND_BUILD_GUIDE.md)
- [**Detailed Docker & Docker Compose Guide**](API_DOCKER_DEPLOYMENT_GUIDE.md)
