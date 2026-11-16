-- SQLite Initialization Script for go-flowAPI
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

CREATE TABLE IF NOT EXISTS flow_api_groups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS flow_api_user_groups (
    group_name TEXT NOT NULL,
    username   TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (group_name, username)
);
CREATE INDEX IF NOT EXISTS ix_flow_api_user_groups_username ON flow_api_user_groups (username);

CREATE TABLE IF NOT EXISTS flow_pipeline_groups (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    description  TEXT,
    name_pattern TEXT,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS flow_pipeline_group_members (
    group_name    TEXT NOT NULL,
    pipeline_name TEXT NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (group_name, pipeline_name)
);
CREATE INDEX IF NOT EXISTS ix_flow_pipeline_group_members_pipeline ON flow_pipeline_group_members (pipeline_name);

CREATE TABLE IF NOT EXISTS flow_pipeline_acl (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    principal_type TEXT NOT NULL,
    principal_name TEXT NOT NULL,
    target_type    TEXT NOT NULL,
    target_name    TEXT NOT NULL,
    permissions    TEXT NOT NULL,
    effect         TEXT NOT NULL DEFAULT 'allow',
    description    TEXT,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (principal_type, principal_name, target_type, target_name, effect)
);
CREATE INDEX IF NOT EXISTS ix_flow_pipeline_acl_target ON flow_pipeline_acl (target_type, target_name);
CREATE INDEX IF NOT EXISTS ix_flow_pipeline_acl_principal ON flow_pipeline_acl (principal_type, principal_name);
