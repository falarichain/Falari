package chain

import (
	"crypto/ecdsa"
	"encoding/hex"
	"testing"
	"time"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// testDirectActionSetup creates a store with 3 enabled governance operators and a test intent.
func testDirectActionSetup(t *testing.T) (*Store, [3]*ecdsa.PrivateKey, [3]string) {
	t.Helper()
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	var privKeys [3]*ecdsa.PrivateKey
	var addresses [3]string

	for i := 0; i < 3; i++ {
		priv, err := ethcrypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		privKeys[i] = priv
		addr := wire.AccountAddress(&priv.PublicKey)
		addresses[i] = addr
		store.data.GovernanceOperators[addr] = wire.GovernanceOperator{
			Operator:    addr,
			PublicKey:   "0x" + hex.EncodeToString(ethcrypto.FromECDSAPub(&priv.PublicKey)),
			Permissions: []string{"all"},
			Enabled:     true,
		}
	}

	intent := testLifecycleIntent()
	store.data.Intents[intent.IntentID] = intent
	store.data.Deals[intent.DealID] = intent.IntentID

	return store, privKeys, addresses
}

func signDirectAction(t *testing.T, store *Store, req *wire.DirectGovernanceActionRequest, privKey *ecdsa.PrivateKey) {
	t.Helper()
	req.ChainID = store.data.ChainID
	req.Nonce = store.data.OperatorNonces[normalizeGovernanceOperator(req.Operator)]
	if req.CreatedAtUnix == 0 {
		req.CreatedAtUnix = time.Now().Unix()
	}
	if err := wire.SignDirectGovernanceAction(req, privKey); err != nil {
		t.Fatalf("failed to sign direct action: %v", err)
	}
}

func signReviewVote(t *testing.T, store *Store, req *wire.DirectActionReviewVoteRequest, privKey *ecdsa.PrivateKey) {
	t.Helper()
	req.ChainID = store.data.ChainID
	req.Nonce = store.data.OperatorNonces[normalizeGovernanceOperator(req.Voter)]
	if req.CreatedAtUnix == 0 {
		req.CreatedAtUnix = time.Now().Unix()
	}
	if err := wire.SignDirectActionReviewVote(req, privKey); err != nil {
		t.Fatalf("failed to sign review vote: %v", err)
	}
}

func TestDirectGovernanceActionFreeze(t *testing.T) {
	store, privKeys, addresses := testDirectActionSetup(t)

	expiresAt := time.Now().Add(48 * time.Hour).Unix()
	req := wire.DirectGovernanceActionRequest{
		Operator:      addresses[0],
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "direct_freeze_reason",
		ExpiresAtUnix: expiresAt,
	}
	signDirectAction(t, store, &req, privKeys[0])

	resp, err := store.DirectGovernanceAction(req)
	if err != nil {
		t.Fatalf("DirectGovernanceAction failed: %v", err)
	}

	// Verify response.
	if resp.Record.ReviewStatus != wire.DirectActionPendingReview {
		t.Fatalf("expected pending_review, got %s", resp.Record.ReviewStatus)
	}
	if resp.GovernanceResult.ModerationStatus != wire.ModerationStatusFrozen {
		t.Fatalf("expected frozen moderation, got %s", resp.GovernanceResult.ModerationStatus)
	}
	if resp.GovernanceResult.AccessStatus != wire.AccessStatusSuspended {
		t.Fatalf("expected suspended access, got %s", resp.GovernanceResult.AccessStatus)
	}

	// Verify blacklist entry has review status.
	entry, ok := store.data.BlacklistedShards["shard_lifecycle"]
	if !ok {
		t.Fatal("expected blacklist entry for shard_lifecycle")
	}
	if entry.ReviewStatus != wire.DirectActionPendingReview {
		t.Fatalf("expected blacklist review_status pending_review, got %s", entry.ReviewStatus)
	}
	if entry.ActionID != resp.Record.ActionID {
		t.Fatalf("expected action_id %s, got %s", resp.Record.ActionID, entry.ActionID)
	}

	// Verify nonce was advanced.
	nonce := store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])]
	if nonce != 1 {
		t.Fatalf("expected nonce 1, got %d", nonce)
	}

	// Verify pre-action state was captured.
	if resp.Record.PreAccessStatus != wire.AccessStatusPublic {
		t.Fatalf("expected pre-access public, got %s", resp.Record.PreAccessStatus)
	}
	if resp.Record.PreModerationStatus != wire.ModerationStatusNone {
		t.Fatalf("expected pre-moderation none, got %s", resp.Record.PreModerationStatus)
	}
}

func TestDirectGovernanceActionBlockDoesNotDeleteDuringReview(t *testing.T) {
	store, privKeys, addresses := testDirectActionSetup(t)

	// Add miner receipt so delete tasks would be created.
	intent := store.data.Intents["intent_lifecycle"]
	intent.Receipts[0][0] = wire.MinerReceipt{
		MinerAddress:   "miner_lifecycle",
		MinerPublicKey: "miner_pub",
		ShardHash:      "shard_lifecycle",
		ShardSize:      32,
	}

	appealDeadline := time.Now().Add(2 * time.Hour).Unix()
	req := wire.DirectGovernanceActionRequest{
		Operator:           addresses[0],
		IntentID:           "intent_lifecycle",
		Action:             "block",
		ReasonHash:         "direct_block_reason",
		PreserveStorage:    false,
		AppealDeadlineUnix: appealDeadline,
	}
	signDirectAction(t, store, &req, privKeys[0])

	resp, err := store.DirectGovernanceAction(req)
	if err != nil {
		t.Fatalf("DirectGovernanceAction block failed: %v", err)
	}

	// Key assertion: storage should NOT be terminating during review.
	if resp.GovernanceResult.StorageStatus != wire.StorageStatusActive {
		t.Fatalf("expected storage active during review, got %s", resp.GovernanceResult.StorageStatus)
	}

	// No delete tasks should exist during review.
	tasks := store.DeleteTasks("intent_lifecycle", "", "")
	if len(tasks.Tasks) != 0 {
		t.Fatalf("expected no delete tasks during review, got %d", len(tasks.Tasks))
	}
}

func TestDirectActionRatifyTriggersDeletion(t *testing.T) {
	store, privKeys, addresses := testDirectActionSetup(t)

	intent := store.data.Intents["intent_lifecycle"]
	intent.Receipts[0][0] = wire.MinerReceipt{
		MinerAddress:   "miner_lifecycle",
		MinerPublicKey: "miner_pub",
		ShardHash:      "shard_lifecycle",
		ShardSize:      32,
	}

	appealDeadline := time.Now().Add(2 * time.Hour).Unix()
	req := wire.DirectGovernanceActionRequest{
		Operator:           addresses[0],
		IntentID:           "intent_lifecycle",
		Action:             "block",
		ReasonHash:         "direct_block_reason",
		PreserveStorage:    false,
		AppealDeadlineUnix: appealDeadline,
	}
	signDirectAction(t, store, &req, privKeys[0])

	resp, err := store.DirectGovernanceAction(req)
	if err != nil {
		t.Fatal(err)
	}

	// Ratify the action.
	if err := store.RatifyDirectAction(resp.Record.ActionID, time.Now().Unix()); err != nil {
		t.Fatalf("RatifyDirectAction failed: %v", err)
	}

	// Verify record is ratified.
	record := store.data.DirectActionRecords[resp.Record.ActionID]
	if record.ReviewStatus != wire.DirectActionRatified {
		t.Fatalf("expected ratified, got %s", record.ReviewStatus)
	}

	// Verify storage is now terminating.
	intent = store.data.Intents["intent_lifecycle"]
	if intent.StorageStatus != wire.StorageStatusTerminating {
		t.Fatalf("expected storage terminating after ratify, got %s", intent.StorageStatus)
	}

	// Verify delete tasks were created.
	tasks := store.DeleteTasks("intent_lifecycle", "", "pending")
	if len(tasks.Tasks) != 1 {
		t.Fatalf("expected 1 pending delete task after ratify, got %d", len(tasks.Tasks))
	}

	// Verify blacklist entry no longer has review status.
	entry := store.data.BlacklistedShards["shard_lifecycle"]
	if entry.ReviewStatus != "" {
		t.Fatalf("expected empty review_status after ratify, got %s", entry.ReviewStatus)
	}
}

func TestDirectActionRejectionRollsback(t *testing.T) {
	store, privKeys, addresses := testDirectActionSetup(t)

	// Freeze action for simpler test (no delete task complications).
	expiresAt := time.Now().Add(48 * time.Hour).Unix()
	req := wire.DirectGovernanceActionRequest{
		Operator:      addresses[0],
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "direct_freeze_reason",
		ExpiresAtUnix: expiresAt,
	}
	signDirectAction(t, store, &req, privKeys[0])

	resp, err := store.DirectGovernanceAction(req)
	if err != nil {
		t.Fatal(err)
	}
	actionID := resp.Record.ActionID

	// Verify intent is suspended.
	intent := store.data.Intents["intent_lifecycle"]
	if intent.AccessStatus != wire.AccessStatusSuspended {
		t.Fatalf("expected suspended, got %s", intent.AccessStatus)
	}

	// Cast rejection vote from operator 1 (1/3 threshold = 1 vote needed).
	voteReq := wire.DirectActionReviewVoteRequest{
		ActionID: actionID,
		Voter:    addresses[1],
		Reject:   true,
	}
	signReviewVote(t, store, &voteReq, privKeys[1])

	voteResp, err := store.CastDirectActionReviewVote(voteReq)
	if err != nil {
		t.Fatalf("CastDirectActionReviewVote failed: %v", err)
	}
	if !voteResp.Rejected {
		t.Fatal("expected action to be rejected")
	}

	// Verify record is rejected.
	record := store.data.DirectActionRecords[actionID]
	if record.ReviewStatus != wire.DirectActionRejected {
		t.Fatalf("expected rejected, got %s", record.ReviewStatus)
	}

	// Verify intent state was rolled back.
	intent = store.data.Intents["intent_lifecycle"]
	if intent.AccessStatus != wire.AccessStatusPublic {
		t.Fatalf("expected public after rollback, got %s", intent.AccessStatus)
	}
	if intent.ModerationStatus != wire.ModerationStatusNone {
		t.Fatalf("expected none moderation after rollback, got %s", intent.ModerationStatus)
	}

	// Verify blacklist entry was removed.
	_, ok := store.data.BlacklistedShards["shard_lifecycle"]
	if ok {
		t.Fatal("expected blacklist entry to be removed after rejection")
	}
}

func TestDirectActionAutoRatifyOnExpiry(t *testing.T) {
	store, privKeys, addresses := testDirectActionSetup(t)

	expiresAt := time.Now().Add(48 * time.Hour).Unix()
	req := wire.DirectGovernanceActionRequest{
		Operator:      addresses[0],
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "direct_freeze_reason",
		ExpiresAtUnix: expiresAt,
	}
	signDirectAction(t, store, &req, privKeys[0])

	resp, err := store.DirectGovernanceAction(req)
	if err != nil {
		t.Fatal(err)
	}
	actionID := resp.Record.ActionID

	// Simulate review window expiry.
	record := store.data.DirectActionRecords[actionID]
	record.ReviewDeadlineUnix = time.Now().Add(-1 * time.Hour).Unix()
	store.data.DirectActionRecords[actionID] = record

	// Run expiry check.
	count := store.ExpireDirectActionReviews()
	if count != 1 {
		t.Fatalf("expected 1 auto-ratified action, got %d", count)
	}

	// Verify record is auto-ratified.
	record = store.data.DirectActionRecords[actionID]
	if record.ReviewStatus != wire.DirectActionAutoRatified {
		t.Fatalf("expected auto_ratified, got %s", record.ReviewStatus)
	}
}

func TestDirectActionRejectAfterExpiryFails(t *testing.T) {
	store, privKeys, addresses := testDirectActionSetup(t)

	expiresAt := time.Now().Add(48 * time.Hour).Unix()
	req := wire.DirectGovernanceActionRequest{
		Operator:      addresses[0],
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "direct_freeze_reason",
		ExpiresAtUnix: expiresAt,
	}
	signDirectAction(t, store, &req, privKeys[0])

	resp, err := store.DirectGovernanceAction(req)
	if err != nil {
		t.Fatal(err)
	}
	actionID := resp.Record.ActionID

	// Set review deadline in the past.
	record := store.data.DirectActionRecords[actionID]
	record.ReviewDeadlineUnix = time.Now().Add(-1 * time.Second).Unix()
	store.data.DirectActionRecords[actionID] = record

	// Attempt to cast reject vote — should fail with auto-ratification.
	voteReq := wire.DirectActionReviewVoteRequest{
		ActionID: actionID,
		Voter:    addresses[1],
		Reject:   true,
	}
	signReviewVote(t, store, &voteReq, privKeys[1])

	_, err = store.CastDirectActionReviewVote(voteReq)
	if err == nil {
		t.Fatal("expected error when voting on expired review window")
	}
}

func TestDirectActionListFiltering(t *testing.T) {
	store, privKeys, addresses := testDirectActionSetup(t)

	// Create two direct actions.
	for i, action := range []string{"freeze", "legal_hold"} {
		req := wire.DirectGovernanceActionRequest{
			Operator:      addresses[0],
			IntentID:      "intent_lifecycle",
			Action:        action,
			ReasonHash:    "reason_" + action,
			ExpiresAtUnix: time.Now().Add(48 * time.Hour).Unix(),
		}
		signDirectAction(t, store, &req, privKeys[0])
		_, err := store.DirectGovernanceAction(req)
		if err != nil {
			t.Fatalf("direct action %d failed: %v", i, err)
		}
	}

	// List all.
	all := store.ListDirectActions("", "")
	if len(all.Records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(all.Records))
	}

	// Filter by status.
	pending := store.ListDirectActions("", wire.DirectActionPendingReview)
	if len(pending.Records) != 2 {
		t.Fatalf("expected 2 pending records, got %d", len(pending.Records))
	}
}
