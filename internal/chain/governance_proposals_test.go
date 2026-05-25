package chain

import (
	"crypto/ecdsa"
	"encoding/hex"
	"testing"
	"time"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func testEncodeHex(raw []byte) string {
	return "0x" + hex.EncodeToString(raw)
}

// testGovernanceSetup creates a store with 3 enabled governance operators and a test intent.
func testGovernanceSetup(t *testing.T) (*Store, [3]*ecdsa.PrivateKey, [3]string) {
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
			PublicKey:   testEncodeHex(ethcrypto.FromECDSAPub(&priv.PublicKey)),
			Permissions: []string{"all"},
			Enabled:     true,
		}
	}

	// Add a test intent.
	intent := testLifecycleIntent()
	store.data.Intents[intent.IntentID] = intent
	store.data.Deals[intent.DealID] = intent.IntentID

	return store, privKeys, addresses
}

func testGovernanceProposalReq(t *testing.T, address string, privKey *ecdsa.PrivateKey) wire.CreateGovernanceProposalRequest {
	t.Helper()
	req := wire.CreateGovernanceProposalRequest{
		Proposer:      address,
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "reason_hash_test",
		ExpiresAtUnix: time.Now().Add(48 * time.Hour).Unix(),
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKey); err != nil {
		t.Fatalf("failed to sign proposal: %v", err)
	}
	return req
}

func TestCreateGovernanceProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	req := testGovernanceProposalReq(t, addresses[0], privKeys[0])
	resp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("CreateGovernanceProposal failed: %v", err)
	}
	if resp.Proposal.Status != wire.GovProposalPending {
		t.Fatalf("expected status pending, got %s", resp.Proposal.Status)
	}
	if resp.Proposal.ProposalID == "" {
		t.Fatal("proposal ID is empty")
	}
	if resp.Proposal.Action != "freeze" {
		t.Fatalf("expected action freeze, got %s", resp.Proposal.Action)
	}
}

func TestCreateGovernanceProposalInvalidSignature(t *testing.T) {
	store, _, addresses := testGovernanceSetup(t)

	req := wire.CreateGovernanceProposalRequest{
		Proposer:      addresses[0],
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "reason_hash",
		ExpiresAtUnix: time.Now().Add(48 * time.Hour).Unix(),
		CreatedAtUnix: time.Now().Unix(),
		Signature:     "0xdeadbeef",
	}
	if _, err := store.CreateGovernanceProposal(req); err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestCreateGovernanceProposalUnauthorizedOperator(t *testing.T) {
	store, privKeys, _ := testGovernanceSetup(t)

	// Use an address that is not in governance operators.
	fakePriv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	fakeAddr := wire.AccountAddress(&fakePriv.PublicKey)
	req := wire.CreateGovernanceProposalRequest{
		Proposer:      fakeAddr,
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "reason_hash",
		ExpiresAtUnix: time.Now().Add(48 * time.Hour).Unix(),
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGovernanceProposal(req); err == nil {
		t.Fatal("expected error for unauthorized operator")
	}
}

func TestCastGovernanceVote(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Create proposal.
	proposalReq := testGovernanceProposalReq(t, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	// Cast vote.
	voteReq := wire.CastGovernanceVoteRequest{
		ProposalID:    proposalResp.Proposal.ProposalID,
		Voter:         addresses[1],
		Approve:       true,
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceVote(&voteReq, privKeys[1]); err != nil {
		t.Fatal(err)
	}
	voteResp, err := store.CastGovernanceVote(voteReq)
	if err != nil {
		t.Fatalf("CastGovernanceVote failed: %v", err)
	}
	if voteResp.ApproveCount != 1 {
		t.Fatalf("expected 1 approval, got %d", voteResp.ApproveCount)
	}
	if voteResp.Threshold < 1 {
		t.Fatalf("expected threshold >= 1, got %d", voteResp.Threshold)
	}
}

func TestCastGovernanceVoteDoubleVoting(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Create proposal.
	proposalReq := testGovernanceProposalReq(t, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	// First vote.
	voteReq := wire.CastGovernanceVoteRequest{
		ProposalID:    proposalResp.Proposal.ProposalID,
		Voter:         addresses[0],
		Approve:       true,
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceVote(&voteReq, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CastGovernanceVote(voteReq); err != nil {
		t.Fatal(err)
	}

	// Second vote (same voter) should fail.
	voteReq2 := wire.CastGovernanceVoteRequest{
		ProposalID:    proposalResp.Proposal.ProposalID,
		Voter:         addresses[0],
		Approve:       false,
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceVote(&voteReq2, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CastGovernanceVote(voteReq2); err == nil {
		t.Fatal("expected error for double voting")
	}
}

func TestExecuteGovernanceProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Create proposal.
	proposalReq := testGovernanceProposalReq(t, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}
	proposalID := proposalResp.Proposal.ProposalID

	// All 3 operators vote approve (threshold for 3 operators = 2).
	for i := 0; i < 3; i++ {
		voteReq := wire.CastGovernanceVoteRequest{
			ProposalID:    proposalID,
			Voter:         addresses[i],
			Approve:       true,
			CreatedAtUnix: time.Now().Unix(),
		}
		if err := wire.SignGovernanceVote(&voteReq, privKeys[i]); err != nil {
			t.Fatal(err)
		}
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	// Check proposal was executed.
	proposal := store.data.GovernanceProposals[proposalID]
	if proposal.Status != wire.GovProposalExecuted {
		t.Fatalf("expected status executed, got %s", proposal.Status)
	}

	// Verify the intent was frozen.
	intent := store.data.Intents["intent_lifecycle"]
	if intent.ModerationStatus != wire.ModerationStatusFrozen {
		t.Fatalf("expected moderation frozen, got %s", intent.ModerationStatus)
	}
}

func TestExecuteGovernanceProposalInsufficientVotes(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Create proposal.
	proposalReq := testGovernanceProposalReq(t, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	// Only 1 approval vote (threshold for 3 operators = 2).
	voteReq := wire.CastGovernanceVoteRequest{
		ProposalID:    proposalResp.Proposal.ProposalID,
		Voter:         addresses[0],
		Approve:       true,
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceVote(&voteReq, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CastGovernanceVote(voteReq); err != nil {
		t.Fatal(err)
	}

	// Try to execute — should fail with insufficient votes.
	_, err = store.ExecuteGovernanceProposal(wire.ExecuteGovernanceProposalRequest{
		ProposalID: proposalResp.Proposal.ProposalID,
	})
	if err == nil {
		t.Fatal("expected error for insufficient votes")
	}
}

func TestCancelGovernanceProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Create proposal.
	proposalReq := testGovernanceProposalReq(t, addresses[0], privKeys[0])
	_, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	// Cancel it.
	cancelReq := wire.CreateGovernanceProposalRequest{
		Proposer:      addresses[0],
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "reason_hash_test",
		ExpiresAtUnix: time.Now().Add(48 * time.Hour).Unix(),
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&cancelReq, privKeys[0]); err != nil {
		t.Fatalf("failed to sign proposal: %v", err)
	}
	cancelResp, err := store.CancelGovernanceProposal(cancelReq)
	if err != nil {
		t.Fatalf("CancelGovernanceProposal failed: %v", err)
	}
	if cancelResp.Proposal.Status != wire.GovProposalCancelled {
		t.Fatalf("expected status cancelled, got %s", cancelResp.Proposal.Status)
	}
}

func TestGovernanceThreshold(t *testing.T) {
	store, _, _ := testGovernanceSetup(t)

	// 3 enabled operators, default thresholds: data moderation = 1/3, operator changes = 2/3.
	// Data moderation: ceil(3 * 1/3) = 1.
	dataModThreshold := store.governanceThresholdLocked("freeze")
	if dataModThreshold != 1 {
		t.Fatalf("expected data moderation threshold 1 for 3 operators, got %d", dataModThreshold)
	}
	// Operator changes: ceil(3 * 2/3) = 2.
	opChangeThreshold := store.governanceThresholdLocked("add_operator")
	if opChangeThreshold != 2 {
		t.Fatalf("expected operator change threshold 2 for 3 operators, got %d", opChangeThreshold)
	}
}

func TestAddOperatorViaProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Generate a key for the new operator.
	newPriv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPubHex := testEncodeHex(ethcrypto.FromECDSAPub(&newPriv.PublicKey))
	newAddr := wire.AccountAddress(&newPriv.PublicKey)

	// Propose add_operator (no intent_id needed).
	req := wire.CreateGovernanceProposalRequest{
		Proposer:          addresses[0],
		Action:            "add_operator",
		ReasonHash:        "add_new_member",
		TargetPublicKey:    newPubHex,
		TargetPermissions: []string{"freeze", "block"},
		CreatedAtUnix:     time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	propResp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("add_operator proposal failed: %v", err)
	}
	if propResp.Proposal.IntentID != "" {
		t.Fatal("operator management proposal should not have intent_id")
	}
	proposalID := propResp.Proposal.ProposalID

	// Vote to approve (need 2 of 3).
	for i := 0; i < 2; i++ {
		voteReq := wire.CastGovernanceVoteRequest{
			ProposalID:    proposalID,
			Voter:         addresses[i],
			Approve:       true,
			CreatedAtUnix: time.Now().Unix(),
		}
		if err := wire.SignGovernanceVote(&voteReq, privKeys[i]); err != nil {
			t.Fatal(err)
		}
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	// Verify new operator was added at the derived address.
	op, ok := store.data.GovernanceOperators[newAddr]
	if !ok {
		t.Fatal("new operator was not added")
	}
	if !op.Enabled {
		t.Fatal("new operator should be enabled")
	}
	if op.PublicKey != newPubHex {
		t.Fatalf("expected public key %s, got %s", newPubHex, op.PublicKey)
	}
	if len(op.Permissions) != 2 || op.Permissions[0] != "freeze" || op.Permissions[1] != "block" {
		t.Fatalf("unexpected permissions: %v", op.Permissions)
	}

	// Verify threshold now reflects 4 operators (operator change = 2/3: ceil(4*2/3) = 3).
	threshold := store.governanceThresholdLocked("add_operator")
	if threshold != 3 {
		t.Fatalf("expected threshold 3 for 4 operators, got %d", threshold)
	}
}

func TestRemoveOperatorViaProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)
	targetAddr := addresses[2]

	// Propose remove_operator.
	req := wire.CreateGovernanceProposalRequest{
		Proposer:       addresses[0],
		Action:         "remove_operator",
		ReasonHash:     "remove_member",
		TargetOperator: targetAddr,
		CreatedAtUnix:  time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	propResp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("remove_operator proposal failed: %v", err)
	}
	proposalID := propResp.Proposal.ProposalID

	// Vote to approve (2 of 3; the target can also vote).
	for i := 0; i < 2; i++ {
		voteReq := wire.CastGovernanceVoteRequest{
			ProposalID:    proposalID,
			Voter:         addresses[i],
			Approve:       true,
			CreatedAtUnix: time.Now().Unix(),
		}
		if err := wire.SignGovernanceVote(&voteReq, privKeys[i]); err != nil {
			t.Fatal(err)
		}
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	// Verify operator was disabled.
	op := store.data.GovernanceOperators[targetAddr]
	if op.Enabled {
		t.Fatal("removed operator should be disabled")
	}

	// Threshold should now reflect 2 enabled operators (operator change = 2/3: ceil(2*2/3) = 2).
	threshold := store.governanceThresholdLocked("remove_operator")
	if threshold != 2 {
		t.Fatalf("expected threshold 2 for 2 enabled operators, got %d", threshold)
	}
}

func TestUpdateOperatorViaProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)
	targetAddr := addresses[1]

	// Propose update_operator with new permissions only (no key change).
	req := wire.CreateGovernanceProposalRequest{
		Proposer:          addresses[0],
		Action:            "update_operator",
		ReasonHash:        "update_permissions",
		TargetOperator:    targetAddr,
		TargetPermissions: []string{"freeze"},
		CreatedAtUnix:     time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	propResp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("update_operator proposal failed: %v", err)
	}
	proposalID := propResp.Proposal.ProposalID

	// Vote to approve.
	for i := 0; i < 2; i++ {
		voteReq := wire.CastGovernanceVoteRequest{
			ProposalID:    proposalID,
			Voter:         addresses[i],
			Approve:       true,
			CreatedAtUnix: time.Now().Unix(),
		}
		if err := wire.SignGovernanceVote(&voteReq, privKeys[i]); err != nil {
			t.Fatal(err)
		}
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	// Verify operator permissions were updated.
	op := store.data.GovernanceOperators[targetAddr]
	if len(op.Permissions) != 1 || op.Permissions[0] != "freeze" {
		t.Fatalf("expected permissions [freeze], got %v", op.Permissions)
	}
}

func TestUpdateOperatorKeyRotationRejected(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)
	targetAddr := addresses[1]

	// Generate a new key for attempted rotation.
	newPriv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPubHex := testEncodeHex(ethcrypto.FromECDSAPub(&newPriv.PublicKey))

	// Propose update_operator with a new public key — should be rejected.
	req := wire.CreateGovernanceProposalRequest{
		Proposer:          addresses[0],
		Action:            "update_operator",
		ReasonHash:        "rotate_key",
		TargetOperator:    targetAddr,
		TargetPublicKey:    newPubHex,
		TargetPermissions: []string{"freeze"},
		CreatedAtUnix:     time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGovernanceProposal(req); err == nil {
		t.Fatal("expected error when attempting key rotation via update_operator")
	}
}

func TestAddOperatorDuplicateRejected(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Try to add an operator with the same public key as an existing operator.
	existingPubHex := testEncodeHex(ethcrypto.FromECDSAPub(&privKeys[1].PublicKey))
	req := wire.CreateGovernanceProposalRequest{
		Proposer:       addresses[0],
		Action:         "add_operator",
		ReasonHash:     "duplicate",
		TargetPublicKey: existingPubHex,
		CreatedAtUnix:  time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGovernanceProposal(req); err == nil {
		t.Fatal("expected error when adding an existing operator")
	}
}

func TestDataModerationThresholdLowerThanOperatorChange(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Default: data moderation = 1/3, so for 3 operators threshold = ceil(3*1/3) = 1.
	// A single vote should auto-execute a freeze proposal.
	proposalReq := testGovernanceProposalReq(t, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}
	proposalID := proposalResp.Proposal.ProposalID

	voteReq := wire.CastGovernanceVoteRequest{
		ProposalID:    proposalID,
		Voter:         addresses[0],
		Approve:       true,
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceVote(&voteReq, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	voteResp, err := store.CastGovernanceVote(voteReq)
	if err != nil {
		t.Fatalf("vote failed: %v", err)
	}
	if !voteResp.Executed {
		t.Fatal("expected freeze to auto-execute with 1 approval (1/3 threshold)")
	}
	if voteResp.Threshold != 1 {
		t.Fatalf("expected threshold 1 for data moderation, got %d", voteResp.Threshold)
	}
}

func TestUpdateConfigViaProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Propose update_config: change data moderation to 2/3, operator change to 3/4.
	req := wire.CreateGovernanceProposalRequest{
		Proposer:                           addresses[0],
		Action:                             "update_config",
		ReasonHash:                         "adjust_thresholds",
		TargetDataModerationThresholdNum:   2,
		TargetDataModerationThresholdDen:   3,
		TargetOperatorChangeThresholdNum:   3,
		TargetOperatorChangeThresholdDen:   4,
		CreatedAtUnix:                      time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	propResp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("update_config proposal failed: %v", err)
	}
	proposalID := propResp.Proposal.ProposalID

	// Vote to approve (operator change threshold = 2/3 for 3 ops = 2).
	for i := 0; i < 2; i++ {
		voteReq := wire.CastGovernanceVoteRequest{
			ProposalID:    proposalID,
			Voter:         addresses[i],
			Approve:       true,
			CreatedAtUnix: time.Now().Unix(),
		}
		if err := wire.SignGovernanceVote(&voteReq, privKeys[i]); err != nil {
			t.Fatal(err)
		}
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	// Verify config was updated.
	if store.data.DataModerationThresholdNum != 2 || store.data.DataModerationThresholdDen != 3 {
		t.Fatalf("expected data moderation 2/3, got %d/%d",
			store.data.DataModerationThresholdNum, store.data.DataModerationThresholdDen)
	}
	if store.data.OperatorChangeThresholdNum != 3 || store.data.OperatorChangeThresholdDen != 4 {
		t.Fatalf("expected operator change 3/4, got %d/%d",
			store.data.OperatorChangeThresholdNum, store.data.OperatorChangeThresholdDen)
	}

	// Verify new thresholds are reflected.
	// Data moderation 2/3 for 3 ops: ceil(3*2/3) = 2.
	dataModThreshold := store.governanceThresholdLocked("freeze")
	if dataModThreshold != 2 {
		t.Fatalf("expected data moderation threshold 2, got %d", dataModThreshold)
	}
	// Operator change 3/4 for 3 ops: ceil(3*3/4) = 3.
	opChangeThreshold := store.governanceThresholdLocked("add_operator")
	if opChangeThreshold != 3 {
		t.Fatalf("expected operator change threshold 3, got %d", opChangeThreshold)
	}
}

// ── update_mining_params tests ──

func TestUpdateMiningParamsViaProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Record original values.
	origParams := store.GetMiningParams()

	// Propose update_mining_params: change storage release rate and validator commission.
	req := wire.CreateGovernanceProposalRequest{
		Proposer:                     addresses[0],
		Action:                       "update_mining_params",
		ReasonHash:                   "adjust_mining_params",
		TargetStorageReleaseRateBPS:  5,
		TargetValidatorCommissionBPS: 1500,
		CreatedAtUnix:                time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	propResp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("update_mining_params proposal failed: %v", err)
	}
	proposalID := propResp.Proposal.ProposalID

	// Vote to approve (need 2 of 3, operator change threshold = 2/3).
	for i := 0; i < 2; i++ {
		voteReq := wire.CastGovernanceVoteRequest{
			ProposalID:    proposalID,
			Voter:         addresses[i],
			Approve:       true,
			CreatedAtUnix: time.Now().Unix(),
		}
		if err := wire.SignGovernanceVote(&voteReq, privKeys[i]); err != nil {
			t.Fatal(err)
		}
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	// Verify mining params were updated.
	updatedParams := store.GetMiningParams()
	if updatedParams.StorageReleaseRateBPS != 5 {
		t.Fatalf("expected storage release rate 5, got %d", updatedParams.StorageReleaseRateBPS)
	}
	if updatedParams.ValidatorCommissionBPS != 1500 {
		t.Fatalf("expected validator commission 1500, got %d", updatedParams.ValidatorCommissionBPS)
	}
	// Unchanged fields should remain at original values.
	if updatedParams.StoredBytesWeightBPS != origParams.StoredBytesWeightBPS {
		t.Fatalf("stored bytes weight should be unchanged: expected %d, got %d",
			origParams.StoredBytesWeightBPS, updatedParams.StoredBytesWeightBPS)
	}
}

func TestUpdateMiningParamsPartialUpdate(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	origParams := store.GetMiningParams()

	// Only change proof score weight.
	req := wire.CreateGovernanceProposalRequest{
		Proposer:                addresses[0],
		Action:                  "update_mining_params",
		ReasonHash:              "tune_proof_weight",
		TargetProofScoreWeightBPS: 4000,
		CreatedAtUnix:           time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	propResp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("proposal failed: %v", err)
	}
	proposalID := propResp.Proposal.ProposalID

	// Vote to approve.
	for i := 0; i < 2; i++ {
		voteReq := wire.CastGovernanceVoteRequest{
			ProposalID:    proposalID,
			Voter:         addresses[i],
			Approve:       true,
			CreatedAtUnix: time.Now().Unix(),
		}
		if err := wire.SignGovernanceVote(&voteReq, privKeys[i]); err != nil {
			t.Fatal(err)
		}
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	updatedParams := store.GetMiningParams()
	if updatedParams.ProofScoreWeightBPS != 4000 {
		t.Fatalf("expected proof score weight 4000, got %d", updatedParams.ProofScoreWeightBPS)
	}
	// All other fields unchanged.
	if updatedParams.StorageReleaseRateBPS != origParams.StorageReleaseRateBPS {
		t.Fatalf("storage release rate should be unchanged")
	}
	if updatedParams.StoredBytesWeightBPS != origParams.StoredBytesWeightBPS {
		t.Fatalf("stored bytes weight should be unchanged")
	}
	if updatedParams.StorageProofSamples != origParams.StorageProofSamples {
		t.Fatalf("storage proof samples should be unchanged")
	}
}

func TestUpdateMiningParamsRequiresNonZeroField(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// All target fields zero — should be rejected.
	req := wire.CreateGovernanceProposalRequest{
		Proposer:      addresses[0],
		Action:        "update_mining_params",
		ReasonHash:    "empty_change",
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGovernanceProposal(req); err == nil {
		t.Fatal("expected error when all target mining params are zero")
	}
}

func TestUpdateMiningParamsUsesOperatorChangeThreshold(t *testing.T) {
	store, _, _ := testGovernanceSetup(t)

	// Verify that update_mining_params uses the operator change threshold (2/3).
	threshold := store.governanceThresholdLocked("update_mining_params")
	// 3 operators, 2/3 threshold = ceil(3*2/3) = 2.
	if threshold != 2 {
		t.Fatalf("expected threshold 2 for update_mining_params with 3 operators, got %d", threshold)
	}
}
