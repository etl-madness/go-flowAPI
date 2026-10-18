package main

import "strings"

const (
	// DefaultSchema defines the database schema prefix for SQL Server / PostgreSQL / Oracle.
	// If tables reside in a custom schema, adjust this constant (e.g. "custom_schema.").
	// Set to "" for unqualified table names.
	DefaultSchema = "dbo."

	// Artifact CRUD Tables (matching go-flow / db_importer conventions)
	TablePipelineContent = DefaultSchema + "flow_pipeline_content"
	TableOptionsContent  = DefaultSchema + "flow_options_content"
	TableConfigContent   = DefaultSchema + "flow_config_content"

	// Execution Telemetry Tables (matching go-flow & builder conventions)
	TablePipelineRuns   = DefaultSchema + "pipeline_runs"
	TablePipelineEvents = DefaultSchema + "pipeline_events"

	// API Event Audit Table (records all API CRUD, upload, auth, and execution events)
	TableAPIAuditEvents = DefaultSchema + "flow_api_events"

	// BasicAuth Users & Role Permissions Table
	TableAPIUsers = DefaultSchema + "flow_api_users"
)

// resolveTableName adjusts the table path for dialects where "dbo." is not a valid default schema
// (such as SQLite or MySQL), while preserving custom schemas when configured.
func resolveTableName(driverType, tableConst string) string {
	switch strings.ToLower(strings.TrimSpace(driverType)) {
	case "sqlite", "sqlite3", "mysql":
		if strings.HasPrefix(tableConst, "dbo.") {
			return strings.TrimPrefix(tableConst, "dbo.")
		}
	}
	return tableConst
}
