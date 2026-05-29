package chain

import (
	"crypto/ecdsa"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"chain/internal/reward"
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

func testGovernanceProposalReq(t *testing.T, store *Store, address string, privKey *ecdsa.PrivateKey) wire.CreateGovernanceProposalRequest {
	t.Helper()
	req := wire.CreateGovernanceProposalRequest{
		Proposer:      address,
		ChainID:       store.data.ChainID,
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "reason_hash_test",
		ExpiresAtUnix: time.Now().Add(48 * time.Hour).Unix(),
		Nonce:         store.data.OperatorNonces[normalizeGovernanceOperator(address)],
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKey); err != nil {
		t.Fatalf("failed to sign proposal: %v", err)
	}
	return req
}

func testGovernanceVoteReq(t *testing.T, store *Store, proposalID, address string, approve bool, privKey *ecdsa.PrivateKey) wire.CastGovernanceVoteRequest {
	t.Helper()
	req := wire.CastGovernanceVoteRequest{
		ProposalID:    proposalID,
		Voter:         address,
		Approve:       approve,
		ChainID:       store.data.ChainID,
		Nonce:         store.data.OperatorNonces[normalizeGovernanceOperator(address)],
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceVote(&req, privKey); err != nil {
		t.Fatalf("failed to sign vote: %v", err)
	}
	return req
}

func testGovernanceExecuteReq(t *testing.T, store *Store, proposalID, address string, privKey *ecdsa.PrivateKey) wire.ExecuteGovernanceProposalRequest {
	t.Helper()
	req := wire.ExecuteGovernanceProposalRequest{
		ProposalID:    proposalID,
		Executor:      address,
		ChainID:       store.data.ChainID,
		Nonce:         store.data.OperatorNonces[normalizeGovernanceOperator(address)],
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceExecute(&req, privKey); err != nil {
		t.Fatalf("failed to sign execute: %v", err)
	}
	return req
}

func TestCreateGovernanceProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	req := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
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
		ChainID:       store.data.ChainID,
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

	fakePriv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	fakeAddr := wire.AccountAddress(&fakePriv.PublicKey)
	req := wire.CreateGovernanceProposalRequest{
		Proposer:      fakeAddr,
		ChainID:       store.data.ChainID,
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

	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	voteReq := testGovernanceVoteReq(t, store, proposalResp.Proposal.ProposalID, addresses[1], true, privKeys[1])
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

	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	// First vote.
	voteReq := testGovernanceVoteReq(t, store, proposalResp.Proposal.ProposalID, addresses[0], true, privKeys[0])
	if _, err := store.CastGovernanceVote(voteReq); err != nil {
		t.Fatal(err)
	}

	// Second vote (same voter) should fail — nonce has advanced.
	voteReq2 := testGovernanceVoteReq(t, store, proposalResp.Proposal.ProposalID, addresses[0], false, privKeys[0])
	if _, err := store.CastGovernanceVote(voteReq2); err == nil {
		t.Fatal("expected error for double voting")
	}
}

func TestExecuteGovernanceProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Create proposal.
	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}
	proposalID := proposalResp.Proposal.ProposalID

	// All 3 operators vote approve (threshold for 3 operators = 2).
	for i := 0; i < 3; i++ {
		voteReq := testGovernanceVoteReq(t, store, proposalID, addresses[i], true, privKeys[i])
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	proposal := store.data.GovernanceProposals[proposalID]
	if proposal.Status != wire.GovProposalExecuted {
		t.Fatalf("expected status executed, got %s", proposal.Status)
	}

	intent := store.data.Intents["intent_lifecycle"]
	if intent.ModerationStatus != wire.ModerationStatusFrozen {
		t.Fatalf("expected moderation frozen, got %s", intent.ModerationStatus)
	}
}

func TestExecuteGovernanceProposalInsufficientVotes(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	// One reject vote — keeps proposal pending (remaining votes can still reach threshold).
	voteReq := testGovernanceVoteReq(t, store, proposalResp.Proposal.ProposalID, addresses[0], false, privKeys[0])
	if _, err := store.CastGovernanceVote(voteReq); err != nil {
		t.Fatal(err)
	}

	// Try to execute with proper auth — should fail with insufficient votes.
	execReq := testGovernanceExecuteReq(t, store, proposalResp.Proposal.ProposalID, addresses[1], privKeys[1])
	_, err = store.ExecuteGovernanceProposal(execReq)
	if err == nil {
		t.Fatal("expected error for insufficient votes")
	}
	if !strings.Contains(err.Error(), "insufficient approval votes") {
		t.Fatalf("expected insufficient votes error, got: %v", err)
	}
}

func TestExecuteGovernanceProposalUnauthorizedExecutor(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	// Use a non-operator key to execute — should be rejected.
	fakePriv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	fakeAddr := wire.AccountAddress(&fakePriv.PublicKey)
	execReq := wire.ExecuteGovernanceProposalRequest{
		ProposalID:    proposalResp.Proposal.ProposalID,
		Executor:      fakeAddr,
		ChainID:       store.data.ChainID,
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceExecute(&execReq, fakePriv); err != nil {
		t.Fatal(err)
	}
	_, err = store.ExecuteGovernanceProposal(execReq)
	if err == nil {
		t.Fatal("expected error for unauthorized executor")
	}
	if !strings.Contains(err.Error(), "not an enabled governance operator") {
		t.Fatalf("expected unauthorized executor error, got: %v", err)
	}
}

func TestExecuteGovernanceProposalNoSignature(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	// Execute without signature — should be rejected.
	_, err = store.ExecuteGovernanceProposal(wire.ExecuteGovernanceProposalRequest{
		ProposalID: proposalResp.Proposal.ProposalID,
		Executor:   addresses[0],
		ChainID:    store.data.ChainID,
	})
	if err == nil {
		t.Fatal("expected error for missing signature")
	}
}

func TestGovernanceNonceReplayProtection(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Create a proposal (this increments addresses[0] nonce to 1).
	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	_, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	// Try to replay the same proposal (nonce 0 is now stale).
	replayReq := wire.CreateGovernanceProposalRequest{
		Proposer:      addresses[0],
		ChainID:       store.data.ChainID,
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "reason_hash_test",
		ExpiresAtUnix: time.Now().Add(48 * time.Hour).Unix(),
		Nonce:         0, // stale nonce
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&replayReq, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateGovernanceProposal(replayReq)
	if err == nil {
		t.Fatal("expected error for nonce replay")
	}
	if !strings.Contains(err.Error(), "invalid proposer nonce") {
		t.Fatalf("expected nonce error, got: %v", err)
	}
}

func TestGovernanceVoteNonceReplayProtection(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	// Create proposal (addresses[0] nonce → 1).
	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	// Vote reject (addresses[1] nonce → 1) — keeps proposal pending.
	voteReq := testGovernanceVoteReq(t, store, proposalResp.Proposal.ProposalID, addresses[1], false, privKeys[1])
	if _, err := store.CastGovernanceVote(voteReq); err != nil {
		t.Fatal(err)
	}

	// Try to replay vote with stale nonce 0.
	replayVote := wire.CastGovernanceVoteRequest{
		ProposalID:    proposalResp.Proposal.ProposalID,
		Voter:         addresses[1],
		Approve:       true,
		ChainID:       store.data.ChainID,
		Nonce:         0, // stale
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceVote(&replayVote, privKeys[1]); err != nil {
		t.Fatal(err)
	}
	_, err = store.CastGovernanceVote(replayVote)
	if err == nil {
		t.Fatal("expected error for vote nonce replay")
	}
	if !strings.Contains(err.Error(), "invalid voter nonce") {
		t.Fatalf("expected nonce error, got: %v", err)
	}
}

func TestCancelGovernanceProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	_, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	cancelReq := wire.CreateGovernanceProposalRequest{
		Proposer:      addresses[0],
		ChainID:       store.data.ChainID,
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

	dataModThreshold := store.governanceThresholdLocked("freeze")
	if dataModThreshold != 1 {
		t.Fatalf("expected data moderation threshold 1 for 3 operators, got %d", dataModThreshold)
	}
	opChangeThreshold := store.governanceThresholdLocked("add_operator")
	if opChangeThreshold != 2 {
		t.Fatalf("expected operator change threshold 2 for 3 operators, got %d", opChangeThreshold)
	}
}

func TestAddOperatorViaProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	newPriv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPubHex := testEncodeHex(ethcrypto.FromECDSAPub(&newPriv.PublicKey))
	newAddr := wire.AccountAddress(&newPriv.PublicKey)

	req := wire.CreateGovernanceProposalRequest{
		Proposer:          addresses[0],
		ChainID:           store.data.ChainID,
		Action:            "add_operator",
		ReasonHash:        "add_new_member",
		TargetPublicKey:   newPubHex,
		TargetPermissions: []string{"freeze", "block"},
		Nonce:             store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
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

	for i := 0; i < 2; i++ {
		voteReq := testGovernanceVoteReq(t, store, proposalID, addresses[i], true, privKeys[i])
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

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

	threshold := store.governanceThresholdLocked("add_operator")
	if threshold != 3 {
		t.Fatalf("expected threshold 3 for 4 operators, got %d", threshold)
	}
}

func TestRemoveOperatorViaProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)
	targetAddr := addresses[2]

	req := wire.CreateGovernanceProposalRequest{
		Proposer:       addresses[0],
		ChainID:        store.data.ChainID,
		Action:         "remove_operator",
		ReasonHash:     "remove_member",
		TargetOperator: targetAddr,
		Nonce:          store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
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

	for i := 0; i < 2; i++ {
		voteReq := testGovernanceVoteReq(t, store, proposalID, addresses[i], true, privKeys[i])
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	op := store.data.GovernanceOperators[targetAddr]
	if op.Enabled {
		t.Fatal("removed operator should be disabled")
	}

	threshold := store.governanceThresholdLocked("remove_operator")
	if threshold != 2 {
		t.Fatalf("expected threshold 2 for 2 enabled operators, got %d", threshold)
	}
}

func TestUpdateOperatorViaProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)
	targetAddr := addresses[1]

	req := wire.CreateGovernanceProposalRequest{
		Proposer:          addresses[0],
		ChainID:           store.data.ChainID,
		Action:            "update_operator",
		ReasonHash:        "update_permissions",
		TargetOperator:    targetAddr,
		TargetPermissions: []string{"freeze"},
		Nonce:             store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
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

	for i := 0; i < 2; i++ {
		voteReq := testGovernanceVoteReq(t, store, proposalID, addresses[i], true, privKeys[i])
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	op := store.data.GovernanceOperators[targetAddr]
	if len(op.Permissions) != 1 || op.Permissions[0] != "freeze" {
		t.Fatalf("expected permissions [freeze], got %v", op.Permissions)
	}
}

func TestUpdateOperatorKeyRotationRejected(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)
	targetAddr := addresses[1]

	newPriv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPubHex := testEncodeHex(ethcrypto.FromECDSAPub(&newPriv.PublicKey))

	req := wire.CreateGovernanceProposalRequest{
		Proposer:          addresses[0],
		ChainID:           store.data.ChainID,
		Action:            "update_operator",
		ReasonHash:        "rotate_key",
		TargetOperator:    targetAddr,
		TargetPublicKey:   newPubHex,
		TargetPermissions: []string{"freeze"},
		Nonce:             store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
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

	existingPubHex := testEncodeHex(ethcrypto.FromECDSAPub(&privKeys[1].PublicKey))
	req := wire.CreateGovernanceProposalRequest{
		Proposer:        addresses[0],
		ChainID:         store.data.ChainID,
		Action:          "add_operator",
		ReasonHash:      "duplicate",
		TargetPublicKey: existingPubHex,
		Nonce:           store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
		CreatedAtUnix:   time.Now().Unix(),
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

	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}
	proposalID := proposalResp.Proposal.ProposalID

	voteReq := testGovernanceVoteReq(t, store, proposalID, addresses[0], true, privKeys[0])
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

	req := wire.CreateGovernanceProposalRequest{
		Proposer:                         addresses[0],
		ChainID:                          store.data.ChainID,
		Action:                           "update_config",
		ReasonHash:                       "adjust_thresholds",
		TargetDataModerationThresholdNum: 2,
		TargetDataModerationThresholdDen: 3,
		TargetOperatorChangeThresholdNum: 3,
		TargetOperatorChangeThresholdDen: 4,
		Nonce:                            store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
		CreatedAtUnix:                    time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	propResp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("update_config proposal failed: %v", err)
	}
	proposalID := propResp.Proposal.ProposalID

	for i := 0; i < 2; i++ {
		voteReq := testGovernanceVoteReq(t, store, proposalID, addresses[i], true, privKeys[i])
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	if store.data.DataModerationThresholdNum != 2 || store.data.DataModerationThresholdDen != 3 {
		t.Fatalf("expected data moderation 2/3, got %d/%d",
			store.data.DataModerationThresholdNum, store.data.DataModerationThresholdDen)
	}
	if store.data.OperatorChangeThresholdNum != 3 || store.data.OperatorChangeThresholdDen != 4 {
		t.Fatalf("expected operator change 3/4, got %d/%d",
			store.data.OperatorChangeThresholdNum, store.data.OperatorChangeThresholdDen)
	}

	dataModThreshold := store.governanceThresholdLocked("freeze")
	if dataModThreshold != 2 {
		t.Fatalf("expected data moderation threshold 2, got %d", dataModThreshold)
	}
	opChangeThreshold := store.governanceThresholdLocked("add_operator")
	if opChangeThreshold != 3 {
		t.Fatalf("expected operator change threshold 3, got %d", opChangeThreshold)
	}
}

// ── update_mining_params tests ──

func TestUpdateMiningParamsViaProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	origParams := store.GetMiningParams()

	req := wire.CreateGovernanceProposalRequest{
		Proposer:                     addresses[0],
		ChainID:                      store.data.ChainID,
		Action:                       "update_mining_params",
		ReasonHash:                   "adjust_mining_params",
		TargetStorageReleaseRateBPS:  5,
		TargetValidatorCommissionBPS: 1500,
		Nonce:                        store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
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

	for i := 0; i < 2; i++ {
		voteReq := testGovernanceVoteReq(t, store, proposalID, addresses[i], true, privKeys[i])
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	updatedParams := store.GetMiningParams()
	if updatedParams.StorageReleaseRateBPS != 5 {
		t.Fatalf("expected storage release rate 5, got %d", updatedParams.StorageReleaseRateBPS)
	}
	if updatedParams.ValidatorCommissionBPS != 1500 {
		t.Fatalf("expected validator commission 1500, got %d", updatedParams.ValidatorCommissionBPS)
	}
	if updatedParams.StoredBytesWeightBPS != origParams.StoredBytesWeightBPS {
		t.Fatalf("stored bytes weight should be unchanged: expected %d, got %d",
			origParams.StoredBytesWeightBPS, updatedParams.StoredBytesWeightBPS)
	}
}

func TestUpdateMiningParamsPartialUpdate(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	origParams := store.GetMiningParams()

	req := wire.CreateGovernanceProposalRequest{
		Proposer:                  addresses[0],
		ChainID:                   store.data.ChainID,
		Action:                    "update_mining_params",
		ReasonHash:                "tune_proof_weight",
		TargetProofScoreWeightBPS: 4000,
		TargetStoredBytesWeightBPS: 3500,
		Nonce:                     store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
		CreatedAtUnix:             time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	propResp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("proposal failed: %v", err)
	}
	proposalID := propResp.Proposal.ProposalID

	for i := 0; i < 2; i++ {
		voteReq := testGovernanceVoteReq(t, store, proposalID, addresses[i], true, privKeys[i])
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
	if updatedParams.StorageReleaseRateBPS != origParams.StorageReleaseRateBPS {
		t.Fatalf("storage release rate should be unchanged")
	}
	if updatedParams.StoredBytesWeightBPS != 3500 {
		t.Fatalf("expected stored bytes weight 3500, got %d", updatedParams.StoredBytesWeightBPS)
	}
	if updatedParams.StorageProofSamples != origParams.StorageProofSamples {
		t.Fatalf("storage proof samples should be unchanged")
	}
}

func TestUpdateMiningParamsRequiresNonZeroField(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	req := wire.CreateGovernanceProposalRequest{
		Proposer:      addresses[0],
		ChainID:       store.data.ChainID,
		Action:        "update_mining_params",
		ReasonHash:    "empty_change",
		Nonce:         store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
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

	threshold := store.governanceThresholdLocked("update_mining_params")
	if threshold != 2 {
		t.Fatalf("expected threshold 2 for update_mining_params with 3 operators, got %d", threshold)
	}
}

func TestUpdateMiningParamsValidatorRewardPerBlock(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	origParams := store.GetMiningParams()
	newRewardPerBlock := 32 * reward.TokenUnit

	req := wire.CreateGovernanceProposalRequest{
		Proposer:                      addresses[0],
		ChainID:                       store.data.ChainID,
		Action:                        "update_mining_params",
		ReasonHash:                    "adjust_validator_reward",
		TargetValidatorRewardPerBlock: newRewardPerBlock,
		Nonce:                         store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
		CreatedAtUnix:                 time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	propResp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("update_mining_params proposal failed: %v", err)
	}
	proposalID := propResp.Proposal.ProposalID

	// Vote with enough operators to reach threshold (2 of 3).
	for i := 0; i < 2; i++ {
		voteReq := testGovernanceVoteReq(t, store, proposalID, addresses[i], true, privKeys[i])
		voteResp, err := store.CastGovernanceVote(voteReq)
		if err != nil {
			t.Fatalf("vote %d failed: %v", i, err)
		}
		if voteResp.Executed {
			break
		}
	}

	updatedParams := store.GetMiningParams()
	if updatedParams.ValidatorRewardPerBlock != newRewardPerBlock {
		t.Fatalf("expected validator_reward_per_block %d, got %d",
			newRewardPerBlock, updatedParams.ValidatorRewardPerBlock)
	}
	// Unrelated fields must remain unchanged.
	if updatedParams.StorageReleaseRateBPS != origParams.StorageReleaseRateBPS {
		t.Fatalf("storage release rate should be unchanged: expected %d, got %d",
			origParams.StorageReleaseRateBPS, updatedParams.StorageReleaseRateBPS)
	}
	if updatedParams.ValidatorCommissionBPS != origParams.ValidatorCommissionBPS {
		t.Fatalf("validator commission should be unchanged: expected %d, got %d",
			origParams.ValidatorCommissionBPS, updatedParams.ValidatorCommissionBPS)
	}
}
