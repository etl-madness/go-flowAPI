package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestLoadStartupConfigDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs-root")

	// CLI flag
	cfg, err := LoadStartupConfig([]string{"-data-dir", dir})
	if err != nil {
		t.Fatalf("LoadStartupConfig(-data-dir): %v", err)
	}
	if cfg.DataDir != filepath.Clean(dir) {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, filepath.Clean(dir))
	}
	if info, statErr := os.Stat(cfg.DataDir); statErr != nil || !info.IsDir() {
		t.Fatalf("expected -data-dir directory to be created: %v", statErr)
	}

	// Environment variable fallback
	envDir := filepath.Join(t.TempDir(), "jobs-env")
	t.Setenv("FLOW_API_DATA_DIR", envDir)
	cfg2, err := LoadStartupConfig(nil)
	if err != nil {
		t.Fatalf("LoadStartupConfig(FLOW_API_DATA_DIR): %v", err)
	}
	if cfg2.DataDir != filepath.Clean(envDir) {
		t.Fatalf("DataDir = %q, want %q", cfg2.DataDir, filepath.Clean(envDir))
	}
}

func TestSanitizeUserDirName(t *testing.T) {
	cases := map[string]string{
		`RSYSLAB\SVC_FLOW_API`: "rsyslab_svc_flow_api",
		"U00001@RSYSLAB.COM":   "u00001_rsyslab_com",
		"../../etc/passwd":     "etc_passwd",
		"   ":                  "anonymous",
		"admin":                "admin",
	}
	for in, want := range cases {
		if got := sanitizeUserDirName(in); got != want {
			t.Errorf("sanitizeUserDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExecutePipelineDataDirIsolationAndCleanup(t *testing.T) {
	dataDir := t.TempDir()
	db, dialect, err := OpenDatabase("sqlite://:memory:")
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer db.Close()

	cfg := &ServerConfig{
		ConnectionString: "sqlite://:memory:",
		ListenAddr:       ":0",
		DataDir:          dataDir,
	}
	srv := NewAPIServer(cfg, db, dialect)

	shellLang := "sh"
	shellCmd := `mkdir -p out_sub && echo "shell-ok" > out_sub/msg.txt && pwd`
	if runtime.GOOS == "windows" {
		shellLang = "powershell"
		shellCmd = `New-Item -ItemType Directory -Force -Path out_sub | Out-Null; Set-Content -Path out_sub\msg.txt -Value "shell-ok" -NoNewline; (Get-Location).Path`
	}

	pipelineXML := `<?xml version="1.0" encoding="UTF-8"?>
<flow>
  <file_save id="s1" file="input/seed.txt">seed-value</file_save>
  <file_read id="r1" file="input/seed.txt" var="v_seed"/>
  <script id="sh1" language="` + shellLang + `" output_var="v_cwd">` + shellCmd + `</script>
  <file_read id="r2" file="out_sub/msg.txt" var="v_shell"/>
</flow>`

	ctx := context.WithValue(context.Background(), authUserContextKey, &APIUser{
		Username:   `RSYSLAB\SVC_FLOW_API`,
		Permission: PermAll,
		IsActive:   true,
	})

	resp, execErr := srv.ExecutePipeline(ctx, ExecutionRequest{
		FileXML: pipelineXML,
	}, nil)
	if execErr != nil {
		t.Fatalf("ExecutePipeline failed: %v", execErr)
	}
	if !strings.EqualFold(resp.Status, "succeeded") {
		t.Fatalf("unexpected status: %s (err=%s, results=%+v)", resp.Status, resp.ErrorMessage, resp.Results)
	}

	// Verify shell ran inside <data-dir>/rsyslab_svc_flow_api/<pid>-...
	expectedUserDir := filepath.Join(dataDir, "rsyslab_svc_flow_api")
	expectedPrefix := filepath.Join(expectedUserDir, fmt.Sprintf("%d-", os.Getpid()))
	var shCWD string
	for _, r := range resp.Results {
		if r.ScriptID == "sh1" {
			shCWD = strings.TrimSpace(r.ResultsString)
		}
	}
	if !strings.HasPrefix(strings.ToLower(filepath.Clean(shCWD)), strings.ToLower(expectedPrefix)) {
		t.Fatalf("expected shell cwd %q to start with %q", shCWD, expectedPrefix)
	}

	// Verify job directory was cleaned up after ExecutePipeline returned
	if _, statErr := os.Stat(shCWD); !os.IsNotExist(statErr) {
		t.Fatalf("expected job workspace %q to be deleted after job completion, stat err=%v", shCWD, statErr)
	}
}

func TestExecutePipelineDataDirRejectsEscape(t *testing.T) {
	dataDir := t.TempDir()
	db, dialect, err := OpenDatabase("sqlite://:memory:")
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer db.Close()

	srv := NewAPIServer(&ServerConfig{DataDir: dataDir}, db, dialect)
	pipelineXML := `<?xml version="1.0" encoding="UTF-8"?>
<flow>
  <file_save id="bad_write" file="../escape.txt">nope</file_save>
</flow>`

	ctx := context.WithValue(context.Background(), authUserContextKey, &APIUser{
		Username:   "admin",
		Permission: PermAll,
		IsActive:   true,
	})

	resp, _ := srv.ExecutePipeline(ctx, ExecutionRequest{FileXML: pipelineXML}, nil)
	if resp == nil || len(resp.Results) == 0 || resp.Results[0].ReturnCode == 0 {
		t.Fatalf("expected path escape to fail, got resp=%+v", resp)
	}
}

func TestExecutePipelineDataDirConcurrentSameUser(t *testing.T) {
	dataDir := t.TempDir()
	db, dialect, err := OpenDatabase("sqlite://:memory:")
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer db.Close()

	srv := NewAPIServer(&ServerConfig{DataDir: dataDir}, db, dialect)

	const workers = 4
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			xmlDoc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<flow>
  <file_save id="w" file="sub/val.txt">job-%d</file_save>
  <file_read id="r" file="sub/val.txt" var="out"/>
</flow>`, idx)
			ctx := context.WithValue(context.Background(), authUserContextKey, &APIUser{
				Username:   "U00001@RSYSLAB.COM",
				Permission: PermAll,
				IsActive:   true,
			})
			resp, err := srv.ExecutePipeline(ctx, ExecutionRequest{FileXML: xmlDoc}, nil)
			if err != nil {
				errs <- err
				return
			}
			for _, r := range resp.Results {
				if r.ReturnCode != 0 {
					errs <- fmt.Errorf("worker %d node %s failed: %v", idx, r.ScriptID, r.ReturnCode)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ExecutePipeline failed: %v", err)
	}

	// All per-job directories under <data-dir>/u00001_rsyslab_com must be cleaned up
	entries, err := os.ReadDir(filepath.Join(dataDir, "u00001_rsyslab_com"))
	if err != nil {
		t.Fatalf("ReadDir user dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 remaining job directories after completion, got %d", len(entries))
	}
}
