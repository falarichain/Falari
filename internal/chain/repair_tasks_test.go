package chain

import (
	"strconv"
	"testing"
	"time"

	chaincrypto "chain/internal/crypto"
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
	pendingProof := store.data.RepairTasks[task.RepairID]
	if pendingProof.Status != repairStatusProofPending || pendingProof.ProofChallengeID == "" {
		t.Fatalf("expected repair task waiting for proof, got %+v", pendingProof)
	}
	if _, ok := store.data.Challenges[pendingProof.ProofChallengeID]; !ok {
		t.Fatalf("expected forced repair proof challenge %q", pendingProof.ProofChallengeID)
	}
	intent := store.data.Intents[resp.IntentID]
	updated, ok := assignmentForShard(intent.Assignments, task.SegmentID, task.ShardIndex)
	if !ok || updated.MinerAddress != task.Assignment.MinerAddress {
		t.Fatalf("expected intent assignment updated, got %+v", intent.Assignments)
	}
	if got := newMinerAfter.RepairRewards; got != 0 {
		t.Fatalf("repair reward should wait for proof, got %d", got)
	}
}

func TestRepairRewardRequiresForcedProof(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	miner := registerTestMiner(t, store, "repair_miner", "http://repair-miner", 10)
	user := newTestUser(t)
	data := []byte("repair proof data")
	shardHash := chaincrypto.HashBytes(data)
	sectorRoot := chaincrypto.DataMerkleRoot(data, chaincrypto.DefaultLeafSize)
	intent := &Intent{IntentView: wire.IntentView{
		IntentID:      "intent_repair_proof",
		User:          user.Addr,
		FileSize:      int64(len(data)),
		Status:        wire.StatusFinalized,
		StorageStatus: wire.StorageStatusActive,
		LockedFee:     gfTokens(2),
		Policy:        wire.StoragePolicy{Duration: 10},
	}}
	store.data.Intents[intent.IntentID] = intent
	store.data.Accounts[user.Addr] = wire.Account{Address: user.Addr, LockedStorage: gfTokens(2)}
	receipt := wire.MinerReceipt{
		MinerAddress:     miner.Address,
		MinerPublicKey:   miner.PublicKey,
		User:             user.Addr,
		IntentID:         intent.IntentID,
		FileRoot:         "file-root",
		SegmentID:        0,
		SegmentRoot:      "segment-root",
		ShardIndex:       0,
		ShardHash:        shardHash,
		ShardSize:        int64(len(data)),
		SectorCommitment: sectorRoot,
		ExpiresAtUnix:    time.Now().Add(time.Hour).Unix(),
	}
	task := wire.RepairTask{
		IntentID:   intent.IntentID,
		RepairID:   "repair_requires_proof",
		SegmentID:  0,
		ShardIndex: 0,
		Status:     repairStatusProofPending,
		Assignment: wire.StorageAssignment{
			SegmentID:    0,
			ShardIndex:   0,
			MinerAddress: miner.Address,
			ShardHash:    shardHash,
			ShardSize:    int64(len(data)),
		},
	}
	challenge := store.storageChallengeForReceiptLocked(intent, receipt, "", task.RepairID, "challenge_repair_proof", "nonce_repair_proof", time.Now().Add(time.Minute).Unix(), 0)
	task.ProofChallengeID = challenge.ChallengeID
	store.data.RepairTasks[task.RepairID] = task
	store.data.Challenges[challenge.ChallengeID] = challenge

	proof := multiSampleProof(t, data, challenge, miner.PublicKey, miner.Address, miner.PrivateKey)
	resp, err := store.SubmitProof(wire.SubmitProofRequest{Proof: proof})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reward != 0 {
		t.Fatalf("forced repair proof should not pay storage proof reward, got %d", resp.Reward)
	}
	completed := store.data.RepairTasks[task.RepairID]
	if completed.Status != repairStatusCompleted || !completed.ProofVerified {
		t.Fatalf("expected repair completed after proof, got %+v", completed)
	}
	stats := store.data.Miners[miner.Address]
	expectedReward := store.computeRepairRewardLocked(task.Assignment.ShardSize)
	if stats.RepairRewards != expectedReward {
		t.Fatalf("expected repair reward %d after proof, got %+v", expectedReward, stats)
	}
}

func TestStartEpochDefaultRewardPerProofIsOneGF(t *testing.T) {
	store, _, resp, _ := setupCommittedAssignedIntent(t)
	intent := store.data.Intents[resp.IntentID]
	intent.Status = wire.StatusFinalized
	intent.StorageStatus = wire.StorageStatusActive

	epoch, err := store.StartEpoch(wire.StartEpochRequest{
		IntentID:          resp.IntentID,
		ChallengesPerDeal: 1,
		DurationSeconds:   600,
	})
	if err != nil {
		t.Fatal(err)
	}
	if epoch.Epoch.RewardPerProof != wire.TokenUnit {
		t.Fatalf("expected default proof reward 1 GF (%d), got %d", wire.TokenUnit, epoch.Epoch.RewardPerProof)
	}
	if len(epoch.Challenges) != 1 || epoch.Challenges[0].Reward != wire.TokenUnit {
		t.Fatalf("expected challenge reward 1 GF, got %+v", epoch.Challenges)
	}
}

func TestFinalizeEpochCreatesRepairTaskForMissedProof(t *testing.T) {
	store, _, resp, _ := setupCommittedAssignedIntent(t)
	// With repair delay = 1 epoch, a single missed proof triggers immediate repair.
	store.data.MiningParams.RepairDelayEpochs = 1
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
	mA := registerTestMiner(t, store, "miner_a", "http://miner-a", 10)
	mB := registerTestMiner(t, store, "miner_b", "http://miner-b", 10)
	mC := registerTestMiner(t, store, "miner_c", "http://miner-c", 10)
	miners := map[string]testMinerIdentity{
		mA.Address: mA,
		mB.Address: mB,
		mC.Address: mC,
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

func TestFindPoolForSegment(t *testing.T) {
	pools := []wire.RepairPool{
		{PoolID: 0, SegmentIDs: [2]int{0, 1}},
		{PoolID: 1, SegmentIDs: [2]int{2, 3}},
	}
	pool, pos := findPoolForSegment(pools, 0)
	if pool == nil || pool.PoolID != 0 || pos != 0 {
		t.Fatalf("expected pool 0 pos 0, got pool=%v pos=%d", pool, pos)
	}
	pool, pos = findPoolForSegment(pools, 1)
	if pool == nil || pool.PoolID != 0 || pos != 1 {
		t.Fatalf("expected pool 0 pos 1, got pool=%v pos=%d", pool, pos)
	}
	pool, pos = findPoolForSegment(pools, 3)
	if pool == nil || pool.PoolID != 1 || pos != 1 {
		t.Fatalf("expected pool 1 pos 1, got pool=%v pos=%d", pool, pos)
	}
	pool, pos = findPoolForSegment(pools, 99)
	if pool != nil || pos != -1 {
		t.Fatalf("expected nil pool for segment 99, got pool=%v pos=%d", pool, pos)
	}
}

func TestCrossParityReceiptsAvailable(t *testing.T) {
	intent := &Intent{
		Receipts: map[int]map[int]wire.MinerReceipt{
			1:  {0: {MinerAddress: "minerA"}},
			-1: {0: {MinerAddress: "minerB"}},
		},
	}
	if !crossParityReceiptsAvailable(intent, 1, -1, 0, nil) {
		t.Fatal("expected available when both peer and parity receipts exist")
	}
	if crossParityReceiptsAvailable(intent, 1, -1, 0, map[string]bool{"minerA": true}) {
		t.Fatal("expected unavailable when peer miner is unavailable")
	}
	if crossParityReceiptsAvailable(intent, 1, -1, 0, map[string]bool{"minerB": true}) {
		t.Fatal("expected unavailable when parity miner is unavailable")
	}
	if crossParityReceiptsAvailable(intent, 1, -1, 1, nil) {
		t.Fatal("expected unavailable for missing shard index")
	}
}

func TestCrossParityRepairTaskCreatedForPooledSegment(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	// Register 5 miners for enough diversity.
	miners := make(map[string]testMinerIdentity)
	for i := 0; i < 5; i++ {
		m := registerTestMiner(t, store, "miner", "http://miner", 100)
		miners[m.Address] = m
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(100))

	req := wire.CreateIntentRequest{
		User:         alice.Addr,
		FileName:     "pooled.bin",
		FileSize:     12,
		SegmentSize:  6,
		FileRoot:     "file-root",
		SegmentRoots: []string{"seg-root-0", "seg-root-1"},
		Segments: []wire.SegmentPlan{
			{SegmentID: 0, SegmentRoot: "seg-root-0", ShardHashes: []string{"s0h0", "s0h1", "s0h2"}},
			{SegmentID: 1, SegmentRoot: "seg-root-1", ShardHashes: []string{"s1h0", "s1h1", "s1h2"}},
		},
		RepairPools: []wire.RepairPool{{
			PoolID:     0,
			SegmentIDs: [2]int{0, 1},
			CrossParity: wire.CrossParityPlan{
				ShardHashes: []string{"cp-h0", "cp-h1", "cp-h2"},
				ShardSize:   2,
			},
		}},
		Erasure:      wire.ErasurePolicy{DataShards: 2, ParityShards: 1},
		Policy:       wire.StoragePolicy{Duration: int64(30 * 24 * time.Hour / time.Second)},
		DeadlineUnix: time.Now().Add(time.Hour).Unix(),
	}
	signCreateIntent(t, store, &req, alice)
	resp, err := store.CreateIntent(req)
	if err != nil {
		t.Fatal(err)
	}
	intent := store.data.Intents[resp.IntentID]

	// Commit all segment assignments.
	var receipts []wire.MinerReceipt
	for _, assignment := range resp.Assignments {
		m, ok := miners[assignment.MinerAddress]
		if !ok {
			t.Fatalf("assignment miner not found: %s", assignment.MinerAddress)
		}
		segRoot := ""
		if assignment.SegmentID >= 0 && assignment.SegmentID < len(intent.Segments) {
			segRoot = intent.Segments[assignment.SegmentID].SegmentRoot
		}
		receipt := wire.MinerReceipt{
			Version:        1,
			MinerAddress:   m.Address,
			MinerPublicKey: m.PublicKey,
			User:           alice.Addr,
			IntentID:       resp.IntentID,
			FileRoot:       "file-root",
			SegmentID:      assignment.SegmentID,
			SegmentRoot:    segRoot,
			ShardIndex:     assignment.ShardIndex,
			ShardID:        resp.IntentID + ":" + strconv.Itoa(assignment.SegmentID) + ":" + strconv.Itoa(assignment.ShardIndex),
			ShardHash:      assignment.ShardHash,
			ShardSize:      assignment.ShardSize,
			SectorCommitment: "sector-" + assignment.ShardHash,
			ExpiresAtUnix:  time.Now().Add(time.Hour).Unix(),
			MinerEndpoint:  m.Endpoint,
		}
		if err := wire.SignReceipt(&receipt, m.PrivateKey); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, receipt)
	}
	bcReq := wire.BatchCommitRequest{IntentID: resp.IntentID, User: alice.Addr, Receipts: receipts}
	signBatchCommit(t, store, &bcReq, alice)
	if _, err := store.BatchCommit(bcReq); err != nil {
		t.Fatal(err)
	}

	// Manually add cross-parity receipts at segmentID -1.
	intent = store.data.Intents[resp.IntentID]
	paritySegID := -1
	minerList := make([]testMinerIdentity, 0, len(miners))
	for _, m := range miners {
		minerList = append(minerList, m)
	}
	intent.Receipts[paritySegID] = make(map[int]wire.MinerReceipt)
	for j := 0; j < 3; j++ {
		m := minerList[j%len(minerList)]
		intent.Receipts[paritySegID][j] = wire.MinerReceipt{
			MinerAddress:   m.Address,
			MinerPublicKey: m.PublicKey,
			IntentID:       resp.IntentID,
			SegmentID:      paritySegID,
			ShardIndex:     j,
			ShardHash:      "cp-h" + strconv.Itoa(j),
			ShardSize:      2,
			ExpiresAtUnix:  time.Now().Add(time.Hour).Unix(),
		}
	}

	// Find which miner holds segment 0, shard 0.
	seg0Shard0Miner := intent.Receipts[0][0].MinerAddress

	// Create repair tasks with that miner unavailable.
	repair, err := store.CreateRepairTasks(wire.CreateRepairRequest{
		IntentID:          resp.IntentID,
		UnavailableMiners: []string{seg0Shard0Miner},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repair.Tasks) == 0 {
		t.Fatal("expected at least one repair task")
	}

	// The repair task for seg 0 shard 0 should use cross-parity mode.
	var crossParityTask *wire.RepairTask
	for i := range repair.Tasks {
		if repair.Tasks[i].SegmentID == 0 && repair.Tasks[i].ShardIndex == 0 {
			crossParityTask = &repair.Tasks[i]
			break
		}
	}
	if crossParityTask == nil {
		t.Fatal("expected repair task for segment 0 shard 0")
	}
	if crossParityTask.RepairMode != "cross_parity" {
		t.Fatalf("expected cross_parity repair mode, got %q", crossParityTask.RepairMode)
	}
	if crossParityTask.RequiredShards != 2 {
		t.Fatalf("expected RequiredShards=2 for cross-parity, got %d", crossParityTask.RequiredShards)
	}
	if crossParityTask.PeerSegmentID != 1 {
		t.Fatalf("expected PeerSegmentID=1, got %d", crossParityTask.PeerSegmentID)
	}
	if len(crossParityTask.SourceReceipts) != 2 {
		t.Fatalf("expected 2 source receipts (peer + parity), got %d", len(crossParityTask.SourceReceipts))
	}
}
