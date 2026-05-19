package chain

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strconv"
	"testing"
	"time"

	"chain/internal/wire"
)

func TestCreateIntentAssignsAndReservesMinerCapacity(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	registerTestMiner(t, store, "miner_a", "http://miner-a", 10)
	registerTestMiner(t, store, "miner_b", "http://miner-b", 10)
	store.data.Accounts["alice"] = wire.Account{Address: "alice", Balance: 10}

	resp, err := store.CreateIntent(testAssignedIntentRequest(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Assignments) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(resp.Assignments))
	}
	seen := map[string]bool{}
	var reserved uint64
	for _, assignment := range resp.Assignments {
		seen[assignment.MinerAddress] = true
		reserved += uint64(assignment.ShardSize)
	}
	if len(seen) != 2 {
		t.Fatalf("expected shards spread across two miners, got %+v", resp.Assignments)
	}
	var minerReserved uint64
	for _, miner := range store.data.Miners {
		minerReserved += miner.ReservedBytes
	}
	if minerReserved != reserved || reserved != 6 {
		t.Fatalf("expected 6 reserved bytes, reserved=%d miners=%d", reserved, minerReserved)
	}
	intent := store.data.Intents[resp.IntentID]
	if len(intent.Assignments) != 2 {
		t.Fatalf("expected intent assignments to persist, got %+v", intent.Assignments)
	}
}

func TestBatchCommitRejectsReceiptFromWrongAssignedMiner(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	minerA := registerTestMiner(t, store, "miner_a", "http://miner-a", 10)
	minerB := registerTestMiner(t, store, "miner_b", "http://miner-b", 10)
	store.data.Accounts["alice"] = wire.Account{Address: "alice", Balance: 10}
	resp, err := store.CreateIntent(testAssignedIntentRequest(0))
	if err != nil {
		t.Fatal(err)
	}
	assignment := resp.Assignments[0]
	wrongMiner := minerA
	if assignment.MinerAddress == "miner_a" {
		wrongMiner = minerB
	}
	receipt := testAssignmentReceipt(t, resp.IntentID, assignment, wrongMiner)

	_, err = store.BatchCommit(wire.BatchCommitRequest{
		IntentID: resp.IntentID,
		User:     "alice",
		Receipts: []wire.MinerReceipt{receipt},
	})
	if err == nil {
		t.Fatal("expected wrong assigned miner receipt to be rejected")
	}
}

func TestBatchCommitReleasesReservationAndMarksUsed(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	miners := map[string]testMinerIdentity{
		"miner_a": registerTestMiner(t, store, "miner_a", "http://miner-a", 10),
		"miner_b": registerTestMiner(t, store, "miner_b", "http://miner-b", 10),
	}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", Balance: 10}
	resp, err := store.CreateIntent(testAssignedIntentRequest(0))
	if err != nil {
		t.Fatal(err)
	}
	assignment := resp.Assignments[0]
	minerBefore := store.data.Miners[assignment.MinerAddress]
	if minerBefore.ReservedBytes < uint64(assignment.ShardSize) {
		t.Fatalf("expected reservation before commit, miner=%+v assignment=%+v", minerBefore, assignment)
	}
	receipt := testAssignmentReceipt(t, resp.IntentID, assignment, miners[assignment.MinerAddress])

	if _, err := store.BatchCommit(wire.BatchCommitRequest{
		IntentID: resp.IntentID,
		User:     "alice",
		Receipts: []wire.MinerReceipt{receipt},
	}); err != nil {
		t.Fatal(err)
	}
	minerAfter := store.data.Miners[assignment.MinerAddress]
	if minerAfter.UsedBytes != uint64(assignment.ShardSize) {
		t.Fatalf("expected used bytes %d, got %+v", assignment.ShardSize, minerAfter)
	}
	if minerAfter.ReservedBytes != minerBefore.ReservedBytes-uint64(assignment.ShardSize) {
		t.Fatalf("expected reservation to release, before=%+v after=%+v", minerBefore, minerAfter)
	}
}

type testMinerIdentity struct {
	Address    string
	PublicKey  string
	PrivateKey ed25519.PrivateKey
	Endpoint   string
}

func registerTestMiner(t *testing.T, store *Store, address string, endpoint string, capacity uint64) testMinerIdentity {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity := testMinerIdentity{
		Address:    address,
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		PrivateKey: priv,
		Endpoint:   endpoint,
	}
	req := wire.RegisterMinerRequest{
		MinerAddress:  address,
		PublicKey:     identity.PublicKey,
		Endpoint:      endpoint,
		CapacityBytes: capacity,
		Stake:         1,
	}
	if err := wire.SignMinerRegistration(&req, priv); err != nil {
		t.Fatal(err)
	}
	store.data.Accounts[address] = wire.Account{Address: address, Balance: 1}
	if _, err := store.RegisterMiner(req); err != nil {
		t.Fatal(err)
	}
	return identity
}

func testAssignedIntentRequest(lockedFee uint64) wire.CreateIntentRequest {
	return wire.CreateIntentRequest{
		User:         "alice",
		FileName:     "file.bin",
		FileSize:     6,
		SegmentSize:  6,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments: []wire.SegmentPlan{{
			SegmentID:   0,
			SegmentRoot: "segment-root",
			ShardHashes: []string{
				"shard-a",
				"shard-b",
			},
		}},
		Erasure:      wire.ErasurePolicy{DataShards: 2, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(30 * 24 * time.Hour / time.Second)},
		LockedFee:    lockedFee,
		DeadlineUnix: time.Now().Add(time.Hour).Unix(),
	}
}

func testAssignmentReceipt(t *testing.T, intentID string, assignment wire.StorageAssignment, miner testMinerIdentity) wire.MinerReceipt {
	t.Helper()
	receipt := wire.MinerReceipt{
		Version:          1,
		MinerAddress:     miner.Address,
		MinerPublicKey:   miner.PublicKey,
		User:             "alice",
		IntentID:         intentID,
		FileRoot:         "file-root",
		SegmentID:        assignment.SegmentID,
		SegmentRoot:      "segment-root",
		ShardIndex:       assignment.ShardIndex,
		ShardID:          intentID + ":0:" + strconv.Itoa(assignment.ShardIndex),
		ShardHash:        assignment.ShardHash,
		ShardSize:        assignment.ShardSize,
		SectorCommitment: "sector-" + assignment.ShardHash,
		ExpiresAtUnix:    time.Now().Add(time.Hour).Unix(),
		MinerEndpoint:    miner.Endpoint,
	}
	if err := wire.SignReceipt(&receipt, miner.PrivateKey); err != nil {
		t.Fatal(err)
	}
	return receipt
}
