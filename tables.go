package main

import (
	"fmt"
	"strings"

	"github.com/etl-madness/flow"
)

// DefaultTableTemplateContent can be overridden at build time via:
//   -ldflags="-X main.DefaultTableTemplateContent=my_templates"
var DefaultTableTemplateContent = "flow_template_content"

// Package-level schema and table name variables initialized from flow.DefaultTableConfig().
// They can be overridden at build time via Go linker flags on github.com/etl-madness/flow
// (e.g. -ldflags="-X github.com/etl-madness/flow.DefaultSchema=etl_flow") or at runtime
// via -tables-config <path> / FLOW_TABLES_CONFIG (JSON or YAML mapping file).
var (
	ActiveTableConfig = flow.DefaultTableConfig()

	DefaultSchema = ActiveTableConfig.SchemaPrefix()

	// Artifact CRUD Tables (matching go-flow / db_importer conventions)
	TablePipelineContent = ActiveTableConfig.Qualify(ActiveTableConfig.PipelineContent)
	TableOptionsContent  = ActiveTableConfig.Qualify(ActiveTableConfig.OptionsContent)
	TableConfigContent   = ActiveTableConfig.Qualify(ActiveTableConfig.ConfigContent)
	TableTemplateContent = ActiveTableConfig.Qualify(DefaultTableTemplateContent)

	// Execution Telemetry Tables (matching go-flow & builder conventions)
	TablePipelineRuns   = ActiveTableConfig.Qualify(ActiveTableConfig.PipelineRuns)
	TablePipelineEvents = ActiveTableConfig.Qualify(ActiveTableConfig.PipelineEvents)

	// API Event Audit Table (records all API CRUD, upload, auth, and execution events)
	TableAPIAuditEvents = ActiveTableConfig.Qualify(ActiveTableConfig.APIAuditEvents)

	// BasicAuth Users & Role Permissions Table
	TableAPIUsers = ActiveTableConfig.Qualify(ActiveTableConfig.APIUsers)

	// Granular User/Group & Pipeline/Pipeline-Group RBAC/ACL Tables
	TableAPIGroups            = ActiveTableConfig.Qualify(ActiveTableConfig.APIGroups)
	TableAPIUserGroups        = ActiveTableConfig.Qualify(ActiveTableConfig.APIUserGroups)
	TablePipelineGroups       = ActiveTableConfig.Qualify(ActiveTableConfig.PipelineGroups)
	TablePipelineGroupMembers = ActiveTableConfig.Qualify(ActiveTableConfig.PipelineGroupMembers)
	TablePipelineACL          = ActiveTableConfig.Qualify(ActiveTableConfig.PipelineACL)
)

// ApplyTableConfig updates ActiveTableConfig and all package-level Table* variables.
func ApplyTableConfig(cfg flow.TableConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	ActiveTableConfig = cfg
	DefaultSchema = cfg.SchemaPrefix()
	TablePipelineContent = cfg.Qualify(cfg.PipelineContent)
	TableOptionsContent = cfg.Qualify(cfg.OptionsContent)
	TableConfigContent = cfg.Qualify(cfg.ConfigContent)
	TableTemplateContent = cfg.Qualify(DefaultTableTemplateContent)
	TablePipelineRuns = cfg.Qualify(cfg.PipelineRuns)
	TablePipelineEvents = cfg.Qualify(cfg.PipelineEvents)
	TableAPIAuditEvents = cfg.Qualify(cfg.APIAuditEvents)
	TableAPIUsers = cfg.Qualify(cfg.APIUsers)
	TableAPIGroups = cfg.Qualify(cfg.APIGroups)
	TableAPIUserGroups = cfg.Qualify(cfg.APIUserGroups)
	TablePipelineGroups = cfg.Qualify(cfg.PipelineGroups)
	TablePipelineGroupMembers = cfg.Qualify(cfg.PipelineGroupMembers)
	TablePipelineACL = cfg.Qualify(cfg.PipelineACL)
	return nil
}

// resolveTableName adjusts the table path for dialects where "dbo." or custom schemas
// are not supported (such as SQLite or MySQL), using ActiveTableConfig.
func resolveTableName(driverType, tableRef string) string {
	return ActiveTableConfig.Resolve(driverType, tableRef)
}

// RenderTemplateDDL renders the DDL for TableTemplateContent in the requested dialect.
func RenderTemplateDDL(dialect string) (string, error) {
	tbl := resolveTableName(dialect, TableTemplateContent)
	switch strings.ToLower(strings.TrimSpace(dialect)) {
	case "sqlite", "sqlite3":
		return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    ID          INTEGER PRIMARY KEY AUTOINCREMENT,
    Type        TEXT     NOT NULL,
    Name        TEXT     NOT NULL,
    Description TEXT,
    TemplateXML TEXT,
    CreatedBy   TEXT     NOT NULL DEFAULT '',
    UpdatedBy   TEXT     NOT NULL DEFAULT '',
    CreatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UpdatedAt   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (Type, Name, CreatedBy)
);
CREATE INDEX IF NOT EXISTS ix_flow_template_content_owner ON %s (CreatedBy, Type);
`, tbl, tbl), nil
	case "postgres", "postgresql", "pgx":
		return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    ID          SERIAL       PRIMARY KEY,
    Type        VARCHAR(32)  NOT NULL,
    Name        VARCHAR(255) NOT NULL,
    Description TEXT,
    TemplateXML TEXT,
    CreatedBy   VARCHAR(255) NOT NULL DEFAULT '',
    UpdatedBy   VARCHAR(255) NOT NULL DEFAULT '',
    CreatedAt   TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UpdatedAt   TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (Type, Name, CreatedBy)
);
CREATE INDEX IF NOT EXISTS ix_flow_template_content_owner ON %s (CreatedBy, Type);
`, tbl, tbl), nil
	case "sqlserver", "mssql", "azuresql":
		return fmt.Sprintf(`
IF OBJECT_ID('%s', 'U') IS NULL
CREATE TABLE %s (
    ID          INT IDENTITY(1,1) PRIMARY KEY,
    Type        NVARCHAR(32)      NOT NULL,
    Name        NVARCHAR(255)     NOT NULL,
    Description NVARCHAR(MAX)     NULL,
    TemplateXML NVARCHAR(MAX)     NULL,
    CreatedBy   NVARCHAR(255)     NOT NULL DEFAULT N'',
    UpdatedBy   NVARCHAR(255)     NOT NULL DEFAULT N'',
    CreatedAt   DATETIME2         NOT NULL DEFAULT SYSUTCDATETIME(),
    UpdatedAt   DATETIME2         NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT UQ_flow_template_type_name_owner UNIQUE (Type, Name, CreatedBy)
);
GO
IF NOT EXISTS (
    SELECT 1 FROM sys.indexes
    WHERE name = 'IX_flow_template_content_owner'
      AND object_id = OBJECT_ID('%s')
)
CREATE NONCLUSTERED INDEX IX_flow_template_content_owner
    ON %s (CreatedBy, Type);
GO
`, tbl, tbl, tbl, tbl), nil
	case "mysql", "mariadb":
		return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    ID          INT AUTO_INCREMENT PRIMARY KEY,
    Type        VARCHAR(32)  NOT NULL,
    Name        VARCHAR(255) NOT NULL,
    Description LONGTEXT,
    TemplateXML LONGTEXT,
    CreatedBy   VARCHAR(255) NOT NULL DEFAULT '',
    UpdatedBy   VARCHAR(255) NOT NULL DEFAULT '',
    CreatedAt   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UpdatedAt   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uq_flow_template_type_name_owner (Type, Name, CreatedBy),
    KEY ix_flow_template_content_owner (CreatedBy, Type)
);
`, tbl), nil
	case "oracle", "ora", "godror":
		return fmt.Sprintf(`
CREATE TABLE %s (
    ID          NUMBER GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    Type        VARCHAR2(32)  NOT NULL,
    Name        VARCHAR2(255) NOT NULL,
    Description CLOB,
    TemplateXML CLOB,
    CreatedBy   VARCHAR2(255) DEFAULT '' NOT NULL,
    UpdatedBy   VARCHAR2(255) DEFAULT '' NOT NULL,
    CreatedAt   TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL,
    UpdatedAt   TIMESTAMP WITH TIME ZONE DEFAULT SYSTIMESTAMP NOT NULL,
    CONSTRAINT UQ_FLOW_TEMPLATE_OWNER UNIQUE (Type, Name, CreatedBy)
);
CREATE INDEX IX_FLOW_TEMPLATE_OWNER ON %s (CreatedBy, Type);
`, tbl, tbl), nil
	default:
		return "", fmt.Errorf("unsupported DDL dialect %q", dialect)
	}
}
