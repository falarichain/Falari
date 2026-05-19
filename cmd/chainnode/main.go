package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"chain/internal/chain"
	"chain/internal/wire"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	state := flag.String("state", "./data/chain.json", "state file path")
	epochInterval := flag.Duration("epoch-interval", 0, "automatic proof epoch interval, disabled when 0")
	epochDuration := flag.Duration("epoch-duration", 10*time.Minute, "automatic proof epoch duration")
	epochChallenges := flag.Int("epoch-challenges", 3, "automatic challenges per finalized deal")
	epochReward := flag.Uint64("epoch-reward", 1, "automatic reward per accepted proof")
	epochSlash := flag.Uint64("epoch-slash", 1, "automatic slash per missed proof")
	settleInterval := flag.Duration("settle-interval", 1*time.Minute, "automatic storage intent settlement interval, disabled when 0")
	blockInterval := flag.Duration("block-interval", 5*time.Second, "automatic block production interval, disabled when 0")
	validatorKey := flag.String("validator-key", "./data/validator.json", "validator identity file path")
	validatorEndpoint := flag.String("validator-endpoint", "", "public validator endpoint")
	validatorStake := flag.Uint64("validator-stake", 0, "validator stake locked from its account")
	validatorFaucet := flag.Bool("validator-faucet", true, "request dev faucet funds for validator stake before registration")
	peers := flag.String("peers", "", "comma-separated peer chain node URLs")
	p2pListen := flag.String("p2p-listen", "", "comma-separated libp2p listen multiaddrs, disabled when empty")
	p2pPeers := flag.String("p2p-peers", "", "comma-separated libp2p peer multiaddrs")
	p2pTopic := flag.String("p2p-topic", "storage-chain/devnet", "libp2p gossipsub topic")
	syncInterval := flag.Duration("sync-interval", 5*time.Second, "peer block sync interval, disabled when 0")
	flag.Parse()

	store, err := chain.OpenStore(*state)
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
	if *validatorStake > 0 && *validatorFaucet {
		if _, err := store.Faucet(wire.FaucetRequest{Address: identity.Address, Amount: *validatorStake}); err != nil {
			log.Fatalf("fund validator stake: %v", err)
		}
	}
	registration, err := identity.RegistrationRequest(endpoint, *validatorStake)
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
	log.Printf("validator %s enabled endpoint=%s stake=%d", identity.Address, endpoint, *validatorStake)
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
	store.StartBlockProducer(*blockInterval)
	network.StartBlockSync(*syncInterval)

	server := chain.NewServer(store, network)
	log.Printf("chain node listening on %s", *addr)
	if err := http.ListenAndServe(*addr, server.Routes()); err != nil {
		log.Fatal(err)
	}
}
