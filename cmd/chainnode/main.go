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
	"chain/internal/middleware"
)

func main() {
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
	validatorKey := flag.String("validator-key", "./data/validator.json", "validator identity file path")
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
	identity, err := chain.LoadOrCreateValidatorIdentity(*validatorKey)
	if err != nil {
		log.Fatalf("load validator identity: %v", err)
	}
	endpoint := *validatorEndpoint
	if endpoint == "" {
		endpoint = "http://localhost" + *addr
	}
	registration, err := identity.RegistrationRequest(endpoint, *validatorStake, *validatorCommissionBPS)
	if err != nil {
		log.Fatalf("sign validator registration: %v", err)
	}
	if _, err := store.RegisterValidator(registration); err != nil {
		log.Fatalf("register validator: %v", err)
	}
	store.SetBlockProducer(identity)
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
	log.Printf("validator %s enabled endpoint=%s stake=%d commission_bps=%d", identity.Address, endpoint, *validatorStake, *validatorCommissionBPS)
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
