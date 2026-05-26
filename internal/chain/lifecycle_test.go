package chain

import (
	"testing"
	"time"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestSetAccessPolicyBlocksManifestAndProviders(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	intent := testLifecycleIntentWithUser(alice.Addr)
	store.data.Intents[intent.IntentID] = intent
	store.data.Deals[intent.DealID] = intent.IntentID
	store.data.Miners["miner_lifecycle"] = wire.MinerStats{
		MinerAddress:  "miner_lifecycle",
		PublicKey:     "miner_pub",
		Endpoint:      "http://miner",
		CapacityBytes: 100,
		Stake:         gfTokens(10),
		Status:        "active",
	}

	if _, err := store.Manifest(intent.IntentID); err != nil {
		t.Fatal(err)
	}
	sapReq := wire.SetAccessPolicyRequest{
		IntentID:     intent.IntentID,
		User:         intent.User,
		AccessStatus: wire.AccessStatusBlocked,
		ReasonHash:   "reason_hash",
	}
	signSetAccessPolicy(t, store, &sapReq, alice)
	if _, err := store.SetAccessPolicy(sapReq); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Manifest(intent.IntentID); err == nil {
		t.Fatal("expected blocked manifest to be unavailable")
	}
	providers, err := store.StorageProviders("shard_lifecycle", "", intent.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers.Providers) != 0 {
		t.Fatalf("expected blocked provider discovery to be empty, got %+v", providers.Providers)
	}
}

func TestTerminateDealBurnsRemainingAndDeleteReceiptReleasesMinerUsage(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	minerPublicKey := wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey))
	minerAddress := wire.AccountAddress(&key.PublicKey)
	intent := testLifecycleIntentWithUser(alice.Addr)
	intent.Receipts[0][0] = wire.MinerReceipt{
		MinerAddress:   minerAddress,
		MinerPublicKey: minerPublicKey,
		ShardHash:      "shard_lifecycle",
		ShardSize:      32,
	}
	store.data.Intents[intent.IntentID] = intent
	store.data.Deals[intent.DealID] = intent.IntentID
	store.data.Accounts[intent.User] = wire.Account{Address: intent.User, LockedStorage: gfTokens(10)}
	store.data.Miners[minerAddress] = wire.MinerStats{
		MinerAddress:  minerAddress,
		PublicKey:     minerPublicKey,
		Endpoint:      "http://miner",
		CapacityBytes: 100,
		UsedBytes:     32,
		Stake:         gfTokens(10),
		Status:        "active",
	}

	tdReq := wire.TerminateDealRequest{IntentID: intent.IntentID, User: intent.User}
	signTerminateDeal(t, store, &tdReq, alice)
	terminated, err := store.TerminateDeal(tdReq)
	if err != nil {
		t.Fatal(err)
	}
	if terminated.StorageStatus != wire.StorageStatusTerminating || terminated.AccessStatus != wire.AccessStatusBlocked {
		t.Fatalf("unexpected termination %+v", terminated)
	}
	if len(terminated.DeleteTasks) != 1 {
		t.Fatalf("expected one delete task, got %+v", terminated.DeleteTasks)
	}
	if terminated.DeleteTasks[0].Status != deleteTaskStatusPending {
		t.Fatalf("expected pending delete task, got %+v", terminated.DeleteTasks[0])
	}
	if account := store.accountLocked(intent.User); account.Balance != 0 || account.LockedStorage != 0 {
		t.Fatalf("expected locked storage burn (no refund), got %+v", account)
	}
	if terminated.BurnedFee != gfTokens(10) {
		t.Fatalf("expected 10 tokens burned, got %d", terminated.BurnedFee)
	}

	deleteReceipt := wire.DeleteReceipt{
		IntentID:       intent.IntentID,
		ShardHash:      "shard_lifecycle",
		MinerAddress:   minerAddress,
		MinerPublicKey: minerPublicKey,
		DeletedAtUnix:  time.Now().Unix(),
	}
	if err := wire.SignDeleteReceipt(&deleteReceipt, key); err != nil {
		t.Fatal(err)
	}
	resp, err := store.SubmitDeleteReceipt(wire.SubmitDeleteReceiptRequest{Receipt: deleteReceipt})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != wire.StorageStatusDeleted {
		t.Fatalf("expected deleted status, got %+v", resp)
	}
	if miner := store.minerStatsLocked(minerAddress); miner.UsedBytes != 0 {
		t.Fatalf("expected miner used bytes to release, got %+v", miner)
	}
	if store.data.Intents[intent.IntentID].Status != wire.StatusDeleted {
		t.Fatalf("expected intent deleted, got %+v", store.data.Intents[intent.IntentID])
	}
}

func TestCommitteeFreezeDealRequiresExpiryAndExpiresBackToDefaultAccess(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	intent := testLifecycleIntent()
	store.data.Intents[intent.IntentID] = intent

	// Freeze without expiry should fail.
	store.mu.Lock()
	_, err = store.governanceDealActionLocked(wire.GovernanceDealActionRequest{
		IntentID:   intent.IntentID,
		Operator:   "committee",
		Action:     "freeze",
		ReasonHash: "freeze_reason",
	}, time.Now().Unix())
	store.mu.Unlock()
	if err == nil {
		t.Fatal("expected freeze without expiry to fail")
	}

	expiresAt := time.Now().Add(time.Minute).Unix()
	store.mu.Lock()
	resp, err := store.governanceDealActionLocked(wire.GovernanceDealActionRequest{
		IntentID:      intent.IntentID,
		Operator:      "committee",
		Action:        "freeze",
		ReasonHash:    "freeze_reason",
		ExpiresAtUnix: expiresAt,
	}, time.Now().Unix())
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if resp.ModerationStatus != wire.ModerationStatusFrozen || resp.ModerationExpiresAtUnix != expiresAt {
		t.Fatalf("unexpected freeze response %+v", resp)
	}
	intent.ModerationExpiresAtUnix = time.Now().Add(-time.Minute).Unix()
	normalizeIntentLifecycle(intent)
	if intent.ModerationStatus != wire.ModerationStatusNone {
		t.Fatalf("expected expired freeze to clear moderation, got %+v", intent)
	}
	if intent.AccessStatus != wire.AccessStatusPublic {
		t.Fatalf("expected expired freeze to restore default access, got %+v", intent)
	}
}

func TestGovernanceLegalHoldBlocksAccessButKeepsProofsActive(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	intent := testLifecycleIntent()
	store.data.Intents[intent.IntentID] = intent
	store.mu.Lock()
	resp, err := store.governanceDealActionLocked(wire.GovernanceDealActionRequest{
		IntentID:        intent.IntentID,
		Operator:        "committee",
		Action:          "legal_hold",
		ReasonHash:      "legal_reason",
		PreserveStorage: true,
	}, time.Now().Unix())
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if resp.AccessStatus != wire.AccessStatusBlocked || resp.ModerationStatus != wire.ModerationStatusLegalHold {
		t.Fatalf("unexpected governance response %+v", resp)
	}
	if !intentAllowsStorageProof(store.data.Intents[intent.IntentID]) {
		t.Fatal("legal hold should keep storage proofs active")
	}
	if _, err := store.Manifest(intent.IntentID); err == nil {
		t.Fatal("legal hold should block public manifest access")
	}
}

func TestGovernanceBlockDealCreatesDeleteTasksAndAudit(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	intent := testLifecycleIntent()
	intent.Receipts[0][0] = wire.MinerReceipt{
		MinerAddress:   "miner_lifecycle",
		MinerPublicKey: "miner_pub",
		ShardHash:      "shard_lifecycle",
		ShardSize:      32,
	}
	store.data.Intents[intent.IntentID] = intent
	appealDeadline := time.Now().Add(2 * time.Hour).Unix()
	store.mu.Lock()
	resp, err := store.governanceDealActionLocked(wire.GovernanceDealActionRequest{
		IntentID:           intent.IntentID,
		Operator:           "dao",
		Action:             "block",
		ReasonHash:         "block_reason",
		PreserveStorage:    false,
		AppealDeadlineUnix: appealDeadline,
	}, time.Now().Unix())
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if resp.GovernanceType != "governance_block_deal" || resp.ModerationStatus != wire.ModerationStatusBlocked {
		t.Fatalf("unexpected block response %+v", resp)
	}
	if resp.StorageStatus != wire.StorageStatusTerminating || resp.AppealDeadlineUnix != appealDeadline {
		t.Fatalf("expected terminating block response with appeal deadline, got %+v", resp)
	}
	tasks := store.DeleteTasks(intent.IntentID, "miner_lifecycle", deleteTaskStatusPending)
	if len(tasks.Tasks) != 1 {
		t.Fatalf("expected one pending delete task after governance block, got %+v", tasks)
	}
	audit := store.GovernanceAudit(intent.IntentID, "dao", "block")
	if len(audit.Records) != 1 {
		t.Fatalf("expected one governance block audit record, got %+v", audit)
	}
	if audit.Records[0].GovernanceType != "governance_block_deal" || audit.Records[0].AppealDeadlineUnix != appealDeadline {
		t.Fatalf("expected governance block audit fields, got %+v", audit.Records[0])
	}
}

func TestGovernanceOperatorAllowlistRejectsUnauthorizedOperator(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	intent := testLifecycleIntent()
	store.data.Intents[intent.IntentID] = intent
	store.data.GovernanceOperators["committee"] = wire.GovernanceOperator{
		Operator:    "committee",
		Permissions: []string{"freeze"},
		Enabled:     true,
	}
	// Operator with only "freeze" permission should be rejected for "block".
	store.mu.Lock()
	err = store.validateGovernanceOperatorLocked("committee", "block")
	store.mu.Unlock()
	if err == nil {
		t.Fatal("expected operator without block permission to be rejected")
	}
	// Operator with "freeze" permission should succeed for "freeze".
	store.mu.Lock()
	err = store.validateGovernanceOperatorLocked("committee", "freeze")
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Verify the freeze action also works end-to-end.
	store.mu.Lock()
	_, err = store.governanceDealActionLocked(wire.GovernanceDealActionRequest{
		IntentID:      intent.IntentID,
		Operator:      "committee",
		Action:        "freeze",
		ReasonHash:    "freeze_reason",
		ExpiresAtUnix: time.Now().Add(time.Hour).Unix(),
	}, time.Now().Unix())
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeleteTaskRetainsPhysicalShardWhenSharedByActiveIntent(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	intentA := testLifecycleIntentWithUser(alice.Addr)
	intentA.IntentID = "intent_a"
	intentA.DealID = "deal_a"
	intentA.Receipts[0][0] = wire.MinerReceipt{
		MinerAddress:   "miner_lifecycle",
		MinerPublicKey: "miner_pub",
		ShardHash:      "shared_shard",
		ShardSize:      32,
	}
	intentB := testLifecycleIntentWithUser(alice.Addr)
	intentB.IntentID = "intent_b"
	intentB.DealID = "deal_b"
	intentB.Receipts[0][0] = wire.MinerReceipt{
		MinerAddress:   "miner_lifecycle",
		MinerPublicKey: "miner_pub",
		ShardHash:      "shared_shard",
		ShardSize:      32,
	}
	store.data.Intents[intentA.IntentID] = intentA
	store.data.Intents[intentB.IntentID] = intentB
	store.data.Accounts[intentA.User] = wire.Account{Address: intentA.User, LockedStorage: gfTokens(10)}

	tdReq := wire.TerminateDealRequest{IntentID: intentA.IntentID, User: intentA.User}
	signTerminateDeal(t, store, &tdReq, alice)
	resp, err := store.TerminateDeal(tdReq)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.DeleteTasks) != 1 {
		t.Fatalf("expected one delete task, got %+v", resp)
	}
	task := resp.DeleteTasks[0]
	if !task.RetainPhysical || task.ActiveReferences != 1 {
		t.Fatalf("expected shared shard delete task to retain physical data, got %+v", task)
	}
}

func TestDeleteTasksQueryFiltersByStatusAndIntent(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	minerPublicKey := wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey))
	minerAddress := wire.AccountAddress(&key.PublicKey)
	intent := testLifecycleIntentWithUser(alice.Addr)
	intent.Receipts[0][0] = wire.MinerReceipt{
		MinerAddress:   minerAddress,
		MinerPublicKey: minerPublicKey,
		ShardHash:      "shard_lifecycle",
		ShardSize:      32,
	}
	store.data.Intents[intent.IntentID] = intent
	store.data.Accounts[intent.User] = wire.Account{Address: intent.User, LockedStorage: gfTokens(10)}
	store.data.Miners[minerAddress] = wire.MinerStats{
		MinerAddress: minerAddress,
		PublicKey:    minerPublicKey,
		Stake:        gfTokens(10),
		Status:       "active",
		UsedBytes:    32,
	}
	tdReq := wire.TerminateDealRequest{IntentID: intent.IntentID, User: intent.User}
	signTerminateDeal(t, store, &tdReq, alice)
	terminated, err := store.TerminateDeal(tdReq)
	if err != nil {
		t.Fatal(err)
	}
	pending := store.DeleteTasks(intent.IntentID, minerAddress, deleteTaskStatusPending)
	if len(pending.Tasks) != 1 {
		t.Fatalf("expected one pending delete task, got %+v", pending)
	}

	deleteReceipt := wire.DeleteReceipt{
		IntentID:       intent.IntentID,
		ShardHash:      "shard_lifecycle",
		MinerAddress:   minerAddress,
		MinerPublicKey: minerPublicKey,
		DeletedAtUnix:  time.Now().Unix(),
	}
	if err := wire.SignDeleteReceipt(&deleteReceipt, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SubmitDeleteReceipt(wire.SubmitDeleteReceiptRequest{Receipt: deleteReceipt}); err != nil {
		t.Fatal(err)
	}
	completed := store.DeleteTasks(intent.IntentID, minerAddress, deleteTaskStatusCompleted)
	if len(completed.Tasks) != len(terminated.DeleteTasks) {
		t.Fatalf("expected completed delete tasks to match generated tasks, got %+v", completed)
	}
}

func testLifecycleIntent() *Intent {
	return &Intent{
		IntentView: wire.IntentView{
			IntentID:         "intent_lifecycle",
			User:             "alice",
			FileName:         "data.bin",
			FileSize:         32,
			SegmentSize:      32,
			FileRoot:         "file_lifecycle",
			SegmentRoots:     []string{"segment_lifecycle"},
			Segments:         []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment_lifecycle", ShardHashes: []string{"shard_lifecycle"}}},
			Erasure:          wire.ErasurePolicy{DataShards: 1, ParityShards: 0, ShardSize: 32},
			Policy:           wire.StoragePolicy{Duration: int64(365 * 24 * time.Hour / time.Second)},
			ExpiresAtUnix:    time.Now().Add(365 * 24 * time.Hour).Unix(),
			LockedFee:        gfTokens(10),
			Status:           wire.StatusFinalized,
			StorageStatus:    wire.StorageStatusActive,
			AccessStatus:     wire.AccessStatusPublic,
			ModerationStatus: wire.ModerationStatusNone,
			DealID:           "deal_lifecycle",
		},
		Receipts: map[int]map[int]wire.MinerReceipt{
			0: {
				0: {
					MinerAddress:   "miner_lifecycle",
					MinerPublicKey: "miner_pub",
					MinerEndpoint:  "http://miner",
					ShardHash:      "shard_lifecycle",
					ShardSize:      32,
				},
			},
		},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
}
