package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"chain/internal/explorer"
)

func main() {
	var (
		databaseURL = flag.String("db", "postgres://localhost:5432/falari_explorer?sslmode=disable", "PostgreSQL connection URL")
		chainURL    = flag.String("chain", "http://localhost:8080", "Chain node HTTP API URL")
		listenAddr  = flag.String("addr", ":9090", "Explorer API listen address")
		syncInterval = flag.Duration("sync-interval", 5*time.Second, "Block sync interval")
	)
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store, err := explorer.NewStore(ctx, *databaseURL, *chainURL)
	if err != nil {
		log.Fatalf("explorer: failed to connect to database: %v", err)
	}
	defer store.Close()

	// Start block syncer
	go func() {
		ticker := time.NewTicker(*syncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := store.Sync(ctx); err != nil {
					log.Printf("explorer: sync error: %v", err)
				}
			}
		}
	}()

	// Start API server
	server := explorer.NewServer(store)
	httpServer := &http.Server{
		Addr:    *listenAddr,
		Handler: server.Routes(),
	}

	go func() {
		log.Printf("explorer: API server listening on %s", *listenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("explorer: API server error: %v", err)
		}
	}()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("explorer: shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	httpServer.Shutdown(shutdownCtx)
	cancel()
}
