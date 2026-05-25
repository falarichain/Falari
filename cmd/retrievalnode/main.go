package main

import (
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	falaridht "chain/internal/dht"
	"chain/internal/gateway"
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
	dhtEnabled := flag.Bool("dht", false, "enable Kademlia DHT for shard discovery")
	dhtBootstrap := flag.String("dht-bootstrap", "", "comma-separated DHT bootstrap peer multiaddrs")
	dhtNamespace := flag.String("dht-namespace", falaridht.DefaultProtocolPrefix, "DHT protocol namespace prefix")
	dhtRepublish := flag.Duration("dht-republish", falaridht.DefaultRepublishInterval, "DHT provider record republish interval")

	gatewayEnabled := flag.Bool("gateway", false, "enable upload gateway (erasure coding + miner dispatch)")
	gatewayStorage := flag.String("gateway-storage", "", "comma-separated storage miner endpoints for gateway uploads")
	gatewayTmp := flag.String("gateway-tmp", "", "temporary directory for gateway uploads (default: data/gateway)")
	dataShards := flag.Int("gateway-data-shards", 4, "data shards for erasure coding")
	parityShards := flag.Int("gateway-parity-shards", 2, "parity shards for erasure coding")
	segmentSize := flag.Int64("gateway-segment-size", 1<<26, "segment size in bytes (default 64 MiB)")
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
	if *p2pListen != "" || *p2pPeers != "" || *dhtEnabled {
		var dhtCfg *falaridht.Config
		if *dhtEnabled {
			cfg := falaridht.DefaultConfig()
			cfg.ProtocolPrefix = *dhtNamespace
			cfg.RepublishInterval = *dhtRepublish
			cfg.ChainURL = *chainURL
			if *dhtBootstrap != "" {
				for _, addr := range strings.Split(*dhtBootstrap, ",") {
					addr = strings.TrimSpace(addr)
					if addr != "" {
						cfg.BootstrapPeers = append(cfg.BootstrapPeers, addr)
					}
				}
			}
			dhtCfg = &cfg
		}
		providerNetwork, err = storage.StartProviderNetworkWithDHT(node, *p2pListen, *p2pPeers, *p2pTopic, registeredEndpoint, *capacity, dhtCfg)
		if err != nil {
			log.Fatalf("start provider discovery: %v", err)
		}
		defer providerNetwork.Close()
		log.Printf("provider discovery enabled peer_id=%s addrs=%v topic=%s dht=%v", providerNetwork.PeerID(), providerNetwork.Addrs(), *p2pTopic, *dhtEnabled)
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

	var gwHandler *gateway.Handler
	if *gatewayEnabled {
		if *chainURL == "" {
			log.Fatal("-gateway requires -chain")
		}
		if *gatewayStorage == "" {
			log.Fatal("-gateway requires -gateway-storage (comma-separated miner endpoints)")
		}
		tmpDir := *gatewayTmp
		if tmpDir == "" {
			tmpDir = *data + "/gateway"
		}
		storageEndpoints := splitCSV(*gatewayStorage)
		gwHandler, err = gateway.New(gateway.Config{
			ChainURL:         *chainURL,
			StorageEndpoints: storageEndpoints,
			TmpDir:           tmpDir,
			DataShards:       *dataShards,
			ParityShards:     *parityShards,
			SegmentSize:      *segmentSize,
		})
		if err != nil {
			log.Fatalf("start gateway: %v", err)
		}
		log.Printf("gateway enabled (upload+download) storage=%v data-shards=%d parity-shards=%d", storageEndpoints, *dataShards, *parityShards)
		// Wire DHT service to gateway for fallback shard discovery.
		if providerNetwork != nil {
			gwHandler.SetDHTService(providerNetwork.DHTService())
		}
	}

	log.Printf("retrieval node %s listening on %s", node.Address(), *addr)

	mux := http.NewServeMux()
	storageMux := storage.NewServerWithProviderNetwork(node, providerNetwork).Routes()
	mux.Handle("/", storageMux)
	if gwHandler != nil {
		gwMux := gwHandler.Routes()
		mux.Handle("/upload", gwMux)
		mux.Handle("/download/", gwMux)
		mux.Handle("/status/", gwMux)
		mux.Handle("/gateway/", gwMux)
	}

	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := []string{}
	current := ""
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			p := trimSpace(current)
			if p != "" {
				parts = append(parts, p)
			}
			current = ""
		} else {
			current += string(s[i])
		}
	}
	p := trimSpace(current)
	if p != "" {
		parts = append(parts, p)
	}
	return parts
}

func trimSpace(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
