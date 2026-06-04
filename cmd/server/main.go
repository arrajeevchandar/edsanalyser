package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"edsanalyser/internal/api"
	"edsanalyser/internal/scanner"
)

func main() {
	addr := env("ADDR", ":"+env("PORT", "8787"))
	dbPath := env("EDS_ANALYSER_DB", filepath.Join(".data", "eds-analyser.sqlite"))

	store, err := scanner.OpenSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	service := scanner.NewService(store, scanner.ServiceOptions{
		Lighthouse: scanner.NewCLILighthouseRunner(90 * time.Second),
	})
	handler := api.NewServer(service, "dist")

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("EDS Analyser listening on http://localhost%s", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
