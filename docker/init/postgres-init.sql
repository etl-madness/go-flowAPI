-- PostgreSQL Initialization Script for go-flowAPI
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
