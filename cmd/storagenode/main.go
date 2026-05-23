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

	"chain/internal/storage"
)

func main() {
	addr := flag.String("addr", ":9090", "HTTP listen address")
	data := flag.String("data", "./data/miner1", "storage data directory")
	chainURL := flag.String("chain", "", "optional chain node URL for miner registration")
	endpoint := flag.String("endpoint", "", "public storage endpoint registered on chain")
	capacity := flag.Uint64("capacity", 1<<40, "declared storage capacity in bytes")
	stake := flag.Uint64("stake", 1000, "declared miner stake")
	faucet := flag.Bool("faucet", true, "request dev faucet funds for stake before registration")
	autoProve := flag.Bool("auto-prove", true, "poll chain node and automatically submit assigned storage proofs")
	proveInterval := flag.Duration("prove-interval", 2*time.Second, "automatic proof polling interval")
	autoRepair := flag.Bool("auto-repair", true, "poll chain node and automatically execute assigned repair tasks")
	repairInterval := flag.Duration("repair-interval", 10*time.Second, "automatic repair polling interval")
	autoDelete := flag.Bool("auto-delete", true, "poll chain node and automatically execute assigned delete tasks")
	deleteInterval := flag.Duration("delete-interval", 10*time.Second, "automatic delete polling interval")
	p2pListen := flag.String("p2p-listen", "", "comma-separated libp2p provider discovery listen multiaddrs, disabled when empty")
	p2pPeers := flag.String("p2p-peers", "", "comma-separated libp2p provider peer multiaddrs")
	p2pTopic := flag.String("p2p-topic", "storage-chain/providers/devnet", "libp2p provider discovery topic")
	flag.Parse()

	node, err := storage.OpenNode(*data)
	if err != nil {
		log.Fatalf("open storage node: %v", err)
	}
	registeredEndpoint := *endpoint
	if registeredEndpoint == "" {
		registeredEndpoint = "http://localhost" + *addr
	}
	if *chainURL != "" {
		if err := node.Register(*chainURL, registeredEndpoint, *capacity, *stake, *faucet); err != nil {
			log.Fatalf("register storage node: %v", err)
		}
		log.Printf("registered storage node %s endpoint=%s capacity=%d stake=%d", node.Address(), registeredEndpoint, *capacity, *stake)
	}
	if *chainURL != "" && *autoProve {
		node.StartAutoProver(*chainURL, *proveInterval)
		log.Printf("auto prover enabled interval=%s", *proveInterval)
	}
	if *chainURL != "" && *autoRepair {
		node.StartAutoRepairer(*chainURL, *repairInterval)
		log.Printf("auto repair enabled interval=%s", *repairInterval)
	}
	if *chainURL != "" && *autoDelete {
		node.StartAutoDeleter(*chainURL, *deleteInterval)
		log.Printf("auto delete enabled interval=%s", *deleteInterval)
	}
	var providerNetwork *storage.ProviderNetwork
	if *p2pListen != "" || *p2pPeers != "" {
		providerNetwork, err = storage.StartProviderNetwork(node, *p2pListen, *p2pPeers, *p2pTopic, registeredEndpoint, *capacity)
		if err != nil {
			log.Fatalf("start storage provider discovery: %v", err)
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
		log.Printf("provider reporter enabled interval=%s", 30*time.Second)
	}

	log.Printf("storage node %s listening on %s", node.Address(), *addr)
	server := storage.NewServerWithProviderNetwork(node, providerNetwork)
	httpServer := &http.Server{
		Addr:    *addr,
		Handler: server.Routes(),
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("storage node server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("storage node: shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	httpServer.Shutdown(shutdownCtx)
}
