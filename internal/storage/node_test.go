package storage

import (
	"encoding/base64"
	"testing"
	"time"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

func TestNodeProvesMultipleChallengeLeaves(t *testing.T) {
	node, err := OpenNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, chaincrypto.DefaultLeafSize*4)
	for i := range data {
		data[i] = byte((i * 17) % 251)
	}
	receipt, err := node.Store(wire.UploadRequest{
		IntentID:    "intent_storage_test",
		User:        "alice",
		FileRoot:    "file_root",
		SegmentID:   0,
		SegmentRoot: "segment_root",
		ShardIndex:  0,
		ShardID:     "shard_0",
		ShardHash:   chaincrypto.HashBytes(data),
		ShardSize:   int64(len(data)),
		DataBase64:  base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	challenge := wire.StorageChallenge{
		ChallengeID:      "challenge_storage_test",
		IntentID:         receipt.IntentID,
		DealID:           "deal_storage_test",
		ShardHash:        receipt.ShardHash,
		ShardSize:        receipt.ShardSize,
		SectorCommitment: receipt.SectorCommitment,
		LeafSize:         chaincrypto.DefaultLeafSize,
		LeafIndex:        0,
		LeafIndices:      []int{0, 1, 3},
		SampleCount:      3,
		MinerAddress:     node.Address(),
		MinerPublicKey:   node.PublicKeyBase64(),
		Nonce:            "nonce_storage_test",
		ExpiresAtUnix:    time.Now().Add(time.Minute).Unix(),
	}
	proof, err := node.Prove(challenge)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.LeafHashes) != 3 || len(proof.MerklePaths) != 3 {
		t.Fatalf("expected multi-sample proof, got %+v", proof)
	}
	if err := wire.VerifyProof(proof); err != nil {
		t.Fatal(err)
	}
	for i, index := range challenge.LeafIndices {
		if !chaincrypto.VerifyMerkleProof(proof.SectorCommitment, proof.LeafHashes[i], index, proof.MerklePaths[i]) {
			t.Fatalf("sample %d did not verify", i)
		}
	}
}

func TestNodeStatusCountsStoredShards(t *testing.T) {
	resetProviderTransportMemoryForTests()
	node, err := OpenNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := []byte("first-shard")
	second := []byte("second-shard-data")
	for i, data := range [][]byte{first, second} {
		_, err := node.Store(wire.UploadRequest{
			IntentID:    "intent_status_test",
			User:        "alice",
			FileRoot:    "file_root",
			SegmentID:   0,
			SegmentRoot: "segment_root",
			ShardIndex:  i,
			ShardID:     "shard_status",
			ShardHash:   chaincrypto.HashBytes(data),
			ShardSize:   int64(len(data)),
			DataBase64:  base64.StdEncoding.EncodeToString(data),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	status := node.Status()
	if status.Status != "ok" || status.Address != node.Address() {
		t.Fatalf("unexpected node status: %+v", status)
	}
	if status.ShardCount != 2 || status.StoredBytes != uint64(len(first)+len(second)) {
		t.Fatalf("unexpected shard counters: %+v", status)
	}
	if status.TransportStats != (wire.StorageTransportStats{}) {
		t.Fatalf("expected empty transport stats for fresh node, got %+v", status.TransportStats)
	}
	if len(status.RecentProviderMemories) != 0 {
		t.Fatalf("expected empty provider memories for fresh node, got %+v", status.RecentProviderMemories)
	}
}

func TestNodeReadsStoredShardByCID(t *testing.T) {
	node, err := OpenNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("cid-readable-shard")
	receipt, err := node.Store(wire.UploadRequest{
		IntentID:    "intent_cid_test",
		User:        "alice",
		FileRoot:    "file_root",
		SegmentID:   0,
		SegmentRoot: "segment_root",
		ShardIndex:  0,
		ShardID:     "shard_cid",
		ShardHash:   chaincrypto.HashBytes(data),
		ShardSize:   int64(len(data)),
		DataBase64:  base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	readBack, err := node.ReadShardByCID(receipt.ShardCID)
	if err != nil {
		t.Fatal(err)
	}
	if string(readBack) != string(data) {
		t.Fatalf("unexpected cid read payload: %q", readBack)
	}
}

func TestNodeStatusIncludesRecentProviderMemories(t *testing.T) {
	resetProviderTransportMemoryForTests()
	node, err := OpenNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := wire.StorageProviderRecord{
		MinerAddress: "miner_status_memory",
		Endpoint:     "http://status-memory",
		PeerID:       "12D3KooWstatusmemory",
	}

	RememberProviderFetchSuccess(provider, "libp2p")
	status := node.Status()
	if len(status.RecentProviderMemories) != 1 {
		t.Fatalf("expected provider memory in status, got %+v", status.RecentProviderMemories)
	}
	memory := status.RecentProviderMemories[0]
	if memory.ProviderKey != "peer:"+provider.PeerID || memory.LastOutcome != "success" || memory.LastTransport != "libp2p" {
		t.Fatalf("unexpected provider memory in status: %+v", memory)
	}
}
