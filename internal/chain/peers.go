package chain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"chain/internal/wire"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	libp2pquic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
)

const defaultGossipTopic = "storage-chain/devnet"

type BlockBroadcaster interface {
	BroadcastBlock(block wire.Block)
}

type TransactionBroadcaster interface {
	BroadcastTransaction(tx wire.Transaction)
}

type ConsensusVoteBroadcaster interface {
	BroadcastConsensusVote(vote wire.ConsensusVote)
}

type StorageProviderBroadcaster interface {
	BroadcastStorageProvider(announcement wire.StorageProviderAnnouncement)
}

type PeerNetwork struct {
	store       *Store
	peers       []string
	client      *http.Client
	ctx         context.Context
	cancel      context.CancelFunc
	host        host.Host
	pubsub      *pubsub.PubSub
	topic       *pubsub.Topic
	sub         *pubsub.Subscription
	gossipTopic string
	mu          sync.RWMutex
}

type PeerNetworkConfig struct {
	HTTPPeers    string
	LibP2PListen string
	LibP2PPeers  string
	GossipTopic  string
}

type gossipEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func NewPeerNetwork(store *Store, rawPeers string) *PeerNetwork {
	network, err := NewPeerNetworkWithConfig(store, PeerNetworkConfig{HTTPPeers: rawPeers})
	if err != nil {
		log.Printf("peer network init failed: %v", err)
		return &PeerNetwork{
			store:  store,
			peers:  parseHTTPPeers(rawPeers),
			client: &http.Client{Timeout: 5 * time.Second},
		}
	}
	return network
}

func NewPeerNetworkWithConfig(store *Store, cfg PeerNetworkConfig) (*PeerNetwork, error) {
	ctx, cancel := context.WithCancel(context.Background())
	network := &PeerNetwork{
		store:       store,
		peers:       parseHTTPPeers(cfg.HTTPPeers),
		client:      &http.Client{Timeout: 5 * time.Second},
		ctx:         ctx,
		cancel:      cancel,
		gossipTopic: cfg.GossipTopic,
	}
	if network.gossipTopic == "" {
		network.gossipTopic = defaultGossipTopic
	}
	if cfg.LibP2PListen == "" && strings.TrimSpace(cfg.LibP2PPeers) == "" {
		return network, nil
	}
	if cfg.LibP2PListen == "" {
		cfg.LibP2PListen = "/ip4/0.0.0.0/tcp/0"
	}
	if err := network.startLibP2P(cfg.LibP2PListen, cfg.LibP2PPeers); err != nil {
		cancel()
		return nil, err
	}
	return network, nil
}

func parseHTTPPeers(rawPeers string) []string {
	var peers []string
	for _, peer := range strings.Split(rawPeers, ",") {
		peer = strings.TrimRight(strings.TrimSpace(peer), "/")
		if peer != "" {
			peers = append(peers, peer)
		}
	}
	return peers
}

func (p *PeerNetwork) Peers() []string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]string(nil), p.peers...)
}

func (p *PeerNetwork) BroadcastBlock(block wire.Block) {
	if p == nil {
		return
	}
	p.publishGossip("block", block)
	for _, peer := range p.peers {
		peer := peer
		go func() {
			if err := p.postJSON(peer+"/p2p/blocks", block); err != nil {
				log.Printf("broadcast block to %s failed: %v", peer, err)
			}
		}()
	}
}

func (p *PeerNetwork) BroadcastTransaction(tx wire.Transaction) {
	if p == nil {
		return
	}
	p.publishGossip("transaction", tx)
	for _, peer := range p.peers {
		peer := peer
		go func() {
			if err := p.postJSON(peer+"/p2p/txs", tx); err != nil {
				log.Printf("broadcast tx to %s failed: %v", peer, err)
			}
		}()
	}
}

func (p *PeerNetwork) BroadcastConsensusVote(vote wire.ConsensusVote) {
	if p == nil {
		return
	}
	p.publishGossip("consensus_vote", vote)
	for _, peer := range p.peers {
		peer := peer
		go func() {
			if err := p.postJSON(peer+"/consensus/votes", wire.SubmitConsensusVoteRequest{Vote: vote}); err != nil {
				log.Printf("broadcast consensus vote to %s failed: %v", peer, err)
			}
		}()
	}
}

func (p *PeerNetwork) BroadcastStorageProvider(announcement wire.StorageProviderAnnouncement) {
	if p == nil {
		return
	}
	p.publishGossip("storage_provider", announcement)
	for _, peer := range p.peers {
		peer := peer
		go func() {
			if err := p.postJSON(peer+"/storage/providers", announcement); err != nil {
				log.Printf("broadcast storage provider to %s failed: %v", peer, err)
			}
		}()
	}
}

func (p *PeerNetwork) StartBlockSync(interval time.Duration) {
	if p == nil || p.store == nil || len(p.peers) == 0 || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			p.SyncOnce()
			<-ticker.C
		}
	}()
}

func (p *PeerNetwork) Close() error {
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
	if p.host != nil {
		return p.host.Close()
	}
	return nil
}

func (p *PeerNetwork) LibP2PEnabled() bool {
	return p != nil && p.host != nil && p.topic != nil
}

func (p *PeerNetwork) LibP2PAddrs() []string {
	if p == nil || p.host == nil {
		return nil
	}
	addrs := make([]string, 0, len(p.host.Addrs()))
	for _, addr := range p.host.Addrs() {
		addrs = append(addrs, addr.String()+"/p2p/"+p.host.ID().String())
	}
	return addrs
}

func (p *PeerNetwork) LibP2PID() string {
	if p == nil || p.host == nil {
		return ""
	}
	return p.host.ID().String()
}

func (p *PeerNetwork) startLibP2P(listenAddrs string, rawPeers string) error {
	var addrs []string
	for _, addr := range strings.Split(listenAddrs, ",") {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		addrs = []string{"/ip4/0.0.0.0/tcp/0", "/ip4/0.0.0.0/udp/0/quic-v1"}
	}
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(addrs...),
		libp2p.Transport(tcp.NewTCPTransport),
		libp2p.Transport(libp2pquic.NewTransport),
		libp2p.EnableHolePunching(),
		libp2p.EnableNATService(),
	)
	if err != nil {
		return err
	}
	p.host = h
	p.registerChainHandshake()

	ps, err := pubsub.NewGossipSub(p.ctx, h)
	if err != nil {
		_ = h.Close()
		return err
	}
	topic, err := ps.Join(p.gossipTopic)
	if err != nil {
		_ = h.Close()
		return err
	}
	sub, err := topic.Subscribe()
	if err != nil {
		_ = topic.Close()
		_ = h.Close()
		return err
	}
	p.host = h
	p.pubsub = ps
	p.topic = topic
	p.sub = sub
	go p.readGossip()
	p.connectLibP2PPeers(rawPeers)
	return nil
}

func (p *PeerNetwork) connectLibP2PPeers(rawPeers string) {
	if p == nil || p.host == nil {
		return
	}
	for _, rawPeer := range strings.Split(rawPeers, ",") {
		rawPeer = strings.TrimSpace(rawPeer)
		if rawPeer == "" {
			continue
		}
		addr, err := multiaddr.NewMultiaddr(rawPeer)
		if err != nil {
			log.Printf("invalid libp2p peer %s: %v", rawPeer, err)
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			log.Printf("invalid libp2p peer addr info %s: %v", rawPeer, err)
			continue
		}
		if err := p.host.Connect(p.ctx, *info); err != nil {
			log.Printf("connect libp2p peer %s failed: %v", rawPeer, err)
			continue
		}
		log.Printf("connected libp2p peer %s", rawPeer)
	}
}

const chainHandshakeProtocol = "/falari/chain/handshake/1.0.0"

func (p *PeerNetwork) registerChainHandshake() {
	if p == nil || p.host == nil {
		return
	}
	p.host.SetStreamHandler(chainHandshakeProtocol, func(stream network.Stream) {
		defer stream.Close()
		var req struct {
			Address   string `json:"address"`
			PublicKey string `json:"public_key"`
			Signature string `json:"signature"`
		}
		if err := json.NewDecoder(stream).Decode(&req); err != nil {
			return
		}
		_ = req
	})
}

func (p *PeerNetwork) publishGossip(messageType string, value any) {
	if p == nil || p.topic == nil {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		log.Printf("marshal gossip %s failed: %v", messageType, err)
		return
	}
	envelope, err := json.Marshal(gossipEnvelope{Type: messageType, Payload: raw})
	if err != nil {
		log.Printf("marshal gossip envelope %s failed: %v", messageType, err)
		return
	}
	if err := p.topic.Publish(p.ctx, envelope); err != nil {
		log.Printf("publish gossip %s failed: %v", messageType, err)
	}
}

func (p *PeerNetwork) readGossip() {
	for {
		msg, err := p.sub.Next(p.ctx)
		if err != nil {
			return
		}
		if p.host != nil && msg.ReceivedFrom == p.host.ID() {
			continue
		}
		var envelope gossipEnvelope
		if err := json.Unmarshal(msg.Data, &envelope); err != nil {
			log.Printf("decode gossip envelope failed: %v", err)
			continue
		}
		switch envelope.Type {
		case "block":
			var block wire.Block
			if err := json.Unmarshal(envelope.Payload, &block); err != nil {
				log.Printf("decode gossip block failed: %v", err)
				continue
			}
			accepted, err := p.store.AcceptBlock(block)
			if err != nil {
				log.Printf("accept gossip block height=%d hash=%s failed: %v", block.Height, block.Hash, err)
				continue
			}
			if accepted {
				log.Printf("accepted gossip block height=%d hash=%s from=%s", block.Height, block.Hash, msg.ReceivedFrom)
				p.store.SubmitLocalConsensusVotesForBlock(block)
			}
		case "transaction":
			var tx wire.Transaction
			if err := json.Unmarshal(envelope.Payload, &tx); err != nil {
				log.Printf("decode gossip tx failed: %v", err)
				continue
			}
			accepted, err := p.store.AcceptTransaction(tx)
			if err != nil {
				log.Printf("accept gossip tx %s failed: %v", tx.TxID, err)
				continue
			}
			if accepted {
				log.Printf("accepted gossip tx %s from=%s", tx.TxID, msg.ReceivedFrom)
			}
		case "consensus_vote":
			var vote wire.ConsensusVote
			if err := json.Unmarshal(envelope.Payload, &vote); err != nil {
				log.Printf("decode gossip consensus vote failed: %v", err)
				continue
			}
			resp, err := p.store.SubmitConsensusVote(wire.SubmitConsensusVoteRequest{Vote: vote})
			if err != nil {
				log.Printf("accept gossip consensus vote height=%d round=%d type=%s validator=%s failed: %v",
					vote.Height, vote.Round, vote.Type, vote.ValidatorAddress, err)
				continue
			}
			if resp.Accepted {
				log.Printf("accepted gossip consensus vote height=%d round=%d type=%s validator=%s power=%d",
					vote.Height, vote.Round, vote.Type, vote.ValidatorAddress, vote.Power)
			}
			if vote.Type == wire.ConsensusVotePrevote && resp.Prevotes.Finalized {
				p.store.MaybeSubmitLocalConsensusPrecommit(resp.Block)
			}
		case "storage_provider":
			var announcement wire.StorageProviderAnnouncement
			if err := json.Unmarshal(envelope.Payload, &announcement); err != nil {
				log.Printf("decode gossip storage provider failed: %v", err)
				continue
			}
			if err := p.store.AcceptStorageProviderAnnouncement(announcement); err != nil {
				log.Printf("accept gossip storage provider failed: %v", err)
				continue
			}
		default:
			log.Printf("unknown gossip message type %q", envelope.Type)
		}
	}
}

func (p *PeerNetwork) SyncOnce() {
	if p == nil || p.store == nil || len(p.peers) == 0 {
		return
	}
	for _, peer := range p.peers {
		if err := p.syncFromPeer(peer); err != nil {
			log.Printf("sync blocks from %s failed: %v", peer, err)
		}
	}
}

func (p *PeerNetwork) syncFromPeer(peer string) error {
	latest, err := p.getBlock(peer + "/blocks/latest")
	if err != nil {
		return err
	}
	localHeight := p.store.Height()
	if latest.Height <= localHeight {
		return nil
	}
	for height := localHeight + 1; height <= latest.Height; height++ {
		block, err := p.getBlock(fmt.Sprintf("%s/blocks/%d", peer, height))
		if err != nil {
			return err
		}
		accepted, err := p.store.AcceptBlock(block)
		if err != nil {
			return err
		}
		if accepted {
			log.Printf("synced block height=%d hash=%s from=%s", block.Height, block.Hash, peer)
			p.store.SubmitLocalConsensusVotesForBlock(block)
		}
	}
	return nil
}

func (p *PeerNetwork) getBlock(url string) (wire.Block, error) {
	resp, err := p.client.Get(url)
	if err != nil {
		return wire.Block{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		return wire.Block{}, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(body.String()))
	}
	var blockResp wire.BlockResponse
	if err := json.NewDecoder(resp.Body).Decode(&blockResp); err != nil {
		return wire.Block{}, err
	}
	return blockResp.Block, nil
}

func (p *PeerNetwork) postJSON(url string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	resp, err := p.client.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(body.String()))
	}
	return nil
}

func (s *Store) SetBlockBroadcaster(broadcaster BlockBroadcaster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcaster = broadcaster
}

func (s *Store) SetTransactionBroadcaster(broadcaster TransactionBroadcaster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.txBroadcaster = broadcaster
}

func (s *Store) SetConsensusVoteBroadcaster(broadcaster ConsensusVoteBroadcaster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.voteBroadcaster = broadcaster
}

func (s *Store) broadcastBlock(block wire.Block) {
	s.mu.Lock()
	broadcaster := s.broadcaster
	s.mu.Unlock()
	if broadcaster != nil {
		broadcaster.BroadcastBlock(block)
	}
}
