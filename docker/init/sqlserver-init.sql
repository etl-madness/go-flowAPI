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

-- 8. API User Groups Table
IF OBJECT_ID('dbo.flow_api_groups', 'U') IS NULL
CREATE TABLE dbo.flow_api_groups (
    id          INT IDENTITY(1,1) NOT NULL PRIMARY KEY,
    name        NVARCHAR(128)     NOT NULL CONSTRAINT UQ_flow_api_groups_name UNIQUE,
    description NVARCHAR(512)     NULL,
    created_at  DATETIME2         NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at  DATETIME2         NOT NULL DEFAULT SYSUTCDATETIME()
);
GO

-- 9. API User-to-Group Membership Table
IF OBJECT_ID('dbo.flow_api_user_groups', 'U') IS NULL
CREATE TABLE dbo.flow_api_user_groups (
    group_name NVARCHAR(128) NOT NULL,
    username   NVARCHAR(255) NOT NULL,
    created_at DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT PK_flow_api_user_groups PRIMARY KEY CLUSTERED (group_name, username)
);
GO

IF NOT EXISTS (
    SELECT 1 FROM sys.indexes
    WHERE name = 'IX_flow_api_user_groups_username'
      AND object_id = OBJECT_ID('dbo.flow_api_user_groups')
)
CREATE NONCLUSTERED INDEX IX_flow_api_user_groups_username
    ON dbo.flow_api_user_groups (username);
GO

-- 10. Pipeline Groups Table
IF OBJECT_ID('dbo.flow_pipeline_groups', 'U') IS NULL
CREATE TABLE dbo.flow_pipeline_groups (
    id           INT IDENTITY(1,1) NOT NULL PRIMARY KEY,
    name         NVARCHAR(128)     NOT NULL CONSTRAINT UQ_flow_pipeline_groups_name UNIQUE,
    description  NVARCHAR(512)     NULL,
    name_pattern NVARCHAR(255)     NULL,
    created_at   DATETIME2         NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at   DATETIME2         NOT NULL DEFAULT SYSUTCDATETIME()
);
GO

-- 11. Pipeline-to-Group Membership Table
IF OBJECT_ID('dbo.flow_pipeline_group_members', 'U') IS NULL
CREATE TABLE dbo.flow_pipeline_group_members (
    group_name    NVARCHAR(128) NOT NULL,
    pipeline_name NVARCHAR(255) NOT NULL,
    created_at    DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT PK_flow_pipeline_group_members PRIMARY KEY CLUSTERED (group_name, pipeline_name)
);
GO

IF NOT EXISTS (
    SELECT 1 FROM sys.indexes
    WHERE name = 'IX_flow_pipeline_group_members_pipeline'
      AND object_id = OBJECT_ID('dbo.flow_pipeline_group_members')
)
CREATE NONCLUSTERED INDEX IX_flow_pipeline_group_members_pipeline
    ON dbo.flow_pipeline_group_members (pipeline_name);
GO

-- 12. Granular Pipeline & Pipeline-Group ACL Rules Table
IF OBJECT_ID('dbo.flow_pipeline_acl', 'U') IS NULL
CREATE TABLE dbo.flow_pipeline_acl (
    id             INT IDENTITY(1,1) NOT NULL PRIMARY KEY,
    principal_type NVARCHAR(32)      NOT NULL,
    principal_name NVARCHAR(255)     NOT NULL,
    target_type    NVARCHAR(32)      NOT NULL,
    target_name    NVARCHAR(255)     NOT NULL,
    permissions    NVARCHAR(128)     NOT NULL,
    effect         NVARCHAR(16)      NOT NULL CONSTRAINT DF_flow_pipeline_acl_effect DEFAULT N'allow',
    description    NVARCHAR(512)     NULL,
    created_at     DATETIME2         NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at     DATETIME2         NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT CK_flow_pipeline_acl_principal_type CHECK (principal_type IN (N'user', N'group')),
    CONSTRAINT CK_flow_pipeline_acl_target_type    CHECK (target_type IN (N'pipeline', N'pipeline_group', N'*')),
    CONSTRAINT CK_flow_pipeline_acl_effect         CHECK (effect IN (N'allow', N'deny')),
    CONSTRAINT UQ_flow_pipeline_acl_rule           UNIQUE NONCLUSTERED (principal_type, principal_name, target_type, target_name, effect)
);
GO

IF NOT EXISTS (
    SELECT 1 FROM sys.indexes
    WHERE name = 'IX_flow_pipeline_acl_target'
      AND object_id = OBJECT_ID('dbo.flow_pipeline_acl')
)
CREATE NONCLUSTERED INDEX IX_flow_pipeline_acl_target
    ON dbo.flow_pipeline_acl (target_type, target_name);
GO

IF NOT EXISTS (
    SELECT 1 FROM sys.indexes
    WHERE name = 'IX_flow_pipeline_acl_principal'
      AND object_id = OBJECT_ID('dbo.flow_pipeline_acl')
)
CREATE NONCLUSTERED INDEX IX_flow_pipeline_acl_principal
    ON dbo.flow_pipeline_acl (principal_type, principal_name);
GO
