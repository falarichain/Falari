package dht

import (
	"context"
	"encoding/hex"
	"log"
	"sync"
	"time"

	cid "github.com/ipfs/go-cid"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
)

const (
	DefaultProtocolPrefix      = "/falari/kad/1.0.0"
	DefaultRepublishInterval   = 60 * time.Second
	DefaultProviderTTL         = 5 * time.Minute
	DefaultLookupTimeout       = 10 * time.Second
	DefaultCacheTTL            = 30 * time.Second
	DefaultCacheCleanupInterval = 2 * time.Minute
	DefaultBootstrapInterval   = 60 * time.Second
	DefaultBlacklistSyncInterval = 30 * time.Second
)

// Config holds DHT service configuration.
type Config struct {
	// ProtocolPrefix is the DHT protocol namespace (default: /falari/kad/1.0.0).
	ProtocolPrefix string
	// RepublishInterval is how often to re-publish all shard records.
	RepublishInterval time.Duration
	// ProviderTTL is the DHT provider record time-to-live.
	ProviderTTL time.Duration
	// LookupTimeout is the max time for a FindProviders query.
	LookupTimeout time.Duration
	// CacheTTL is how long to cache lookup results.
	CacheTTL time.Duration
	// BootstrapPeers are optional initial peer multiaddrs for DHT bootstrap.
	BootstrapPeers []string
	// ChainURL is the chain node URL for fetching bootstrap peers and blacklist.
	ChainURL string
	// BlacklistSyncInterval is how often to sync the blacklist from the chain.
	BlacklistSyncInterval time.Duration
}

// DefaultConfig returns sensible defaults for the DHT service.
func DefaultConfig() Config {
	return Config{
		ProtocolPrefix:        DefaultProtocolPrefix,
		RepublishInterval:     DefaultRepublishInterval,
		ProviderTTL:           DefaultProviderTTL,
		LookupTimeout:         DefaultLookupTimeout,
		CacheTTL:              DefaultCacheTTL,
		BlacklistSyncInterval: DefaultBlacklistSyncInterval,
	}
}

// Service wraps a Kademlia DHT instance for Falari shard discovery.
type Service struct {
	host   host.Host
	dht    *dht.IpfsDHT
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	blacklist *BlacklistCache
	cache     *ProviderCache

	mu         sync.RWMutex
	shardIndex map[string]struct{} // set of shard hashes this node holds
}

// New creates a new DHT service attached to the given libp2p host.
func New(h host.Host, cfg Config) (*Service, error) {
	if cfg.ProtocolPrefix == "" {
		cfg.ProtocolPrefix = DefaultProtocolPrefix
	}
	if cfg.RepublishInterval == 0 {
		cfg.RepublishInterval = DefaultRepublishInterval
	}
	if cfg.ProviderTTL == 0 {
		cfg.ProviderTTL = DefaultProviderTTL
	}
	if cfg.LookupTimeout == 0 {
		cfg.LookupTimeout = DefaultLookupTimeout
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = DefaultCacheTTL
	}
	if cfg.BlacklistSyncInterval == 0 {
		cfg.BlacklistSyncInterval = DefaultBlacklistSyncInterval
	}

	ctx, cancel := context.WithCancel(context.Background())

	opts := []dht.Option{
		dht.Mode(dht.ModeServer),
		dht.ProtocolPrefix(cfg.protocolID()),
		dht.RoutingTableRefreshPeriod(30 * time.Second),
		dht.RoutingTableRefreshQueryTimeout(5 * time.Second),
	}

	// Configure bootstrap peers if provided.
	if len(cfg.BootstrapPeers) > 0 {
		var bootstrappers []peer.AddrInfo
		for _, rawAddr := range cfg.BootstrapPeers {
			addr, err := multiaddr.NewMultiaddr(rawAddr)
			if err != nil {
				log.Printf("dht: invalid bootstrap peer %s: %v", rawAddr, err)
				continue
			}
			info, err := peer.AddrInfoFromP2pAddr(addr)
			if err != nil {
				log.Printf("dht: invalid bootstrap peer addr %s: %v", rawAddr, err)
				continue
			}
			bootstrappers = append(bootstrappers, *info)
		}
		if len(bootstrappers) > 0 {
			opts = append(opts, dht.BootstrapPeers(bootstrappers...))
		}
	}

	d, err := dht.New(ctx, h, opts...)
	if err != nil {
		cancel()
		return nil, err
	}

	return &Service{
		host:       h,
		dht:        d,
		cfg:        cfg,
		ctx:        ctx,
		cancel:     cancel,
		blacklist:  NewBlacklistCache(),
		cache:      NewProviderCache(cfg.CacheTTL),
		shardIndex: make(map[string]struct{}),
	}, nil
}

func (cfg Config) protocolID() protocol.ID {
	return protocol.ID(cfg.ProtocolPrefix)
}

// Start bootstraps the DHT and begins background loops.
func (s *Service) Start() error {
	if err := s.dht.Bootstrap(s.ctx); err != nil {
		log.Printf("dht: initial bootstrap failed: %v", err)
	}
	go s.bootstrapLoop()
	s.cache.StartCleanupLoop(s.ctx, DefaultCacheCleanupInterval)
	go s.blacklist.SyncLoop(s.ctx, s.cfg.ChainURL, s.cfg.BlacklistSyncInterval)
	return nil
}

// bootstrapLoop periodically refreshes the DHT routing table.
func (s *Service) bootstrapLoop() {
	ticker := time.NewTicker(DefaultBootstrapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.dht.Bootstrap(s.ctx); err != nil {
				log.Printf("dht: bootstrap refresh failed: %v", err)
			}
		}
	}
}

// Close shuts down the DHT service.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.cancel()
	if s.dht != nil {
		return s.dht.Close()
	}
	return nil
}

// PeerID returns the local peer ID.
func (s *Service) PeerID() string {
	if s == nil || s.host == nil {
		return ""
	}
	return s.host.ID().String()
}

// PeerAddrs returns the local peer multiaddrs.
func (s *Service) PeerAddrs() []string {
	if s == nil || s.host == nil {
		return nil
	}
	addrs := make([]string, 0, len(s.host.Addrs()))
	for _, addr := range s.host.Addrs() {
		addrs = append(addrs, addr.String()+"/p2p/"+s.host.ID().String())
	}
	return addrs
}

// RoutingTableSize returns the number of peers in the DHT routing table.
func (s *Service) RoutingTableSize() int {
	if s == nil || s.dht == nil {
		return 0
	}
	return s.dht.RoutingTable().Size()
}

// Blacklist returns the blacklist cache for external access.
func (s *Service) Blacklist() *BlacklistCache {
	if s == nil {
		return nil
	}
	return s.blacklist
}

// AddShard registers a shard hash as held by this node.
func (s *Service) AddShard(shardHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shardIndex[shardHash] = struct{}{}
}

// RemoveShard unregisters a shard hash.
func (s *Service) RemoveShard(shardHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.shardIndex, shardHash)
}

// ShardCount returns the number of shards held by this node.
func (s *Service) ShardCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.shardIndex)
}

// shardHashToCID converts a hex-encoded shard hash to a CID for DHT provider records.
// The input is always treated as plain hex (raw bytes), then encoded as a SHA2-256
// multihash with raw CID codec. This avoids ambiguity between multihash-prefixed
// and plain hex formats.
func shardHashToCID(shardHash string) (cid.Cid, error) {
	raw, err := hex.DecodeString(shardHash)
	if err != nil {
		return cid.Cid{}, err
	}
	mh, err := multihash.Encode(raw, multihash.SHA2_256)
	if err != nil {
		return cid.Cid{}, err
	}
	return cid.NewCidV1(cid.Raw, mh), nil
}
