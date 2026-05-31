package chain

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestDefaultFeeMultipliers(t *testing.T) {
	m := defaultFeeMultipliers()
	if m.BridgeOut != 20000 {
		t.Fatalf("expected bridge_out 20000, got %d", m.BridgeOut)
	}
	if m.CreateIntent != 15000 {
		t.Fatalf("expected create_intent 15000, got %d", m.CreateIntent)
	}
	if m.UploadNFTTemplate != 15000 {
		t.Fatalf("expected upload_nft_template 15000, got %d", m.UploadNFTTemplate)
	}
	if m.RegisterValidator != 15000 {
		t.Fatalf("expected register_validator 15000, got %d", m.RegisterValidator)
	}
	if m.BatchCommit != 15000 {
		t.Fatalf("expected batch_commit 15000, got %d", m.BatchCommit)
	}
}

func TestFeeMultiplierValidation(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	baseFee := store.data.FeeMarket.BaseFee // default 100_000_000

	// transfer has 1.0x multiplier — fee == baseFee should pass.
	txTransfer := wire.Transaction{Type: "transfer", Fee: baseFee}
	store.mu.Lock()
	err = store.validateTransactionFeeLocked(txTransfer)
	store.mu.Unlock()
	if err != nil {
		t.Fatalf("transfer with baseFee should pass: %v", err)
	}

	// bridge_out has 2.0x multiplier — fee == baseFee should fail.
	txBridge := wire.Transaction{Type: "bridge_out", Fee: baseFee}
	store.mu.Lock()
	err = store.validateTransactionFeeLocked(txBridge)
	store.mu.Unlock()
	if err == nil {
		t.Fatal("bridge_out with baseFee should fail (needs 2x)")
	}

	// bridge_out with 2x fee should pass.
	txBridge2x := wire.Transaction{Type: "bridge_out", Fee: baseFee * 2}
	store.mu.Lock()
	err = store.validateTransactionFeeLocked(txBridge2x)
	store.mu.Unlock()
	if err != nil {
		t.Fatalf("bridge_out with 2x baseFee should pass: %v", err)
	}

	// create_intent has 1.5x multiplier — fee == baseFee should fail.
	txIntent := wire.Transaction{Type: "create_intent", Fee: baseFee}
	store.mu.Lock()
	err = store.validateTransactionFeeLocked(txIntent)
	store.mu.Unlock()
	if err == nil {
		t.Fatal("create_intent with baseFee should fail (needs 1.5x)")
	}

	// create_intent with 1.5x fee should pass.
	txIntent15x := wire.Transaction{Type: "create_intent", Fee: baseFee * 3 / 2}
	store.mu.Lock()
	err = store.validateTransactionFeeLocked(txIntent15x)
	store.mu.Unlock()
	if err != nil {
		t.Fatalf("create_intent with 1.5x baseFee should pass: %v", err)
	}
}

func TestSetFeeMarketViaStore(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	newBaseFee := uint64(200_000_000)
	newTargetTxs := 20
	req := wire.SetFeeMarketRequest{
		BaseFee:        &newBaseFee,
		TargetBlockTxs: &newTargetTxs,
	}
	result, err := store.SetFeeMarket(req)
	if err != nil {
		t.Fatalf("SetFeeMarket failed: %v", err)
	}
	if result.BaseFee != newBaseFee {
		t.Fatalf("expected base_fee %d, got %d", newBaseFee, result.BaseFee)
	}
	if result.TargetBlockTxs != newTargetTxs {
		t.Fatalf("expected target_block_txs %d, got %d", newTargetTxs, result.TargetBlockTxs)
	}

	// Verify via GetFeeMarket.
	got := store.GetFeeMarket()
	if got.BaseFee != newBaseFee {
		t.Fatalf("GetFeeMarket: expected base_fee %d, got %d", newBaseFee, got.BaseFee)
	}
}

func TestSetFeeMarketValidation(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	// Zero base fee should fail.
	zeroBaseFee := uint64(0)
	_, err = store.SetFeeMarket(wire.SetFeeMarketRequest{BaseFee: &zeroBaseFee})
	if err == nil {
		t.Fatal("expected error for zero base_fee")
	}

	// Out-of-range multiplier should fail.
	badMult := uint64(500) // below 1000 minimum
	_, err = store.SetFeeMarket(wire.SetFeeMarketRequest{
		Multipliers: &wire.FeeMultipliers{BridgeOut: badMult},
	})
	if err == nil {
		t.Fatal("expected error for out-of-range multiplier")
	}

	// Valid multiplier should succeed.
	goodMult := uint64(30000) // 3.0x
	_, err = store.SetFeeMarket(wire.SetFeeMarketRequest{
		Multipliers: &wire.FeeMultipliers{BridgeOut: goodMult},
	})
	if err != nil {
		t.Fatalf("valid multiplier should succeed: %v", err)
	}

	// Verify the multiplier was applied.
	got := store.GetFeeMarket()
	if got.Multipliers.BridgeOut != goodMult {
		t.Fatalf("expected bridge_out multiplier %d, got %d", goodMult, got.Multipliers.BridgeOut)
	}
}

func TestUpdateFeeMarketViaProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	origFM := store.GetFeeMarket()

	req := wire.CreateGovernanceProposalRequest{
		Proposer:                addresses[0],
		ChainID:                 store.data.ChainID,
		Action:                  "update_fee_market",
		ReasonHash:              "adjust_fee_market",
		TargetFeeMarketBaseFee:  200_000_000,
		TargetFeeMultiplierBridgeOut: 30000,
		Nonce:                   store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
		CreatedAtUnix:           time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	propResp, err := store.CreateGovernanceProposal(req)
	if err != nil {
		t.Fatalf("update_fee_market proposal failed: %v", err)
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

	updatedFM := store.GetFeeMarket()
	if updatedFM.BaseFee != 200_000_000 {
		t.Fatalf("expected base_fee 200000000, got %d", updatedFM.BaseFee)
	}
	if updatedFM.Multipliers.BridgeOut != 30000 {
		t.Fatalf("expected bridge_out multiplier 30000, got %d", updatedFM.Multipliers.BridgeOut)
	}
	// Unrelated multipliers should remain unchanged.
	if updatedFM.Multipliers.CreateIntent != origFM.Multipliers.CreateIntent {
		t.Fatalf("create_intent multiplier should be unchanged: expected %d, got %d",
			origFM.Multipliers.CreateIntent, updatedFM.Multipliers.CreateIntent)
	}
}

func TestUpdateFeeMarketRequiresNonZeroField(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	req := wire.CreateGovernanceProposalRequest{
		Proposer:      addresses[0],
		ChainID:       store.data.ChainID,
		Action:        "update_fee_market",
		ReasonHash:    "empty_update",
		Nonce:         store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreateGovernanceProposal(req)
	if err == nil {
		t.Fatal("expected error for update_fee_market with no target fields")
	}
}
