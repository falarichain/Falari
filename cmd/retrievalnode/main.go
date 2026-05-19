package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"chain/internal/storage"
)

func main() {
	addr := flag.String("addr", ":9091", "HTTP listen address")
	data := flag.String("data", "./data/retrieval1", "retrieval data directory")
	chainURL := flag.String("chain", "", "chain node URL for receipt submission and miner registration")
	endpoint := flag.String("endpoint", "", "public endpoint registered on chain")
	capacity := flag.Uint64("capacity", 1<<40, "declared capacity in bytes")
	stake := flag.Uint64("stake", 1000, "declared stake")
	faucet := flag.Bool("faucet", true, "request dev faucet before registration")
	autoCollect := flag.Bool("auto-collect", true, "automatically sign and submit retrieval receipts")
	collectInterval := flag.Duration("collect-interval", 30*time.Second, "auto receipt collection interval")
	cacheSize := flag.Int("cache-size", 512, "max in-memory shard cache entries")
	p2pListen := flag.String("p2p-listen", "", "libp2p provider discovery listen addrs")
	p2pPeers := flag.String("p2p-peers", "", "libp2p provider discovery peer addrs")
	p2pTopic := flag.String("p2p-topic", "storage-chain/providers/devnet", "libp2p provider discovery topic")
	flag.Parse()

	node, err := storage.OpenNode(*data)
	if err != nil {
		log.Fatalf("open retrieval node: %v", err)
	}

	registeredEndpoint := *endpoint
	if registeredEndpoint == "" {
		registeredEndpoint = "http://localhost" + *addr
	}
	if *chainURL != "" {
		if err := node.Register(*chainURL, registeredEndpoint, *capacity, *stake, *faucet); err != nil {
			log.Fatalf("register retrieval node: %v", err)
		}
		log.Printf("registered retrieval node %s endpoint=%s capacity=%d stake=%d", node.Address(), registeredEndpoint, *capacity, *stake)
	}

	var providerNetwork *storage.ProviderNetwork
	if *p2pListen != "" || *p2pPeers != "" {
		providerNetwork, err = storage.StartProviderNetwork(node, *p2pListen, *p2pPeers, *p2pTopic, registeredEndpoint, *capacity)
		if err != nil {
			log.Fatalf("start provider discovery: %v", err)
		}
		defer providerNetwork.Close()
		log.Printf("provider discovery enabled peer_id=%s addrs=%v topic=%s", providerNetwork.PeerID(), providerNetwork.Addrs(), *p2pTopic)
	}

	if *chainURL != "" {
		node.StartProviderReporter(*chainURL, registeredEndpoint, *capacity, 30*time.Second, func() (string, []string) {
			if providerNetwork == nil {
				return "", nil
			}
			return providerNetwork.PeerID(), providerNetwork.Addrs()
		})
	}

	if *chainURL != "" && *autoCollect {
		node.StartAutoReceiptCollector(*chainURL, *collectInterval)
		log.Printf("auto receipt collection enabled interval=%s", *collectInterval)
	}

	if *cacheSize > 0 {
		node.EnableShardCache(*cacheSize)
	}

	log.Printf("retrieval node %s listening on %s", node.Address(), *addr)
	server := storage.NewServerWithProviderNetwork(node, providerNetwork)
	if err := http.ListenAndServe(*addr, server.Routes()); err != nil {
		log.Fatal(err)
	}
}
