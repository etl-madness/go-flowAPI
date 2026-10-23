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
     - `execution-only`: Execute and validate pipelines (`POST /api/v1/execute`, `POST /api/v1/execute/stream`, `POST /api/v1/validate`) and read/list/export artifacts & query logs
     - `read-only`: Read/list/export artifacts and query telemetry & audit logs (`GET /api/v1/...`)
     - `write` or `upload`: Upload files and create/update/delete/read artifacts (`/api/v1/upload/...`, `/api/v1/artifacts/...`)
     - `all`: Full access across all endpoints including user management (`/api/v1/users`) and DB connectivity testing (`/api/v1/db/test`)
5. **Full Event & Telemetry Logging (`telemetry.go`)**:
   - Every API call is logged to `TableAPIAuditEvents` (`dbo.flow_api_events`).
   - Every pipeline execution logs run summaries to `TablePipelineRuns` (`dbo.pipeline_runs`) and node events (including optional `node_output` with 64 KB UTF-8 safe truncation and secret redaction) to `TablePipelineEvents` (`dbo.pipeline_events`).
6. **Interactive Swagger UI & OpenAPI 3.0.3 Spec (`swagger.go`)**:
   - Served out-of-the-box at `/swagger/` (and `/docs`) with integrated HTTP BasicAuth **"Authorize"** support.
   - OpenAPI 3.0.3 JSON specification served at `/swagger/doc.json`, `/swagger/openapi.json`, and `/api/v1/openapi.json`.

---

## Database Setup & Initial Admin User Bootstrap

1. **Provision Database Tables**: `go-flowAPI` does **not** auto-create tables at startup. See **[`SCHEMA.md`](./SCHEMA.md)** for full DDL scripts for SQL Server, PostgreSQL, MySQL, SQLite, and Oracle.
2. **Bootstrap Initial `all`-Permission User**: See **[`BOOTSTRAP.md`](./BOOTSTRAP.md)** for step-by-step instructions on adding an initial administrator user with `"all"` permissions via CLI flags (`-bootstrap-user` / `-bootstrap-pass`), environment variables, or direct SQL (`MERGE` / `INSERT`) across all 5 dialects.

---

## Running `go-flowAPI`

When your database tables (`dbo.flow_api_users`, etc.) and users are already set up, `FLOW_API_BOOTSTRAP_USER`, `FLOW_API_BOOTSTRAP_PASS`, and `FLOW_API_BOOTSTRAP_PERM` are **not required**:

```bash
# 1. Run from source (uses StartupConnectionString in config.go, or override via -dsn / FLOW_API_DSN)
go run .
go run . -addr :8080 -dsn "sqlserver://sa:YourStrong!Passw0rd@localhost:1433?database=PROTO&trustServerCertificate=true"

# 2. Build and run compiled binary
go build -o go-flowAPI.exe .
./go-flowAPI.exe -addr :8080 -dsn "sqlserver://sa:YourStrong!Passw0rd@localhost:1433?database=PROTO&trustServerCertificate=true"

# (Optional) Seed/update an initial admin user at startup only if needed:
go run . -addr :8080 -bootstrap-user admin -bootstrap-pass "YourStrong!AdminPass"
```

### Running with Docker Compose

See **[`DOCKER.md`](./DOCKER.md)** for the full Docker Quickstart Guide. Four Docker Compose configurations are included to cover each database backend (defaulting to port `8080`):

```bash
# 1. Connect to an existing / host database via FLOW_API_DSN (no bootstrap env vars required if users exist)
FLOW_API_DSN="sqlserver://flow_api:YourStrong!Passw0rd@host.docker.internal:1433?database=PROTO&trustServerCertificate=true" \
docker compose up -d --build

# 2. SQL Server 2022 container + automatic schema provisioning
docker compose -f docker-compose.sqlserver.yml up -d --build

# 3. PostgreSQL 16 container + automatic schema provisioning
docker compose -f docker-compose.postgres.yml up -d --build

# 4. SQLite with persistent Docker volume + automatic schema provisioning
docker compose -f docker-compose.sqlite.yml up -d --build
```

> **Note when connecting from Docker to a Windows Host SQL Server**: Unlike native Windows builds (`go run .` / `go-flowAPI.exe`) which can use Windows Integrated Authentication (`sqlserver://HOST:1433?...`), a Linux Docker container requires **SQL Server Authentication** (`sqlserver://username:password@host.docker.internal:1433?...`) with SQL Server Mixed Mode and TCP/IP (port `1433`) enabled. See **[`DOCKER.md`](./DOCKER.md#important-notes-for-connecting-to-a-windows-host-sql-server-from-docker)** for details and SQL login setup.

### Command-Line Flags & Environment Variables

| Flag | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `-dsn` | `FLOW_API_DSN` | `StartupConnectionString` | Startup database connection string |
| `-addr` | `FLOW_API_ADDR` / `PORT` | `:8080` | HTTP listen address |
| `-secure-key` | `FLOW_SECURE_KEY` | `""` | Default encryption/decryption passphrase for encrypted artifacts |
| `-key-file` | — | `""` | Path to file containing default encryption/decryption key |
| `-bootstrap-user` | `FLOW_API_BOOTSTRAP_USER` | `""` | Initial API username to create/update at startup |
| `-bootstrap-pass` | `FLOW_API_BOOTSTRAP_PASS` | `""` | Password for `-bootstrap-user` (automatically bcrypt-hashed) |
| `-bootstrap-perm` | `FLOW_API_BOOTSTRAP_PERM` | `"all"` | Permission role for `-bootstrap-user` (`all`, `execution-only`, `read-only`, `write`, `upload`) |
| `-hash-password` | — | `""` | Generate and print a bcrypt hash for the given password and exit |
| `-debug` | `FLOW_API_DEBUG` | `false` | Enable verbose debug logging |

---

## API Endpoints & Permission Matrix

| Method & Path | Required Permission | Description |
| :--- | :--- | :--- |
| `GET /swagger/` (or `/docs`) | Public (Authorize in UI) | Interactive Swagger UI with BasicAuth support |
| `GET /swagger/doc.json` (or `/api/v1/openapi.json`) | Public | OpenAPI 3.0.3 JSON specification |
| `GET /api/v1/health` | Public (No BasicAuth required) | Health check and database driver/schema status |
| `POST /api/v1/upload/{type}` | `write`, `upload`, or `all` | Upload `.xml`/`.json` file (`multipart/form-data` or raw body) into `pipelines`, `options`, or `configs` |
| `GET /api/v1/artifacts/{type}` | `read-only`, `write`, `upload`, or `all` | List artifacts (`pipelines`, `options`, `configs`) |
| `GET /api/v1/artifacts/{type}/{name}` | `read-only`, `write`, `upload`, or `all` | Fetch artifact by name (use `?decrypt=true` to decrypt) |
| `GET /api/v1/artifacts/{type}/id/{id}` | `read-only`, `write`, `upload`, or `all` | Fetch artifact by database table `ID` (`?decrypt=true` supported) |
| `GET /api/v1/artifacts/{type}/{name}/export` | `read-only`, `write`, `upload`, or `all` | Export artifact as raw XML attachment |
| `GET /api/v1/artifacts/{type}/id/{id}/export` | `read-only`, `write`, `upload`, or `all` | Export artifact by database table `ID` as raw XML attachment |
| `POST /api/v1/artifacts/{type}` | `write`, `upload`, or `all` | Create a new artifact (fails with `409 Conflict` if it already exists unless `upsert=true`) |
| `PUT /api/v1/artifacts/{type}/{name}` | `write`, `upload`, or `all` | Update an existing artifact by name |
| `PUT /api/v1/artifacts/{type}/id/{id}` | `write`, `upload`, or `all` | Update an existing artifact by database table `ID` |
| `DELETE /api/v1/artifacts/{type}/{name}` | `write`, `upload`, or `all` | Delete an artifact by name |
| `DELETE /api/v1/artifacts/{type}/id/{id}` | `write`, `upload`, or `all` | Delete an artifact by database table `ID` |
| `POST /api/v1/execute` | `execution-only` or `all` | Execute a pipeline synchronously with all CLI/Builder options |
| `POST /api/v1/execute/{type}/{id}` | `execution-only` or `all` | Execute by artifact type (`pipeline`, `options`, `config`) and database table `ID` |
| `POST /api/v1/execute/stream` | `execution-only` or `all` | Execute a pipeline and stream live SSE events (`event: node_event`, `event: done`) |
| `POST\|GET /api/v1/execute/stream/{type}/{id}` | `execution-only` or `all` | Stream live SSE execution by artifact type and database table `ID` (also `/api/v1/execute/{type}/{id}/stream`) |
| `POST /api/v1/validate` | `execution-only` or `all` | Validate pipeline syntax, XSD, and preflight checks (`validate=true`) |
| `POST /api/v1/validate/{type}/{id}` | `execution-only` or `all` | Validate an artifact by type and database table `ID` |
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

### 3. Execute or Stream by Artifact Type and Database Table `ID` (`execution-only` / `all`)
```bash
# Synchronous execution of pipeline ID=1 from dbo.flow_pipeline_content
curl -u runner_svc:runner-secret-pass \
  -X POST "http://localhost:8085/api/v1/execute/pipeline/1?format=json&node_output=true"

# Synchronous execution of saved CLI options ID=2 from dbo.flow_options_content
curl -u runner_svc:runner-secret-pass \
  -X POST "http://localhost:8085/api/v1/execute/options/2"

# Live SSE streaming execution of pipeline ID=1
curl -N -u runner_svc:runner-secret-pass \
  -X POST "http://localhost:8085/api/v1/execute/stream/pipeline/1" \
  -H "Content-Type: application/json" \
  -d '{"node_output": true, "vars": {"ENV": "staging"}}'
```

### 4. Query Pipeline Events & `node_output` (`read-only` / `all`)
```bash
curl -u auditor:auditor-secret-pass \
  "http://localhost:8085/api/v1/events?limit=20"
```

