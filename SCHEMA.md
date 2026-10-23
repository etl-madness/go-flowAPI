# Database Schema Reference (`go-flowAPI`)

`go-flowAPI` does **not** auto-migrate tables at startup. Provision the required tables in your target database using the DDL scripts below before starting the API.

## Adjustable Schema & Table Constants (`tables.go`)

All table and schema paths are controlled via constants in [`tables.go`](./tables.go):

| Constant | Default Value | Description |
| :--- | :--- | :--- |
| `DefaultSchema` | `"dbo."` | Default schema prefix (adjusted or stripped automatically for SQLite/MySQL/Oracle) |
| `TablePipelineContent` | `DefaultSchema + "flow_pipeline_content"` | Stores pipeline XML definitions (`Name`, `Description`, `PipelineXML`) |
| `TableOptionsContent` | `DefaultSchema + "flow_options_content"` | Stores reusable CLI/execution option XML profiles (`Name`, `Description`, `OptionsXML`) |
| `TableConfigContent` | `DefaultSchema + "flow_config_content"` | Stores reusable `<Configuration>` XML profiles (`Name`, `Description`, `ConfigXML`) |
| `TablePipelineRuns` | `DefaultSchema + "pipeline_runs"` | Stores high-level pipeline execution run summaries |
| `TablePipelineEvents` | `DefaultSchema + "pipeline_events"` | Stores per-node execution events and `node_output` |
| `TableAPIAuditEvents` | `DefaultSchema + "flow_api_events"` | Stores API-level audit logs for all CRUD, upload, auth, and execution calls |
| `TableAPIUsers` | `DefaultSchema + "flow_api_users"` | Stores BasicAuth credentials and role permissions (`execution-only`, `read-only`, `write`/`upload`, `all`) |

---

## 1. Microsoft SQL Server / Azure SQL (`sqlserver`)

```sql
-- 1. Pipeline Content Table (compatible with go-flow & db_importer)
IF OBJECT_ID('dbo.flow_pipeline_content', 'U') IS NULL
CREATE TABLE dbo.flow_pipeline_content (
    ID          INT IDENTITY(1,1) PRIMARY KEY,
    Name        NVARCHAR(255) NOT NULL UNIQUE,
    Description NVARCHAR(MAX) NULL,
    PipelineXML NVARCHAR(MAX) NULL,
    CreatedAt   DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME(),
    UpdatedAt   DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME()
);

-- 2. Options Content Table (compatible with go-flow & db_importer)
IF OBJECT_ID('dbo.flow_options_content', 'U') IS NULL
CREATE TABLE dbo.flow_options_content (
    ID          INT IDENTITY(1,1) PRIMARY KEY,
    Name        NVARCHAR(255) NOT NULL UNIQUE,
    Description NVARCHAR(MAX) NULL,
    OptionsXML  NVARCHAR(MAX) NULL,
    CreatedAt   DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME(),
    UpdatedAt   DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME()
);

-- 3. Config Content Table
IF OBJECT_ID('dbo.flow_config_content', 'U') IS NULL
CREATE TABLE dbo.flow_config_content (
    ID          INT IDENTITY(1,1) PRIMARY KEY,
    Name        NVARCHAR(255) NOT NULL UNIQUE,
    Description NVARCHAR(MAX) NULL,
    ConfigXML   NVARCHAR(MAX) NULL,
    CreatedAt   DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME(),
    UpdatedAt   DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME()
);

-- 4. Pipeline Runs Summary Table (compatible with go-flow observability)
IF OBJECT_ID('dbo.pipeline_runs', 'U') IS NULL
CREATE TABLE dbo.pipeline_runs (
    run_id           NVARCHAR(64)  NOT NULL PRIMARY KEY,
    file_path        NVARCHAR(512) NULL,
    config_path      NVARCHAR(512) NULL,
    status           NVARCHAR(32)  NOT NULL,
    started_at       DATETIME2     NOT NULL,
    finished_at      DATETIME2     NOT NULL,
    duration_ms      BIGINT        NOT NULL,
    task_count       INT           NOT NULL,
    user_name        NVARCHAR(255) NULL,
    os_user_name     NVARCHAR(255) NULL,
    db_user_name     NVARCHAR(255) NULL,
    spid             BIGINT        NULL,
    application_name NVARCHAR(255) NULL,
    hostname         NVARCHAR(255) NULL,
    options_path     NVARCHAR(512) NULL,
    error_class      NVARCHAR(128) NULL,
    error_message    NVARCHAR(MAX) NULL
);

-- 5. Pipeline Events Telemetry Table (compatible with go-flow observability + node_output)
IF OBJECT_ID('dbo.pipeline_events', 'U') IS NULL
CREATE TABLE dbo.pipeline_events (
    id               BIGINT IDENTITY(1,1) PRIMARY KEY,
    run_id           NVARCHAR(64)  NOT NULL,
    execution_id     NVARCHAR(64)  NULL,
    sequence_num     BIGINT        NOT NULL,
    occurred_at      DATETIME2     NOT NULL,
    event_type       NVARCHAR(64)  NOT NULL,
    node_kind        NVARCHAR(64)  NULL,
    node_id          NVARCHAR(255) NULL,
    status           NVARCHAR(64)  NULL,
    user_name        NVARCHAR(255) NULL,
    os_user_name     NVARCHAR(255) NULL,
    db_user_name     NVARCHAR(255) NULL,
    spid             NVARCHAR(64)  NULL,
    application_name NVARCHAR(255) NULL,
    hostname         NVARCHAR(255) NULL,
    options_path     NVARCHAR(512) NULL,
    error_message    NVARCHAR(MAX) NULL,
    rows_read        BIGINT        NULL,
    rows_written     BIGINT        NULL,
    rows_affected    BIGINT        NULL,
    node_output      NVARCHAR(MAX) NULL
);

-- 6. API Audit Events Table
IF OBJECT_ID('dbo.flow_api_events', 'U') IS NULL
CREATE TABLE dbo.flow_api_events (
    event_id      NVARCHAR(64)  NOT NULL PRIMARY KEY,
    occurred_at   DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME(),
    event_type    NVARCHAR(128) NOT NULL,
    http_method   NVARCHAR(16)  NOT NULL,
    path          NVARCHAR(512) NOT NULL,
    artifact_type NVARCHAR(64)  NULL,
    artifact_name NVARCHAR(255) NULL,
    run_id        NVARCHAR(64)  NULL,
    auth_user     NVARCHAR(255) NULL,
    remote_addr   NVARCHAR(128) NULL,
    status_code   INT           NOT NULL,
    status        NVARCHAR(32)  NOT NULL,
    duration_ms   BIGINT        NOT NULL,
    error_message NVARCHAR(MAX) NULL,
    details_json  NVARCHAR(MAX) NULL
);

-- 7. API BasicAuth Users & Permissions Table
IF OBJECT_ID('dbo.flow_api_users', 'U') IS NULL
CREATE TABLE dbo.flow_api_users (
    username      NVARCHAR(255) NOT NULL PRIMARY KEY,
    password_hash NVARCHAR(512) NOT NULL,
    permission    NVARCHAR(128) NOT NULL, -- 'execution-only', 'read-only', 'write', 'upload', 'all' (or comma-separated)
    is_active     BIT           NOT NULL DEFAULT 1,
    description   NVARCHAR(512) NULL,
    created_at    DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at    DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME()
);
```

---

## 2. PostgreSQL (`postgres`)

```sql
CREATE SCHEMA IF NOT EXISTS dbo;

CREATE TABLE IF NOT EXISTS dbo.flow_pipeline_content (
    ID          SERIAL       PRIMARY KEY,
    Name        VARCHAR(255) NOT NULL UNIQUE,
    Description TEXT,
    PipelineXML TEXT,
    CreatedAt   TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UpdatedAt   TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS dbo.flow_options_content (
    ID          SERIAL       PRIMARY KEY,
    Name        VARCHAR(255) NOT NULL UNIQUE,
    Description TEXT,
    OptionsXML  TEXT,
    CreatedAt   TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UpdatedAt   TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS dbo.flow_config_content (
    ID          SERIAL       PRIMARY KEY,
    Name        VARCHAR(255) NOT NULL UNIQUE,
    Description TEXT,
    ConfigXML   TEXT,
    CreatedAt   TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UpdatedAt   TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS dbo.pipeline_runs (
    run_id           VARCHAR(64)  PRIMARY KEY,
    file_path        VARCHAR(512),
    config_path      VARCHAR(512),
    status           VARCHAR(32)  NOT NULL,
    started_at       TIMESTAMPTZ  NOT NULL,
    finished_at      TIMESTAMPTZ  NOT NULL,
    duration_ms      BIGINT       NOT NULL,
    task_count       INT          NOT NULL,
    user_name        VARCHAR(255),
    os_user_name     VARCHAR(255),
    db_user_name     VARCHAR(255),
    spid             BIGINT,
    application_name VARCHAR(255),
    hostname         VARCHAR(255),
    options_path     VARCHAR(512),
    error_class      VARCHAR(128),
    error_message    TEXT
);

CREATE TABLE IF NOT EXISTS dbo.pipeline_events (
    id               BIGSERIAL    PRIMARY KEY,
    run_id           VARCHAR(64)  NOT NULL,
    execution_id     VARCHAR(64),
    sequence_num     BIGINT       NOT NULL,
    occurred_at      TIMESTAMPTZ  NOT NULL,
    event_type       VARCHAR(64)  NOT NULL,
    node_kind        VARCHAR(64),
    node_id          VARCHAR(255),
    status           VARCHAR(64),
    user_name        VARCHAR(255),
    os_user_name     VARCHAR(255),
    db_user_name     VARCHAR(255),
    spid             VARCHAR(64),
    application_name VARCHAR(255),
    hostname         VARCHAR(255),
    options_path     VARCHAR(512),
    error_message    TEXT,
    rows_read        BIGINT,
    rows_written     BIGINT,
    rows_affected    BIGINT,
    node_output      TEXT
);

CREATE TABLE IF NOT EXISTS dbo.flow_api_events (
    event_id      VARCHAR(64)  PRIMARY KEY,
    occurred_at   TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    event_type    VARCHAR(128) NOT NULL,
    http_method   VARCHAR(16)  NOT NULL,
    path          VARCHAR(512) NOT NULL,
    artifact_type VARCHAR(64),
    artifact_name VARCHAR(255),
    run_id        VARCHAR(64),
    auth_user     VARCHAR(255),
    remote_addr   VARCHAR(128),
    status_code   INT          NOT NULL,
    status        VARCHAR(32)  NOT NULL,
    duration_ms   BIGINT       NOT NULL,
    error_message TEXT,
    details_json  TEXT
);

CREATE TABLE IF NOT EXISTS dbo.flow_api_users (
    username      VARCHAR(255) PRIMARY KEY,
    password_hash VARCHAR(512) NOT NULL,
    permission    VARCHAR(128) NOT NULL,
    is_active     BOOLEAN      NOT NULL DEFAULT TRUE,
    description   VARCHAR(512),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

---

## 3. MySQL / MariaDB (`mysql`)

> Note: When `DefaultSchema = "dbo."`, `resolveTableName` strips `"dbo."` on MySQL so tables are created in the active database selected by the connection string.

```sql
CREATE TABLE IF NOT EXISTS flow_pipeline_content (
    ID          INT AUTO_INCREMENT PRIMARY KEY,
    Name        VARCHAR(255) NOT NULL UNIQUE,
    Description LONGTEXT     NULL,
    PipelineXML LONGTEXT     NULL,
    CreatedAt   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UpdatedAt   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

CREATE TABLE IF NOT EXISTS flow_options_content (
    ID          INT AUTO_INCREMENT PRIMARY KEY,
    Name        VARCHAR(255) NOT NULL UNIQUE,
    Description LONGTEXT     NULL,
    OptionsXML  LONGTEXT     NULL,
    CreatedAt   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UpdatedAt   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

CREATE TABLE IF NOT EXISTS flow_config_content (
    ID          INT AUTO_INCREMENT PRIMARY KEY,
    Name        VARCHAR(255) NOT NULL UNIQUE,
    Description LONGTEXT     NULL,
    ConfigXML   LONGTEXT     NULL,
    CreatedAt   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UpdatedAt   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

CREATE TABLE IF NOT EXISTS pipeline_runs (
    run_id           VARCHAR(64)  NOT NULL PRIMARY KEY,
    file_path        VARCHAR(512) NULL,
    config_path      VARCHAR(512) NULL,
    status           VARCHAR(32)  NOT NULL,
    started_at       DATETIME(6)  NOT NULL,
    finished_at      DATETIME(6)  NOT NULL,
    duration_ms      BIGINT       NOT NULL,
    task_count       INT          NOT NULL,
    user_name        VARCHAR(255) NULL,
    os_user_name     VARCHAR(255) NULL,
    db_user_name     VARCHAR(255) NULL,
    spid             BIGINT       NULL,
    application_name VARCHAR(255) NULL,
    hostname         VARCHAR(255) NULL,
    options_path     VARCHAR(512) NULL,
    error_class      VARCHAR(128) NULL,
    error_message    LONGTEXT     NULL
);

CREATE TABLE IF NOT EXISTS pipeline_events (
    id               BIGINT AUTO_INCREMENT PRIMARY KEY,
    run_id           VARCHAR(64)  NOT NULL,
    execution_id     VARCHAR(64)  NULL,
    sequence_num     BIGINT       NOT NULL,
    occurred_at      DATETIME(6)  NOT NULL,
    event_type       VARCHAR(64)  NOT NULL,
    node_kind        VARCHAR(64)  NULL,
    node_id          VARCHAR(255) NULL,
    status           VARCHAR(64)  NULL,
    user_name        VARCHAR(255) NULL,
    os_user_name     VARCHAR(255) NULL,
    db_user_name     VARCHAR(255) NULL,
    spid             VARCHAR(64)  NULL,
    application_name VARCHAR(255) NULL,
    hostname         VARCHAR(255) NULL,
    options_path     VARCHAR(512) NULL,
    error_message    LONGTEXT     NULL,
    rows_read        BIGINT       NULL,
    rows_written     BIGINT       NULL,
    rows_affected    BIGINT       NULL,
    node_output      LONGTEXT     NULL
);

CREATE TABLE IF NOT EXISTS flow_api_events (
    event_id      VARCHAR(64)  NOT NULL PRIMARY KEY,
    occurred_at   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    event_type    VARCHAR(128) NOT NULL,
    http_method   VARCHAR(16)  NOT NULL,
    path          VARCHAR(512) NOT NULL,
    artifact_type VARCHAR(64)  NULL,
    artifact_name VARCHAR(255) NULL,
    run_id        VARCHAR(64)  NULL,
    auth_user     VARCHAR(255) NULL,
    remote_addr   VARCHAR(128) NULL,
    status_code   INT          NOT NULL,
    status        VARCHAR(32)  NOT NULL,
    duration_ms   BIGINT       NOT NULL,
    error_message LONGTEXT     NULL,
    details_json  LONGTEXT     NULL
);

CREATE TABLE IF NOT EXISTS flow_api_users (
    username      VARCHAR(255) NOT NULL PRIMARY KEY,
    password_hash VARCHAR(512) NOT NULL,
    permission    VARCHAR(128) NOT NULL,
    is_active     TINYINT(1)   NOT NULL DEFAULT 1,
    description   VARCHAR(512) NULL,
    created_at    DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at    DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);
```

---

## 4. SQLite (`sqlite`)

> Note: For SQLite, schema prefixes (such as `dbo.`) are automatically stripped by `resolveTableName`.

```sql
CREATE TABLE IF NOT EXISTS flow_pipeline_content (
    ID          INTEGER PRIMARY KEY AUTOINCREMENT,
    Name        TEXT NOT NULL UNIQUE,
    Description TEXT,
    PipelineXML TEXT,
    CreatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UpdatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS flow_options_content (
    ID          INTEGER PRIMARY KEY AUTOINCREMENT,
    Name        TEXT NOT NULL UNIQUE,
    Description TEXT,
    OptionsXML  TEXT,
    CreatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UpdatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS flow_config_content (
    ID          INTEGER PRIMARY KEY AUTOINCREMENT,
    Name        TEXT NOT NULL UNIQUE,
    Description TEXT,
    ConfigXML   TEXT,
    CreatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UpdatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS pipeline_runs (
    run_id           TEXT PRIMARY KEY,
    file_path        TEXT,
    config_path      TEXT,
    status           TEXT NOT NULL,
    started_at       DATETIME NOT NULL,
    finished_at      DATETIME NOT NULL,
    duration_ms      INTEGER NOT NULL,
    task_count       INTEGER NOT NULL,
    user_name        TEXT,
    os_user_name     TEXT,
    db_user_name     TEXT,
    spid             INTEGER,
    application_name TEXT,
    hostname         TEXT,
    options_path     TEXT,
    error_class      TEXT,
    error_message    TEXT
);

CREATE TABLE IF NOT EXISTS pipeline_events (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id           TEXT NOT NULL,
    execution_id     TEXT,
    sequence_num     INTEGER NOT NULL,
    occurred_at      DATETIME NOT NULL,
    event_type       TEXT NOT NULL,
    node_kind        TEXT,
    node_id          TEXT,
    status           TEXT,
    user_name        TEXT,
    os_user_name     TEXT,
    db_user_name     TEXT,
    spid             TEXT,
    application_name TEXT,
    hostname         TEXT,
    options_path     TEXT,
    error_message    TEXT,
    rows_read        INTEGER,
    rows_written     INTEGER,
    rows_affected    INTEGER,
    node_output      TEXT
);

CREATE TABLE IF NOT EXISTS flow_api_events (
    event_id      TEXT PRIMARY KEY,
    occurred_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    event_type    TEXT NOT NULL,
    http_method   TEXT NOT NULL,
    path          TEXT NOT NULL,
    artifact_type TEXT,
    artifact_name TEXT,
    run_id        TEXT,
    auth_user     TEXT,
    remote_addr   TEXT,
    status_code   INTEGER NOT NULL,
    status        TEXT NOT NULL,
    duration_ms   INTEGER NOT NULL,
    error_message TEXT,
    details_json  TEXT
);

CREATE TABLE IF NOT EXISTS flow_api_users (
    username      TEXT PRIMARY KEY,
    password_hash TEXT NOT NULL,
    permission    TEXT NOT NULL,
    is_active     INTEGER NOT NULL DEFAULT 1,
    description   TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

---

## 5. Oracle (`oracle`)

```sql
CREATE TABLE flow_pipeline_content (
    ID          NUMBER GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    Name        VARCHAR2(255) NOT NULL UNIQUE,
    Description CLOB,
    PipelineXML CLOB,
    CreatedAt   TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL,
    UpdatedAt   TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL
);

CREATE TABLE flow_options_content (
    ID          NUMBER GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    Name        VARCHAR2(255) NOT NULL UNIQUE,
    Description CLOB,
    OptionsXML  CLOB,
    CreatedAt   TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL,
    UpdatedAt   TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL
);

CREATE TABLE flow_config_content (
    ID          NUMBER GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    Name        VARCHAR2(255) NOT NULL UNIQUE,
    Description CLOB,
    ConfigXML   CLOB,
    CreatedAt   TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL,
    UpdatedAt   TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL
);

CREATE TABLE pipeline_runs (
    run_id           VARCHAR2(64)  PRIMARY KEY,
    file_path        VARCHAR2(512),
    config_path      VARCHAR2(512),
    status           VARCHAR2(32)  NOT NULL,
    started_at       TIMESTAMP WITH TIME ZONE NOT NULL,
    finished_at      TIMESTAMP WITH TIME ZONE NOT NULL,
    duration_ms      NUMBER(19)    NOT NULL,
    task_count       NUMBER(10)    NOT NULL,
    user_name        VARCHAR2(255),
    os_user_name     VARCHAR2(255),
    db_user_name     VARCHAR2(255),
    spid             NUMBER(19),
    application_name VARCHAR2(255),
    hostname         VARCHAR2(255),
    options_path     VARCHAR2(512),
    error_class      VARCHAR2(128),
    error_message    CLOB
);

CREATE TABLE pipeline_events (
    id               NUMBER GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    run_id           VARCHAR2(64)  NOT NULL,
    execution_id     VARCHAR2(64),
    sequence_num     NUMBER(19)    NOT NULL,
    occurred_at      TIMESTAMP WITH TIME ZONE NOT NULL,
    event_type       VARCHAR2(64)  NOT NULL,
    node_kind        VARCHAR2(64),
    node_id          VARCHAR2(255),
    status           VARCHAR2(64),
    user_name        VARCHAR2(255),
    os_user_name     VARCHAR2(255),
    db_user_name     VARCHAR2(255),
    spid             VARCHAR2(64),
    application_name VARCHAR2(255),
    hostname         VARCHAR2(255),
    options_path     VARCHAR2(512),
    error_message    CLOB,
    rows_read        NUMBER(19),
    rows_written     NUMBER(19),
    rows_affected    NUMBER(19),
    node_output      CLOB
);

CREATE TABLE flow_api_events (
    event_id      VARCHAR2(64)  PRIMARY KEY,
    occurred_at   TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL,
    event_type    VARCHAR2(128) NOT NULL,
    http_method   VARCHAR2(16)  NOT NULL,
    path          VARCHAR2(512) NOT NULL,
    artifact_type VARCHAR2(64),
    artifact_name VARCHAR2(255),
    run_id        VARCHAR2(64),
    auth_user     VARCHAR2(255),
    remote_addr   VARCHAR2(128),
    status_code   NUMBER(10)    NOT NULL,
    status        VARCHAR2(32)  NOT NULL,
    duration_ms   NUMBER(19)    NOT NULL,
    error_message CLOB,
    details_json  CLOB
);

CREATE TABLE flow_api_users (
    username      VARCHAR2(255) PRIMARY KEY,
    password_hash VARCHAR2(512) NOT NULL,
    permission    VARCHAR2(128) NOT NULL,
    is_active     NUMBER(1)     DEFAULT 1 NOT NULL,
    description   VARCHAR2(512),
    created_at    TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL,
    updated_at    TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL
);
```

---

## Seeding Initial BasicAuth Users & Roles

`flow_api_users.password_hash` supports:
1. **bcrypt hashes** (`$2a$...` or `$2b$...`) — recommended for production, and automatically generated when creating users via `POST /api/v1/users`.
2. **SHA-256 hex hashes** prefixed with `sha256:` (e.g., `sha256:5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8`).
3. **Plaintext** (evaluated with constant-time SHA-256 digest comparison; convenient for quick bootstrap in a dev database).

### Supported Permission Values (`permission` column)

| Permission Value | Allowed Operations |
| :--- | :--- |
| `execution-only` | `POST /api/v1/execute`, `POST /api/v1/execute/stream`, `POST /api/v1/validate`, plus all `read-only` operations |
| `read-only` | `GET /api/v1/artifacts/...`, `GET /api/v1/logs/runs`, `GET /api/v1/logs/events`, `GET /api/v1/logs/api-events` |
| `write` or `upload` | `POST /api/v1/artifacts/{type}/upload`, `POST/PUT/DELETE/GET /api/v1/artifacts/{type}...` |
| `all` | Full access to all endpoints (Read, Write/Upload, Execute, `/api/v1/databases/test`, `/api/v1/users`) |

> You can also combine permissions with a comma, e.g. `read-only,execution-only`.

### Example Seed SQL

```sql
-- 1. Admin user ('all' permissions)
INSERT INTO dbo.flow_api_users (username, password_hash, permission, is_active, description)
VALUES ('admin', 'admin-secret-pass', 'all', 1, 'Full administrator account');

-- 2. Execution-only service account ('execution-only')
INSERT INTO dbo.flow_api_users (username, password_hash, permission, is_active, description)
VALUES ('runner_svc', 'runner-secret-pass', 'execution-only', 1, 'Pipeline execution service account');

-- 3. Read-only auditor/viewer account ('read-only')
INSERT INTO dbo.flow_api_users (username, password_hash, permission, is_active, description)
VALUES ('auditor', 'auditor-secret-pass', 'read-only', 1, 'Read-only artifact and telemetry viewer');

-- 4. Publisher / uploader account ('upload' or 'write')
INSERT INTO dbo.flow_api_users (username, password_hash, permission, is_active, description)
VALUES ('publisher', 'publisher-secret-pass', 'upload', 1, 'CI/CD artifact uploader account');
```
