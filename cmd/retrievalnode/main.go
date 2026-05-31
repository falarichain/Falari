package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"chain/internal/config"
	falaridht "chain/internal/dht"
	"chain/internal/gateway"
	"chain/internal/middleware"
	"chain/internal/storage"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file (flags override config values)")
	addr := flag.String("addr", ":9091", "HTTP listen address")
	data := flag.String("data", "./data/retrieval1", "retrieval data directory")
	chainURL := flag.String("chain", "", "chain node URL for receipt submission and miner registration")
	endpoint := flag.String("endpoint", "", "public endpoint registered on chain")
	capacity := flag.Uint64("capacity", 1<<40, "declared capacity in bytes")
	stake := flag.Uint64("stake", 1000, "declared stake")
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
	segmentSize := flag.Int64("gateway-segment-size", 4<<20, "segment size in bytes (default 4 MiB)")
	gatewayMaxUploadBytes := flag.Int64("gateway-max-upload-bytes", 1<<30, "maximum HTTP upload request size in bytes")
	gatewayAgentKeyFile := flag.String("gateway-agent-key-file", "", "JSON map of agent key id to private key for gateway-side signing")
	gatewayAllowPrivateKeyAPIKeys := flag.Bool("gateway-allow-private-key-api-keys", true, "allow legacy API keys that include private keys in request headers")
	corsOrigins := flag.String("cors-origins", "", "comma-separated allowed CORS origins (empty disables CORS)")
	rateLimitRPS := flag.Float64("rate-limit-rps", 0, "per-IP request rate limit (requests/sec, 0 disables)")
	rateLimitBurst := flag.Int("rate-limit-burst", 0, "rate limit burst size (default: rps+1)")
	trustedProxies := flag.String("trusted-proxies", "", "comma-separated trusted proxy CIDRs/IPs for X-Forwarded-For")
	production := flag.Bool("production", false, "enable production mode with strict safety checks")
	flag.Parse()

	// Load YAML config and apply flag overrides (flag > config > default).
	if *configPath != "" {
		var cfg config.RetrievalNodeConfig
		if err := config.Load(*configPath, &cfg); err != nil {
			log.Fatalf("load config: %v", err)
		}
		if !config.IsFlagSet("addr") && cfg.HTTP.Addr != "" {
			*addr = cfg.HTTP.Addr
		}
		if !config.IsFlagSet("data") && cfg.Data != "" {
			*data = cfg.Data
		}
		if !config.IsFlagSet("chain") && cfg.Chain.URL != "" {
			*chainURL = cfg.Chain.URL
		}
		if !config.IsFlagSet("endpoint") && cfg.Chain.Endpoint != "" {
			*endpoint = cfg.Chain.Endpoint
		}
		if !config.IsFlagSet("capacity") && cfg.Chain.Capacity != 0 {
			*capacity = cfg.Chain.Capacity
		}
		if !config.IsFlagSet("stake") && cfg.Chain.Stake != 0 {
			*stake = cfg.Chain.Stake
		}
		if !config.IsFlagSet("auto-collect") {
			*autoCollect = cfg.AutoCollect.Enabled
		}
		if !config.IsFlagSet("collect-interval") && cfg.AutoCollect.Interval.Duration() != 0 {
			*collectInterval = cfg.AutoCollect.Interval.Duration()
		}
		if !config.IsFlagSet("cache-size") && cfg.CacheSize != 0 {
			*cacheSize = cfg.CacheSize
		}
		if !config.IsFlagSet("p2p-listen") && cfg.P2P.Listen != "" {
			*p2pListen = cfg.P2P.Listen
		}
		if !config.IsFlagSet("p2p-peers") && cfg.P2P.Peers != "" {
			*p2pPeers = cfg.P2P.Peers
		}
		if !config.IsFlagSet("p2p-topic") && cfg.P2P.Topic != "" {
			*p2pTopic = cfg.P2P.Topic
		}
		if !config.IsFlagSet("dht") && cfg.DHT.Enabled {
			*dhtEnabled = cfg.DHT.Enabled
		}
		if !config.IsFlagSet("dht-bootstrap") && len(cfg.DHT.Bootstrap) > 0 {
			*dhtBootstrap = strings.Join(cfg.DHT.Bootstrap, ",")
		}
		if !config.IsFlagSet("dht-namespace") && cfg.DHT.Namespace != "" {
			*dhtNamespace = cfg.DHT.Namespace
		}
		if !config.IsFlagSet("dht-republish") && cfg.DHT.Republish.Duration() != 0 {
			*dhtRepublish = cfg.DHT.Republish.Duration()
		}
		if !config.IsFlagSet("gateway") && cfg.Gateway.Enabled {
			*gatewayEnabled = cfg.Gateway.Enabled
		}
		if !config.IsFlagSet("gateway-storage") && len(cfg.Gateway.StorageEndpoints) > 0 {
			*gatewayStorage = strings.Join(cfg.Gateway.StorageEndpoints, ",")
		}
		if !config.IsFlagSet("gateway-tmp") && cfg.Gateway.TmpDir != "" {
			*gatewayTmp = cfg.Gateway.TmpDir
		}
		if !config.IsFlagSet("gateway-data-shards") && cfg.Gateway.DataShards != 0 {
			*dataShards = cfg.Gateway.DataShards
		}
		if !config.IsFlagSet("gateway-parity-shards") && cfg.Gateway.ParityShards != 0 {
			*parityShards = cfg.Gateway.ParityShards
		}
		if !config.IsFlagSet("gateway-segment-size") && cfg.Gateway.SegmentSize != 0 {
			*segmentSize = cfg.Gateway.SegmentSize
		}
		if !config.IsFlagSet("gateway-max-upload-bytes") && cfg.Gateway.MaxUploadBytes != 0 {
			*gatewayMaxUploadBytes = cfg.Gateway.MaxUploadBytes
		}
		if !config.IsFlagSet("gateway-agent-key-file") && cfg.Gateway.AgentKeyFile != "" {
			*gatewayAgentKeyFile = cfg.Gateway.AgentKeyFile
		}
		if !config.IsFlagSet("gateway-allow-private-key-api-keys") {
			*gatewayAllowPrivateKeyAPIKeys = cfg.Gateway.AllowPrivateKeyAPIKeys
		}
		if !config.IsFlagSet("cors-origins") && len(cfg.HTTP.CORSOrigins) > 0 {
			*corsOrigins = strings.Join(cfg.HTTP.CORSOrigins, ",")
		}
		if !config.IsFlagSet("rate-limit-rps") && cfg.HTTP.RateLimitRPS != 0 {
			*rateLimitRPS = cfg.HTTP.RateLimitRPS
		}
		if !config.IsFlagSet("rate-limit-burst") && cfg.HTTP.RateLimitBurst != 0 {
			*rateLimitBurst = cfg.HTTP.RateLimitBurst
		}
		if !config.IsFlagSet("trusted-proxies") && len(cfg.HTTP.TrustedProxies) > 0 {
			*trustedProxies = strings.Join(cfg.HTTP.TrustedProxies, ",")
		}
		if !config.IsFlagSet("production") && cfg.HTTP.Production {
			*production = cfg.HTTP.Production
		}
		log.Printf("config loaded from %s", *configPath)
	}

	if *production {
		var errs []string
		if *p2pTopic == "storage-chain/providers/devnet" || *p2pTopic == "storage-chain/devnet" {
			errs = append(errs, "production mode requires a non-default --p2p-topic")
		}
		if *endpoint == "" {
			errs = append(errs, "production mode requires explicit --endpoint")
		}
		if *rateLimitRPS <= 0 {
			errs = append(errs, "production mode requires --rate-limit-rps to be set")
		}
		if *chainURL == "" {
			errs = append(errs, "production mode requires --chain to register on-chain")
		}
		if *gatewayEnabled {
			if *gatewayAgentKeyFile == "" {
				errs = append(errs, "production gateway requires --gateway-agent-key-file")
			}
			if *gatewayAllowPrivateKeyAPIKeys {
				errs = append(errs, "production gateway requires --gateway-allow-private-key-api-keys=false")
			}
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

	minerKey := os.Getenv("MINER_PRIVATE_KEY")
	if minerKey == "" {
		log.Fatal("MINER_PRIVATE_KEY environment variable is not set; generate a key with: genkey")
	}
	node, err := storage.OpenNode(*data, minerKey)
	if err != nil {
		log.Fatalf("open retrieval node: %v", err)
	}

	registeredEndpoint := *endpoint
	if registeredEndpoint == "" {
		registeredEndpoint = "http://localhost" + *addr
	}
	node.ConfigureChain(*chainURL, registeredEndpoint)
	node.RequireChainAuthorization(*production)
	if *chainURL != "" {
		if err := node.Register(*chainURL, registeredEndpoint, *capacity, *stake); err != nil {
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
		if providerNetwork.DHTService() != nil {
			node.SetDHTService(providerNetwork.DHTService())
			log.Printf("DHT service wired into retrieval node for repair discovery")
		}
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
		agentPrivateKeys, err := loadGatewayAgentKeys(*gatewayAgentKeyFile)
		if err != nil {
			log.Fatalf("load gateway agent keys: %v", err)
		}
		if !*gatewayAllowPrivateKeyAPIKeys && len(agentPrivateKeys) == 0 {
			log.Fatal("-gateway-agent-key-file must contain at least one key when private-key API headers are disabled")
		}
		gwHandler, err = gateway.New(gateway.Config{
			ChainURL:               *chainURL,
			StorageEndpoints:       storageEndpoints,
			TmpDir:                 tmpDir,
			DataShards:             *dataShards,
			ParityShards:           *parityShards,
			SegmentSize:            *segmentSize,
			MaxUploadBytes:         *gatewayMaxUploadBytes,
			AgentPrivateKeys:       agentPrivateKeys,
			AllowPrivateKeyAPIKeys: *gatewayAllowPrivateKeyAPIKeys,
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

	handler := middleware.Chain(
		middleware.CORS(origins),
		middleware.RateLimitWithTrustedProxies(*rateLimitRPS, *rateLimitBurst, splitCSV(*trustedProxies)),
	)(mux)

	log.Printf("retrieval node %s listening on %s", node.Address(), *addr)
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("retrieval node server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("retrieval node: shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	httpServer.Shutdown(shutdownCtx)
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

func loadGatewayAgentKeys(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	keys := map[string]string{}
	if err := json.Unmarshal(raw, &keys); err == nil && len(keys) > 0 {
		return keys, nil
	}
	var single struct {
		AgentKeyID string `json:"agent_key_id"`
		PrivateKey string `json:"private_key"`
	}
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, err
	}
	if single.AgentKeyID == "" || single.PrivateKey == "" {
		return nil, nil
	}
	return map[string]string{single.AgentKeyID: single.PrivateKey}, nil
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
