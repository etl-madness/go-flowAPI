# `go-flowAPI` — CRUD, File Upload, Execution & Telemetry API for `go-flow`

`go-flowAPI` is a unified REST API for managing `go-flow` database artifacts (`pipelines`, `options`, `configs`), uploading XML/JSON files to the database, executing pipelines with full CLI/Builder parity (including SSE live streaming and `node_output` logging), and auditing all API and pipeline events.

## Key Features

1. **Adjustable Table & Schema Paths (`tables.go`)**:
   - Controlled via `const` declarations (`DefaultSchema = "dbo."`, `TablePipelineContent`, `TableOptionsContent`, `TableConfigContent`, `TablePipelineRuns`, `TablePipelineEvents`, `TableAPIAuditEvents`, `TableAPIUsers`).
2. **Startup Connection String Variable (`config.go`)**:
   - Defined in `var StartupConnectionString` and loaded at startup (can be overridden via `FLOW_API_DSN` or `-dsn`).
3. **5 Supported Database Dialects**:
   - Microsoft SQL Server / Azure SQL (`sqlserver://` / `mssql://`)
   - PostgreSQL (`postgres://` / `postgresql://`)
   - MySQL / MariaDB (`mysql://`)
   - SQLite (`sqlite://`)
   - Oracle (`oracle://`)
4. **Database-Backed BasicAuth & Role Permissioning (`auth.go`)**:
   - Authenticates against `TableAPIUsers` (`dbo.flow_api_users`) and enforces permissions:
     - `execution-only`: Execute and validate pipelines (`POST /api/v1/execute`, `POST /api/v1/execute/stream`, `POST /api/v1/validate`)
     - `read-only`: Read/list/export artifacts and query telemetry & audit logs (`GET /api/v1/...`)
     - `write` or `upload`: Upload files and create/update/delete/read artifacts (`/api/v1/upload/...`, `/api/v1/artifacts/...`)
     - `all`: Full access across all endpoints including user management (`/api/v1/users`) and DB connectivity testing (`/api/v1/db/test`)
5. **Full Event & Telemetry Logging (`telemetry.go`)**:
   - Every API call is logged to `TableAPIAuditEvents` (`dbo.flow_api_events`).
   - Every pipeline execution logs run summaries to `TablePipelineRuns` (`dbo.pipeline_runs`) and node events (including optional `node_output` with 64 KB UTF-8 safe truncation and secret redaction) to `TablePipelineEvents` (`dbo.pipeline_events`).

---

## Database Setup

`go-flowAPI` does **not** auto-create tables at startup. See **[`SCHEMA.md`](./SCHEMA.md)** for full DDL scripts for SQL Server, PostgreSQL, MySQL, SQLite, and Oracle, as well as sample `INSERT` statements to seed your initial BasicAuth users.

---

## Running `go-flowAPI`

```bash
# Run using StartupConnectionString in config.go (or override via -dsn / FLOW_API_DSN)
go run . -addr :8085 -dsn "sqlserver://sa:YourStrong!Passw0rd@localhost:1433?database=master"
```

### Command-Line Flags & Environment Variables

| Flag | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `-dsn` | `FLOW_API_DSN` | `StartupConnectionString` | Startup database connection string |
| `-addr` | `FLOW_API_ADDR` | `:8085` | HTTP listen address |
| `-secure-key` | `FLOW_SECURE_KEY` | `""` | Default 32-byte encryption key for encrypted artifacts |

---

## API Endpoints & Permission Matrix

| Method & Path | Required Permission | Description |
| :--- | :--- | :--- |
| `GET /api/v1/health` | `read-only`, `write`/`upload`, `execution-only`, or `all` | Health check and database driver/schema status |
| `POST /api/v1/upload/{type}` | `write`, `upload`, or `all` | Upload `.xml`/`.json` file (`multipart/form-data` or raw body) into `pipelines`, `options`, or `configs` |
| `GET /api/v1/artifacts/{type}` | `read-only`, `write`, `upload`, or `all` | List artifacts (`pipelines`, `options`, `configs`) |
| `GET /api/v1/artifacts/{type}/{name}` | `read-only`, `write`, `upload`, or `all` | Fetch artifact (use `?raw=true` to download raw file or `?decrypt=true` to decrypt) |
| `POST /api/v1/artifacts/{type}` | `write`, `upload`, or `all` | Create a new artifact (fails with `409 Conflict` if it already exists) |
| `PUT /api/v1/artifacts/{type}/{name}` | `write`, `upload`, or `all` | Create or update (upsert) an artifact |
| `DELETE /api/v1/artifacts/{type}/{name}` | `write`, `upload`, or `all` | Delete an artifact |
| `POST /api/v1/execute` | `execution-only` or `all` | Execute a pipeline synchronously with all CLI/Builder options |
| `POST /api/v1/execute/stream` | `execution-only` or `all` | Execute a pipeline and stream live SSE events (`event: flow_event`, `event: complete`) |
| `POST /api/v1/validate` | `execution-only` or `all` | Validate pipeline syntax, XSD, and preflight checks (`validate=true`) |
| `GET /api/v1/runs` | `read-only` or `all` | Query `pipeline_runs` history |
| `GET /api/v1/events` | `read-only` or `all` | Query `pipeline_events` (including `node_output`) |
| `GET /api/v1/audit` | `read-only` or `all` | Query `flow_api_events` API audit trail |
| `POST /api/v1/db/test` | `all` | Test connectivity to the startup DB or an external target DSN |
| `GET /api/v1/users` | `all` | List BasicAuth users and permissions |
| `POST /api/v1/users` | `all` | Create or update a BasicAuth user (automatically bcrypt-hashes password) |
| `DELETE /api/v1/users/{username}` | `all` | Delete a BasicAuth user |

---

## Example Requests

### 1. Upload a Pipeline XML File (`write` / `upload` / `all`)
```bash
curl -u publisher:publisher-secret-pass \
  -F "file=@my_pipeline.xml" \
  "http://localhost:8085/api/v1/upload/pipelines?name=daily_etl"
```

### 2. Execute a Pipeline with `node_output` Logging (`execution-only` / `all`)
```bash
curl -u runner_svc:runner-secret-pass \
  -X POST http://localhost:8085/api/v1/execute \
  -H "Content-Type: application/json" \
  -d '{
    "file": "db:daily_etl",
    "format": "json",
    "node_output": true,
    "log_to_db": true,
    "vars": {
      "ENV": "production"
    }
  }'
```

### 3. Query Pipeline Events & `node_output` (`read-only` / `all`)
```bash
curl -u auditor:auditor-secret-pass \
  "http://localhost:8085/api/v1/events?limit=20"
```
