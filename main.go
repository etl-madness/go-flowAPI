package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
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

	db, dialect, err := OpenDatabase(cfg.ConnectionString)
	if err != nil {
		log.Fatalf("[go-flowAPI] failed to connect to startup database (%s): %v", RedactDSN(cfg.ConnectionString), err)
	}
	defer db.Close()

	srv := NewAPIServer(cfg, db, dialect)

	if cfg.BootstrapUser != "" {
		if cfg.BootstrapPass == "" {
			log.Fatalf("[go-flowAPI] -bootstrap-pass (or FLOW_API_BOOTSTRAP_PASS) is required when -bootstrap-user is specified")
		}
		u, err := srv.UpsertAPIUser(context.Background(), cfg.BootstrapUser, cfg.BootstrapPass, cfg.BootstrapPerm, "Initial bootstrap administrator account", true)
		if err != nil {
			log.Fatalf("[go-flowAPI] failed bootstrapping initial user %q: %v", cfg.BootstrapUser, err)
		}
		log.Printf("[go-flowAPI] bootstrapped API user %q with permission=%q in %s", u.Username, u.Permission, resolveTableName(dialect, TableAPIUsers))
		srv.logAPIEvent(context.Background(), APIAuditEvent{
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

	// Log API startup event (best-effort if table already exists)
	srv.logAPIEvent(context.Background(), APIAuditEvent{
		EventType:   "system.startup",
		HTTPMethod:  "SYSTEM",
		Path:        "/",
		StatusCode:  http.StatusOK,
		Status:      "SUCCESS",
		DetailsJSON: fmt.Sprintf(`{"addr":%q,"dialect":%q,"default_schema":%q}`, cfg.ListenAddr, dialect, DefaultSchema),
	})

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[go-flowAPI] listening on %s (dialect=%s, defaultSchema=%q, swagger=http://localhost%s/swagger/)", cfg.ListenAddr, dialect, DefaultSchema, cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[go-flowAPI] HTTP server error: %v", err)
		}
	}()

	<-stopCh
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
}
