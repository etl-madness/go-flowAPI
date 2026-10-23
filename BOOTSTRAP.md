# Bootstrapping `go-flowAPI` & Adding an Initial Admin User (`all` Permissions)

Because every `/api/v1/*` endpoint in `go-flowAPI` is protected by HTTP Basic Authentication against [`TableAPIUsers`](./tables.go) (`dbo.flow_api_users`), you need at least one initial user with **`all` permissions** before you can manage users or administer the API over HTTP.

This guide covers all supported ways to provision that initial `all`-permission user across **SQL Server / Azure SQL**, **PostgreSQL**, **MySQL / MariaDB**, **SQLite**, and **Oracle**.

---

## Prerequisite: Ensure `flow_api_users` Table Exists

Before adding a user, make sure the required database tables from **[`SCHEMA.md`](./SCHEMA.md)** have been created in your target database (at minimum, `flow_api_users` and `flow_api_events`).

> **Note on Schema Prefix (`tables.go`)**:
> - By default, `DefaultSchema = "dbo."` in [`tables.go`](./tables.go).
> - On **SQL Server**, **PostgreSQL**, and **Oracle**, the table is referenced as `dbo.flow_api_users` (unless you customize `DefaultSchema`).
> - On **SQLite** and **MySQL**, the `dbo.` prefix is automatically stripped by `resolveTableName`, so the table is referenced as `flow_api_users`.

---

## Method 1: Bootstrap via CLI Flags or Environment Variables (Recommended)

`go-flowAPI` includes built-in startup flags (`-bootstrap-user`, `-bootstrap-pass`, `-bootstrap-perm`) that automatically **bcrypt-hash** the password and upsert the initial user into `TableAPIUsers` at server startup.

### Option A: Command-Line Flags

```bash
go run . \
  -dsn "sqlserver://sa:Password123!@localhost:1433?database=master&trustServerCertificate=true" \
  -addr ":8080" \
  -bootstrap-user "admin" \
  -bootstrap-pass "ChangeMe!StrongAdminPass2026" \
  -bootstrap-perm "all"
```

*(Note: `-bootstrap-perm` defaults to `"all"`, so you can omit `-bootstrap-perm` when creating a full administrator.)*

### Option B: Environment Variables

```bash
export FLOW_API_DSN="sqlserver://sa:Password123!@localhost:1433?database=master&trustServerCertificate=true"
export FLOW_API_ADDR=":8080"
export FLOW_API_BOOTSTRAP_USER="admin"
export FLOW_API_BOOTSTRAP_PASS="ChangeMe!StrongAdminPass2026"
export FLOW_API_BOOTSTRAP_PERM="all"

go run .
```

**PowerShell:**

```powershell
$env:FLOW_API_DSN = "sqlserver://sa:Password123!@localhost:1433?database=master&trustServerCertificate=true"
$env:FLOW_API_BOOTSTRAP_USER = "admin"
$env:FLOW_API_BOOTSTRAP_PASS = "ChangeMe!StrongAdminPass2026"
go run . -addr :8080
```

When the server starts, you will see:
```text
[go-flowAPI] bootstrapped API user "admin" with permission="all" in dbo.flow_api_users
[go-flowAPI] listening on :8080 (dialect=sqlserver, defaultSchema="dbo.", swagger=http://localhost:8080/swagger/)
```

---

## Method 2: Bootstrap Directly via SQL (`INSERT` / `MERGE`)

If your database administrator provisions users directly in SQL before starting `go-flowAPI`, you can insert a row into `flow_api_users` with `permission = 'all'` and `is_active = 1` (or `TRUE`).

### Step 2a: Choose a Password Hash Format

`VerifyPassword` in [`auth.go`](./auth.go) supports three formats in the `password_hash` column:

1. **Bcrypt Hash (`$2a$...` or `$2b$...`) — Recommended for Production**
   Generate a bcrypt hash directly using `go-flowAPI`:
   ```bash
   go run . -hash-password "ChangeMe!StrongAdminPass2026"
   # Output example:
   # $2a$10$w8rV7yN2X...exampleBcryptHashString...
   ```
2. **SHA-256 Hex Digest (`sha256:<hex>`)**
   Prefix a lowercase 64-character SHA-256 hex digest with `sha256:`:
   - **Bash / Linux / macOS**:
     ```bash
     printf "sha256:%s\n" "$(printf '%s' 'ChangeMe!StrongAdminPass2026' | sha256sum | awk '{print $1}')"
     ```
   - **PowerShell**:
     ```powershell
     $bytes = [System.Text.Encoding]::UTF8.GetBytes("ChangeMe!StrongAdminPass2026")
     $hash  = [System.BitConverter]::ToString([System.Security.Cryptography.SHA256]::Create().ComputeHash($bytes)).Replace("-","").ToLower()
     "sha256:$hash"
     ```
3. **Plaintext (Quick Dev/Test Bootstrap Only)**
   If `password_hash` does not start with `$2a$`/`$2b$`/`$2y$` or `sha256:`, `go-flowAPI` compares it using a constant-time SHA-256 digest comparison. You can insert a plaintext password initially and immediately rotate it to bcrypt by calling `POST /api/v1/users`.

---

### Step 2b: Run the SQL for Your Database Dialect

Replace `<PASSWORD_OR_HASH>` below with the bcrypt hash from `go run . -hash-password "..."` (or `sha256:...` / initial password).

#### 1. Microsoft SQL Server / Azure SQL (`sqlserver`)
```sql
MERGE INTO dbo.flow_api_users AS target
USING (SELECT N'admin' AS username) AS source
ON (target.username = source.username)
WHEN MATCHED THEN
    UPDATE SET
        password_hash = N'<PASSWORD_OR_HASH>',
        permission    = N'all',
        is_active     = 1,
        description   = N'Initial administrator account with all permissions',
        updated_at    = SYSUTCDATETIME()
WHEN NOT MATCHED THEN
    INSERT (username, password_hash, permission, is_active, description)
    VALUES (N'admin', N'<PASSWORD_OR_HASH>', N'all', 1, N'Initial administrator account with all permissions');
```

#### 2. PostgreSQL (`postgres`)
```sql
INSERT INTO dbo.flow_api_users (username, password_hash, permission, is_active, description)
VALUES ('admin', '<PASSWORD_OR_HASH>', 'all', TRUE, 'Initial administrator account with all permissions')
ON CONFLICT (username) DO UPDATE SET
    password_hash = EXCLUDED.password_hash,
    permission    = 'all',
    is_active     = TRUE,
    description   = EXCLUDED.description,
    updated_at    = CURRENT_TIMESTAMP;
```

#### 3. MySQL / MariaDB (`mysql`)
```sql
INSERT INTO flow_api_users (username, password_hash, permission, is_active, description)
VALUES ('admin', '<PASSWORD_OR_HASH>', 'all', 1, 'Initial administrator account with all permissions')
ON DUPLICATE KEY UPDATE
    password_hash = VALUES(password_hash),
    permission    = 'all',
    is_active     = 1,
    description   = VALUES(description),
    updated_at    = CURRENT_TIMESTAMP(6);
```

#### 4. SQLite (`sqlite`)
```sql
INSERT INTO flow_api_users (username, password_hash, permission, is_active, description)
VALUES ('admin', '<PASSWORD_OR_HASH>', 'all', 1, 'Initial administrator account with all permissions')
ON CONFLICT(username) DO UPDATE SET
    password_hash = excluded.password_hash,
    permission    = 'all',
    is_active     = 1,
    description   = excluded.description,
    updated_at    = CURRENT_TIMESTAMP;
```

#### 5. Oracle (`oracle`)
```sql
MERGE INTO dbo.flow_api_users target
USING (SELECT 'admin' AS username FROM DUAL) source
ON (target.username = source.username)
WHEN MATCHED THEN
    UPDATE SET
        target.password_hash = '<PASSWORD_OR_HASH>',
        target.permission    = 'all',
        target.is_active     = 1,
        target.description   = 'Initial administrator account with all permissions',
        target.updated_at    = SYSTIMESTAMP
WHEN NOT MATCHED THEN
    INSERT (username, password_hash, permission, is_active, description)
    VALUES ('admin', '<PASSWORD_OR_HASH>', 'all', 1, 'Initial administrator account with all permissions');
```

---

## Step 3: Verify via Swagger UI or CLI

### 1. Using Interactive Swagger UI
1. Open **`http://localhost:8080/swagger/`** (or `http://localhost:8080/docs`) in your browser.
2. Click the **"Authorize"** button in the top-right corner.
3. Enter your bootstrap `username` (`admin`) and `password`, then click **Authorize**.
4. Expand **`GET /api/v1/health`** or **`GET /api/v1/users`**, click **Try it out**, and click **Execute**.

### 2. Using `curl`
```bash
# Verify health and confirm permission="all"
curl -u "admin:ChangeMe!StrongAdminPass2026" \
  http://localhost:8080/api/v1/health
```

Expected response:
```json
{
  "application_name": "go-flowAPI",
  "authenticated_as": "admin",
  "db_spid": 64,
  "db_user": "sa",
  "dialect": "sqlserver",
  "permission": "all",
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

## Step 4: Provision Scoped Users via `POST /api/v1/users`

Once your initial `all`-permission user is active, you can create least-privilege accounts over the API (passwords are automatically bcrypt-hashed by `POST /api/v1/users`):

```bash
# 1. Create an execution-only service account
curl -u "admin:ChangeMe!StrongAdminPass2026" \
  -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -d '{
    "username": "runner_svc",
    "password": "RunnerPass!2026",
    "permission": "execution-only",
    "description": "CI/CD pipeline runner service account",
    "is_active": true
  }'

# 2. Create a read-only auditor account
curl -u "admin:ChangeMe!StrongAdminPass2026" \
  -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -d '{
    "username": "auditor",
    "password": "AuditorPass!2026",
    "permission": "read-only",
    "description": "Read-only telemetry and artifact viewer",
    "is_active": true
  }'

# 3. Create an artifact uploader/writer account
curl -u "admin:ChangeMe!StrongAdminPass2026" \
  -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -d '{
    "username": "publisher",
    "password": "PublisherPass!2026",
    "permission": "upload",
    "description": "Pipeline XML uploader account",
    "is_active": true
  }'
```

### Summary of Permission Scopes

| Permission | Allowed Endpoints |
| :--- | :--- |
| **`all`** | Full access to **all** endpoints: Artifact CRUD, File Upload, Pipeline Execution/Validation, Telemetry/Audit Logs, Database Connectivity Test (`POST /api/v1/databases/test`), and User Management (`GET/POST/DELETE /api/v1/users`). |
| **`execution-only`** | `POST /api/v1/execute`, `POST /api/v1/execute/stream`, `POST /api/v1/validate`, plus all `read-only` operations (`GET /api/v1/artifacts/{type}...`, `GET /api/v1/runs`, `GET /api/v1/events`, `GET /api/v1/audit`) |
| **`read-only`** | `GET /api/v1/artifacts/{type}...`, `GET /api/v1/runs`, `GET /api/v1/events`, `GET /api/v1/audit` |
| **`write`** / **`upload`** | All `read-only` artifact operations plus `POST /api/v1/upload/{type}`, `POST /api/v1/artifacts/{type}/upload`, `POST/PUT/DELETE /api/v1/artifacts/{type}...` |
| **Combined** | Comma-separated combinations such as `"read-only,execution-only"` or `"upload,execution-only"` are also supported. |
