# Docker Quickstart Guide (`go-flowAPI`)

This guide walks through building and running `go-flowAPI` in Docker across all four included Docker Compose configurations:
1. **SQLite (Persistent Volume)** — Fastest single-container setup (`docker-compose.sqlite.yml`)
2. **PostgreSQL 16 Container** — Full stack with automated schema initialization (`docker-compose.postgres.yml`)
3. **SQL Server 2022 Container** — Full stack with automated `PROTO` DB and schema initialization (`docker-compose.sqlserver.yml`)
4. **Existing / Host Database** — Connects to an external or host database via `FLOW_API_DSN` (`docker-compose.yml`)

---

## Included Docker Files

| File | Purpose |
| :--- | :--- |
| [`Dockerfile`](./Dockerfile) | Multi-stage Alpine build (`golang:1.26-alpine` -> `alpine:3.21`) using BuildKit `additional_contexts` to include `../go-flow` |
| [`.dockerignore`](./.dockerignore) | Excludes `.git`, local `.exe` binaries, and local `.db` files from the build context |
| [`docker-compose.yml`](./docker-compose.yml) | Connects `go-flowAPI` to an existing or host database (`host.docker.internal`) |
| [`docker-compose.sqlite.yml`](./docker-compose.sqlite.yml) | Runs `go-flowAPI` with a persistent SQLite volume (`/data/flow_api.db`) and auto-schema init |
| [`docker-compose.postgres.yml`](./docker-compose.postgres.yml) | Runs PostgreSQL 16 + `go-flowAPI` with automated `dbo.*` schema initialization |
| [`docker-compose.sqlserver.yml`](./docker-compose.sqlserver.yml) | Runs SQL Server 2022 + `sqlserver-init` job + `go-flowAPI` |
| [`docker/docker-entrypoint.sh`](./docker/docker-entrypoint.sh) | Container entrypoint that auto-initializes SQLite tables when `FLOW_API_DSN` targets SQLite |
| [`docker/init/sqlite-init.sql`](./docker/init/sqlite-init.sql) | SQLite DDL script for all 7 `go-flowAPI` tables |
| [`docker/init/postgres-init.sql`](./docker/init/postgres-init.sql) | PostgreSQL DDL script (`dbo` schema + all 7 tables) |
| [`docker/init/sqlserver-init.sql`](./docker/init/sqlserver-init.sql) | SQL Server DDL script (`PROTO` database + all 7 `dbo.*` tables) |

---

## Prerequisites

1. **Docker Desktop** (or Docker Engine 23.0+ with Docker Compose v2).
2. **Sibling `go-flow` Directory (Image Build-Time Only)**: Because [`go.mod`](./go.mod) has `replace github.com/etl-madness/go-flow => ../go-flow`, Docker needs access to `../go-flow` **only while building the image** (`docker compose build` / `docker build`) so the Go compiler inside the `builder` stage can compile the static `go-flowAPI` binary. Once the image is built, the final `go-flow-api:latest` container image is completely self-contained and has **zero dependency** on `../go-flow` or any local source files.

---

## Option 1: Quickstart with SQLite (`docker-compose.sqlite.yml`)

Best for quick local testing with zero external database dependencies. The entrypoint script automatically creates all required tables in `/data/flow_api.db` and seeds the initial admin user before starting the server.

```bash
# Start in the background
docker compose -f docker-compose.sqlite.yml up -d --build

# Follow logs
docker compose -f docker-compose.sqlite.yml logs -f
```

- **API Base URL**: `http://localhost:5180`
- **Swagger UI**: `http://localhost:5180/swagger/`
- **Default Admin Credentials**: `admin` / `Admin123!` (`all` permissions)
- **Stop**: `docker compose -f docker-compose.sqlite.yml down` (add `-v` to wipe the SQLite volume)

---

## Option 2: Quickstart with PostgreSQL 16 (`docker-compose.postgres.yml`)

Spins up a `postgres:16-alpine` container, automatically executes [`docker/init/postgres-init.sql`](./docker/init/postgres-init.sql) on first boot to create the `dbo` schema and all 7 tables, waits for PostgreSQL to become healthy, and starts `go-flowAPI`.

```bash
# Start PostgreSQL + go-flowAPI
docker compose -f docker-compose.postgres.yml up -d --build

# Follow logs
docker compose -f docker-compose.postgres.yml logs -f go-flow-api
```

- **API Base URL**: `http://localhost:5180`
- **Swagger UI**: `http://localhost:5180/swagger/`
- **PostgreSQL Host Port**: `localhost:5432` (User: `flow`, Password: `flowpass`, DB: `flowdb`)
- **Default Admin Credentials**: `admin` / `Admin123!`
- **Stop**: `docker compose -f docker-compose.postgres.yml down` (add `-v` to wipe PostgreSQL data)

---

## Option 3: Quickstart with SQL Server 2022 (`docker-compose.sqlserver.yml`)

Spins up `mcr.microsoft.com/mssql/server:2022-latest`, waits for the healthcheck to pass, runs the one-shot `sqlserver-init` container to execute [`docker/init/sqlserver-init.sql`](./docker/init/sqlserver-init.sql) (creating the `PROTO` database and all 7 `dbo.*` tables), and then starts `go-flowAPI`.

```bash
# Start SQL Server + schema initializer + go-flowAPI
docker compose -f docker-compose.sqlserver.yml up -d --build

# Follow logs
docker compose -f docker-compose.sqlserver.yml logs -f
```

- **API Base URL**: `http://localhost:5180`
- **Swagger UI**: `http://localhost:5180/swagger/`
- **SQL Server Host Port**: `localhost:1433` (User: `sa`, Password: `YourStrong!Passw0rd`, DB: `PROTO`)
- **Default Admin Credentials**: `admin` / `Admin123!`
- **Stop**: `docker compose -f docker-compose.sqlserver.yml down` (add `-v` to wipe SQL Server data)

---

## Option 4: Connect to an Existing or Host Database (`docker-compose.yml`)

Use the default [`docker-compose.yml`](./docker-compose.yml) when you already have a database running on your host machine or network (ensure you have provisioned the tables from **[`SCHEMA.md`](./SCHEMA.md)** in that database first).

### PowerShell
```powershell
$env:FLOW_API_DSN = "sqlserver://flow_api:YourStrong!Passw0rd@host.docker.internal:1433?database=PROTO&trustServerCertificate=true&app+name=goflow-api-connector"

docker compose up -d --build
```

### Bash / Linux / macOS
```bash
FLOW_API_DSN="sqlserver://flow_api:YourStrong!Passw0rd@host.docker.internal:1433?database=PROTO&trustServerCertificate=true&app+name=goflow-api-connector" \
docker compose up -d --build
```

*(Note: `FLOW_API_BOOTSTRAP_USER`, `FLOW_API_BOOTSTRAP_PASS`, and `FLOW_API_BOOTSTRAP_PERM` are completely optional and default to empty in [`docker-compose.yml`](./docker-compose.yml) when connecting to a database where `dbo.flow_api_users` is already populated.)*

### Important Notes for Connecting to a Windows Host SQL Server from Docker

When running `go-flowAPI` natively on Windows (`go run .` or `.\go-flowAPI.exe`), a connection string without credentials—such as `sqlserver://T15P:1433?database=PROTO&trustServerCertificate=true`—automatically uses **Windows Integrated Authentication (SSPI)** and can even connect over local Shared Memory.

When running inside a **Linux Docker container**, keep these three requirements in mind:

1. **Use SQL Server Authentication (`username:password@`)**:
   Linux containers do not have access to your logged-in Windows SSPI token. Always include a SQL Server login and password in `FLOW_API_DSN`:
   ```text
   sqlserver://<username>:<password>@host.docker.internal:1433?database=PROTO&trustServerCertificate=true
   ```
   Ensure your SQL Server instance is configured for **SQL Server and Windows Authentication mode (Mixed Mode)** (in SSMS: right-click server -> *Properties* -> *Security* -> *SQL Server and Windows Authentication mode*, then restart SQL Server).

   To create a dedicated SQL login for `go-flowAPI` in SSMS:
   ```sql
   USE [master];
   CREATE LOGIN [flow_api] WITH PASSWORD = N'YourStrong!Passw0rd', CHECK_POLICY = OFF;
   GO
   USE [PROTO];
   CREATE USER [flow_api] FOR LOGIN [flow_api];
   ALTER ROLE [db_datareader] ADD MEMBER [flow_api];
   ALTER ROLE [db_datawriter] ADD MEMBER [flow_api];
   GO
   ```

2. **Hostname Resolution (`host.docker.internal` or `extra_hosts`)**:
   Inside a container, `localhost` refers to the container itself. Use `host.docker.internal` to reach the host machine, or map your host's computer name (e.g., `T15P:host-gateway`) under `extra_hosts` in [`docker-compose.yml`](./docker-compose.yml).

3. **Enable TCP/IP on Port `1433`**:
   Docker containers connect to the host database exclusively over TCP/IP. In **SQL Server Configuration Manager** -> **SQL Server Network Configuration** -> **Protocols for MSSQLSERVER**, verify that **TCP/IP** is **Enabled** and listening on TCP port `1433` (under *TCP/IP Properties* -> *IP Addresses* -> *IPAll*), and that Windows Firewall allows inbound connections on port `1433`.

---

## Customizing via Environment Variables or `.env`

All compose files support overriding ports, credentials, and settings via shell environment variables or a local `.env` file in `go-flowAPI/`:

```dotenv
# Port exposed on the host machine
FLOW_API_HOST_PORT=5180

# Initial BasicAuth administrator seeded at startup
FLOW_API_BOOTSTRAP_USER=admin
FLOW_API_BOOTSTRAP_PASS=ChangeMe!StrongAdminPass2026
FLOW_API_BOOTSTRAP_PERM=all

# Optional default encryption passphrase for encrypted artifacts
FLOW_SECURE_KEY=my-default-encryption-passphrase

# Verbose debug logging
FLOW_API_DEBUG=false

# Database-specific overrides (for sqlserver / postgres compose files)
MSSQL_SA_PASSWORD=YourStrong!Passw0rd
MSSQL_HOST_PORT=1433
POSTGRES_USER=flow
POSTGRES_PASSWORD=flowpass
POSTGRES_DB=flowdb
POSTGRES_HOST_PORT=5432
```

---

## Verifying Your Container Deployment

Once your chosen stack is running:

### 1. Open Interactive Swagger UI
Navigate to **[`http://localhost:5180/swagger/`](http://localhost:5180/swagger/)** in your browser:
1. Click the **"Authorize"** button in the top-right corner.
2. Enter username `admin` and password `Admin123!` (or your custom `FLOW_API_BOOTSTRAP_PASS`).
3. Click **Authorize** -> **Close**, then test any endpoint directly from the browser.

### 2. Check Health & Database Session Identity via `curl`
```bash
curl -u admin:Admin123! http://localhost:5180/api/v1/health
```

### 3. Test Database Connectivity
```bash
curl -u admin:Admin123! \
  -X POST http://localhost:5180/api/v1/db/test \
  -H "Content-Type: application/json" \
  -d '{}'
```

### 4. Upload and Execute a Pipeline
```bash
# Upload a pipeline XML file
curl -u admin:Admin123! \
  -F "file=@my_pipeline.xml" \
  "http://localhost:5180/api/v1/upload/pipelines?name=demo_pipeline"

# Execute the uploaded pipeline with node_output capture
curl -u admin:Admin123! \
  -X POST http://localhost:5180/api/v1/execute \
  -H "Content-Type: application/json" \
  -d '{
    "file": "db:demo_pipeline",
    "format": "json",
    "node_output": true,
    "log_to_db": true
  }'
```

---

## Standalone `docker build` & `docker run` (Without Compose)

If you want to build and run the image directly with the `docker` CLI, pass `--build-context go-flow=../go-flow` so BuildKit can resolve the `../go-flow` module replacement:

```bash
# Build image
docker build --build-context go-flow=../go-flow -t go-flow-api:latest .

# Run against an existing database (where tables & users are already set up)
docker run -d \
  --name go-flow-api \
  -p 5180:5180 \
  --add-host=host.docker.internal:host-gateway \
  -e FLOW_API_DSN="sqlserver://sa:YourStrong!Passw0rd@host.docker.internal:1433?database=PROTO&trustServerCertificate=true" \
  go-flow-api:latest

# Or run with a fresh SQLite persistent volume (and optional initial user bootstrap)
docker run -d \
  --name go-flow-api-sqlite \
  -p 5180:5180 \
  -e FLOW_API_DSN="sqlite:///data/flow_api.db" \
  -e FLOW_API_BOOTSTRAP_USER="admin" \
  -e FLOW_API_BOOTSTRAP_PASS="Admin123!" \
  -v flow_api_sqlite:/data \
  go-flow-api:latest
```
