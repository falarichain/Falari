package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	libp2p "github.com/libp2p/go-libp2p"
	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	protocol "github.com/libp2p/go-libp2p/core/protocol"
	libp2pquic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
)

const blockProtocolID = protocol.ID("/falari/storage/block/1.0.0")

type blockRequest struct {
	CID string `json:"cid"`
}

type blockResponse struct {
	CID        string `json:"cid,omitempty"`
	DataBase64 string `json:"data_base64,omitempty"`
	Error      string `json:"error,omitempty"`
}

type BlockTransportClient struct {
	host host.Host
}

var (
	defaultBlockClientOnce sync.Once
	defaultBlockClient     *BlockTransportClient
	defaultBlockClientErr  error
)

func (p *ProviderNetwork) handleBlockStream(stream network.Stream) {
	defer stream.Close()

	var req blockRequest
	if err := json.NewDecoder(stream).Decode(&req); err != nil {
		_ = json.NewEncoder(stream).Encode(blockResponse{Error: err.Error()})
		return
	}
	if strings.TrimSpace(req.CID) == "" {
		_ = json.NewEncoder(stream).Encode(blockResponse{Error: "cid is required"})
		return
	}
	data, err := p.node.ReadShardByCID(req.CID)
	if err != nil {
		_ = json.NewEncoder(stream).Encode(blockResponse{Error: err.Error()})
		return
	}
	p.node.recordLibP2PServeHit()
	_ = json.NewEncoder(stream).Encode(blockResponse{
		CID:        req.CID,
		DataBase64: base64.StdEncoding.EncodeToString(data),
	})
}

func FetchBlockViaLibP2P(ctx context.Context, cid string, peerID string, peerAddrs []string) ([]byte, error) {
	client, err := DefaultBlockTransportClient()
	if err != nil {
		return nil, err
	}
	return client.FetchBlock(ctx, cid, peerID, peerAddrs)
}

func DefaultBlockTransportClient() (*BlockTransportClient, error) {
	defaultBlockClientOnce.Do(func() {
		var hostInstance host.Host
		hostInstance, defaultBlockClientErr = libp2p.New(
			libp2p.Transport(tcp.NewTCPTransport),
			libp2p.Transport(libp2pquic.NewTransport),
			libp2p.EnableHolePunching(),
		)
		if defaultBlockClientErr == nil {
			defaultBlockClient = &BlockTransportClient{host: hostInstance}
		}
	})
	return defaultBlockClient, defaultBlockClientErr
}

func (c *BlockTransportClient) Close() error {
	if c == nil || c.host == nil {
		return nil
	}
	return c.host.Close()
}

func (c *BlockTransportClient) HostID() string {
	if c == nil || c.host == nil {
		return ""
	}
	return c.host.ID().String()
}

func (c *BlockTransportClient) FetchBlock(ctx context.Context, cid string, peerID string, peerAddrs []string) ([]byte, error) {
	if strings.TrimSpace(cid) == "" {
		return nil, errors.New("cid is required")
	}
	if c == nil || c.host == nil {
		return nil, errors.New("block transport client is not initialized")
	}
	info, err := peerAddrInfo(peerID, peerAddrs)
	if err != nil {
		return nil, err
	}
	if connectErr := c.host.Connect(ctx, *info); connectErr != nil {
		return nil, connectErr
	}
	stream, err := c.host.NewStream(ctx, info.ID, blockProtocolID)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	if err := json.NewEncoder(stream).Encode(blockRequest{CID: cid}); err != nil {
		return nil, err
	}
	if closer, ok := any(stream).(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
	var resp blockResponse
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty libp2p block response")
		}
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return base64.StdEncoding.DecodeString(resp.DataBase64)
}

func peerAddrInfo(peerID string, peerAddrs []string) (*peer.AddrInfo, error) {
	id, err := peer.Decode(peerID)
	if err != nil {
		return nil, err
	}
	info := &peer.AddrInfo{ID: id}
	for _, raw := range peerAddrs {
		addr, err := multiaddr.NewMultiaddr(raw)
		if err != nil {
			continue
		}
		if strings.Contains(raw, "/p2p/") {
			parsed, err := peer.AddrInfoFromP2pAddr(addr)
			if err != nil {
				continue
			}
			if parsed.ID == id {
				info.Addrs = append(info.Addrs, parsed.Addrs...)
			}
			continue
		}
		info.Addrs = append(info.Addrs, addr)
	}
	if len(info.Addrs) == 0 {
		return nil, errors.New("peer addrs are required for libp2p block fetch")
	}
	return info, nil
}
