package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etl-madness/flow"
)

func TestTablesConfigOverrideAndCustomSQLiteEndToEnd(t *testing.T) {
	origCfg := ActiveTableConfig
	t.Cleanup(func() {
		_ = ApplyTableConfig(origCfg)
	})

	cfgPath := filepath.Join(t.TempDir(), "tables.json")
	raw := `{
  "schema": "custom_etl",
  "pipeline_content": "custom_pipelines",
  "options_content": "custom_options",
  "config_content": "custom_configs",
  "pipeline_runs": "custom_runs",
  "pipeline_events": "custom_events",
  "api_events": "custom_api_events",
  "api_users": "custom_users",
  "api_groups": "custom_groups",
  "api_user_groups": "custom_user_groups",
  "pipeline_groups": "custom_pipeline_groups",
  "pipeline_group_members": "custom_pipeline_group_members",
  "pipeline_acl": "custom_acl"
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	srvCfg, err := LoadStartupConfig([]string{"-tables-config", cfgPath, "-print-ddl", "sqlite"})
	if err != nil {
		t.Fatalf("LoadStartupConfig(-tables-config): %v", err)
	}
	if srvCfg.PrintDDLDialect != "sqlite" {
		t.Fatalf("PrintDDLDialect = %q, want sqlite", srvCfg.PrintDDLDialect)
	}
	if TablePipelineContent != "custom_etl.custom_pipelines" {
		t.Fatalf("TablePipelineContent = %q, want custom_etl.custom_pipelines", TablePipelineContent)
	}
	if resolveTableName("sqlite", TablePipelineContent) != "custom_pipelines" {
		t.Fatalf("resolveTableName(sqlite) = %q, want custom_pipelines", resolveTableName("sqlite", TablePipelineContent))
	}
	if defaultSQLResourceColumn("custom_pipelines") != "PipelineXML" {
		t.Fatalf("defaultSQLResourceColumn(custom_pipelines) = %q", defaultSQLResourceColumn("custom_pipelines"))
	}

	// Render SQLite DDL with custom table names, execute it, and run CRUD + user bootstrap + pipeline execution
	ddl, err := ActiveTableConfig.RenderDDL("sqlite")
	if err != nil {
		t.Fatalf("RenderDDL(sqlite): %v", err)
	}
	if !strings.Contains(ddl, "CREATE TABLE IF NOT EXISTS custom_pipelines") {
		t.Fatalf("expected custom_pipelines in rendered DDL")
	}

	db, dialect, err := OpenDatabase("sqlite://:memory:")
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("db.Exec(ddl): %v", err)
	}

	srv := NewAPIServer(srvCfg, db, dialect)
	ctx := context.Background()

	u, err := srv.UpsertAPIUser(ctx, "custom_admin", "StrongPass!123", PermAll, "test", true)
	if err != nil || u.Username != "custom_admin" {
		t.Fatalf("UpsertAPIUser on custom_users failed: %v", err)
	}
	if _, err := srv.AuthenticateUser(ctx, "custom_admin", "StrongPass!123"); err != nil {
		t.Fatalf("AuthenticateUser on custom_users failed: %v", err)
	}
	if _, err := srv.UpsertArtifact(ctx, "pipeline", "demo.xml", "desc", `<flow><variable name="x" value="1"/></flow>`, false, ""); err != nil {
		t.Fatalf("UpsertArtifact on custom_pipelines failed: %v", err)
	}
	rec, err := srv.GetArtifact(ctx, "pipeline", "demo.xml", false, "")
	if err != nil || rec == nil {
		t.Fatalf("GetArtifact on custom_pipelines failed: %v", err)
	}

	// Verify ApplyTableConfig rejects invalid config
	if err := ApplyTableConfig(flow.TableConfig{Schema: "bad;schema"}); err == nil {
		t.Fatal("expected ApplyTableConfig to reject invalid schema")
	}
}
