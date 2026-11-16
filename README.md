# `go-flowAPI` — CRUD, File Upload, Execution & Telemetry API for `Flow Ecosystem`

`go-flowAPI` is a unified REST API for managing `go-flow` database artifacts (`pipelines`, `options`, `configs`), uploading XML/JSON files to the database, executing pipelines with full CLI/Builder parity (including SSE live streaming and `node_output` logging), and auditing all API and pipeline events.

**Online Documentation:** [https://etl-madness.github.io/flow-docs/](https://etl-madness.github.io/flow-docs/)


## Key Features

1. **Adjustable Table & Schema Paths (`tables.go`)**:
   - Controlled via `const` declarations (`DefaultSchema = "dbo."`, `TablePipelineContent`, `TableOptionsContent`, `TableConfigContent`, `TablePipelineRuns`, `TablePipelineEvents`, `TableAPIAuditEvents`, `TableAPIUsers`, `TableAPIGroups`, `TableAPIUserGroups`, `TablePipelineGroups`, `TablePipelineGroupMembers`, `TablePipelineACL`).
2. **Startup Connection String Variable (`config.go`)**:
   - Defined in `var StartupConnectionString` and loaded at startup (can be overridden via `FLOW_API_DSN` or `-dsn`).
3. **5 Supported Database Dialects**:
   - Microsoft SQL Server / Azure SQL (`sqlserver://` / `mssql://`)
   - PostgreSQL (`postgres://` / `postgresql://`)
   - MySQL / MariaDB (`mysql://`)
   - SQLite (`sqlite://`)
   - Oracle (`oracle://`)
4. **Database-Backed BasicAuth, Windows/AD Groups & Granular Pipeline ACLs (`auth.go`, `acl.go`)**:
   - Authenticates against `TableAPIUsers` (`dbo.flow_api_users`) as well as Windows/AD principals and groups, supporting global roles plus per-pipeline and per-pipeline-group ACLs (`create`, `read`, `update`, `delete`, `execute` with `allow` / `deny` precedence):
     - `scoped`: Zero global rights — strictly governed by user- and group-level rules in `TablePipelineACL` (`dbo.flow_pipeline_acl`)
     - `execution-only`: Execute and validate pipelines (`POST /api/v1/execute`, `POST /api/v1/execute/stream`, `POST /api/v1/validate`) and read/list/export artifacts & query logs
     - `read-only`: Read/list/export artifacts and query telemetry & audit logs (`GET /api/v1/...`)
     - `write` or `upload`: Upload files and create/update/delete/read artifacts (`/api/v1/upload/...`, `/api/v1/artifacts/...`)
     - `all`: Full access across all endpoints including user, group, pipeline-group, and ACL management (`/api/v1/users`, `/api/v1/groups`, `/api/v1/pipeline-groups`, `/api/v1/acls`) and DB connectivity testing (`/api/v1/db/test`)
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
go run . -addr :8080 -dsn "sqlserver://sa:YourStrong!Passw0rd@localhost:1433?database=EXAMPLE_DB&trustServerCertificate=true"

# 2. Build and run compiled binary
go build -o go-flowAPI.exe .
./go-flowAPI.exe -addr :8080 -dsn "sqlserver://sa:YourStrong!Passw0rd@localhost:1433?database=EXAMPLE_DB&trustServerCertificate=true"

# (Optional) Seed/update an initial admin user at startup only if needed:
go run . -addr :8080 -bootstrap-user admin -bootstrap-pass "YourStrong!AdminPass"
```

### Running with Docker Compose

See **[`DOCKER.md`](./DOCKER.md)** for the full Docker Quickstart Guide. Four Docker Compose configurations are included to cover each database backend (defaulting to port `8080`):

```bash
# 1. Connect to an existing / host database via FLOW_API_DSN (no bootstrap env vars required if users exist)
FLOW_API_DSN="sqlserver://flow_api:YourStrong!Passw0rd@host.docker.internal:1433?database=EXAMPLE_DB&trustServerCertificate=true" \
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
| `-env-file` | `FLOW_API_ENV_FILE` | `""` | Path to `KEY=VALUE` environment configuration file (`go-flow-api.env`) |
| `-log-file` | `FLOW_API_LOG_FILE` | `""` | Optional file path to append server logs (recommended for Windows SCM / SysVinit) |
| `-service-name` | — | `"GoFlowAPI"` | Service name when running under the Windows Service Control Manager |
| `-secure-key` | `FLOW_SECURE_KEY` | `""` | Default encryption/decryption passphrase for encrypted artifacts |
| `-key-file` | — | `""` | Path to file containing default encryption/decryption key |
| `-bootstrap-user` | `FLOW_API_BOOTSTRAP_USER` | `""` | Initial API username to create/update at startup |
| `-bootstrap-pass` | `FLOW_API_BOOTSTRAP_PASS` | `""` | Password for `-bootstrap-user` (automatically bcrypt-hashed) |
| `-bootstrap-perm` | `FLOW_API_BOOTSTRAP_PERM` | `"all"` | Permission role for `-bootstrap-user` (`all`, `scoped`, `execution-only`, `read-only`, `write`, `upload`) |
| `-strict-acl` | `FLOW_API_STRICT_ACL` | `false` | Enforce strict ACL-only pipeline authorization for all non-`all` users |
| `-hash-password` | — | `""` | Generate and print a bcrypt hash for the given password and exit |
| `-debug` | `FLOW_API_DEBUG` | `false` | Enable verbose debug logging |

---

## OS Packaging, Installation & Background Services (Unix `/opt/flow-api` & Windows WinGet)

Build scripts, package manifests, OS service units (`systemd`, OpenRC, SysVinit, FreeBSD `rc.d`, macOS `launchd`, and native Windows Service helpers), `man` pages, GNU `info` documentation, WiX `.msi` definitions, and WinGet manifests are located in [`packaging/`](packaging/README.md).

All packages compile `go-flowAPI` (`go-flowAPI.exe` on Windows) as a static pure-Go binary (`CGO_ENABLED=0`), use non-colliding package identifiers (**`go-flow-api`** on Unix, **`ETLMadness.GoFlowAPI`** on WinGet), and install isolated under **`/opt/flow-api`** (or `C:\Program Files\ETL Madness\Flow API\`) so they never conflict with system binaries or with `go-flow` (`/opt/flow`):

| Target Platform | Package Format | Build Script | Install Command | OS Service / `rc` Integration |
| :--- | :--- | :--- | :--- | :--- |
| **Debian / Ubuntu** | `.deb` | `sh ./packaging/scripts/build-deb.sh [amd64\|arm64]` | `sudo apt install ./packaging/dist/go-flow-api_*.deb` | `systemd` (`go-flow-api.service`), OpenRC & SysVinit |
| **RHEL / Fedora / SUSE / openSUSE** | `.rpm` | `sh ./packaging/scripts/build-rpm.sh [amd64\|arm64]` | `sudo dnf install ./packaging/dist/go-flow-api-*.rpm` *(or `zypper`)* | `systemd` (`go-flow-api.service`), OpenRC & SysVinit |
| **Arch Linux** | `.pkg.tar.zst` | `sh ./packaging/scripts/build-arch.sh [amd64\|arm64]` | `sudo pacman -U ./packaging/dist/go-flow-api-*.pkg.tar.zst` | `systemd` (`go-flow-api.service`) |
| **macOS** | `.pkg` | `sh ./packaging/scripts/build-macos-pkg.sh [arm64\|amd64\|universal]` | `sudo installer -pkg ./packaging/dist/go-flow-api-*.pkg -target /` | `launchd` (`com.etlmadness.go-flow-api.plist`) |
| **FreeBSD** | `.pkg` | `sh ./packaging/scripts/build-freebsd-pkg.sh [amd64\|arm64]` | `sudo pkg add ./packaging/dist/go-flow-api-*.pkg` | `rc.d` (`/usr/local/etc/rc.d/go_flow_api` + `sysrc`) |
| **Windows (WinGet Portable)** | `.zip` + WinGet YAML | `.\packaging\scripts\Build-Windows.ps1 -Format zip` | `winget install --manifest packaging\dist\winget\e\ETLMadness\GoFlowAPI\<ver>\` | Native SCM in `go-flowAPI.exe` + `Install-GoFlowAPIService.ps1` |
| **Windows (Enterprise MSI)** | `.msi` + WinGet YAML | `.\packaging\scripts\Build-Windows.ps1 -Format all` | `msiexec /i packaging\dist\go-flow-api-*-windows-amd64.msi` | Native SCM in `go-flowAPI.exe` + `Install-GoFlowAPIService.ps1` |

To automatically build native package(s) for the current Unix host into `packaging/dist/`:

```sh
sh ./packaging/scripts/build-all.sh host
```

See **[`packaging/README.md`](packaging/README.md)** and **[`packaging/winget/README.md`](packaging/winget/README.md)** for full build options, `go-flow-api.env` configuration, **[running outside Docker with Kerberos tickets (`klist`) & Active Directory permissions](packaging/README.md#8-running-outside-docker-with-kerberos-tickets-klist--active-directory-permissions)**, and step-by-step instructions to enable, monitor, and manage `systemd`, OpenRC, SysVinit, FreeBSD `rc.d`, macOS `launchd`, and Windows Services.

---

## API Endpoints & Permission Matrix

Full endpoint schemas and `curl` examples are documented in **[`docs/API_ENDPOINTS_REFERENCE.md`](docs/API_ENDPOINTS_REFERENCE.md)** and served interactively at `/swagger/`.

| Method & Path | Required Permission | Description |
| :--- | :--- | :--- |
| `GET /swagger/` (or `/docs`) | Public (Authorize in UI) | Interactive Swagger UI with BasicAuth support |
| `GET /swagger/doc.json` (or `/api/v1/openapi.json`) | Public | OpenAPI 3.0.3 JSON specification |
| `GET /api/v1/health` | Public (Optional BasicAuth) | Health check, DB session identity, caller `groups`, and configured table status |
| `POST\|PUT /api/v1/upload/{type}` | `write`, `upload`, `all`, or Pipeline ACL (`create`/`update`) | Upload `.xml`/`.json` file (`multipart/form-data` or raw body) into `pipelines`, `options`, or `configs` (supports optional `group` for pipelines) |
| `GET /api/v1/artifacts/{type}` | `read-only`+ or Pipeline ACL (`read`) | List artifacts (`pipelines`, `options`, `configs`); filters `pipelines` to readable items |
| `GET /api/v1/artifacts/{type}/{name}` | `read-only`+ or Pipeline ACL (`read`) | Fetch artifact by name (use `?decrypt=true` to decrypt) |
| `GET /api/v1/artifacts/{type}/id/{id}` | `read-only`+ or Pipeline ACL (`read`) | Fetch artifact by database table `ID` (`?decrypt=true` supported) |
| `GET /api/v1/artifacts/{type}/{name}/export` | `read-only`+ or Pipeline ACL (`read`) | Export artifact as raw XML attachment |
| `GET /api/v1/artifacts/{type}/id/{id}/export` | `read-only`+ or Pipeline ACL (`read`) | Export artifact by database table `ID` as raw XML attachment |
| `POST /api/v1/artifacts/{type}` | `write`, `upload`, `all`, or Pipeline ACL (`create`/`update`) | Create/upsert artifact (`upsert=true` supported; optional `groups` array for `pipeline`) |
| `PUT /api/v1/artifacts/{type}/{name}` | `write`, `upload`, `all`, or Pipeline ACL (`update`) | Update an existing artifact by name |
| `PUT /api/v1/artifacts/{type}/id/{id}` | `write`, `upload`, `all`, or Pipeline ACL (`update`) | Update an existing artifact by database table `ID` |
| `DELETE /api/v1/artifacts/{type}/{name}` | `write`, `upload`, `all`, or Pipeline ACL (`delete`) | Delete an artifact by name |
| `DELETE /api/v1/artifacts/{type}/id/{id}` | `write`, `upload`, `all`, or Pipeline ACL (`delete`) | Delete an artifact by database table `ID` |
| `POST /api/v1/execute` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Execute a pipeline synchronously with all CLI/Builder options |
| `POST /api/v1/execute/{type}/{id}` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Execute by artifact type (`pipeline`, `options`, `config`) and database table `ID` |
| `POST\|GET /api/v1/execute/stream` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Execute a pipeline and stream live SSE events (`event: node_event`, `event: done`) |
| `POST\|GET /api/v1/execute/stream/{type}/{id}` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Stream live SSE execution by artifact type and database table `ID` (also `/api/v1/execute/{type}/{id}/stream`) |
| `POST /api/v1/validate` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Validate pipeline syntax, XSD, and semantic AST rules (`validate=true`) |
| `POST /api/v1/validate/{type}/{id}` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Validate an artifact by type and database table `ID` |
| `POST /api/v1/crypto/keygen` | `read-only` or higher | Generate AES-256, RSA, ECDSA, Ed25519 (+ X.509 cert), or OpenPGP keypairs |
| `POST /api/v1/crypto/encrypt` | `read-only` or higher | Encrypt XML/text payload into a `FLOWENC:v1:` AES-256-GCM envelope |
| `POST /api/v1/crypto/decrypt` | `read-only` or higher | Decrypt a `FLOWENC:v1:` envelope |
| `POST /api/v1/crypto/sign` | `read-only` or higher | Digitally sign XML payloads (`OPENSSL`, `PKCS7`, or `PGP`) |
| `POST /api/v1/crypto/verify` | `read-only` or higher | Verify wrapped (`FLOWSIGNED:v1:`) or detached digital signatures |
| `GET /api/v1/runs` | `read-only` or higher | Query `pipeline_runs` history |
| `GET /api/v1/events` | `read-only` or higher | Query `pipeline_events` (including `node_output`) |
| `GET /api/v1/audit` | `read-only` or higher | Query `flow_api_events` API audit trail |
| `POST /api/v1/db/test` | `all` | Test connectivity to the startup DB or an external target DSN |
| `GET /api/v1/users` | `all` | List BasicAuth/Windows users, roles (`all`, `scoped`, `write`, `upload`, `execution-only`, `read-only`), and `groups` |
| `POST /api/v1/users` | `all` | Create or update a user (automatically bcrypt-hashes password; optional `groups`) |
| `DELETE /api/v1/users/{username}` | `all` | Delete a user and clean up group memberships/direct ACLs |
| `GET\|POST /api/v1/groups` | `all` | List or create/update API user groups (`TableAPIGroups`) |
| `GET\|PUT\|DELETE /api/v1/groups/{name}` | `all` | Inspect, update, or delete an API user group |
| `GET\|POST\|DELETE /api/v1/groups/{name}/members[/{username}]` | `all` | List, add, or remove user memberships (`TableAPIUserGroups`) |
| `GET\|POST /api/v1/pipeline-groups` | `GET`: Auth; `POST`: `all` | List or create/update pipeline groups with optional `name_pattern` glob (`TablePipelineGroups`) |
| `GET\|PUT\|DELETE /api/v1/pipeline-groups/{name}` | `GET`: Auth; `PUT\|DELETE`: `all` | Inspect, update, or delete a pipeline group |
| `GET\|POST\|DELETE /api/v1/pipeline-groups/{name}/pipelines[/{pipeline}]` | `GET`: Auth; `POST\|DELETE`: `all` | List, add, or remove explicit pipeline memberships (`TablePipelineGroupMembers`) |
| `GET\|POST /api/v1/acls` | `all` | List (filterable) or create/upsert granular `allow`/`deny` pipeline ACL rules (`TablePipelineACL`) |
| `PUT\|DELETE /api/v1/acls/{id}` | `all` | Update or delete a granular pipeline ACL rule by ID |
| `GET /api/v1/permissions/effective` (or `/api/v1/me/permissions`) | Any authenticated user (`all` to inspect others) | Compute effective per-pipeline CRUD & execute permissions (`?username=&groups=` supported for admins) |

---

## Example Requests

### 1. Upload a Pipeline XML File (`write` / `upload` / `all`)
```bash
curl -u publisher:publisher-secret-pass \
  -F "file=@my_pipeline.xml" \
  "http://localhost:5180/api/v1/upload/pipelines?name=daily_etl"
```

### 2. Execute a Pipeline with `node_output` Logging (`execution-only` / `all`)
```bash
curl -u runner_svc:runner-secret-pass \
  -X POST http://localhost:5180/api/v1/execute \
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
  -X POST "http://localhost:5180/api/v1/execute/pipeline/1?format=json&node_output=true"

# Synchronous execution of saved CLI options ID=2 from dbo.flow_options_content
curl -u runner_svc:runner-secret-pass \
  -X POST "http://localhost:5180/api/v1/execute/options/2"

# Live SSE streaming execution of pipeline ID=1
curl -N -u runner_svc:runner-secret-pass \
  -X POST "http://localhost:5180/api/v1/execute/stream/pipeline/1" \
  -H "Content-Type: application/json" \
  -d '{"node_output": true, "vars": {"ENV": "staging"}}'
```

### 4. Query Pipeline Events & `node_output` (`read-only` / `all`)
```bash
curl -u auditor:auditor-secret-pass \
  "http://localhost:5180/api/v1/events?limit=20"
```

