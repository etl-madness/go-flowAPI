package main

import (
	"github.com/etl-madness/flow"
)

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
