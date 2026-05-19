package chain

import (
	"testing"

	"chain/internal/reward"
	"chain/internal/wire"
)

func TestSubmitValidatorEvidenceSlashesLockedStake(t *testing.T) {
	store, identity := registeredTestValidator(t, 10)
	req := testDoubleVoteEvidenceRequest(t, identity, 1, 10)

	resp, err := store.SubmitValidatorEvidence(req)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted {
		t.Fatal("expected evidence to be accepted")
	}
	if resp.Evidence.Slashed != 5 {
		t.Fatalf("expected slash 5, got %d", resp.Evidence.Slashed)
	}
	account := store.accountLocked(identity.Address)
	if account.LockedStake != 5 {
		t.Fatalf("expected locked stake 5, got %d", account.LockedStake)
	}
	validator := store.data.Validators[identity.Address]
	if validator.Stake != 5 || validator.Slashed != 5 || validator.EvidenceCount != 1 {
		t.Fatalf("unexpected validator after evidence: %+v", validator)
	}
	if store.data.RewardPools == nil || store.data.RewardPools.RepairRemaining != reward.RepairPoolInitial+5 {
		t.Fatalf("expected slashed funds in repair pool, pools=%+v", store.data.RewardPools)
	}
	if _, ok := store.data.ValidatorEvidence[resp.Evidence.EvidenceID]; !ok {
		t.Fatal("expected evidence to be stored")
	}
}

func TestSubmitValidatorEvidenceDuplicateDoesNotSlashTwice(t *testing.T) {
	store, identity := registeredTestValidator(t, 10)
	req := testDoubleVoteEvidenceRequest(t, identity, 1, 10)

	first, err := store.SubmitValidatorEvidence(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SubmitValidatorEvidence(req)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Accepted || second.Accepted {
		t.Fatalf("expected first accepted and second ignored, first=%v second=%v", first.Accepted, second.Accepted)
	}
	if second.Evidence.Slashed != first.Evidence.Slashed {
		t.Fatalf("duplicate should return stored evidence, got %+v want %+v", second.Evidence, first.Evidence)
	}
	account := store.accountLocked(identity.Address)
	if account.LockedStake != 5 {
		t.Fatalf("expected duplicate to leave locked stake 5, got %d", account.LockedStake)
	}
}

func TestSubmitValidatorEvidenceRejectsNonConflictingVotes(t *testing.T) {
	store, identity := registeredTestValidator(t, 10)
	vote := signTestVoteHash(t, identity, 1, "block-a", 10)

	_, err := store.SubmitValidatorEvidence(wire.SubmitValidatorEvidenceRequest{VoteA: vote, VoteB: vote})
	if err == nil {
		t.Fatal("expected non-conflicting votes to be rejected")
	}
}

func TestValidatorEvidenceTransactionReplaySlashesPeer(t *testing.T) {
	producer, identity := registeredTestValidator(t, 10)
	req := testDoubleVoteEvidenceRequest(t, identity, 1, 10)
	resp, err := producer.SubmitValidatorEvidence(req)
	if err != nil {
		t.Fatal(err)
	}
	var evidenceTx wire.Transaction
	for _, tx := range producer.data.PendingTxs {
		if tx.Type == "validator_evidence" {
			evidenceTx = tx
			break
		}
	}
	if evidenceTx.TxID == "" {
		t.Fatal("expected validator evidence transaction in mempool")
	}

	peer, _ := registeredTestValidatorWithIdentity(t, identity, 10)
	peer.data.PendingTxs = nil
	peer.data.AppliedTxs = map[string]bool{}
	if err := peer.applyTransactionLocked(evidenceTx); err != nil {
		t.Fatal(err)
	}
	account := peer.accountLocked(identity.Address)
	if account.LockedStake != 5 {
		t.Fatalf("expected replay locked stake 5, got %d", account.LockedStake)
	}
	if stored := peer.data.ValidatorEvidence[resp.Evidence.EvidenceID]; stored.EvidenceID == "" {
		t.Fatal("expected replay to store evidence")
	}
}

func registeredTestValidator(t *testing.T, stake uint64) (*Store, *ValidatorIdentity) {
	t.Helper()
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	return registeredTestValidatorWithIdentity(t, identity, stake)
}

func registeredTestValidatorWithIdentity(t *testing.T, identity *ValidatorIdentity, stake uint64) (*Store, *ValidatorIdentity) {
	t.Helper()
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Faucet(wire.FaucetRequest{Address: identity.Address, Amount: stake}); err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://localhost:8080", stake)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	return store, identity
}

func testDoubleVoteEvidenceRequest(t *testing.T, identity *ValidatorIdentity, height uint64, power uint64) wire.SubmitValidatorEvidenceRequest {
	t.Helper()
	return wire.SubmitValidatorEvidenceRequest{
		VoteA: signTestVoteHash(t, identity, height, "block-a", power),
		VoteB: signTestVoteHash(t, identity, height, "block-b", power),
	}
}

func signTestVoteHash(t *testing.T, identity *ValidatorIdentity, height uint64, blockHash string, power uint64) wire.BlockVote {
	t.Helper()
	vote := wire.BlockVote{
		Height:             height,
		BlockHash:          blockHash,
		ValidatorAddress:   identity.Address,
		ValidatorPublicKey: identity.PublicKeyBase64(),
		Power:              power,
	}
	if err := wire.SignBlockVote(&vote, identity.PrivateKey); err != nil {
		t.Fatal(err)
	}
	return vote
}
