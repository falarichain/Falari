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

	"chain/internal/config"
	falaridht "chain/internal/dht"
	"chain/internal/middleware"
	"chain/internal/storage"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file (flags override config values)")
	addr := flag.String("addr", ":9090", "HTTP listen address")
	data := flag.String("data", "./data/miner1", "storage data directory")
	chainURL := flag.String("chain", "", "optional chain node URL for miner registration")
	endpoint := flag.String("endpoint", "", "public storage endpoint registered on chain")
	capacity := flag.Uint64("capacity", 1<<40, "declared storage capacity in bytes")
	stake := flag.Uint64("stake", 1000, "declared miner stake")
	autoProve := flag.Bool("auto-prove", true, "poll chain node and automatically submit assigned storage proofs")
	proveInterval := flag.Duration("prove-interval", 2*time.Second, "automatic proof polling interval")
	autoRepair := flag.Bool("auto-repair", true, "poll chain node and automatically execute assigned repair tasks")
	repairInterval := flag.Duration("repair-interval", 10*time.Second, "automatic repair polling interval")
	autoDelete := flag.Bool("auto-delete", true, "poll chain node and automatically execute assigned delete tasks")
	deleteInterval := flag.Duration("delete-interval", 10*time.Second, "automatic delete polling interval")
	p2pListen := flag.String("p2p-listen", "", "comma-separated libp2p provider discovery listen multiaddrs, disabled when empty")
	p2pPeers := flag.String("p2p-peers", "", "comma-separated libp2p provider peer multiaddrs")
	p2pTopic := flag.String("p2p-topic", "storage-chain/providers/devnet", "libp2p provider discovery topic")
	dhtEnabled := flag.Bool("dht", false, "enable Kademlia DHT for shard discovery")
	dhtBootstrap := flag.String("dht-bootstrap", "", "comma-separated DHT bootstrap peer multiaddrs")
	dhtNamespace := flag.String("dht-namespace", falaridht.DefaultProtocolPrefix, "DHT protocol namespace prefix")
	dhtRepublish := flag.Duration("dht-republish", falaridht.DefaultRepublishInterval, "DHT provider record republish interval")
	corsOrigins := flag.String("cors-origins", "", "comma-separated allowed CORS origins (empty disables CORS)")
	rateLimitRPS := flag.Float64("rate-limit-rps", 0, "per-IP request rate limit (requests/sec, 0 disables)")
	rateLimitBurst := flag.Int("rate-limit-burst", 0, "rate limit burst size (default: rps+1)")
	trustedProxies := flag.String("trusted-proxies", "", "comma-separated trusted proxy CIDRs/IPs for X-Forwarded-For")
	production := flag.Bool("production", false, "enable production mode with strict safety checks")
	flag.Parse()

	// Load YAML config and apply flag overrides (flag > config > default).
	if *configPath != "" {
		var cfg config.StorageNodeConfig
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
		if !config.IsFlagSet("auto-prove") {
			*autoProve = cfg.AutoProve.Enabled
		}
		if !config.IsFlagSet("prove-interval") && cfg.AutoProve.Interval.Duration() != 0 {
			*proveInterval = cfg.AutoProve.Interval.Duration()
		}
		if !config.IsFlagSet("auto-repair") {
			*autoRepair = cfg.AutoRepair.Enabled
		}
		if !config.IsFlagSet("repair-interval") && cfg.AutoRepair.Interval.Duration() != 0 {
			*repairInterval = cfg.AutoRepair.Interval.Duration()
		}
		if !config.IsFlagSet("auto-delete") {
			*autoDelete = cfg.AutoDelete.Enabled
		}
		if !config.IsFlagSet("delete-interval") && cfg.AutoDelete.Interval.Duration() != 0 {
			*deleteInterval = cfg.AutoDelete.Interval.Duration()
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
		log.Fatalf("open storage node: %v", err)
	}
	registeredEndpoint := *endpoint
	if registeredEndpoint == "" {
		registeredEndpoint = "http://localhost" + *addr
	}
	node.ConfigureChain(*chainURL, registeredEndpoint)
	node.RequireChainAuthorization(*production)
	if *chainURL != "" {
		if err := node.Register(*chainURL, registeredEndpoint, *capacity, *stake); err != nil {
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
			log.Fatalf("start storage provider discovery: %v", err)
		}
		defer providerNetwork.Close()
		log.Printf("provider discovery enabled peer_id=%s addrs=%v topic=%s dht=%v", providerNetwork.PeerID(), providerNetwork.Addrs(), *p2pTopic, *dhtEnabled)
		if providerNetwork.DHTService() != nil {
			node.SetDHTService(providerNetwork.DHTService())
			log.Printf("DHT service wired into storage node for repair discovery")
		}
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
	handler := middleware.Chain(
		middleware.CORS(origins),
		middleware.RateLimitWithTrustedProxies(*rateLimitRPS, *rateLimitBurst, parseCSV(*trustedProxies)),
	)(server.Routes())
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

func parseCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	var values []string
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}
