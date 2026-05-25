package wire

import (
	"testing"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestSignAndVerifyGovernanceProposal(t *testing.T) {
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := AccountAddress(&priv.PublicKey)

	req := CreateGovernanceProposalRequest{
		Proposer:      addr,
		IntentID:      "intent_test123",
		Action:        "freeze",
		ReasonHash:    "reason_hash_abc",
		ExpiresAtUnix: time.Now().Add(24 * time.Hour).Unix(),
		CreatedAtUnix: time.Now().Unix(),
	}

	if err := SignGovernanceProposal(&req, priv); err != nil {
		t.Fatalf("SignGovernanceProposal failed: %v", err)
	}
	if req.Signature == "" {
		t.Fatal("signature is empty after signing")
	}

	if err := VerifyGovernanceProposal(req, addr); err != nil {
		t.Fatalf("VerifyGovernanceProposal failed: %v", err)
	}
}

func TestVerifyGovernanceProposalTampered(t *testing.T) {
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := AccountAddress(&priv.PublicKey)

	req := CreateGovernanceProposalRequest{
		Proposer:      addr,
		IntentID:      "intent_original",
		Action:        "block",
		ReasonHash:    "reason_hash",
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := SignGovernanceProposal(&req, priv); err != nil {
		t.Fatal(err)
	}

	// Tamper with intent ID.
	req.IntentID = "intent_tampered"

	if err := VerifyGovernanceProposal(req, addr); err == nil {
		t.Fatal("expected verification to fail on tampered proposal")
	}
}

func TestVerifyGovernanceProposalWrongKey(t *testing.T) {
	priv1, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	priv2, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr1 := AccountAddress(&priv1.PublicKey)
	addr2 := AccountAddress(&priv2.PublicKey)

	req := CreateGovernanceProposalRequest{
		Proposer:      addr1,
		IntentID:      "intent_wrongkey",
		Action:        "freeze",
		ReasonHash:    "reason_hash",
		ExpiresAtUnix: time.Now().Add(24 * time.Hour).Unix(),
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := SignGovernanceProposal(&req, priv1); err != nil {
		t.Fatal(err)
	}

	// Verify with a different address (from a different key).
	if err := VerifyGovernanceProposal(req, addr2); err == nil {
		t.Fatal("expected verification to fail with wrong key")
	}
}

func TestSignAndVerifyGovernanceVote(t *testing.T) {
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := AccountAddress(&priv.PublicKey)

	req := CastGovernanceVoteRequest{
		ProposalID:    "gov_proposal_test123",
		Voter:         addr,
		Approve:       true,
		CreatedAtUnix: time.Now().Unix(),
	}

	if err := SignGovernanceVote(&req, priv); err != nil {
		t.Fatalf("SignGovernanceVote failed: %v", err)
	}
	if req.Signature == "" {
		t.Fatal("signature is empty after signing")
	}

	if err := VerifyGovernanceVote(req, addr); err != nil {
		t.Fatalf("VerifyGovernanceVote failed: %v", err)
	}
}

func TestVerifyGovernanceVoteTampered(t *testing.T) {
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := AccountAddress(&priv.PublicKey)

	req := CastGovernanceVoteRequest{
		ProposalID:    "gov_proposal_test456",
		Voter:         addr,
		Approve:       true,
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := SignGovernanceVote(&req, priv); err != nil {
		t.Fatal(err)
	}

	// Tamper: change approve to false.
	req.Approve = false

	if err := VerifyGovernanceVote(req, addr); err == nil {
		t.Fatal("expected verification to fail on tampered vote")
	}
}
