package storage

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

func TestDownloadSourceShardUsesDiscoveredLibP2PProvider(t *testing.T) {
	resetProviderTransportMemoryForTests()
	sourceNode, err := OpenNode(t.TempDir(), testNodeKey(t))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("repair-source-via-libp2p")
	receipt, err := sourceNode.Store(wire.UploadRequest{
		IntentID:    "intent_repair_source",
		User:        "alice",
		FileRoot:    "file_root",
		SegmentID:   0,
		SegmentRoot: "segment_root",
		ShardIndex:  0,
		ShardID:     "shard_source",
		ShardHash:   chaincrypto.HashBytes(data),
		ShardSize:   int64(len(data)),
		DataBase64:  base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	network, err := StartProviderNetwork(sourceNode, "/ip4/127.0.0.1/tcp/0", "", "storage-chain/providers/test-repair-libp2p", "", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer network.Close()

	record, err := sourceNode.ProviderRecord("", 1<<20, network.PeerID(), network.Addrs(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	chainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/storage/providers" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(wire.StorageProvidersResponse{
			ShardHash: receipt.ShardHash,
			ShardCID:  receipt.ShardCID,
			Providers: []wire.StorageProviderRecord{record},
		})
	}))
	defer chainServer.Close()

	receipt.MinerEndpoint = ""
	readBack, err := downloadSourceShard(sourceNode, chainServer.URL, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if string(readBack) != string(data) {
		t.Fatalf("unexpected discovered libp2p repair payload: %q", readBack)
	}
	stats := sourceNode.Status().TransportStats
	if stats.LibP2PFetchSuccess != 1 || stats.HTTPFallbacks != 0 {
		t.Fatalf("unexpected transport stats after libp2p repair fetch: %+v", stats)
	}
	memories := sourceNode.Status().RecentProviderMemories
	if len(memories) != 1 {
		t.Fatalf("expected one provider memory after repair fetch, got %+v", memories)
	}
	if memories[0].ProviderKey != "peer:"+record.PeerID || memories[0].LastOutcome != "success" || memories[0].LastTransport != "libp2p" {
		t.Fatalf("unexpected provider memory after repair fetch: %+v", memories[0])
	}
}
