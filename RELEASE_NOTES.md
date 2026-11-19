# Release Notes — `go-flowAPI`

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

2. **Container Image Scripting Support (`Dockerfile` / `docker/`)**:
   - Added PowerShell (`pwsh`) and `.NET` `dotnet-script` runtime support inside the `go-flowAPI` container image so pipelines utilizing PowerShell or C# `<script>` nodes execute out-of-the-box in containerized deployments.

3. **Code Quality, Duplication Reduction & Test Coverage (`acl.go`, `handlers_acl.go`, `handlers.go`, `swagger.go`, `api_test.go`)**:
   - Extracted shared group-membership SQL queries, HTTP parameter/CRUD helpers, and OpenAPI 3.0.3 schema/response builders to eliminate duplicated code blocks.
   - Added comprehensive unit and integration tests covering structured SQL URI lookups across dialects, ACL enforcement, group CRUD, telemetry filters, Kerberos SPN/config helpers, and server bootstrap validation (`84%+` overall statement coverage).

---

## Release Notes (`v1.3.1`) — Granular Pipeline & Pipeline-Group ACLs and Default-Strict Authorization

### Overview

- **Granular Pipeline & Pipeline-Group ACLs (`dbo.flow_pipeline_acl`)**: User- and group-level `allow` / `deny` rules for `create`, `read`, `update`, `delete`, `execute`, and `all` across individual pipelines, logical pipeline groups (explicit memberships + case-insensitive `name_pattern` globs such as `fin_*`), or wildcard `*`.
- **Default Strict Pipeline Authorization (`-strict-acl=true` by default)**: Non-admin (`non-"all"`) accounts have zero pipeline access by default unless granted via explicit `allow` rules in `dbo.flow_pipeline_acl` (with explicit `deny` overriding `allow`).
- **API User Groups & Pipeline Groups Management**: Added `/api/v1/groups`, `/api/v1/pipeline-groups`, and `/api/v1/acls` endpoints alongside Active Directory / Windows group resolution (`X-Flow-Windows-Groups`).
- **Effective Permissions Matrix & Simulator**: Added `GET /api/v1/permissions/effective` and `GET /api/v1/me/permissions` with admin principal inspection (`?username=...`) and simulated group evaluation (`?groups=...`).
