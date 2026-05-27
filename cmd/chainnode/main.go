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

	"chain/internal/chain"
	"chain/internal/config"
	"chain/internal/middleware"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file (flags override config values)")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	state := flag.String("state", "./data/chain.json", "state file path")
	genesis := flag.String("genesis", "", "genesis file path (applied only on first start)")
	epochInterval := flag.Duration("epoch-interval", 0, "automatic proof epoch interval, disabled when 0")
	epochDuration := flag.Duration("epoch-duration", 10*time.Minute, "automatic proof epoch duration")
	epochChallenges := flag.Int("epoch-challenges", 3, "automatic challenges per finalized deal")
	epochReward := flag.Uint64("epoch-reward", 1, "automatic reward per accepted proof")
	epochSlash := flag.Uint64("epoch-slash", 1, "automatic slash per missed proof")
	settleInterval := flag.Duration("settle-interval", 1*time.Minute, "automatic storage intent settlement interval, disabled when 0")
	renewInterval := flag.Duration("renew-interval", 1*time.Minute, "automatic renewable deal renewal interval, disabled when 0")
	blockInterval := flag.Duration("block-interval", 5*time.Second, "automatic block production interval, disabled when 0")
	validatorEndpoint := flag.String("validator-endpoint", "", "public validator endpoint")
	validatorStake := flag.Uint64("validator-stake", chain.MinValidatorStake, "validator stake locked from its account")
	validatorCommissionBPS := flag.Uint64("validator-commission-bps", 0, "validator commission rate in basis points (0 = use global default)")
	peers := flag.String("peers", "", "comma-separated peer chain node URLs")
	p2pListen := flag.String("p2p-listen", "", "comma-separated libp2p listen multiaddrs, disabled when empty")
	p2pPeers := flag.String("p2p-peers", "", "comma-separated libp2p peer multiaddrs")
	p2pTopic := flag.String("p2p-topic", "storage-chain/devnet", "libp2p gossipsub topic")
	syncInterval := flag.Duration("sync-interval", 5*time.Second, "peer block sync interval, disabled when 0")
	corsOrigins := flag.String("cors-origins", "", "comma-separated allowed CORS origins (empty disables CORS)")
	rateLimitRPS := flag.Float64("rate-limit-rps", 0, "per-IP request rate limit (requests/sec, 0 disables)")
	rateLimitBurst := flag.Int("rate-limit-burst", 0, "rate limit burst size (default: rps+1)")
	trustedProxies := flag.String("trusted-proxies", "", "comma-separated trusted proxy CIDRs/IPs for X-Forwarded-For")
	production := flag.Bool("production", false, "enable production mode with strict safety checks")
	flag.Parse()

	// Load YAML config and apply flag overrides (flag > config > default).
	if *configPath != "" {
		var cfg config.ChainNodeConfig
		if err := config.Load(*configPath, &cfg); err != nil {
			log.Fatalf("load config: %v", err)
		}
		if !config.IsFlagSet("addr") && cfg.HTTP.Addr != "" {
			*addr = cfg.HTTP.Addr
		}
		if !config.IsFlagSet("state") && cfg.State != "" {
			*state = cfg.State
		}
		if !config.IsFlagSet("genesis") && cfg.Genesis != "" {
			*genesis = cfg.Genesis
		}
		if !config.IsFlagSet("epoch-interval") && cfg.Epoch.Interval.Duration() != 0 {
			*epochInterval = cfg.Epoch.Interval.Duration()
		}
		if !config.IsFlagSet("epoch-duration") && cfg.Epoch.Duration.Duration() != 0 {
			*epochDuration = cfg.Epoch.Duration.Duration()
		}
		if !config.IsFlagSet("epoch-challenges") && cfg.Epoch.Challenges != 0 {
			*epochChallenges = cfg.Epoch.Challenges
		}
		if !config.IsFlagSet("epoch-reward") && cfg.Epoch.Reward != 0 {
			*epochReward = cfg.Epoch.Reward
		}
		if !config.IsFlagSet("epoch-slash") && cfg.Epoch.Slash != 0 {
			*epochSlash = cfg.Epoch.Slash
		}
		if !config.IsFlagSet("settle-interval") && cfg.Settle.Duration() != 0 {
			*settleInterval = cfg.Settle.Duration()
		}
		if !config.IsFlagSet("renew-interval") && cfg.Renew.Duration() != 0 {
			*renewInterval = cfg.Renew.Duration()
		}
		if !config.IsFlagSet("block-interval") && cfg.Block.Duration() != 0 {
			*blockInterval = cfg.Block.Duration()
		}
		if !config.IsFlagSet("validator-endpoint") && cfg.Validator.Endpoint != "" {
			*validatorEndpoint = cfg.Validator.Endpoint
		}
		if !config.IsFlagSet("validator-stake") && cfg.Validator.Stake != 0 {
			*validatorStake = cfg.Validator.Stake
		}
		if !config.IsFlagSet("validator-commission-bps") && cfg.Validator.CommissionBPS != 0 {
			*validatorCommissionBPS = cfg.Validator.CommissionBPS
		}
		if !config.IsFlagSet("peers") && cfg.Peers != "" {
			*peers = cfg.Peers
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
		if !config.IsFlagSet("sync-interval") && cfg.Sync.Duration() != 0 {
			*syncInterval = cfg.Sync.Duration()
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
		if *p2pTopic == "storage-chain/devnet" || *p2pTopic == "storage-chain/providers/devnet" {
			errs = append(errs, "production mode requires a non-default --p2p-topic")
		}
		if *validatorEndpoint == "" {
			errs = append(errs, "production mode requires explicit --validator-endpoint")
		}
		if *validatorStake < chain.MinValidatorStake {
			errs = append(errs, "production mode requires --validator-stake >= 1000000 GF")
		}
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
	proxies := parseCSV(*trustedProxies)

	store, err := chain.OpenStoreWithGenesis(*state, *genesis)
	if err != nil {
		log.Fatalf("open chain state: %v", err)
	}
	defer store.Close()
	identity, err := chain.LoadOperatorIdentityFromEnv()
	if err != nil {
		log.Fatalf("load operator identity: %v", err)
	}
	endpoint := *validatorEndpoint
	if endpoint == "" {
		endpoint = "http://localhost" + *addr
	}
	registration, err := identity.RegistrationRequest(store.ChainID(), store.AccountNonce(identity.OwnerAddress), endpoint, *validatorStake, *validatorCommissionBPS)
	if err != nil {
		log.Fatalf("sign validator registration: %v", err)
	}
	if _, err := store.RegisterValidator(registration); err != nil {
		log.Fatalf("register validator: %v", err)
	}
	store.SetOperatorIdentity(identity)
	network, err := chain.NewPeerNetworkWithConfig(store, chain.PeerNetworkConfig{
		HTTPPeers:    *peers,
		LibP2PListen: *p2pListen,
		LibP2PPeers:  *p2pPeers,
		GossipTopic:  *p2pTopic,
	})
	if err != nil {
		log.Fatalf("create peer network: %v", err)
	}
	defer network.Close()
	store.SetBlockBroadcaster(network)
	store.SetTransactionBroadcaster(network)
	store.SetConsensusVoteBroadcaster(network)
	log.Printf("validator %s enabled endpoint=%s stake=%d commission_bps=%d", identity.OwnerAddress, endpoint, *validatorStake, *validatorCommissionBPS)
	if len(network.Peers()) > 0 {
		log.Printf("peer network enabled peers=%v", network.Peers())
	}
	if network.LibP2PEnabled() {
		log.Printf("libp2p enabled peer_id=%s addrs=%v topic=%s", network.LibP2PID(), network.LibP2PAddrs(), *p2pTopic)
	}
	store.StartEpochScheduler(chain.EpochSchedulerConfig{
		Interval:          *epochInterval,
		Duration:          *epochDuration,
		ChallengesPerDeal: *epochChallenges,
		RewardPerProof:    *epochReward,
		SlashPerMissed:    *epochSlash,
	})
	store.StartIntentSettlementScheduler(chain.IntentSettlementSchedulerConfig{Interval: *settleInterval})
	store.StartAutoRenewScheduler(*renewInterval)
	store.SetBlockInterval(*blockInterval)
	store.StartBlockProducer(*blockInterval)
	store.StartBlockTimeoutChecker(*blockInterval)
	network.StartBlockSync(*syncInterval)

	server := chain.NewServer(store, network)
	handler := middleware.Chain(
		middleware.CORS(origins),
		middleware.RateLimitWithTrustedProxies(*rateLimitRPS, *rateLimitBurst, proxies),
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
		log.Printf("chain node listening on %s", *addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("chain node server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("chain node: shutting down...")

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
