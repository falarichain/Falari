package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"chain/internal/explorer"
	"chain/internal/middleware"
)

func main() {
	var (
		databaseURL    = flag.String("db", "postgres://localhost:5432/falari_explorer?sslmode=disable", "PostgreSQL connection URL")
		chainURL       = flag.String("chain", "http://localhost:8080", "Chain node HTTP API URL")
		listenAddr     = flag.String("addr", ":9090", "Explorer API listen address")
		syncInterval   = flag.Duration("sync-interval", 5*time.Second, "Block sync interval")
		corsOrigins    = flag.String("cors-origins", "", "comma-separated allowed CORS origins (empty disables CORS)")
		rateLimitRPS   = flag.Float64("rate-limit-rps", 0, "per-IP request rate limit (requests/sec, 0 disables)")
		rateLimitBurst = flag.Int("rate-limit-burst", 0, "rate limit burst size (default: rps+1)")
		production     = flag.Bool("production", false, "enable production mode with strict safety checks")
	)
	flag.Parse()

	if *production {
		var errs []string
		if *rateLimitRPS <= 0 {
			errs = append(errs, "production mode requires --rate-limit-rps to be set")
		}
		if len(errs) > 0 {
			for _, e := range errs {
				log.Printf("PRODUCTION CHECK FAILED: %s", e)
			}
			os.Exit(1)
		}
		log.Println("production mode enabled: all safety checks passed")
	}

	var origins []string
	if *corsOrigins != "" {
		for _, o := range strings.Split(*corsOrigins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				origins = append(origins, o)
			}
		}
	}

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
	handler := middleware.Chain(
		middleware.CORS(origins),
		middleware.RateLimit(*rateLimitRPS, *rateLimitBurst),
	)(server.Routes())
	httpServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           handler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
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
