---
title: go-flowAPI Docker & Container Deployment Guide
description: Detailed Docker, BuildKit, and Docker Compose deployment instructions for go-flowAPI across SQLite, PostgreSQL, SQL Server, and External Databases
---

# `go-flowAPI` — Detailed Docker & Container Deployment Guide

> **Production & Quickstart Container Guide for `go-flowAPI`**  
> **Container Image:** `go-flow-api:latest` (Multi-stage `golang:1.26-alpine` → `alpine:3.21`)  
> **Default Container Port:** `5180` | **Volume Mount:** `/data`

This guide covers building and deploying **`go-flowAPI`** in Docker using **Docker Compose** (four ready-to-run stacks) or standalone **`docker build` / `docker run`** commands, including multi-architecture builds (`linux/amd64` and `linux/arm64`), automated schema initialization, and host database connectivity.

---

## Table of Contents

1. [Multi-Stage Docker Build Architecture](#1-multi-stage-docker-build-architecture)
2. [Included Docker & Initialization Files](#2-included-docker--initialization-files)
3. [Overview of the 4 Docker Compose Stacks](#3-overview-of-the-4-docker-compose-stacks)
4. [Stack 1: SQLite with Persistent Volume (`docker-compose.sqlite.yml`)](#4-stack-1-sqlite-with-persistent-volume-docker-composesqliteyml)
5. [Stack 2: PostgreSQL 16 Container (`docker-compose.postgres.yml`)](#5-stack-2-postgresql-16-container-docker-composepostgresyml)
6. [Stack 3: SQL Server 2022 Container (`docker-compose.sqlserver.yml`)](#6-stack-3-sql-server-2022-container-docker-composesqlserveryml)
7. [Stack 4: Existing or Host Database (`docker-compose.yml`)](#7-stack-4-existing-or-host-database-docker-composeyml)
8. [Connecting from a Linux Container to a Windows Host SQL Server](#8-connecting-from-a-linux-container-to-a-windows-host-sql-server)
9. [Standalone `docker build`, `docker buildx` & `docker run`](#9-standalone-docker-build-docker-buildx--docker-run)
10. [Environment Variables & `.env` Reference](#10-environment-variables--env-reference)
11. [Verifying & Troubleshooting Your Container Deployment](#11-verifying--troubleshooting-your-container-deployment)

---

## 1. Multi-Stage Docker Build Architecture

The [`Dockerfile`](../Dockerfile) uses a clean two-stage build so the `builder` stage (`golang:1.26-alpine`) compiles a 100% static Linux binary (`CGO_ENABLED=0`) directly from `go.mod` and copies it into a minimal `alpine:3.21` runtime image with zero external host directory dependencies.

```mermaid
flowchart LR
    subgraph Contexts["Build Context"]
        CTX_MAIN["Primary Context (.)<br/>go-flowAPI/ source &amp; docker/ scripts"]
    end

    subgraph Stage1["Stage 1: Builder (golang:1.26-alpine)"]
        MOD["COPY go.mod go.sum<br/>go mod download"]
        BUILD["CGO_ENABLED=0 GOOS=linux<br/>go build -trimpath -ldflags='-s -w'<br/>/out/go-flowAPI"]
        MOD --> BUILD
    end

    subgraph Stage2["Stage 2: Runtime (alpine:3.21)"]
        BIN["/app/go-flowAPI<br/>(Static Executable)"]
        INIT["/app/docker/init/*.sql<br/>(SQLite, Postgres, SQL Server DDLs)"]
        ENTRY["/usr/local/bin/docker-entrypoint.sh<br/>(Auto-init SQLite + exec go-flowAPI)"]
        VOL[("Volume: /data<br/>Port: 5180")]
    end

    CTX_MAIN --> MOD
    BUILD --> BIN
    CTX_MAIN --> INIT & ENTRY
    ENTRY --> BIN
    BIN --> VOL
```

---

## 2. Included Docker & Initialization Files

| File Path | Role & Description |
| :--- | :--- |
| `Dockerfile` | Multi-stage Alpine build (`golang:1.26-alpine` → `alpine:3.21`) exposing port `5180` and `/data` volume. |
| `.dockerignore` | Excludes `.git`, local `.exe` binaries, test `.db` files, and `.private` configs from the build context. |
| `docker-compose.yml` | Connects `go-flowAPI` to an external or host database via `FLOW_API_DSN` (`host.docker.internal`). |
| `docker-compose.sqlite.yml` | Single-container deployment with a persistent SQLite volume (`/data/flow_api.db`) and auto-schema provisioning. |
| `docker-compose.postgres.yml` | Multi-container stack: `postgres:16-alpine` + automated `dbo.*` schema init + `go-flowAPI`. |
| `docker-compose.sqlserver.yml` | Multi-container stack: `mssql/server:2022-latest` + one-shot `sqlserver-init` schema runner + `go-flowAPI`. |
| `docker/docker-entrypoint.sh` | Entrypoint script that normalizes SQLite DSN paths, automatically creates tables via `sqlite3`, and starts `go-flowAPI`. |
| `docker/init/sqlite-init.sql` | Idempotent SQLite DDL script for all 12 `go-flowAPI` tables (`CREATE TABLE IF NOT EXISTS`). |
| `docker/init/postgres-init.sql` | PostgreSQL initialization script creating the `dbo` schema and all 12 metadata, ACL/group, and telemetry tables. |
| `docker/init/sqlserver-init.sql` | SQL Server initialization script creating the target database and all 12 `dbo.*` tables. |

---

## 3. Overview of the 4 Docker Compose Stacks

```mermaid
flowchart TD
    subgraph Stack1["Option 1: docker-compose.sqlite.yml"]
        S1_API["go-flow-api-sqlite (:5180)"] --> S1_VOL[("Docker Volume: sqlite_data<br/>/data/flow_api.db")]
    end

    subgraph Stack2["Option 2: docker-compose.postgres.yml"]
        S2_API["go-flow-api-postgres (:5180)"] -->|"postgres://flow:flowpass@postgres:5432/flowdb"| S2_PG["go-flow-postgres (:5432)<br/>postgres:16-alpine"]
        S2_PG --> S2_VOL[("Docker Volume: postgres_data")]
    end

    subgraph Stack3["Option 3: docker-compose.sqlserver.yml"]
        S3_INIT["go-flow-sqlserver-init<br/>(Runs sqlserver-init.sql)"] -->|"sqlcmd DDL init"| S3_MS["go-flow-sqlserver (:1433)<br/>mssql/server:2022-latest"]
        S3_API["go-flow-api-sqlserver (:5180)"] -->|"Depends on sqlserver-init"| S3_MS
        S3_MS --> S3_VOL[("Docker Volume: sqlserver_data")]
    end

    subgraph Stack4["Option 4: docker-compose.yml (External / Host DB)"]
        S4_API["go-flow-api (:5180)"] -->|"FLOW_API_DSN<br/>(host.docker.internal)"| S4_EXT[("Existing Host or Network DB<br/>SQL Server / Postgres / MySQL / Oracle")]
    end
```

---

## 4. Stack 1: SQLite with Persistent Volume (`docker-compose.sqlite.yml`)

Ideal for rapid local evaluation, edge deployments, or single-node runners with zero external database dependencies. On container startup, `docker-entrypoint.sh` detects the `sqlite://` DSN, executes `/app/docker/init/sqlite-init.sql` against `/data/flow_api.db`, and seeds the bootstrap admin account.

```bash
# Build and launch in detached mode
docker compose -f docker-compose.sqlite.yml up -d --build

# View startup logs
docker compose -f docker-compose.sqlite.yml logs -f

# Stop container (preserve SQLite data volume)
docker compose -f docker-compose.sqlite.yml down

# Stop container and wipe SQLite volume
docker compose -f docker-compose.sqlite.yml down -v
```

- **API Endpoint**: `http://localhost:5180`
- **Swagger UI**: `http://localhost:5180/swagger/`
- **Default Credentials**: `admin` / `Admin123!` (`permission = "all"`)

---

## 5. Stack 2: PostgreSQL 16 Container (`docker-compose.postgres.yml`)

Provisions a dedicated `postgres:16-alpine` instance, mounts `docker/init/postgres-init.sql` into `/docker-entrypoint-initdb.d/`, waits for the `pg_isready` healthcheck to pass, and starts `go-flowAPI`.

```bash
# Start PostgreSQL 16 + go-flowAPI
docker compose -f docker-compose.postgres.yml up -d --build

# Follow go-flowAPI container logs
docker compose -f docker-compose.postgres.yml logs -f go-flow-api

# Stop stack (add -v to remove postgres_data volume)
docker compose -f docker-compose.postgres.yml down
```

- **API Endpoint**: `http://localhost:5180`
- **PostgreSQL Host Port**: `localhost:5432` (`user=flow`, `password=flowpass`, `dbname=flowdb`)
- **Default Credentials**: `admin` / `Admin123!`

---

## 6. Stack 3: SQL Server 2022 Container (`docker-compose.sqlserver.yml`)

Starts Microsoft SQL Server 2022 (`mcr.microsoft.com/mssql/server:2022-latest`), waits for the `sqlcmd` healthcheck to succeed, executes a one-shot `sqlserver-init` container to create the database and all 12 `dbo.*` tables, and then launches `go-flowAPI`.

```bash
# Start SQL Server 2022 + schema initializer + go-flowAPI
docker compose -f docker-compose.sqlserver.yml up -d --build

# Follow logs across all 3 services
docker compose -f docker-compose.sqlserver.yml logs -f

# Stop stack (add -v to remove sqlserver_data volume)
docker compose -f docker-compose.sqlserver.yml down
```

- **API Endpoint**: `http://localhost:5180`
- **SQL Server Host Port**: `localhost:1433` (`user=sa`, `password=YourStrong!Passw0rd`)
- **Default Credentials**: `admin` / `Admin123!`

---

## 7. Stack 4: Existing or Host Database (`docker-compose.yml`)

Use [`docker-compose.yml`](../docker-compose.yml) when connecting to an existing enterprise database (SQL Server, Azure SQL, PostgreSQL, MySQL/MariaDB, or Oracle) where the schema from `SCHEMA.md` has already been created.

### PowerShell (Windows)
```powershell
$env:FLOW_API_DSN = "sqlserver://flow_api:YourStrong!Passw0rd@host.docker.internal:1433?database=EXAMPLE_DB&trustServerCertificate=true&app+name=goflow-api-connector"

docker compose up -d --build
```

### Bash / Zsh (Linux / macOS / FreeBSD)
```bash
FLOW_API_DSN="sqlserver://flow_api:YourStrong!Passw0rd@host.docker.internal:1433?database=EXAMPLE_DB&trustServerCertificate=true&app+name=goflow-api-connector" \
docker compose up -d --build
```

> [!NOTE]
> When `dbo.flow_api_users` is already populated in your database, `FLOW_API_BOOTSTRAP_USER`, `FLOW_API_BOOTSTRAP_PASS`, and `FLOW_API_BOOTSTRAP_PERM` are optional and can be omitted.

---

## 8. Connecting from a Linux Container to a Windows Host SQL Server

When running `go-flowAPI.exe` natively on Windows, a connection string without credentials (e.g., `sqlserver://db.example.com:1433?database=EXAMPLE_DB&trustServerCertificate=true`) uses **Windows Integrated Authentication (SSPI)**. Inside a Linux Docker container, connections cross the container network boundary to the Windows host:

```mermaid
flowchart LR
    subgraph Container["Linux Docker Container (go-flow-api)"]
        APP["/app/go-flowAPI<br/>Pure-Go go-mssqldb driver"]
    end

    subgraph Bridge["Docker Host Gateway"]
        DNS["host.docker.internal<br/>(extra_hosts -> host-gateway)"]
    end

    subgraph WinHost["Windows Host Machine"]
        FW["Windows Firewall<br/>Inbound TCP Port 1433"]
        MSSQL["SQL Server Instance<br/>Mixed Mode Auth + TCP/IP Enabled"]
    end

    APP -->|"sqlserver://flow_api:Pass@host.docker.internal:1433"| DNS
    DNS --> FW --> MSSQL
```

### Checklist for Windows Host SQL Server Connectivity:
1. **Use SQL Server Authentication (`username:password@`)**:
   For SQL Server authentication, ensure SQL Server is configured for **SQL Server and Windows Authentication mode (Mixed Mode)** and supply explicit credentials in `FLOW_API_DSN`:
   ```sql
   USE [master];
   CREATE LOGIN [flow_api] WITH PASSWORD = N'YourStrong!Passw0rd', CHECK_POLICY = OFF;
   GO
   USE [EXAMPLE_DB];
   CREATE USER [flow_api] FOR LOGIN [flow_api];
   ALTER ROLE [db_datareader] ADD MEMBER [flow_api];
   ALTER ROLE [db_datawriter] ADD MEMBER [flow_api];
   GO
   ```
   For Windows/AD authentication instead, use the [Kerberos sidecar guide](./AD_KERBEROS_SIDECAR_GUIDE.md); Linux containers do not automatically inherit the Windows host's login.
2. **Use `host.docker.internal` (or `extra_hosts`)**:
   Inside the container, `localhost` refers to the container itself. Use `host.docker.internal` to reach the host machine. If your pipelines stored in the database reference your Windows host's machine name (e.g., `db.example.com`), map that hostname under `extra_hosts` in `docker-compose.yml`:
   ```yaml
   extra_hosts:
     - "host.docker.internal:host-gateway"
     - "db.example.com:host-gateway"
   ```
3. **Enable TCP/IP on Port `1433`**:
   In **SQL Server Configuration Manager** → **SQL Server Network Configuration** → **Protocols for MSSQLSERVER**, ensure **TCP/IP** is **Enabled** on port `1433`.

---

## 9. Standalone `docker build`, `docker buildx` & `docker run`

If you prefer using the `docker` CLI directly without Docker Compose, build the image from the `go-flowAPI` root directory:

### 9.1 Build a Single-Architecture Image
```bash
docker build -t go-flow-api:latest .
```

### 9.2 Build a Multi-Architecture Image (`linux/amd64` + `linux/arm64`)
```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t myregistry.example.com/etl-madness/go-flow-api:latest \
  --push .
```

### 9.3 Run the Container with `docker run`

#### A. Against an External / Host SQL Server
```bash
docker run -d \
  --name go-flow-api \
  --restart unless-stopped \
  -p 5180:5180 \
  --add-host=host.docker.internal:host-gateway \
  -e FLOW_API_DSN="sqlserver://flow_api:YourStrong!Passw0rd@host.docker.internal:1433?database=EXAMPLE_DB&trustServerCertificate=true" \
  go-flow-api:latest
```

#### B. Self-Contained SQLite Mode with Persistent Volume
```bash
docker run -d \
  --name go-flow-api-sqlite \
  --restart unless-stopped \
  -p 5180:5180 \
  -e FLOW_API_DSN="sqlite:///data/flow_api.db" \
  -e FLOW_API_BOOTSTRAP_USER="admin" \
  -e FLOW_API_BOOTSTRAP_PASS="Admin123!" \
  -e FLOW_API_BOOTSTRAP_PERM="all" \
  -v flow_api_sqlite:/data \
  go-flow-api:latest
```

---

## 10. Environment Variables & `.env` Reference

All Docker and Docker Compose deployments can be configured via environment variables or a `.env` file in the `go-flowAPI` directory:

| Environment Variable | CLI Flag Equivalent | Default in Container | Description |
| :--- | :--- | :--- | :--- |
| `FLOW_API_DSN` | `-dsn` | `StartupConnectionString` | Primary database connection string (`sqlserver://`, `postgres://`, `mysql://`, `sqlite://`, `oracle://`). |
| `FLOW_API_ADDR` | `-addr` | `:5180` | HTTP listen address inside the container. |
| `PORT` | `-addr` | `5180` | Fallback port if `FLOW_API_ADDR` is not set. |
| `FLOW_API_HOST_PORT` | *(Docker Compose)* | `5180` | Host port mapped to container port `5180`. |
| `FLOW_API_BOOTSTRAP_USER` | `-bootstrap-user` | `""` (`admin` in quickstarts) | Optional initial BasicAuth username to seed/upsert at startup. |
| `FLOW_API_BOOTSTRAP_PASS` | `-bootstrap-pass` | `""` (`Admin123!` in quickstarts) | Password for `FLOW_API_BOOTSTRAP_USER` (automatically bcrypt-hashed). |
| `FLOW_API_BOOTSTRAP_PERM` | `-bootstrap-perm` | `all` | Permission scope for bootstrap user (`all`, `scoped`, `execution-only`, `read-only`, `write`, `upload`). |
| `FLOW_API_STRICT_ACL` | `-strict-acl` | `true` | Enforce strict ACL-only pipeline authorization so non-`all` users require explicit `dbo.flow_pipeline_acl` grants (`false` opts out). |
| `FLOW_SECURE_KEY` | `-secure-key` | `""` | Default AES-256-GCM passphrase for encrypting/decrypting artifacts (`FLOWENC:v1:`). |
| `FLOW_API_DEBUG` | `-debug` | `false` | Enables verbose debug logging (`true` or `1`). |
| `HOST_USERDOMAIN` | — | `""` | Optional Windows domain passed from host for Windows group/user identity fallback. |
| `HOST_USERNAME` | — | `""` | Optional Windows username passed from host. |

---

## 11. Verifying & Troubleshooting Your Container Deployment

### 1. Public Health Check (No BasicAuth Required)
```bash
curl http://localhost:5180/api/v1/health
```

### 2. Authenticated Health Check (Confirms User & Permission Scope)
```bash
curl -u admin:Admin123! http://localhost:5180/api/v1/health
```

### 3. Interactive Swagger UI
Open **`http://localhost:5180/swagger/`** (or `http://localhost:5180/docs`) in your browser and click **Authorize** to test any endpoint interactively.

### 4. Inspect Container Logs & Health
```bash
docker ps --filter "name=go-flow"
docker logs --tail 100 -f go-flow-api
```

---

## Related Documentation

- [**`go-flowAPI` Documentation Hub**](index.md)
- [**Cross-Platform Compilation & Build Guide**](COMPILATION_AND_BUILD_GUIDE.md)
- [**Complete API Endpoints & RBAC Reference**](API_ENDPOINTS_REFERENCE.md)
