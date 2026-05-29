package storage

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	falaridht "chain/internal/dht"
	"chain/internal/wire"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	host "github.com/libp2p/go-libp2p/core/host"
	peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const defaultProviderTopic = "storage-chain/providers/devnet"

type ProviderNetwork struct {
	node          *Node
	endpoint      string
	capacityBytes uint64
	ttl           time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
	host          host.Host
	topic         *pubsub.Topic
	sub           *pubsub.Subscription
	mu            sync.RWMutex
	providers     map[string]wire.StorageProviderRecord
	dhtService    *falaridht.Service
}

func StartProviderNetwork(node *Node, listenAddrs string, rawPeers string, topicName string, endpoint string, capacityBytes uint64) (*ProviderNetwork, error) {
	return StartProviderNetworkWithDHT(node, listenAddrs, rawPeers, topicName, endpoint, capacityBytes, nil)
}

// StartProviderNetworkWithDHT creates a provider network with optional DHT support.
// If dhtCfg is non-nil, DHT-based discovery is enabled alongside GossipSub.
func StartProviderNetworkWithDHT(node *Node, listenAddrs string, rawPeers string, topicName string, endpoint string, capacityBytes uint64, dhtCfg *falaridht.Config) (*ProviderNetwork, error) {
	if strings.TrimSpace(listenAddrs) == "" {
		listenAddrs = "/ip4/0.0.0.0/tcp/0"
	}
	if topicName == "" {
		topicName = defaultProviderTopic
	}
	ctx, cancel := context.WithCancel(context.Background())
	addrs := splitCSV(listenAddrs)
	if len(addrs) == 0 {
		addrs = []string{"/ip4/0.0.0.0/tcp/0"}
	}
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(addrs...),
		libp2p.EnableHolePunching(),
	)
	if err != nil {
		cancel()
		return nil, err
	}
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		cancel()
		_ = h.Close()
		return nil, err
	}
	topic, err := ps.Join(topicName)
	if err != nil {
		cancel()
		_ = h.Close()
		return nil, err
	}
	sub, err := topic.Subscribe()
	if err != nil {
		cancel()
		_ = topic.Close()
		_ = h.Close()
		return nil, err
	}
	network := &ProviderNetwork{
		node:          node,
		endpoint:      endpoint,
		capacityBytes: capacityBytes,
		ttl:           2 * time.Minute,
		ctx:           ctx,
		cancel:        cancel,
		host:          h,
		topic:         topic,
		sub:           sub,
		providers:     map[string]wire.StorageProviderRecord{},
	}
	network.host.SetStreamHandler(blockProtocolID, network.handleBlockStream)

	// Start DHT service if configured.
	if dhtCfg != nil {
		dhtService, dhtErr := falaridht.New(h, *dhtCfg)
		if dhtErr != nil {
			log.Printf("provider: DHT init failed (continuing without DHT): %v", dhtErr)
		} else {
			network.dhtService = dhtService
			if err := dhtService.Start(); err != nil {
				log.Printf("provider: DHT start failed: %v", err)
			} else {
				log.Printf("provider: DHT enabled (protocol=%s)", dhtCfg.ProtocolPrefix)
				// Start DHT publish loop.
				dhtService.StartPublishLoop(func(shardHash string) (wire.DHTProviderRecord, error) {
					return network.buildDHTRecord(shardHash)
				})
			}
		}
	}

	go network.readLoop()
	network.connectPeers(rawPeers)
	network.AnnounceOnce()
	go network.announceLoop()
	return network, nil
}

// DHTService returns the DHT service (may be nil if DHT is not enabled).
func (p *ProviderNetwork) DHTService() *falaridht.Service {
	if p == nil {
		return nil
	}
	return p.dhtService
}

func (p *ProviderNetwork) Close() error {
	if p == nil {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
	}
	if p.sub != nil {
		p.sub.Cancel()
	}
	if p.topic != nil {
		_ = p.topic.Close()
	}
	if p.dhtService != nil {
		_ = p.dhtService.Close()
	}
	if p.host != nil {
		return p.host.Close()
	}
	return nil
}

func (p *ProviderNetwork) PeerID() string {
	if p == nil || p.host == nil {
		return ""
	}
	return p.host.ID().String()
}

func (p *ProviderNetwork) Addrs() []string {
	if p == nil || p.host == nil {
		return nil
	}
	addrs := make([]string, 0, len(p.host.Addrs()))
	for _, addr := range p.host.Addrs() {
		addrs = append(addrs, addr.String()+"/p2p/"+p.host.ID().String())
	}
	return addrs
}

func (p *ProviderNetwork) Providers(shardHash string) []wire.StorageProviderRecord {
	if p == nil {
		return nil
	}
	now := time.Now().Unix()
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]wire.StorageProviderRecord, 0, len(p.providers))
	for _, provider := range p.providers {
		if provider.ExpiresAtUnix > 0 && provider.ExpiresAtUnix < now {
			continue
		}
		if shardHash != "" && !providerHasShard(provider, shardHash) {
			continue
		}
		out = append(out, provider)
	}
	return out
}

func (p *ProviderNetwork) AnnounceOnce() {
	if p == nil || p.topic == nil {
		return
	}
	record, err := p.node.ProviderRecord(p.endpoint, p.capacityBytes, p.PeerID(), p.Addrs(), p.ttl)
	if err != nil {
		log.Printf("provider announce build failed: %v", err)
		return
	}
	p.storeProvider(record)
	raw, err := json.Marshal(wire.StorageProviderAnnouncement{Provider: record})
	if err != nil {
		log.Printf("provider announce marshal failed: %v", err)
		return
	}
	if err := p.topic.Publish(p.ctx, raw); err != nil {
		log.Printf("provider announce publish failed: %v", err)
	}
	// Also publish to DHT if available.
	if p.dhtService != nil {
		// Collect unique shard hashes from both sources to avoid duplicate publishes.
		seen := make(map[string]struct{})
		for _, shard := range record.Shards {
			seen[shard.ShardHash] = struct{}{}
		}
		for _, hash := range record.ShardHashes {
			seen[hash] = struct{}{}
		}
		for hash := range seen {
			dhtRecord, buildErr := p.buildDHTRecord(hash)
			if buildErr != nil {
				continue
			}
			if pubErr := p.dhtService.PublishShard(dhtRecord); pubErr != nil {
				log.Printf("provider: DHT publish shard %s failed: %v", hash, pubErr)
			}
		}
	}
}

func (p *ProviderNetwork) announceLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.AnnounceOnce()
		}
	}
}

func (p *ProviderNetwork) readLoop() {
	for {
		msg, err := p.sub.Next(p.ctx)
		if err != nil {
			return
		}
		if p.host != nil && msg.ReceivedFrom == p.host.ID() {
			continue
		}
		var announcement wire.StorageProviderAnnouncement
		if err := json.Unmarshal(msg.Data, &announcement); err != nil {
			log.Printf("provider announce decode failed: %v", err)
			continue
		}
		if err := wire.VerifyStorageProvider(announcement.Provider); err != nil {
			log.Printf("provider announce verify failed: %v", err)
			continue
		}
		p.storeProvider(announcement.Provider)
	}
}

func (p *ProviderNetwork) storeProvider(record wire.StorageProviderRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.providers[record.MinerAddress] = record
}

func (p *ProviderNetwork) connectPeers(rawPeers string) {
	for _, rawPeer := range splitCSV(rawPeers) {
		addr, err := multiaddr.NewMultiaddr(rawPeer)
		if err != nil {
			log.Printf("invalid provider peer %s: %v", rawPeer, err)
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			log.Printf("invalid provider peer addr info %s: %v", rawPeer, err)
			continue
		}
		if err := p.host.Connect(p.ctx, *info); err != nil {
			log.Printf("connect provider peer %s failed: %v", rawPeer, err)
		}
	}
}

// buildDHTRecord constructs a DHT provider record for a shard.
func (p *ProviderNetwork) buildDHTRecord(shardHash string) (wire.DHTProviderRecord, error) {
	record := wire.DHTProviderRecord{
		MinerAddress:   p.node.Address(),
		PublicKey:      p.node.PublicKeyHex(),
		Endpoint:       p.endpoint,
		PeerID:         p.PeerID(),
		PeerAddrs:      p.Addrs(),
		ShardHash:      shardHash,
		HealthScoreBPS: p.node.HealthScoreBPS(),
		ExpiresAtUnix:  time.Now().Add(falaridht.DefaultProviderTTL).Unix(),
	}
	if err := wire.SignDHTProvider(&record, p.node.PrivateKey()); err != nil {
		return wire.DHTProviderRecord{}, err
	}
	return record, nil
}

func splitCSV(raw string) []string {
	var out []string
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func providerHasShard(provider wire.StorageProviderRecord, shardHash string) bool {
	for _, hash := range provider.ShardHashes {
		if hash == shardHash {
			return true
		}
	}
	for _, shard := range provider.Shards {
		if shard.ShardHash == shardHash {
			return true
		}
	}
	return false
}
