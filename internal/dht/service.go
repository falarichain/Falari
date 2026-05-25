package dht

import (
	"context"
	"errors"
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
	DefaultProtocolPrefix  = "/falari"
	DefaultRepublishInterval = 60 * time.Second
	DefaultProviderTTL       = 5 * time.Minute
	DefaultLookupTimeout     = 10 * time.Second
	DefaultCacheTTL          = 30 * time.Second
	DefaultBootstrapInterval = 60 * time.Second
)

// Config holds DHT service configuration.
type Config struct {
	// ProtocolPrefix is the DHT protocol namespace (default: /falari).
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
}

// DefaultConfig returns sensible defaults for the DHT service.
func DefaultConfig() Config {
	return Config{
		ProtocolPrefix:    DefaultProtocolPrefix,
		RepublishInterval: DefaultRepublishInterval,
		ProviderTTL:       DefaultProviderTTL,
		LookupTimeout:     DefaultLookupTimeout,
		CacheTTL:          DefaultCacheTTL,
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

	mu          sync.RWMutex
	shardIndex  map[string]struct{} // set of shard hashes this node holds
	chainMiners map[string]peerInfo // cached chain-registered miners for bootstrap
}

type peerInfo struct {
	peerID    string
	peerAddrs []string
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
		host:        h,
		dht:         d,
		cfg:         cfg,
		ctx:         ctx,
		cancel:      cancel,
		blacklist:   NewBlacklistCache(),
		cache:       NewProviderCache(cfg.CacheTTL),
		shardIndex:  make(map[string]struct{}),
		chainMiners: make(map[string]peerInfo),
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
	go s.blacklist.SyncLoop(s.ctx, s.cfg.ChainURL, 30*time.Second)
	return nil
}

// bootstrapLoop periodically re-bootstraps from chain-registered miners.
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
// Uses SHA2-256 multihash code with raw codec.
func shardHashToCID(shardHash string) (cid.Cid, error) {
	decoded, err := multihash.FromHexString(shardHash)
	if err != nil {
		// Try as plain hex (without multihash prefix).
		raw, hexErr := decodeHex(shardHash)
		if hexErr != nil {
			return cid.Cid{}, err
		}
		mh, mhErr := multihash.Encode(raw, multihash.SHA2_256)
		if mhErr != nil {
			return cid.Cid{}, mhErr
		}
		decoded = mh
	}
	return cid.NewCidV1(cid.Raw, decoded), nil
}

func decodeHex(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("odd-length hex string")
	}
	b := make([]byte, len(s)/2)
	for i := 0; i < len(b); i++ {
		hi := unhex(s[2*i])
		lo := unhex(s[2*i+1])
		if hi == 0xFF || lo == 0xFF {
			return nil, errors.New("invalid hex character")
		}
		b[i] = hi<<4 | lo
	}
	return b, nil
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10
	}
	return 0xFF
}
