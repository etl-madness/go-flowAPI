-- SQL Server Initialization Script for go-flowAPI
IF DB_ID('EXAMPLE_DB') IS NULL
BEGIN
    CREATE DATABASE [EXAMPLE_DB];
END
GO

USE [EXAMPLE_DB];
GO

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
GO

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
GO

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
GO

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
GO

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
GO

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
GO

-- 7. API BasicAuth Users & Permissions Table
IF OBJECT_ID('dbo.flow_api_users', 'U') IS NULL
CREATE TABLE dbo.flow_api_users (
    username      NVARCHAR(255) NOT NULL PRIMARY KEY,
    password_hash NVARCHAR(512) NOT NULL,
    permission    NVARCHAR(128) NOT NULL,
    is_active     BIT           NOT NULL DEFAULT 1,
    description   NVARCHAR(512) NULL,
    created_at    DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at    DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME()
);
GO
