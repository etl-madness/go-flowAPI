package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	cfg, err := LoadStartupConfig(os.Args[1:])
	if err != nil {
		log.Fatalf("[go-flowAPI] configuration error: %v", err)
	}

	if cfg.HashPassword != "" {
		hash, err := HashPasswordBcrypt(cfg.HashPassword)
		if err != nil {
			log.Fatalf("[go-flowAPI] failed hashing password: %v", err)
		}
		fmt.Println(hash)
		return
	}

	if cfg.PrintDDLDialect != "" {
		ddl, err := ActiveTableConfig.RenderDDL(cfg.PrintDDLDialect)
		if err != nil {
			log.Fatalf("[go-flowAPI] failed generating DDL for %q: %v", cfg.PrintDDLDialect, err)
		}
		fmt.Print(ddl)
		return
	}

	logFile, err := configureLogOutput(cfg.LogFilePath)
	if err != nil {
		log.Fatalf("[go-flowAPI] failed opening log file %q: %v", cfg.LogFilePath, err)
	}
	if logFile != nil {
		defer logFile.Close()
	}

	handled, err := maybeRunOSService(cfg)
	if err != nil {
		log.Fatalf("[go-flowAPI] OS service error: %v", err)
	}
	if handled {
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runAPIServer(ctx, cfg, nil); err != nil {
		log.Fatalf("[go-flowAPI] %v", err)
	}
}

func configureLogOutput(logFilePath string) (*os.File, error) {
	if logFilePath == "" {
		return nil, nil
	}
	dir := filepath.Dir(logFilePath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return nil, err
	}
	log.SetOutput(f)
	return f, nil
}

func runAPIServer(ctx context.Context, cfg *ServerConfig, onReady func()) error {
	db, dialect, err := OpenDatabase(cfg.ConnectionString)
	if err != nil {
		return fmt.Errorf("failed to connect to startup database (%s): %w", RedactDSN(cfg.ConnectionString), err)
	}
	defer db.Close()

	srv := NewAPIServer(cfg, db, dialect)

	if cfg.BootstrapUser != "" {
		if cfg.BootstrapPass == "" {
			return errors.New("-bootstrap-pass (or FLOW_API_BOOTSTRAP_PASS) is required when -bootstrap-user is specified")
		}
		u, err := srv.UpsertAPIUser(ctx, cfg.BootstrapUser, cfg.BootstrapPass, cfg.BootstrapPerm, "Initial bootstrap administrator account", true)
		if err != nil {
			return fmt.Errorf("failed bootstrapping initial user %q: %w", cfg.BootstrapUser, err)
		}
		log.Printf("[go-flowAPI] bootstrapped API user %q with permission=%q in %s", u.Username, u.Permission, resolveTableName(dialect, TableAPIUsers))
		srv.logAPIEvent(ctx, APIAuditEvent{
			EventType:    "user.bootstrap",
			HTTPMethod:   "SYSTEM",
			Path:         "/api/v1/users",
			ArtifactType: "user",
			ArtifactName: u.Username,
			AuthUser:     "system",
			StatusCode:   http.StatusCreated,
			Status:       "SUCCESS",
			DetailsJSON:  fmt.Sprintf(`{"username":%q,"permission":%q}`, u.Username, u.Permission),
		})
	}

	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("HTTP listen error on %s: %w", cfg.ListenAddr, err)
	}

	// Log API startup event (best-effort if table already exists)
	srv.logAPIEvent(ctx, APIAuditEvent{
		EventType:   "system.startup",
		HTTPMethod:  "SYSTEM",
		Path:        "/",
		StatusCode:  http.StatusOK,
		Status:      "SUCCESS",
		DetailsJSON: fmt.Sprintf(`{"addr":%q,"dialect":%q,"default_schema":%q}`, ln.Addr().String(), dialect, DefaultSchema),
	})

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	serveErrCh := make(chan error, 1)
	go func() {
		log.Printf("[go-flowAPI] listening on %s (dialect=%s, defaultSchema=%q, swagger=http://localhost%s/swagger/)", cfg.ListenAddr, dialect, DefaultSchema, cfg.ListenAddr)
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- fmt.Errorf("HTTP server error: %w", err)
			return
		}
		serveErrCh <- nil
	}()

	if onReady != nil {
		onReady()
	}

	select {
	case err := <-serveErrCh:
		return err
	case <-ctx.Done():
	}

	log.Printf("[go-flowAPI] shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	srv.logAPIEvent(shutdownCtx, APIAuditEvent{
		EventType:   "system.shutdown",
		HTTPMethod:  "SYSTEM",
		Path:        "/",
		StatusCode:  http.StatusOK,
		Status:      "SUCCESS",
		DetailsJSON: `{"reason":"graceful_shutdown"}`,
	})

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("[go-flowAPI] graceful shutdown error: %v", err)
	}
	return <-serveErrCh
}
