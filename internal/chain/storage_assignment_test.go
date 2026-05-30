package chain

import (
	"crypto/ecdsa"
	"strconv"
	"testing"
	"time"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestCreateIntentAssignsAndReservesMinerCapacity(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	registerTestMiner(t, store, "miner_a", "http://miner-a", 10)
	registerTestMiner(t, store, "miner_b", "http://miner-b", 10)
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(10))

	resp, err := store.CreateIntent(testAssignedIntentRequest(t, store, alice, 0))
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
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(10))
	resp, err := store.CreateIntent(testAssignedIntentRequest(t, store, alice, 0))
	if err != nil {
		t.Fatal(err)
	}
	assignment := resp.Assignments[0]
	wrongMiner := minerA
	if assignment.MinerAddress == minerA.Address {
		wrongMiner = minerB
	}
	receipt := testAssignmentReceipt(t, resp.IntentID, assignment, wrongMiner, alice.Addr)

	bcReq := wire.BatchCommitRequest{
		IntentID: resp.IntentID,
		User:     alice.Addr,
		Receipts: []wire.MinerReceipt{receipt},
	}
	signBatchCommit(t, store, &bcReq, alice)
	_, err = store.BatchCommit(bcReq)
	if err == nil {
		t.Fatal("expected wrong assigned miner receipt to be rejected")
	}
}

func TestBatchCommitReleasesReservationAndMarksUsed(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	minerA := registerTestMiner(t, store, "miner_a", "http://miner-a", 10)
	minerB := registerTestMiner(t, store, "miner_b", "http://miner-b", 10)
	miners := map[string]testMinerIdentity{
		minerA.Address: minerA,
		minerB.Address: minerB,
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(10))
	resp, err := store.CreateIntent(testAssignedIntentRequest(t, store, alice, 0))
	if err != nil {
		t.Fatal(err)
	}
	assignment := resp.Assignments[0]
	minerBefore := store.data.Miners[assignment.MinerAddress]
	if minerBefore.ReservedBytes < uint64(assignment.ShardSize) {
		t.Fatalf("expected reservation before commit, miner=%+v assignment=%+v", minerBefore, assignment)
	}
	receipt := testAssignmentReceipt(t, resp.IntentID, assignment, miners[assignment.MinerAddress], alice.Addr)

	bcReq := wire.BatchCommitRequest{
		IntentID: resp.IntentID,
		User:     alice.Addr,
		Receipts: []wire.MinerReceipt{receipt},
	}
	signBatchCommit(t, store, &bcReq, alice)
	if _, err := store.BatchCommit(bcReq); err != nil {
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
	PrivateKey *ecdsa.PrivateKey
	Endpoint   string
}

func registerTestMiner(t *testing.T, store *Store, _ string, endpoint string, capacity uint64) testMinerIdentity {
	t.Helper()
	// Disable min-capacity and stake-per-TiB checks so tests can use small capacities.
	params := store.miningParamsLocked()
	params.MinCapacityBytes = 0
	params.StakePerTiB = 0
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	address := wire.AccountAddress(&key.PublicKey)
	identity := testMinerIdentity{
		Address:    address,
		PublicKey:  wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey)),
		PrivateKey: key,
		Endpoint:   endpoint,
	}
	req := wire.RegisterMinerRequest{
		MinerAddress:  address,
		PublicKey:     identity.PublicKey,
		Endpoint:      endpoint,
		CapacityBytes: capacity,
		Stake:         gfTokens(1),
	}
	if err := wire.SignMinerRegistration(&req, store.data.ChainID, store.accountLocked(address).Nonce, key); err != nil {
		t.Fatal(err)
	}
	store.data.Accounts[address] = wire.Account{Address: address, Balance: gfTokens(1)}
	if _, err := store.RegisterMiner(req); err != nil {
		t.Fatal(err)
	}
	return identity
}

func testAssignmentReceipt(t *testing.T, intentID string, assignment wire.StorageAssignment, miner testMinerIdentity, userAddr string) wire.MinerReceipt {
	t.Helper()
	receipt := wire.MinerReceipt{
		Version:          1,
		MinerAddress:     miner.Address,
		MinerPublicKey:   miner.PublicKey,
		User:             userAddr,
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
