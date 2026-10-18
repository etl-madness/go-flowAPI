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

	db, dialect, err := OpenDatabase(cfg.ConnectionString)
	if err != nil {
		log.Fatalf("[go-flowAPI] failed to connect to startup database (%s): %v", RedactDSN(cfg.ConnectionString), err)
	}
	defer db.Close()

	srv := NewAPIServer(cfg, db, dialect)

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
		log.Printf("[go-flowAPI] listening on %s (dialect=%s, defaultSchema=%q)", cfg.ListenAddr, dialect, DefaultSchema)
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
