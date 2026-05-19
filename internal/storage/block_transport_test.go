package storage

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

func TestFetchBlockViaLibP2P(t *testing.T) {
	node, err := OpenNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("libp2p-block-transport")
	receipt, err := node.Store(wire.UploadRequest{
		IntentID:    "intent_libp2p_test",
		User:        "alice",
		FileRoot:    "file_root",
		SegmentID:   0,
		SegmentRoot: "segment_root",
		ShardIndex:  0,
		ShardID:     "shard_libp2p",
		ShardHash:   chaincrypto.HashBytes(data),
		ShardSize:   int64(len(data)),
		DataBase64:  base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	network, err := StartProviderNetwork(node, "/ip4/127.0.0.1/tcp/0", "", "storage-chain/providers/test-libp2p-block", "http://miner", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer network.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	readBack, err := FetchBlockViaLibP2P(ctx, receipt.ShardCID, network.PeerID(), network.Addrs())
	if err != nil {
		t.Fatal(err)
	}
	if string(readBack) != string(data) {
		t.Fatalf("unexpected libp2p block payload: %q", readBack)
	}
	stats := node.Status().TransportStats
	if stats.LibP2PServeHits != 1 {
		t.Fatalf("expected p2p serve hit to be tracked, got %+v", stats)
	}
}

func TestDefaultBlockTransportClientIsReusable(t *testing.T) {
	first, err := DefaultBlockTransportClient()
	if err != nil {
		t.Fatal(err)
	}
	second, err := DefaultBlockTransportClient()
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second == nil || first != second {
		t.Fatalf("expected reusable default block client, got first=%p second=%p", first, second)
	}
	if first.HostID() == "" {
		t.Fatal("expected default block client to own a libp2p host")
	}
}
