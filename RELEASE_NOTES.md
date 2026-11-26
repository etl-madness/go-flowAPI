# Release Notes — `go-flowAPI`

## Release Notes (`v1.3.10`) — Database Server Telemetry (`db_server`) & Cross-DB `sql_bulk` Source/Target Tracking

### Overview

Version `1.3.10` upgrades `go-flowAPI` to **`github.com/etl-madness/flow v1.4.7`** and adds database server/catalog tracking (`db_server`) across pipeline run summaries, step execution events, health checks, and database connection tests:

1. **Database Server Identity Capture (`db.go`, `telemetry.go`, `executor.go`, `handlers.go`)**:
   - Added `FetchDatabaseSessionIdentityWithServer` across SQL Server/Azure SQL, PostgreSQL, MySQL/MariaDB, Oracle, and SQLite (capturing `<server>/<database>` or `sqlite:<filename>`).
   - Persists `db_server` in `pipeline_runs` and `pipeline_events` when the column exists (remaining 100% backwards-compatible with existing schemas).
   - For cross-database `<sql_bulk>` nodes, records both `source->target` database servers (`db_server`) and `source->target` SPIDs (`spid`).
   - In `QueryPipelineRuns`, automatically falls back to the active logging database server for legacy rows or existing tables without `db_server`, so `FlowUI` immediately displays the logging database server.
   - Included `db_server` in `/api/v1/health` and `/api/v1/db/test` responses.

2. **Container Init SQL (`docker/init/{sqlite,postgres,sqlserver}-init.sql`)**:
   - Added the `db_server` column to `pipeline_runs` and `pipeline_events` in all bundled Docker initialization scripts.

---

## Release Notes (`v1.3.9`) — Docker Build-Time Schema/Table Overrides (`FLOW_EXTRA_LDFLAGS`), Dynamic SQLite Entrypoint DDL & Unix SQL Server Auth Fix

### Overview

Version `1.3.9` extends build-time database schema and table name overrides to all Docker builds, ensures containerized SQLite initialization always honors build-time and runtime table overrides, and incorporates the Unix SQL Server authentication selection fix from `github.com/etl-madness/flow`:

1. **Docker Build-Time Schema & Table Overrides (`Dockerfile`, `docker-compose*.yml`)**:
   - Added `ARG FLOW_EXTRA_LDFLAGS=""` to `Dockerfile` and appended `${FLOW_EXTRA_LDFLAGS}` to `go build -ldflags="-s -w ${FLOW_EXTRA_LDFLAGS}"`.
   - Forwarded `FLOW_EXTRA_LDFLAGS` under `build.args` in `docker-compose.yml`, `docker-compose.ad-sidecar.yml`, `docker-compose.sqlserver.yml`, `docker-compose.postgres.yml`, and `docker-compose.sqlite.yml` so custom default schemas (e.g., `-X github.com/etl-madness/flow.DefaultSchema=etl`) can be baked into container images via `docker build --build-arg` or `docker compose up -d --build`.

2. **Dynamic SQLite Schema Initialization (`docker/docker-entrypoint.sh`)**:
   - Updated `docker/docker-entrypoint.sh` to always initialize SQLite databases via `/app/go-flowAPI -print-ddl sqlite` (passing `-tables-config "$FLOW_TABLES_CONFIG"` when set), ensuring build-time `DefaultTable*` overrides are honored automatically without requiring a separate runtime config file.

3. **Unix SQL Server Authentication Selection Fix (`github.com/etl-madness/flow`)**:
   - Inherits the `smart_unix` authenticator fix in `flow/krb5_auth.go`: bare `user:password` SQL logins (such as `sa`) without an explicit `authenticator=krb5` parameter now fall back cleanly to standard SQL Server Authentication instead of attempting a Kerberos AS-REQ when `/etc/krb5.conf` or `KRB5CCNAME` is present on Linux, FreeBSD, OpenBSD, or macOS hosts.

---

## Release Notes (`v1.3.7`) — Per-Job Sandboxed Workspaces (`-data-dir`), Configurable Schema/Tables (`-tables-config`, `-print-ddl`), OpenBSD Packaging & Shared Kerberos Auth

### Overview

Version `1.3.7` upgrades `go-flowAPI` to **`github.com/etl-madness/flow v1.4.5`** and introduces per-job sandboxed workspaces, build-time and runtime database schema/table name overrides with dynamic DDL generation, native OpenBSD packaging, and comprehensive FreeBSD Active Directory Kerberos documentation:

1. **Per-Job Sandboxed Workspace Isolation (`-data-dir` / `FLOW_API_DATA_DIR`, `config.go`, `executor.go`, `datadir_test.go`)**:
   - Added `-data-dir` CLI flag and `FLOW_API_DATA_DIR` environment variable.
   - When configured, every pipeline execution creates an isolated `<data-dir>/<user_login>/<pid>-<run_id>` directory for the duration of the job (using sanitized usernames such as `RSYSLAB\SVC_FLOW_API` → `rsyslab_svc_flow_api`), confines all script subprocesses (`cmd.Dir`, `TMPDIR`/`TEMP`/`TMP`/`FLOW_JOB_DIR`), Flow file nodes (`<file_read>`, `<file_save>`, `<excel_*>`, `<template>`, `<xml_xpath>`, `<json_path>`, `<yaml_path>`), XSLT output, XSD temp files, and local file-backed stores (`sqlite`, `bbolt`, `badger`) strictly inside the job directory, and deletes the directory automatically when the job finishes.

2. **Configurable Database Schema & Table Names + Dynamic DDL (`-tables-config`, `-print-ddl`, `tables.go`, `config.go`, `main.go`, `tables_test.go`)**:
   - Replaced compile-time constants in `tables.go` with `flow.TableConfig`-backed variables supporting both **build-time overrides** (`-ldflags "-X github.com/etl-madness/flow.DefaultSchema=..."`) and **runtime JSON/YAML mapping files** via `-tables-config <path>` / `FLOW_TABLES_CONFIG`.
   - Added `-print-ddl <dialect>` (`sqlserver`, `postgres`, `mysql`, `sqlite`, `oracle`) to output ready-to-run `CREATE SCHEMA` / `CREATE TABLE` / `CREATE INDEX` DDL matching the active schema and table names.
   - Updated `docker/docker-entrypoint.sh` to initialize SQLite via `go-flowAPI -tables-config "$FLOW_TABLES_CONFIG" -print-ddl sqlite` when `FLOW_TABLES_CONFIG` is set.
   - Updated `packaging/scripts/common.sh`, `Build-Packages.ps1`, and `Build-Windows.ps1` to accept `FLOW_EXTRA_LDFLAGS` for baking custom build-time schema/table defaults directly into `.deb`, `.rpm`, `.pkg`, `.tgz`, `.zip`, and `.msi` packages.

3. **OpenBSD Packaging & FreeBSD Active Directory Integration (`packaging/openbsd/`, `packaging/services/openbsd/`, `docs/FREEBSD_API_AD_INTEGRATION_GUIDE.md`)**:
   - Added full OpenBSD package support (`build-openbsd-pkg.sh`, `PLIST.tmpl`, `+DESC`, `+INSTALL`, `+DEINSTALL`, and `/etc/rc.d/go_flow_api` with `rcctl` integration).
   - Added `docs/FREEBSD_API_AD_INTEGRATION_GUIDE.md` covering MIT Kerberos (`kinit` / `klist` / `ktutil -f`), `go_flow_api` `rc.d` configuration, `/etc/cron.d` ticket renewal, `go_flow_api_env` `PATH` setup for `pwsh`/tools, and `-data-dir` usage (and bundled it in the FreeBSD `.pkg`).
   - Updated `go-flow-api.env`, `Install-GoFlowAPIService.ps1`, and man pages (`go-flowAPI(1)`, `go-flow-api.env(5)`) for `-data-dir`, `-tables-config`, and `-print-ddl`.

4. **Shared Unix Kerberos SQL Server Auth (`db.go`)**:
   - Delegated `configureUnixKerberosSQLServerAuth` and Kerberos config/SPN normalization helpers directly to `github.com/etl-madness/flow v1.4.5`, eliminating ~140 lines of duplicated code across `go-flowAPI` and `go-flow`.

---

## Release Notes (`v1.3.2`) — Parameterized SQL Resource URIs, Container Scripting Runtimes & Quality Hardening

### Overview

Version `1.3.2` hardens external `sql://` and `db://` artifact URI resolution against SQL injection, expands scripting runtime support in container images, and improves maintainability and test coverage across the API server:

1. **Parameterized SQL Resource URI Lookups (`executor.go` — CodeQL `go/sql-injection` Remediation)**:
   - Replaced arbitrary raw `#<SQL_QUERY>` execution in `loadSQLResourceURI` with structured URL-query-style lookup fragments:
     ```text
     sql://<driver>@<dsn>#table=<schema.table>[&column=<xml_col>][&key_col=<col>&key=<val> | &name=<val> | &id=<val>]
     ```
   - Validates every SQL identifier (`schema`, `table`, `column`/`col`, `key_col`) strictly against `^[A-Za-z_][A-Za-z0-9_]{0,127}$` and reconstructs identifier tokens from a constant ASCII alphabet so zero user-tainted string bytes enter SQL text.
   - Automatically defaults the `column` parameter for standard repository tables (`flow_pipeline_content` → `PipelineXML`, `flow_options_content` → `OptionsXML`, `flow_config_content` → `ConfigXML`) and supports concise `&name=<val>` (`WHERE Name = <param>`) and `&id=<val>` (`WHERE ID = <param>`) shorthands.
   - Binds all lookup filter values exclusively as dialect-aware query parameters (`@p1` for SQL Server/Azure SQL, `$1` for PostgreSQL, `:1` for Oracle, `?` for MySQL/SQLite), matching `go-flow v1.2.2`.
   - Rejects raw `#SELECT ...` fragments, duplicate/unknown parameters, and invalid identifiers prior to opening any database connection.

2. **Local Filesystem Path-Traversal Hardening (`config.go`, `executor.go` — CodeQL `go/path-injection` Remediation)**:
   - Added centralized path sanitization and directory confinement helpers (`resolveSafeLocalPath`, `readSafeLocalFile`, `writeSafeLocalFile`) for all local file operations (`resolveKey`, `loadInlineOrFileBytes`, `resolveArtifactBytes`, `transformXMLWithXSLT`, and `ValidateXSD`).
   - Rejects path-traversal segments (`..`), null/control characters, and non-local relative paths (`filepath.IsLocal`), confining local file fallback reads and XSLT output writes strictly to permitted working and temporary directories.

3. **Container Image Scripting Support (`Dockerfile` / `docker/`)**:
   - Added PowerShell (`pwsh`) and `.NET` `dotnet-script` runtime support inside the `go-flowAPI` container image so pipelines utilizing PowerShell or C# `<script>` nodes execute out-of-the-box in containerized deployments.

4. **Code Quality, Duplication Reduction & Test Coverage (`acl.go`, `handlers_acl.go`, `handlers.go`, `swagger.go`, `api_test.go`)**:
   - Extracted shared group-membership SQL queries, HTTP parameter/CRUD helpers, and OpenAPI 3.0.3 schema/response builders to eliminate duplicated code blocks.
   - Added comprehensive unit and integration tests covering structured SQL URI lookups across dialects, ACL enforcement, group CRUD, telemetry filters, Kerberos SPN/config helpers, and server bootstrap validation (`84%+` overall statement coverage).

---

## Release Notes (`v1.3.1`) — Granular Pipeline & Pipeline-Group ACLs and Default-Strict Authorization

### Overview

- **Granular Pipeline & Pipeline-Group ACLs (`dbo.flow_pipeline_acl`)**: User- and group-level `allow` / `deny` rules for `create`, `read`, `update`, `delete`, `execute`, and `all` across individual pipelines, logical pipeline groups (explicit memberships + case-insensitive `name_pattern` globs such as `fin_*`), or wildcard `*`.
- **Default Strict Pipeline Authorization (`-strict-acl=true` by default)**: Non-admin (`non-"all"`) accounts have zero pipeline access by default unless granted via explicit `allow` rules in `dbo.flow_pipeline_acl` (with explicit `deny` overriding `allow`).
- **API User Groups & Pipeline Groups Management**: Added `/api/v1/groups`, `/api/v1/pipeline-groups`, and `/api/v1/acls` endpoints alongside Active Directory / Windows group resolution (`X-Flow-Windows-Groups`).
- **Effective Permissions Matrix & Simulator**: Added `GET /api/v1/permissions/effective` and `GET /api/v1/me/permissions` with admin principal inspection (`?username=...`) and simulated group evaluation (`?groups=...`).
