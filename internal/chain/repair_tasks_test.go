package chain

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestCreateRepairTasksAssignsReplacementAndReservesCapacity(t *testing.T) {
	store, miners, resp, _ := setupCommittedAssignedIntent(t)
	assignment := resp.Assignments[0]
	oldMinerBefore := store.data.Miners[assignment.MinerAddress]

	repair, err := store.CreateRepairTasks(wire.CreateRepairRequest{
		IntentID:          resp.IntentID,
		UnavailableMiners: []string{assignment.MinerAddress},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repair.Tasks) != 1 {
		t.Fatalf("expected 1 repair task, got %+v", repair.Tasks)
	}
	task := repair.Tasks[0]
	if task.Status != repairStatusPending || task.Assignment.MinerAddress == "" {
		t.Fatalf("unexpected repair task: %+v", task)
	}
	if task.Assignment.MinerAddress == assignment.MinerAddress {
		t.Fatalf("repair reassigned to old miner: %+v", task)
	}
	if _, ok := miners[task.Assignment.MinerAddress]; !ok {
		t.Fatalf("repair assigned to unknown miner: %+v", task)
	}
	newMiner := store.data.Miners[task.Assignment.MinerAddress]
	if newMiner.ReservedBytes < uint64(task.Assignment.ShardSize) {
		t.Fatalf("expected replacement capacity reserved, miner=%+v task=%+v", newMiner, task)
	}
	oldMinerAfter := store.data.Miners[assignment.MinerAddress]
	if oldMinerAfter.UsedBytes != oldMinerBefore.UsedBytes {
		t.Fatalf("repair task creation should not release old used bytes, before=%+v after=%+v", oldMinerBefore, oldMinerAfter)
	}
}

func TestRepairCommitCompletesTaskAndMovesUsedBytes(t *testing.T) {
	store, miners, resp, alice := setupCommittedAssignedIntent(t)
	oldAssignment := resp.Assignments[0]
	repair, err := store.CreateRepairTasks(wire.CreateRepairRequest{
		IntentID:          resp.IntentID,
		UnavailableMiners: []string{oldAssignment.MinerAddress},
	})
	if err != nil {
		t.Fatal(err)
	}
	task := repair.Tasks[0]
	oldMinerBefore := store.data.Miners[oldAssignment.MinerAddress]
	newMinerBefore := store.data.Miners[task.Assignment.MinerAddress]
	receipt := testAssignmentReceipt(t, resp.IntentID, task.Assignment, miners[task.Assignment.MinerAddress], alice.Addr)

	bcReq := wire.BatchCommitRequest{
		IntentID: resp.IntentID,
		User:     alice.Addr,
		Receipts: []wire.MinerReceipt{receipt},
	}
	signBatchCommit(t, store, &bcReq, alice)
	if _, err := store.BatchCommit(bcReq); err != nil {
		t.Fatal(err)
	}
	oldMinerAfter := store.data.Miners[oldAssignment.MinerAddress]
	newMinerAfter := store.data.Miners[task.Assignment.MinerAddress]
	if oldMinerAfter.UsedBytes != oldMinerBefore.UsedBytes-uint64(oldAssignment.ShardSize) {
		t.Fatalf("expected old miner used bytes released, before=%+v after=%+v", oldMinerBefore, oldMinerAfter)
	}
	if newMinerAfter.UsedBytes != newMinerBefore.UsedBytes+uint64(task.Assignment.ShardSize) {
		t.Fatalf("expected new miner used bytes increased, before=%+v after=%+v", newMinerBefore, newMinerAfter)
	}
	if newMinerAfter.ReservedBytes != newMinerBefore.ReservedBytes-uint64(task.Assignment.ShardSize) {
		t.Fatalf("expected new miner reservation released, before=%+v after=%+v", newMinerBefore, newMinerAfter)
	}
	completed := store.data.RepairTasks[task.RepairID]
	if completed.Status != repairStatusCompleted {
		t.Fatalf("expected repair task completed, got %+v", completed)
	}
	intent := store.data.Intents[resp.IntentID]
	updated, ok := assignmentForShard(intent.Assignments, task.SegmentID, task.ShardIndex)
	if !ok || updated.MinerAddress != task.Assignment.MinerAddress {
		t.Fatalf("expected intent assignment updated, got %+v", intent.Assignments)
	}
}

func TestFinalizeEpochCreatesRepairTaskForMissedProof(t *testing.T) {
	store, _, resp, _ := setupCommittedAssignedIntent(t)
	intent := store.data.Intents[resp.IntentID]
	intent.Status = wire.StatusFinalized
	intent.DealID = "deal_auto_repair"
	store.data.Deals[intent.DealID] = intent.IntentID
	assignment := resp.Assignments[0]
	receipt := intent.Receipts[assignment.SegmentID][assignment.ShardIndex]
	challenge := wire.StorageChallenge{
		ChallengeID:      "challenge_auto_repair",
		EpochID:          "epoch_auto_repair",
		IntentID:         intent.IntentID,
		DealID:           intent.DealID,
		SegmentID:        assignment.SegmentID,
		SegmentRoot:      receipt.SegmentRoot,
		ShardIndex:       assignment.ShardIndex,
		ShardHash:        receipt.ShardHash,
		ShardSize:        receipt.ShardSize,
		SectorCommitment: receipt.SectorCommitment,
		MinerAddress:     receipt.MinerAddress,
		MinerPublicKey:   receipt.MinerPublicKey,
		MinerEndpoint:    receipt.MinerEndpoint,
		ExpiresAtUnix:    time.Now().Add(-time.Minute).Unix(),
	}
	store.data.Challenges[challenge.ChallengeID] = challenge
	store.data.Epochs[challenge.EpochID] = wire.ProofEpoch{
		EpochID:             challenge.EpochID,
		ChallengeIDs:        []string{challenge.ChallengeID},
		StartedAtUnix:       time.Now().Add(-time.Hour).Unix(),
		DeadlineUnix:        time.Now().Add(-time.Minute).Unix(),
		Status:              "active",
		SlashPerMissedProof: 1,
	}

	result, err := store.FinalizeEpoch(wire.FinalizeEpochRequest{EpochID: challenge.EpochID})
	if err != nil {
		t.Fatal(err)
	}
	if result.MissedProofs != 1 || len(result.RepairTasks) != 1 {
		t.Fatalf("expected one missed proof repair task, got %+v", result)
	}
	task := result.RepairTasks[0]
	if task.Reason != "missed_proof" || task.OldMinerAddress != assignment.MinerAddress {
		t.Fatalf("unexpected repair task: %+v", task)
	}
	if task.Assignment.MinerAddress == assignment.MinerAddress {
		t.Fatalf("repair task should not target old miner: %+v", task)
	}
	if stored := store.data.RepairTasks[task.RepairID]; stored.Status != repairStatusPending {
		t.Fatalf("expected pending stored repair task, got %+v", stored)
	}
	if replacement := store.data.Miners[task.Assignment.MinerAddress]; replacement.ReservedBytes < uint64(task.Assignment.ShardSize) {
		t.Fatalf("expected replacement miner capacity reserved, miner=%+v task=%+v", replacement, task)
	}
	if old := store.data.Miners[assignment.MinerAddress]; old.ProofFailure != 1 || old.Slashed != 1 {
		t.Fatalf("expected old miner proof failure/slash, got %+v", old)
	}
}

func TestRepairPlanIncludesPendingRepairTasks(t *testing.T) {
	store, _, resp, _ := setupCommittedAssignedIntent(t)
	assignment := resp.Assignments[0]
	repair, err := store.CreateRepairTasks(wire.CreateRepairRequest{
		IntentID:          resp.IntentID,
		UnavailableMiners: []string{assignment.MinerAddress},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.RepairPlan(resp.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].RepairID != repair.Tasks[0].RepairID {
		t.Fatalf("expected repair plan to include pending task, plan=%+v repair=%+v", plan, repair)
	}
}

func setupCommittedAssignedIntent(t *testing.T) (*Store, map[string]testMinerIdentity, wire.CreateIntentResponse, testUser) {
	t.Helper()
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	miners := map[string]testMinerIdentity{
		"miner_a": registerTestMiner(t, store, "miner_a", "http://miner-a", 10),
		"miner_b": registerTestMiner(t, store, "miner_b", "http://miner-b", 10),
		"miner_c": registerTestMiner(t, store, "miner_c", "http://miner-c", 10),
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(10))
	resp, err := store.CreateIntent(testRepairIntentRequest(t, store, alice))
	if err != nil {
		t.Fatal(err)
	}
	receipts := make([]wire.MinerReceipt, 0, len(resp.Assignments))
	for _, assignment := range resp.Assignments {
		receipts = append(receipts, testAssignmentReceipt(t, resp.IntentID, assignment, miners[assignment.MinerAddress], alice.Addr))
	}
	bcReq := wire.BatchCommitRequest{
		IntentID: resp.IntentID,
		User:     alice.Addr,
		Receipts: receipts,
	}
	signBatchCommit(t, store, &bcReq, alice)
	if _, err := store.BatchCommit(bcReq); err != nil {
		t.Fatal(err)
	}
	return store, miners, resp, alice
}

func testRepairIntentRequest(t *testing.T, store *Store, u testUser) wire.CreateIntentRequest {
	t.Helper()
	req := wire.CreateIntentRequest{
		User:         u.Addr,
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
				"shard-c",
			},
		}},
		Erasure:      wire.ErasurePolicy{DataShards: 2, ParityShards: 1},
		Policy:       wire.StoragePolicy{Duration: int64(30 * 24 * time.Hour / time.Second)},
		DeadlineUnix: time.Now().Add(time.Hour).Unix(),
	}
	signCreateIntent(t, store, &req, u)
	return req
}
