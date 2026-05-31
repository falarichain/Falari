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

func TestRepairTaskCrossParityXOR(t *testing.T) {
	// Set up source node that holds the two source shards.
	sourceNode, err := OpenNode(t.TempDir(), testNodeKey(t))
	if err != nil {
		t.Fatal(err)
	}
	// peer shard (segment 1, shard 0) and cross-parity shard (segment -1, shard 0)
	peerData := []byte("peer-segment-shard-data!!") // 24 bytes
	// cross-parity = peer XOR lost, so lost = peer XOR cross-parity
	crossParityData := make([]byte, len(peerData))
	for i := range peerData {
		crossParityData[i] = peerData[i] ^ 0xAB // simulate XOR with lost shard
	}
	peerHash := chaincrypto.HashBytes(peerData)
	cpHash := chaincrypto.HashBytes(crossParityData)

	if _, err := sourceNode.Store(wire.UploadRequest{
		IntentID: "intent_cp", User: "alice", FileRoot: "fr",
		SegmentID: 1, SegmentRoot: "sr1", ShardIndex: 0,
		ShardID: "peer-shard", ShardHash: peerHash, ShardSize: int64(len(peerData)),
		DataBase64: base64.StdEncoding.EncodeToString(peerData),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceNode.Store(wire.UploadRequest{
		IntentID: "intent_cp", User: "alice", FileRoot: "fr",
		SegmentID: -1, SegmentRoot: "", ShardIndex: 0,
		ShardID: "cp-shard", ShardHash: cpHash, ShardSize: int64(len(crossParityData)),
		DataBase64: base64.StdEncoding.EncodeToString(crossParityData),
	}); err != nil {
		t.Fatal(err)
	}

	// Mock chain server that returns the source node as provider.
	chainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/storage/providers":
			_ = json.NewEncoder(w).Encode(wire.StorageProvidersResponse{
				Providers: []wire.StorageProviderRecord{{
					MinerAddress: sourceNode.address,
					Endpoint:     "", // will be filled below
					ShardHashes:  []string{peerHash, cpHash},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer chainServer.Close()

	// Start an HTTP server on the source node to serve shard downloads.
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shards/"+peerHash+".bin" {
			w.Write(peerData)
			return
		}
		if r.URL.Path == "/shards/"+cpHash+".bin" {
			w.Write(crossParityData)
			return
		}
		http.NotFound(w, r)
	}))
	defer sourceServer.Close()

	// Create the repair node.
	repairNode, err := OpenNode(t.TempDir(), testNodeKey(t))
	if err != nil {
		t.Fatal(err)
	}

	// Expected result: peer XOR cross-parity = lost shard.
	expectedLost := make([]byte, len(peerData))
	for i := range peerData {
		expectedLost[i] = peerData[i] ^ crossParityData[i]
	}
	expectedHash := chaincrypto.HashBytes(expectedLost)

	// Build the repair task.
	task := wire.RepairTask{
		IntentID:     "intent_cp",
		RepairID:     "repair_cp_test",
		SegmentID:    0,
		ShardIndex:   0,
		RepairMode:   "cross_parity",
		RequiredShards: 2,
		Assignment: wire.StorageAssignment{
			SegmentID:    0,
			ShardIndex:   0,
			MinerAddress: repairNode.address,
			ShardHash:    expectedHash,
			ShardSize:    int64(len(expectedLost)),
		},
		SourceReceipts: []wire.MinerReceipt{
			{
				MinerAddress: sourceNode.address, MinerEndpoint: sourceServer.URL,
				IntentID: "intent_cp", User: "alice", FileRoot: "fr",
				SegmentID: 1, ShardIndex: 0, ShardHash: peerHash, ShardSize: int64(len(peerData)),
			},
			{
				MinerAddress: sourceNode.address, MinerEndpoint: sourceServer.URL,
				IntentID: "intent_cp", User: "alice", FileRoot: "fr",
				SegmentID: -1, ShardIndex: 0, ShardHash: cpHash, ShardSize: int64(len(crossParityData)),
			},
		},
	}

	resultReceipt, err := repairNode.repairTask(chainServer.URL, task)
	if err != nil {
		t.Fatal(err)
	}
	if resultReceipt.ShardHash != expectedHash {
		t.Fatalf("expected shard hash %s, got %s", expectedHash, resultReceipt.ShardHash)
	}
	if resultReceipt.SegmentID != 0 || resultReceipt.ShardIndex != 0 {
		t.Fatalf("unexpected receipt segment/shard: %d/%d", resultReceipt.SegmentID, resultReceipt.ShardIndex)
	}
	if resultReceipt.MinerAddress != repairNode.address {
		t.Fatalf("receipt miner mismatch: %s vs %s", resultReceipt.MinerAddress, repairNode.address)
	}
}

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
