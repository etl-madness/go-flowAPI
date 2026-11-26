# `go-flowAPI` — Orchestration, Artifact CRUD, Execution & Telemetry REST API (`v1.3.10`)

> **Version:** `1.3.10` | **Core Engine:** `github.com/etl-madness/flow v1.4.7` | **OpenAPI 3.0.3:** `/api/v1/openapi.json` | **Swagger UI:** `/swagger/`  
> **Companion Studio UI:** `FlowUI v1.3.3` | **Release Notes:** [`RELEASE_NOTES.md`](./RELEASE_NOTES.md) | **Online Docs:** [etl-madness.github.io/flow-docs](https://etl-madness.github.io/flow-docs/)

**`go-flowAPI`** is the centralized REST and Server-Sent Events (SSE) API gateway for the **ETL Madness Flow Ecosystem**. It provides database-backed artifact CRUD and file upload (`pipelines`, `options`, `configs`), synchronous and live-streaming pipeline execution with per-job sandboxed workspaces, granular user/group pipeline ACLs, cryptographic utilities, Active Directory Kerberos integration, and full audit/telemetry logging across **SQL Server, PostgreSQL, MySQL/MariaDB, SQLite, and Oracle**.

---

## What's New in `v1.3.10`

- **Database Server Telemetry (`db_server`) & Cross-DB `sql_bulk` Tracking**: Captures `<server>/<database>` across all 5 database engines in `pipeline_runs`, `pipeline_events`, `/api/v1/health`, and `/api/v1/db/test`. Cross-database `<sql_bulk>` steps log both `source->target` database servers and `source->target` SPIDs, while `QueryPipelineRuns` falls back to the active logging DB server for legacy rows.
- **Docker Build-Time Schema & Table Overrides (`FLOW_EXTRA_LDFLAGS`)**: `Dockerfile` and all `docker-compose*.yml` files accept `FLOW_EXTRA_LDFLAGS` as a build arg (e.g. `FLOW_EXTRA_LDFLAGS="-X github.com/etl-madness/flow.DefaultSchema=etl" docker compose up -d --build`), and `docker/docker-entrypoint.sh` always initializes SQLite via `/app/go-flowAPI -print-ddl sqlite` so both build-time and runtime table overrides are honored automatically.
- **Per-Job Sandboxed Workspace Isolation (`-data-dir` / `FLOW_API_DATA_DIR`)**: Each pipeline execution creates an isolated `<data-dir>/<user_login>/<pid>-<run_id>` directory for the duration of the job, confines all script subprocesses (`cmd.Dir`, `TMPDIR`/`TEMP`/`TMP`/`FLOW_JOB_DIR`), Flow file nodes (`<file_read>`, `<file_save>`, `<excel_*>`, `<template>`, `<xml_xpath>`, `<json_path>`, `<yaml_path>`), XSLT output, XSD temp files, and local file-backed stores (`sqlite`, `bbolt`, `badger`) strictly inside it, rejects `..` and external path escapes, and removes the directory automatically when the job completes.
- **Configurable Database Schema & Table Names (`-tables-config`, `-print-ddl`, `-ldflags`)**: Override the default schema (`dbo`) and/or any of the 12 table names either at **build time** (`-ldflags="-X github.com/etl-madness/flow.DefaultSchema=etl_flow"` or `FLOW_EXTRA_LDFLAGS` in OS packaging scripts and Docker builds) or at **runtime** via a JSON/YAML mapping file (`-tables-config` / `FLOW_TABLES_CONFIG`). Generate matching `CREATE SCHEMA` / `CREATE TABLE` / `CREATE INDEX` SQL for any dialect with `-print-ddl <sqlserver|postgres|mysql|sqlite|oracle>`.
- **Shared Unix Kerberos SQL Server Auth (`authenticator=krb5`)**: Powered by `github.com/etl-madness/flow v1.4.5`, automatically registers pure-Go Kerberos (`krb5`) auth on Linux, FreeBSD, OpenBSD, and macOS, strips `FILE:` prefixes from `KRB5CCNAME`/`KRB5_KTNAME`, qualifies `MSSQLSvc` SPNs, and cleanly falls back to standard SQL Server Authentication for bare `user:password` SQL logins.
- **Native OpenBSD Packaging & FreeBSD AD Kerberos Guide**: Added full OpenBSD `.tgz` packaging (`packaging/scripts/build-openbsd-pkg.sh` + `/etc/rc.d/go_flow_api`) and [`docs/FREEBSD_API_AD_INTEGRATION_GUIDE.md`](./docs/FREEBSD_API_AD_INTEGRATION_GUIDE.md).

---

## Key Features

1. **5 Pure-Go Database Backends (`CGO_ENABLED=0`)**:
   - Microsoft SQL Server / Azure SQL (`sqlserver://` / `mssql://`), PostgreSQL (`postgres://`), MySQL / MariaDB (`mysql://`), SQLite (`sqlite://`), and Oracle (`oracle://`).
2. **Customizable Schema & 12-Table Layout (`tables.go`)**:
   - Backed by shared `flow.TableConfig` across `go-flowAPI`, `go-flow`, `builder`, and `db_importer`, with strict SQL identifier validation and `-print-ddl <dialect>` SQL generation.
3. **Database-Backed BasicAuth, Windows/AD Groups & Default-Strict Pipeline ACLs (`auth.go`, `acl.go`)**:
   - Authenticates against `TableAPIUsers` (`dbo.flow_api_users`) and Active Directory / Windows user & group identities (`X-Flow-Windows-User`, `X-Flow-Windows-Groups`).
   - **Default-strict ACLs (`-strict-acl=true`)**: Non-admin (`non-"all"`) accounts have zero access to pipelines unless explicitly granted via `allow` rules in `TablePipelineACL` (`dbo.flow_pipeline_acl`, with explicit `deny` overriding `allow`):
     - `scoped`: Zero global rights — strictly governed by per-pipeline and per-pipeline-group ACL rules.
     - `execution-only`: Non-pipeline read/crypto/telemetry access plus pipeline execution/validation granted via ACLs.
     - `read-only`: Read/export `config`/`options`, query telemetry/audit logs, and read pipelines granted via ACLs.
     - `write` / `upload`: Upload/CRUD `config`/`options` and manage pipelines granted via ACLs.
     - `all`: Full administrative access across all endpoints (users, groups, pipeline groups, ACLs, `/api/v1/db/test`).
4. **Full Audit & Execution Telemetry (`telemetry.go`)**:
   - Every HTTP request is audited to `TableAPIAuditEvents` (`dbo.flow_api_events`).
   - Every pipeline run records summaries in `TablePipelineRuns` (`dbo.pipeline_runs`) and step-level events (including optional secret-redacted `node_output`) in `TablePipelineEvents` (`dbo.pipeline_events`).
5. **Security Hardening**:
   - Parameterized `sql://` / `db://` artifact URI lookups (`#table=...&column=...&name=...`), strict path-traversal confinement, and optional per-job `-data-dir` sandboxing.
6. **Interactive Swagger UI & OpenAPI 3.0.3 Spec (`swagger.go`)**:
   - Served at `/swagger/` (or `/docs`) with BasicAuth **Authorize** support; OpenAPI JSON at `/api/v1/openapi.json`.

---

## Quick Start

### 1. Provision the Database Schema & Bootstrap Admin User
`go-flowAPI` does **not** auto-create database tables at startup. Generate DDL for your target database (with default or custom schema/tables) and optionally seed an initial `all`-permission admin user:

```sh
# Generate DDL for your dialect (sqlserver, postgres, mysql, sqlite, or oracle)
go-flowAPI -print-ddl sqlite    | sqlite3 flow_api.db
go-flowAPI -print-ddl sqlserver > init-sqlserver.sql
go-flowAPI -print-ddl postgres  > init-postgres.sql
```

See **[`SCHEMA.md`](./SCHEMA.md)** for full DDL reference and **[`BOOTSTRAP.md`](./BOOTSTRAP.md)** for bootstrapping initial users.

### 2. Run `go-flowAPI`

```sh
# Build a static pure-Go binary
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o go-flowAPI .

# Run with SQL Server / PostgreSQL / SQLite and per-job sandboxed workspaces
./go-flowAPI \
  -addr :5180 \
  -dsn "sqlserver://sa:YourStrong!Passw0rd@localhost:1433?database=EXAMPLE_DB&trustServerCertificate=true" \
  -data-dir /var/lib/go-flow-api/jobs

# One-time initial admin user bootstrap (omit once users exist in flow_api_users)
./go-flowAPI -addr :5180 -dsn "sqlite://flow_api.db" -bootstrap-user admin -bootstrap-pass "YourStrong!AdminPass"
```

### 3. Customizing Database Schema & Table Names

- **At Runtime (`-tables-config` / `FLOW_TABLES_CONFIG`)**: Provide a JSON or YAML file overriding `schema` and/or any of the 12 table names:
  ```json
  {
    "schema": "etl_flow",
    "pipeline_runs": "etl_pipeline_runs",
    "pipeline_events": "etl_pipeline_events"
  }
  ```
  ```sh
  ./go-flowAPI -tables-config tables.json -print-ddl sqlserver > init-etl.sql
  ./go-flowAPI -tables-config tables.json -addr :5180
  ```
- **At Build Time (`-ldflags` / `FLOW_EXTRA_LDFLAGS`)**:
  ```sh
  CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/etl-madness/flow.DefaultSchema=etl_flow" \
    -o go-flowAPI .
  ```

### 4. Running with Docker Compose
See **[`DOCKER.md`](./DOCKER.md)** and **[`docs/AD_KERBEROS_SIDECAR_GUIDE.md`](./docs/AD_KERBEROS_SIDECAR_GUIDE.md)** for complete Docker guides:

```sh
# SQLite quickstart with persistent volume & auto-init
docker compose -f docker-compose.sqlite.yml up -d --build

# PostgreSQL 16 + auto-init
docker compose -f docker-compose.postgres.yml up -d --build

# SQL Server 2022 + auto-init
docker compose -f docker-compose.sqlserver.yml up -d --build

# Existing / host database (with optional build-time schema override)
FLOW_EXTRA_LDFLAGS="-X github.com/etl-madness/flow.DefaultSchema=etl_flow" \
FLOW_API_DSN="sqlserver://flow_api:YourStrong!Passw0rd@host.docker.internal:1433?database=EXAMPLE_DB&trustServerCertificate=true" \
  docker compose up -d --build
```

---

## Command-Line Flags & Environment Variables

| Flag | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `-dsn` | `FLOW_API_DSN` | `StartupConnectionString` | Startup database connection string |
| `-addr` | `FLOW_API_ADDR` / `PORT` | `:8080` | HTTP listen address |
| `-env-file` | `FLOW_API_ENV_FILE` | `""` | Path to `KEY=VALUE` environment config file (`go-flow-api.env`) |
| `-log-file` | `FLOW_API_LOG_FILE` | `""` | Optional log file path (recommended for Windows SCM / SysVinit / OpenBSD) |
| `-data-dir` | `FLOW_API_DATA_DIR` | `""` | Root directory for per-job `<user_login>/<pid>-<run_id>` sandboxed workspaces |
| `-tables-config` | `FLOW_TABLES_CONFIG` | `""` | Path to JSON/YAML database schema & table name mapping file |
| `-print-ddl` | — | `""` | Print `CREATE TABLE` DDL for `sqlserver`, `postgres`, `mysql`, `sqlite`, or `oracle` and exit |
| `-strict-acl` | `FLOW_API_STRICT_ACL` | `true` | Require explicit `flow_pipeline_acl` `allow` rules for all non-`all` users |
| `-secure-key` | `FLOW_SECURE_KEY` | `""` | Default AES-256-GCM passphrase for encrypted artifacts (`FLOWENC:v1:`) |
| `-key-file` | — | `""` | Path to file containing default encryption/decryption passphrase |
| `-bootstrap-user` | `FLOW_API_BOOTSTRAP_USER` | `""` | Optional initial API username to seed/upsert at startup |
| `-bootstrap-pass` | `FLOW_API_BOOTSTRAP_PASS` | `""` | Password for `-bootstrap-user` (bcrypt-hashed automatically) |
| `-bootstrap-perm` | `FLOW_API_BOOTSTRAP_PERM` | `"all"` | Role for `-bootstrap-user` (`all`, `scoped`, `execution-only`, `read-only`, `write`, `upload`) |
| `-service-name` | — | `"GoFlowAPI"` | Windows Service Control Manager (SCM) service name |
| `-hash-password` | — | `""` | Generate and print a bcrypt hash for the given plaintext password and exit |
| `-debug` | `FLOW_API_DEBUG` | `false` | Enable verbose debug logging |

---

## OS Packaging & Background Services (`/opt/flow-api` & Windows SCM)

All OS package scripts, service units, `man`/`info` manuals, and WinGet/MSI manifests live in **[`packaging/`](./packaging/README.md)**. Packages compile static binaries (`CGO_ENABLED=0`), install isolated under `/opt/flow-api` (or `C:\Program Files\ETL Madness\Flow API\`), and support `FLOW_EXTRA_LDFLAGS` for custom build-time schema/table defaults:

| Target Platform | Package Format | Build Script | OS Service Integration |
| :--- | :--- | :--- | :--- |
| **Debian / Ubuntu** | `.deb` | `sh ./packaging/scripts/build-deb.sh [amd64\|arm64]` | `systemd`, OpenRC, SysVinit |
| **RHEL / Fedora / SUSE** | `.rpm` | `sh ./packaging/scripts/build-rpm.sh [amd64\|arm64]` | `systemd`, OpenRC, SysVinit |
| **Arch Linux** | `.pkg.tar.zst` | `sh ./packaging/scripts/build-arch.sh [amd64\|arm64]` | `systemd` |
| **macOS** | `.pkg` | `sh ./packaging/scripts/build-macos-pkg.sh [arm64\|amd64\|universal]` | `launchd` |
| **FreeBSD** | `.pkg` | `FREEBSD_MAJOR=15 sh ./packaging/scripts/build-freebsd-pkg.sh [amd64\|arm64]` | `rc.d` (`/usr/local/etc/rc.d/go_flow_api` + `sysrc`) |
| **OpenBSD** | `.tgz` | `sh ./packaging/scripts/build-openbsd-pkg.sh [amd64\|arm64]` | `rc.d` (`/etc/rc.d/go_flow_api` + `rcctl`) |
| **Windows** | `.zip`, `.msi`, WinGet | `.\packaging\scripts\Build-Windows.ps1 -Format all` | Native Windows SCM (`Install-GoFlowAPIService.ps1`) |

```sh
# Build for current host OS (optionally baking a custom default schema)
export FLOW_EXTRA_LDFLAGS="-X github.com/etl-madness/flow.DefaultSchema=etl_flow"
sh ./packaging/scripts/build-all.sh host
```

For FreeBSD bare-metal Active Directory Kerberos (`kinit` / `ktutil` / `rc.d`) setup, see **[`docs/FREEBSD_API_AD_INTEGRATION_GUIDE.md`](./docs/FREEBSD_API_AD_INTEGRATION_GUIDE.md)**.

---

## API Endpoints & Permission Matrix

Complete request/response schemas are documented in **[`docs/API_ENDPOINTS_REFERENCE.md`](./docs/API_ENDPOINTS_REFERENCE.md)** and interactively at `/swagger/`.

| Method & Path | Required Permission | Description |
| :--- | :--- | :--- |
| `GET /swagger/` (or `/docs`) | Public | Interactive Swagger UI with BasicAuth support |
| `GET /api/v1/openapi.json` | Public | OpenAPI 3.0.3 JSON specification |
| `GET /api/v1/health` | Public *(Optional BasicAuth)* | Health check, DB session identity, caller `groups`, and active `tables` map |
| `POST\|PUT /api/v1/upload/{type}` | `write`, `upload`, `all`, or Pipeline ACL (`create`/`update`) | Upload `.xml`/`.json` into `pipelines`, `options`, or `configs` |
| `GET /api/v1/artifacts/{type}[/{name}\|/id/{id}][/export]` | `read-only`+ or Pipeline ACL (`read`) | List, fetch (`?decrypt=true`), or export artifacts by name or table `ID` |
| `POST\|PUT\|DELETE /api/v1/artifacts/{type}[/{name}\|/id/{id}]` | `write`, `upload`, `all`, or Pipeline ACL | Create/upsert, update, or delete artifacts |
| `POST /api/v1/execute[/{type}/{id}]` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Execute a pipeline synchronously |
| `POST\|GET /api/v1/execute/stream[/{type}/{id}]` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Stream live SSE execution events (`node_event`, `done`) |
| `POST /api/v1/validate[/{type}/{id}]` | `execution-only`, `all`, or Pipeline ACL (`execute`) | Validate pipeline XML syntax, XSD, and AST rules |
| `POST /api/v1/crypto/{keygen,encrypt,decrypt,sign,verify}` | `read-only`+ | AES-256-GCM encryption/decryption and OpenSSL/PKCS#7/PGP signing & verification |
| `GET /api/v1/{runs,events,audit}` | `read-only`+ | Query `pipeline_runs`, `pipeline_events` (incl. `node_output`), and `flow_api_events` |
| `POST /api/v1/db/test` | `all` | Test connectivity to the startup DB or an external target DSN |
| `GET\|POST\|DELETE /api/v1/users[/{username}]` | `all` | Manage API users (`TableAPIUsers`) |
| `GET\|POST\|PUT\|DELETE /api/v1/groups[/{name}[/members[/{username}]]]` | `all` | Manage API user groups and memberships |
| `GET\|POST\|PUT\|DELETE /api/v1/pipeline-groups[/{name}[/pipelines[/{pipeline}]]]` | `GET`: Auth; Mutate: `all` | Manage pipeline groups (`name_pattern` globs & explicit memberships) |
| `GET\|POST\|PUT\|DELETE /api/v1/acls[/{id}]` | `all` | Manage granular user/group `allow`/`deny` pipeline ACL rules |
| `GET /api/v1/permissions/effective` *(or `/api/v1/me/permissions`)* | Authenticated (`all` to inspect others) | Inspect effective per-pipeline CRUD & execute permissions (`?username=&groups=`) |

---

## Example `curl` Requests

```sh
# 1. Verify health and active database table names
curl -s http://localhost:5180/api/v1/health

# 2. Upload a pipeline XML file
curl -u publisher:publisher-secret-pass \
  -F "file=@my_pipeline.xml" \
  "http://localhost:5180/api/v1/upload/pipelines?name=daily_etl"

# 3. Execute a pipeline synchronously with node_output logging
curl -u runner_svc:runner-secret-pass \
  -X POST http://localhost:5180/api/v1/execute \
  -H "Content-Type: application/json" \
  -d '{"file": "db:daily_etl", "format": "json", "node_output": true, "vars": {"ENV": "production"}}'

# 4. Stream live SSE execution by pipeline ID
curl -N -u runner_svc:runner-secret-pass \
  -X POST "http://localhost:5180/api/v1/execute/stream/pipeline/1" \
  -H "Content-Type: application/json" \
  -d '{"node_output": true, "vars": {"ENV": "staging"}}'

# 5. Query recent step events & node_output
curl -u auditor:auditor-secret-pass \
  "http://localhost:5180/api/v1/events?limit=20"
```

---

## Documentation Index

- **[`RELEASE_NOTES.md`](./RELEASE_NOTES.md)** — Version history (`v1.3.10`, `v1.3.9`, `v1.3.7`, `v1.3.2`, `v1.3.1`)
- **[`SCHEMA.md`](./SCHEMA.md)** & **[`BOOTSTRAP.md`](./BOOTSTRAP.md)** — Database schema & initial admin bootstrap
- **[`DOCKER.md`](./DOCKER.md)** & **[`docs/AD_KERBEROS_SIDECAR_GUIDE.md`](./docs/AD_KERBEROS_SIDECAR_GUIDE.md)** — Docker Compose & Kerberos sidecar deployments
- **[`docs/FREEBSD_API_AD_INTEGRATION_GUIDE.md`](./docs/FREEBSD_API_AD_INTEGRATION_GUIDE.md)** — Native FreeBSD Active Directory Kerberos & `rc.d` guide
- **[`docs/API_ENDPOINTS_REFERENCE.md`](./docs/API_ENDPOINTS_REFERENCE.md)** — Full REST & SSE endpoint reference
- **[`packaging/README.md`](./packaging/README.md)** — Multi-OS packaging, `FLOW_EXTRA_LDFLAGS`, and OS service management
