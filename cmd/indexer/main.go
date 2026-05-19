package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"chain/internal/indexer"
)

func main() {
	addr := flag.String("addr", ":9095", "HTTP listen address")
	chainURL := flag.String("chain", "http://localhost:8080", "chain node URL")
	syncInterval := flag.Duration("sync-interval", 5*time.Second, "chain sync interval")
	flag.Parse()

	ix := indexer.New(*chainURL)
	ix.Start(*syncInterval)

	time.Sleep(500 * time.Millisecond)

	log.Printf("indexer listening on %s, syncing from %s every %s", *addr, *chainURL, *syncInterval)
	if err := http.ListenAndServe(*addr, ix.Routes()); err != nil {
		log.Fatal(err)
	}
}
