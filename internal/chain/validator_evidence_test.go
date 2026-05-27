package chain

import (
	"testing"

	"chain/internal/reward"
	"chain/internal/wire"
)

func TestSubmitValidatorEvidenceSlashesLockedStake(t *testing.T) {
	stake := MinValidatorStake
	expectedSlash := stake / 2
	store, identity := registeredTestValidator(t, stake)
	req := testDoubleVoteEvidenceRequest(t, identity, 1, stake)

	resp, err := store.SubmitValidatorEvidence(req)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted {
		t.Fatal("expected evidence to be accepted")
	}
	if resp.Evidence.Slashed != expectedSlash {
		t.Fatalf("expected slash %d, got %d", expectedSlash, resp.Evidence.Slashed)
	}
	account := store.accountLocked(identity.OwnerAddress)
	if account.LockedStake != stake-expectedSlash {
		t.Fatalf("expected locked stake %d, got %d", stake-expectedSlash, account.LockedStake)
	}
	validator := store.data.Validators[identity.OwnerAddress]
	if validator.Stake != stake-expectedSlash || validator.Slashed != expectedSlash || validator.EvidenceCount != 1 {
		t.Fatalf("unexpected validator after evidence: %+v", validator)
	}
	if store.data.RewardPools == nil || store.data.RewardPools.RepairRemaining != reward.RepairPoolInitial+expectedSlash {
		t.Fatalf("expected slashed funds in repair pool, pools=%+v", store.data.RewardPools)
	}
	if _, ok := store.data.ValidatorEvidence[resp.Evidence.EvidenceID]; !ok {
		t.Fatal("expected evidence to be stored")
	}
}

func TestSubmitValidatorEvidenceDuplicateDoesNotSlashTwice(t *testing.T) {
	stake := MinValidatorStake
	expectedSlash := stake / 2
	store, identity := registeredTestValidator(t, stake)
	req := testDoubleVoteEvidenceRequest(t, identity, 1, stake)

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
	account := store.accountLocked(identity.OwnerAddress)
	if account.LockedStake != stake-expectedSlash {
		t.Fatalf("expected duplicate to leave locked stake %d, got %d", stake-expectedSlash, account.LockedStake)
	}
}

func TestSubmitValidatorEvidenceRejectsNonConflictingVotes(t *testing.T) {
	store, identity := registeredTestValidator(t, MinValidatorStake)
	vote := signTestVoteHash(t, identity, 1, "block-a", MinValidatorStake)

	_, err := store.SubmitValidatorEvidence(wire.SubmitValidatorEvidenceRequest{VoteA: vote, VoteB: vote})
	if err == nil {
		t.Fatal("expected non-conflicting votes to be rejected")
	}
}

func TestValidatorEvidenceTransactionReplaySlashesPeer(t *testing.T) {
	stake := MinValidatorStake
	expectedSlash := stake / 2
	producer, identity := registeredTestValidator(t, stake)
	req := testDoubleVoteEvidenceRequest(t, identity, 1, stake)
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

	peer, _ := registeredTestValidatorWithIdentity(t, identity, stake)
	peer.data.PendingTxs = nil
	peer.data.AppliedTxs = map[string]bool{}
	if err := peer.applyTransactionLocked(evidenceTx); err != nil {
		t.Fatal(err)
	}
	account := peer.accountLocked(identity.OwnerAddress)
	if account.LockedStake != stake-expectedSlash {
		t.Fatalf("expected replay locked stake %d, got %d", stake-expectedSlash, account.LockedStake)
	}
	if stored := peer.data.ValidatorEvidence[resp.Evidence.EvidenceID]; stored.EvidenceID == "" {
		t.Fatal("expected replay to store evidence")
	}
}

func registeredTestValidator(t *testing.T, stake uint64) (*Store, *OperatorIdentity) {
	t.Helper()
	identity := testOperatorIdentity(t)
	return registeredTestValidatorWithIdentity(t, identity, stake)
}

func registeredTestValidatorWithIdentity(t *testing.T, identity *OperatorIdentity, stake uint64) (*Store, *OperatorIdentity) {
	t.Helper()
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreditBalance(identity.OwnerAddress, stake); err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest(store.ChainID(), store.AccountNonce(identity.OwnerAddress), "http://localhost:8080", stake, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	return store, identity
}

func testDoubleVoteEvidenceRequest(t *testing.T, identity *OperatorIdentity, height uint64, power uint64) wire.SubmitValidatorEvidenceRequest {
	t.Helper()
	return wire.SubmitValidatorEvidenceRequest{
		VoteA: signTestVoteHash(t, identity, height, "block-a", power),
		VoteB: signTestVoteHash(t, identity, height, "block-b", power),
	}
}

func signTestVoteHash(t *testing.T, identity *OperatorIdentity, height uint64, blockHash string, power uint64) wire.BlockVote {
	t.Helper()
	vote := wire.BlockVote{
		Height:             height,
		BlockHash:          blockHash,
		ValidatorAddress:   identity.OperatorAddress,
		ValidatorPublicKey: identity.OperatorPublicKeyHex(),
		Power:              power,
	}
	if err := wire.SignBlockVote(&vote, identity.OperatorPrivateKey); err != nil {
		t.Fatal(err)
	}
	return vote
}
