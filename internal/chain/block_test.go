package chain

import (
	"crypto/ecdsa"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chain/internal/consensus"
	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestTransactionsArePackedIntoBlocks(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(store.ChainID(), store.AccountNonce(identity.OwnerAddress), "http://localhost:8080", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, store, identity, MinValidatorStake)
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetOperatorIdentity(identity)

	aliceKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	alice := wire.AccountAddress(&aliceKey.PublicKey)
	if err := store.CreditBalance(alice, gfTokens(100)); err != nil {
		t.Fatal(err)
	}
	transferReq := wire.TransferRequest{From: alice, To: "bob", Amount: gfTokens(25), Fee: gfTokens(1), Nonce: 0}
	if err := wire.SignTransfer(&transferReq, aliceKey, store.data.ChainID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transfer(transferReq); err != nil {
		t.Fatal(err)
	}

	mempool := store.Mempool()
	if len(mempool.Pending) != 3 {
		t.Fatalf("expected 3 pending txs, got %d", len(mempool.Pending))
	}

	produced, err := store.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected block to be produced")
	}
	if produced.Block.Height != 1 {
		t.Fatalf("expected height 1, got %d", produced.Block.Height)
	}
	if len(produced.Block.Transactions) != 3 {
		t.Fatalf("expected 3 txs, got %d", len(produced.Block.Transactions))
	}
	if produced.Block.Hash == "" || produced.Block.TxRoot == "" {
		t.Fatal("block hash and tx root must be set")
	}
	if !produced.Block.Finality.Finalized || produced.Block.Finality.VotingPower != produced.Block.Finality.TotalPower {
		t.Fatalf("expected single-validator block finalized, finality=%+v", produced.Block.Finality)
	}
	if produced.Block.ProducerAddress != identity.OwnerAddress {
		t.Fatal("block producer mismatch")
	}
	if err := wire.VerifyBlockSignature(produced.Block); err != nil {
		t.Fatal(err)
	}
	if len(store.Mempool().Pending) != 0 {
		t.Fatal("mempool should be empty after block production")
	}

	latest, err := store.LatestBlock()
	if err != nil {
		t.Fatal(err)
	}
	if latest.Hash != produced.Block.Hash {
		t.Fatal("latest block mismatch")
	}

	empty, err := store.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !empty.Produced {
		t.Fatal("empty mempool should produce an empty block")
	}
	if empty.Block.Height != 2 || len(empty.Block.Transactions) != 0 {
		t.Fatalf("expected empty block at height 2, got height=%d txs=%d", empty.Block.Height, len(empty.Block.Transactions))
	}
}

func TestRegisterValidatorRequiresMinimumStake(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity := testOperatorIdentity(t)
	fundValidatorForTest(t, store, identity, MinValidatorStake)
	registration, err := identity.RegistrationRequest(store.ChainID(), store.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake-1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterValidator(registration); err == nil {
		t.Fatal("expected validator registration below minimum stake to be rejected")
	}
}

func TestBlockProductionCapsTransactionCount(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(store.ChainID(), store.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, store, identity, MinValidatorStake)
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetOperatorIdentity(identity)
	if produced, err := store.ProduceBlock(); err != nil || !produced.Produced {
		t.Fatalf("expected validator registration block, produced=%t err=%v", produced.Produced, err)
	}

	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)
	store.data.Accounts[from] = wire.Account{Address: from, Balance: gfTokens(1000)}

	for i := 0; i < defaultMaxBlockTxs+5; i++ {
		tx := signedTransferTx(t, "tx", wire.TransferRequest{
			From:   from,
			To:     "0x00000000000000000000000000000000000000b0",
			Amount: gfTokens(1),
			Nonce:  uint64(i),
			Fee:    gfTokens(1),
		}, privateKey, store.data.ChainID)
		if accepted, err := store.AcceptTransaction(tx); err != nil || !accepted {
			t.Fatalf("expected tx %d accepted, accepted=%t err=%v", i, accepted, err)
		}
	}

	produced, err := store.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected block to be produced")
	}
	if len(produced.Block.Transactions) != defaultMaxBlockTxs {
		t.Fatalf("expected %d transactions, got %d", defaultMaxBlockTxs, len(produced.Block.Transactions))
	}
	if len(store.Mempool().Pending) != 5 {
		t.Fatalf("expected 5 transactions left in mempool, got %d", len(store.Mempool().Pending))
	}
}

func TestAcceptTransactionRejectsOversizedPayload(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`"` + strings.Repeat("x", defaultMaxTxBytes+1) + `"`)
	payloadHash := chaincrypto.HashBytes(payload)
	tx := wire.Transaction{
		TxID:          chaincrypto.HashBytes([]byte("oversized:" + payloadHash)),
		Type:          "oversized",
		PayloadHash:   payloadHash,
		Payload:       payload,
		CreatedAtUnix: time.Now().Unix(),
	}
	if accepted, err := store.AcceptTransaction(tx); err == nil || accepted {
		t.Fatalf("expected oversized transaction rejected, accepted=%t err=%v", accepted, err)
	}
}

func TestAcceptPeerBlock(t *testing.T) {
	producer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(producer.ChainID(), producer.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, producer, identity, MinValidatorStake)
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetOperatorIdentity(identity)
	if err := producer.CreditBalance("alice", 100); err != nil {
		t.Fatal(err)
	}
	produced, err := producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected producer to create block")
	}

	peer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, peer, identity, MinValidatorStake)
	peer.data.PendingTxs = append(peer.data.PendingTxs, produced.Block.Transactions...)

	accepted, err := peer.AcceptBlock(produced.Block)
	if err != nil {
		t.Fatal(err)
	}
	if !accepted {
		t.Fatal("expected peer block to be accepted")
	}
	if len(peer.data.Blocks) != 1 {
		t.Fatalf("expected peer height 1, got %d", len(peer.data.Blocks))
	}
	if len(peer.data.PendingTxs) != 0 {
		t.Fatal("accepted block should remove matching pending txs")
	}
	if peer.accountLocked("alice").Balance != 100 {
		t.Fatal("accepted block should replay genesis_credit transaction")
	}
	if peer.data.Validators[identity.OwnerAddress].ProducedBlocks != 1 {
		t.Fatal("peer should track producer block count")
	}

	accepted, err = peer.AcceptBlock(produced.Block)
	if err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatal("duplicate peer block should be ignored")
	}
}

func TestMempoolReplacesSameNonceWithHigherFee(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)

	lowFee := signedTransferTx(t, "low-fee", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000b0",
		Amount: gfTokens(10),
		Nonce:  0,
		Fee:    gfTokens(1),
	}, privateKey, store.data.ChainID)
	if accepted, err := store.AcceptTransaction(lowFee); err != nil || !accepted {
		t.Fatalf("expected low fee tx accepted, accepted=%t err=%v", accepted, err)
	}

	replacement := signedTransferTx(t, "replacement", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000b0",
		Amount: gfTokens(10),
		Nonce:  0,
		Fee:    gfTokens(3),
	}, privateKey, store.data.ChainID)
	if accepted, err := store.AcceptTransaction(replacement); err != nil || !accepted {
		t.Fatalf("expected replacement tx accepted, accepted=%t err=%v", accepted, err)
	}
	mempool := store.Mempool()
	if len(mempool.Pending) != 1 || mempool.Pending[0].TxID != replacement.TxID || mempool.Pending[0].Fee != gfTokens(3) {
		t.Fatalf("unexpected replacement mempool: %+v", mempool.Pending)
	}

	tooCheap := signedTransferTx(t, "too-cheap", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000b0",
		Amount: gfTokens(10),
		Nonce:  0,
		Fee:    gfTokens(2),
	}, privateKey, store.data.ChainID)
	if accepted, err := store.AcceptTransaction(tooCheap); err == nil || accepted {
		t.Fatalf("expected lower fee replacement rejected, accepted=%t err=%v", accepted, err)
	}
}

func TestMempoolProducesContiguousNonceOrder(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(store.ChainID(), store.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, store, identity, MinValidatorStake)
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetOperatorIdentity(identity)

	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)
	store.data.Accounts[from] = wire.Account{Address: from, Balance: gfTokens(200)}

	nonceOneHighFee := signedTransferTx(t, "nonce-one", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000b1",
		Amount: gfTokens(10),
		Nonce:  1,
		Fee:    gfTokens(100),
	}, privateKey, store.data.ChainID)
	nonceZeroLowFee := signedTransferTx(t, "nonce-zero", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000b0",
		Amount: gfTokens(10),
		Nonce:  0,
		Fee:    gfTokens(1),
	}, privateKey, store.data.ChainID)
	if accepted, err := store.AcceptTransaction(nonceOneHighFee); err != nil || !accepted {
		t.Fatalf("expected nonce 1 tx accepted, accepted=%t err=%v", accepted, err)
	}
	if accepted, err := store.AcceptTransaction(nonceZeroLowFee); err != nil || !accepted {
		t.Fatalf("expected nonce 0 tx accepted, accepted=%t err=%v", accepted, err)
	}

	produced, err := store.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected block to be produced")
	}
	var transferOrder []uint64
	for _, tx := range produced.Block.Transactions {
		if tx.Type == "transfer" && tx.From == from {
			transferOrder = append(transferOrder, tx.Nonce)
		}
	}
	if len(transferOrder) != 2 || transferOrder[0] != 0 || transferOrder[1] != 1 {
		t.Fatalf("expected contiguous nonce order [0 1], got %v", transferOrder)
	}
	account := store.accountLocked(from)
	if account.Nonce != 2 || account.Balance != gfTokens(79) {
		t.Fatalf("unexpected sender account after block: %+v", account)
	}
}

func TestBlockProductionChargesFeesToProducer(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(store.ChainID(), store.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, store, identity, MinValidatorStake)
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetOperatorIdentity(identity)

	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)
	to := "0x00000000000000000000000000000000000000f1"
	store.data.Accounts[from] = wire.Account{Address: from, Balance: gfTokens(100)}
	tx := signedTransferTx(t, "fee-transfer", wire.TransferRequest{
		From:   from,
		To:     to,
		Amount: gfTokens(25),
		Nonce:  0,
		Fee:    gfTokens(5),
	}, privateKey, store.data.ChainID)
	if accepted, err := store.AcceptTransaction(tx); err != nil || !accepted {
		t.Fatalf("expected tx accepted, accepted=%t err=%v", accepted, err)
	}

	// Producer receives the transaction fees PLUS the block production reward.
	// The producer (validator owner) also retains any remaining balance after
	// staking. Capture balance before block production to measure the delta.
	producerBalBefore := store.accountLocked(identity.OwnerAddress).Balance
	produced, err := store.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected block to be produced")
	}
	fromAccount := store.accountLocked(from)
	toAccount := store.accountLocked(to)
	producerAccount := store.accountLocked(identity.OwnerAddress)
	if fromAccount.Balance != gfTokens(70) {
		t.Fatalf("unexpected sender balance: %d", fromAccount.Balance)
	}
	if toAccount.Balance != gfTokens(25) {
		t.Fatalf("unexpected recipient balance: %d", toAccount.Balance)
	}
	// Producer balance must increase by at least the transfer fee + block reward.
	params := store.miningParamsLocked()
	perBlock := params.ValidatorRewardPerBlock
	productionBPS := params.BlockProductionRewardBPS
	if productionBPS == 0 {
		productionBPS = 3000
	}
	blockReward := perBlock * productionBPS / 10000
	minExpectedGain := gfTokens(5) + blockReward
	actualGain := producerAccount.Balance - producerBalBefore
	if actualGain < minExpectedGain {
		t.Fatalf("producer gain %d less than expected minimum %d (fee %d + block reward %d)",
			actualGain, minExpectedGain, gfTokens(5), blockReward)
	}
}

func TestChargeFeePayerEqualsProducerIsNoOp(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	addr := wire.NormalizeAddress("0x00000000000000000000000000000000000000aa")
	initialBal := uint64(1_000_000_000)
	store.data.Accounts[addr] = wire.Account{Address: addr, Balance: initialBal}

	tx := wire.Transaction{
		TxID: "self-charge-test",
		From: addr,
		Fee:  100_000_000,
	}
	store.mu.Lock()
	err = store.chargeTransactionFeeLocked(tx, addr)
	store.mu.Unlock()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := store.accountLocked(addr).Balance
	if got != initialBal {
		t.Fatalf("self-charge must be no-op: want balance %d, got %d (delta %d)",
			initialBal, got, int64(got)-int64(initialBal))
	}
}

func TestChargeFeePayerDiffersFromProducer(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	payer := wire.NormalizeAddress("0x00000000000000000000000000000000000000aa")
	producer := wire.NormalizeAddress("0x00000000000000000000000000000000000000bb")
	payerBal := uint64(1_000_000_000)
	producerBal := uint64(500_000_000)
	fee := uint64(100_000_000)
	store.data.Accounts[payer] = wire.Account{Address: payer, Balance: payerBal}
	store.data.Accounts[producer] = wire.Account{Address: producer, Balance: producerBal}

	tx := wire.Transaction{
		TxID: "normal-charge-test",
		From: payer,
		Fee:  fee,
	}
	store.mu.Lock()
	err = store.chargeTransactionFeeLocked(tx, producer)
	store.mu.Unlock()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := store.accountLocked(payer).Balance; got != payerBal-fee {
		t.Fatalf("payer balance: want %d, got %d", payerBal-fee, got)
	}
	if got := store.accountLocked(producer).Balance; got != producerBal+fee {
		t.Fatalf("producer balance: want %d, got %d", producerBal+fee, got)
	}
}

func TestMempoolRejectsTransactionsBelowBaseFee(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.FeeMarket.BaseFee = 3
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)
	store.data.Accounts[from] = wire.Account{Address: from, Balance: 100}
	tx := signedTransferTx(t, "cheap-transfer", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000f2",
		Amount: 10,
		Nonce:  0,
		Fee:    1,
	}, privateKey, store.data.ChainID)
	if accepted, err := store.AcceptTransaction(tx); err == nil || accepted {
		t.Fatalf("expected low fee tx rejected, accepted=%t err=%v", accepted, err)
	}
}

func TestFeeMarketAdjustsAfterBlocks(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.FeeMarket.TargetBlockTxs = 1
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(store.ChainID(), store.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, store, identity, MinValidatorStake)
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetOperatorIdentity(identity)

	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)
	store.data.Accounts[from] = wire.Account{Address: from, Balance: gfTokens(100)}
	tx := signedTransferTx(t, "busy-transfer", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000f3",
		Amount: gfTokens(10),
		Nonce:  0,
		Fee:    gfTokens(2),
	}, privateKey, store.data.ChainID)
	if accepted, err := store.AcceptTransaction(tx); err != nil || !accepted {
		t.Fatalf("expected tx accepted, accepted=%t err=%v", accepted, err)
	}
	if _, err := store.ProduceBlock(); err != nil {
		t.Fatal(err)
	}
	if store.data.FeeMarket.BaseFee != 112_500_000 || store.data.FeeMarket.LastBlockTxs != 2 {
		t.Fatalf("expected base fee to increase after busy block, got %+v", store.data.FeeMarket)
	}
}

func TestSimplifiedBFTFinalizesAfterTwoThirdsVotes(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identities := make([]*OperatorIdentity, 0, 3)
	for i := 0; i < 3; i++ {
		identity := testOperatorIdentity(t)
		identities = append(identities, identity)
		store.data.Validators[identity.OwnerAddress] = wire.ValidatorInfo{
			OwnerAddress:      identity.OwnerAddress,
			OperatorPublicKey: identity.OperatorPublicKeyHex(),
			Stake:             1,
			Status:            wire.ValidatorStatusActive,
		}
		store.data.ConsensusValidators[identity.OwnerAddress] = true
	}
	proposerAddr, err := store.selectProposerLocked(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	var producer *OperatorIdentity
	for _, identity := range identities {
		if identity.OwnerAddress == proposerAddr {
			producer = identity
			break
		}
	}
	if producer == nil {
		t.Fatal("proposer identity not found")
	}
	store.SetOperatorIdentity(producer)
	if err := store.CreditBalance("alice", 1); err != nil {
		t.Fatal(err)
	}
	produced, err := store.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected block")
	}
	if produced.Block.Finality.TotalPower != 3 || produced.Block.Finality.ThresholdPower != 3 {
		t.Fatalf("unexpected BFT threshold: %+v", produced.Block.Finality)
	}
	if produced.Block.Finality.Finalized {
		t.Fatalf("block should not be finalized with producer vote only: %+v", produced.Block.Finality)
	}

	for _, identity := range identities {
		if identity.OwnerAddress == producer.OwnerAddress {
			continue
		}
		resp, err := store.AcceptBlockVote(signTestBlockVote(t, produced.Block, identity, 1))
		if err != nil {
			t.Fatal(err)
		}
		if resp.Block.Finality.VotingPower == 2 && resp.Block.Finality.Finalized {
			t.Fatalf("2 of 3 voting power should not finalize with threshold 3: %+v", resp.Block.Finality)
		}
	}
	latest, err := store.LatestBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !latest.Finality.Finalized || latest.Finality.VotingPower != 3 {
		t.Fatalf("expected block finalized after 3 votes, finality=%+v", latest.Finality)
	}
}

func TestConsensusPrevotePrecommitFinalizesBlock(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identities := make([]*OperatorIdentity, 0, 3)
	for i := 0; i < 3; i++ {
		identity := testOperatorIdentity(t)
		identities = append(identities, identity)
		store.data.Validators[identity.OwnerAddress] = wire.ValidatorInfo{
			OwnerAddress:      identity.OwnerAddress,
			OperatorPublicKey: identity.OperatorPublicKeyHex(),
			Stake:             1,
			Status:            wire.ValidatorStatusActive,
		}
		store.data.ConsensusValidators[identity.OwnerAddress] = true
	}
	proposerAddr, err := store.selectProposerLocked(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	var producer *OperatorIdentity
	for _, identity := range identities {
		if identity.OwnerAddress == proposerAddr {
			producer = identity
			break
		}
	}
	if producer == nil {
		t.Fatal("proposer identity not found")
	}
	store.SetOperatorIdentity(producer)
	if err := store.CreditBalance("alice", 1); err != nil {
		t.Fatal(err)
	}
	produced, err := store.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if produced.Block.Finality.Finalized {
		t.Fatal("expected three-validator block to wait for consensus precommits")
	}

	for _, identity := range identities {
		resp, err := store.SubmitConsensusVote(wire.SubmitConsensusVoteRequest{
			Vote: signTestConsensusVote(t, produced.Block, identity, wire.ConsensusVotePrevote, 1),
		})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Prevotes.VotingPower > resp.Prevotes.TotalPower {
			t.Fatalf("invalid prevote finality response: %+v", resp.Prevotes)
		}
	}
	if store.data.ConsensusPhase != consensus.PhasePrecommit {
		t.Fatalf("expected precommit phase after prevote quorum, got %s", store.data.ConsensusPhase)
	}
	for i, identity := range identities {
		resp, err := store.SubmitConsensusVote(wire.SubmitConsensusVoteRequest{
			Vote: signTestConsensusVote(t, produced.Block, identity, wire.ConsensusVotePrecommit, 1),
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == len(identities)-1 && !resp.Finalized {
			t.Fatal("expected finality after quorum precommits")
		}
	}
	latest, err := store.LatestBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !latest.Finality.Finalized || latest.Finality.VotingPower != 3 || latest.Finality.Round != produced.Block.Round {
		t.Fatalf("expected block finalized by precommit quorum, finality=%+v", latest.Finality)
	}
	if store.data.ConsensusHeight != latest.Height+1 || store.data.ConsensusPhase != consensus.PhasePropose {
		t.Fatalf("expected consensus to advance, height=%d phase=%s", store.data.ConsensusHeight, store.data.ConsensusPhase)
	}
}

func TestSubmitConsensusVoteBroadcastsAcceptedVoteOnce(t *testing.T) {
	store, identity := registeredTestValidator(t, MinValidatorStake)
	store.SetOperatorIdentity(identity)
	if err := store.CreditBalance("alice", 1); err != nil {
		t.Fatal(err)
	}
	produced, err := store.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	store.data.ConsensusVotes = map[string]wire.ConsensusVote{}
	peerIdentity := testOperatorIdentity(t)
	store.data.Validators[peerIdentity.OwnerAddress] = wire.ValidatorInfo{
		OwnerAddress:      peerIdentity.OwnerAddress,
		OperatorPublicKey: peerIdentity.OperatorPublicKeyHex(),
		Stake:             10,
		Status:            wire.ValidatorStatusActive,
	}
	store.data.ConsensusValidators[peerIdentity.OwnerAddress] = true
	broadcaster := &captureConsensusVoteBroadcaster{votes: make(chan wire.ConsensusVote, 2)}
	store.SetConsensusVoteBroadcaster(broadcaster)
	vote := signTestConsensusVote(t, produced.Block, identity, wire.ConsensusVotePrevote, validatorPower(store.data.Validators[identity.OwnerAddress]))
	resp, err := store.SubmitConsensusVote(wire.SubmitConsensusVoteRequest{Vote: vote})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted {
		t.Fatal("expected vote accepted")
	}
	select {
	case got := <-broadcaster.votes:
		if got.Signature != vote.Signature {
			t.Fatalf("broadcast vote mismatch: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected accepted vote to be broadcast")
	}
	duplicate, err := store.SubmitConsensusVote(wire.SubmitConsensusVoteRequest{Vote: vote})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Accepted {
		t.Fatal("expected duplicate vote ignored")
	}
	select {
	case got := <-broadcaster.votes:
		t.Fatalf("duplicate vote should not broadcast again: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAcceptBlockRejectsInvalidFinalityCertificate(t *testing.T) {
	producer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(producer.ChainID(), producer.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, producer, identity, MinValidatorStake)
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetOperatorIdentity(identity)
	if err := producer.CreditBalance("alice", 1); err != nil {
		t.Fatal(err)
	}
	produced, err := producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	badBlock := produced.Block
	badBlock.Finality.VotingPower = 999

	peer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.AcceptBlock(badBlock); err == nil {
		t.Fatal("expected invalid finality certificate to be rejected")
	}
}

func TestAcceptBlockRejectsNonCanonicalFinalityVotes(t *testing.T) {
	producer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(producer.ChainID(), producer.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, producer, identity, MinValidatorStake)
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetOperatorIdentity(identity)
	if err := producer.CreditBalance("alice", 1); err != nil {
		t.Fatal(err)
	}
	produced, err := producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	badBlock := produced.Block
	badBlock.Finality.Votes = append(badBlock.Finality.Votes, badBlock.Finality.Votes[0])

	peer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.AcceptBlock(badBlock); err == nil {
		t.Fatal("expected duplicate finality votes to be rejected")
	}
}

func TestLevelDBStorePersistsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain.ldb")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(store.ChainID(), store.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, store, identity, MinValidatorStake)
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetOperatorIdentity(identity)
	if err := store.CreditBalance("alice", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProduceBlock(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	if reopened.Height() != 1 {
		t.Fatalf("expected reopened height 1, got %d", reopened.Height())
	}
	account, err := reopened.Account("alice")
	if err != nil {
		t.Fatal(err)
	}
	if account.Balance != 100 {
		t.Fatalf("expected persisted alice balance 100, got %d", account.Balance)
	}
	if len(reopened.Mempool().Pending) != 0 {
		t.Fatal("expected empty persisted mempool after produced block")
	}
}

func TestJSONStoreStillPersistsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain.json")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreditBalance("alice", 50); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	account, err := reopened.Account("alice")
	if err != nil {
		t.Fatal(err)
	}
	if account.Balance != 50 {
		t.Fatalf("expected persisted json balance 50, got %d", account.Balance)
	}
}

func TestAcceptPeerProofEpochBlocks(t *testing.T) {
	producer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(producer.ChainID(), producer.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, producer, identity, MinValidatorStake)
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetOperatorIdentity(identity)
	seedFinalizedDealForEpochTest(producer)

	peer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	seedFinalizedDealForEpochTest(peer)
	peerRegistration, err := identity.RegistrationRequest(peer.ChainID(), peer.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, peer, identity, MinValidatorStake)
	if _, err := peer.RegisterValidator(peerRegistration); err != nil {
		t.Fatal(err)
	}

	epochResp, err := producer.StartEpoch(wire.StartEpochRequest{
		IntentID:            "intent_epoch",
		ChallengesPerDeal:   1,
		DurationSeconds:     60,
		RewardPerProof:      5,
		SlashPerMissedProof: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	startBlock, err := producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !startBlock.Produced {
		t.Fatal("expected start epoch block")
	}
	if accepted, err := peer.AcceptBlock(startBlock.Block); err != nil {
		t.Fatal(err)
	} else if !accepted {
		t.Fatal("expected peer to accept start epoch block")
	}
	if _, ok := peer.data.Epochs[epochResp.Epoch.EpochID]; !ok {
		t.Fatal("peer should replay epoch")
	}
	for _, challenge := range epochResp.Challenges {
		if _, ok := peer.data.Challenges[challenge.ChallengeID]; !ok {
			t.Fatal("peer should replay epoch challenge")
		}
	}

	if _, err := producer.FinalizeEpoch(wire.FinalizeEpochRequest{EpochID: epochResp.Epoch.EpochID}); err != nil {
		t.Fatal(err)
	}
	finalizeBlock, err := producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !finalizeBlock.Produced {
		t.Fatal("expected finalize epoch block")
	}
	if accepted, err := peer.AcceptBlock(finalizeBlock.Block); err != nil {
		t.Fatal(err)
	} else if !accepted {
		t.Fatal("expected peer to accept finalize epoch block")
	}
	miner := peer.data.Miners["miner_epoch"]
	if miner.ProofFailure != 1 || miner.Slashed != 3 || miner.Stake != 7 {
		t.Fatalf("unexpected replayed miner slash state: failure=%d slashed=%d stake=%d", miner.ProofFailure, miner.Slashed, miner.Stake)
	}
	account := peer.accountLocked("miner_epoch")
	if account.LockedStake != 7 {
		t.Fatalf("expected locked stake 7, got %d", account.LockedStake)
	}
}

func TestRoundRobinRejectsOutOfTurnProducer(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	first := testOperatorIdentity(t)
	second := testOperatorIdentity(t)
	store.data.Validators[first.OwnerAddress] = wire.ValidatorInfo{
		OwnerAddress:      first.OwnerAddress,
		OperatorPublicKey: first.OperatorPublicKeyHex(),
		Status:            "active",
	}
	store.data.Validators[second.OwnerAddress] = wire.ValidatorInfo{
		OwnerAddress:      second.OwnerAddress,
		OperatorPublicKey: second.OperatorPublicKeyHex(),
		Status:            "active",
	}
	store.data.ConsensusValidators[first.OwnerAddress] = true
	store.data.ConsensusValidators[second.OwnerAddress] = true

	expected := store.consensusValidatorAddressesLocked()[0]
	outOfTurn := first
	if expected == first.OwnerAddress {
		outOfTurn = second
	}
	store.SetOperatorIdentity(outOfTurn)
	if err := store.CreditBalance("alice", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProduceBlock(); err == nil {
		t.Fatal("expected out-of-turn producer to be rejected")
	}

	if expected == first.OwnerAddress {
		store.SetOperatorIdentity(first)
	} else {
		store.SetOperatorIdentity(second)
	}
	produced, err := store.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected in-turn producer to produce block")
	}
	if produced.Block.ProducerAddress != expected {
		t.Fatalf("expected producer %s, got %s", expected, produced.Block.ProducerAddress)
	}
}

func seedFinalizedDealForEpochTest(store *Store) {
	store.data.Miners["miner_epoch"] = wire.MinerStats{
		MinerAddress:  "miner_epoch",
		PublicKey:     "miner_pub",
		Endpoint:      "http://miner.local",
		CapacityBytes: 1024,
		UsedBytes:     1,
		Stake:         10,
		Status:        wire.MinerStatusActive,
	}
	store.data.Accounts["miner_epoch"] = wire.Account{
		Address:     "miner_epoch",
		LockedStake: 10,
	}
	store.data.Intents["intent_epoch"] = &Intent{
		IntentView: wire.IntentView{
			IntentID:     "intent_epoch",
			User:         "alice",
			FileName:     "epoch.bin",
			FileSize:     1,
			SegmentSize:  1,
			FileRoot:     "file_root",
			SegmentRoots: []string{"segment_root"},
			Segments: []wire.SegmentPlan{{
				SegmentID:   0,
				SegmentRoot: "segment_root",
				ShardHashes: []string{
					"shard_hash",
				},
			}},
			Erasure: wire.ErasurePolicy{
				DataShards:   1,
				ParityShards: 0,
				ShardSize:    1,
			},
			Status: wire.StatusFinalized,
			DealID: "deal_epoch",
		},
		Receipts: map[int]map[int]wire.MinerReceipt{
			0: {
				0: {
					MinerAddress:     "miner_epoch",
					MinerPublicKey:   "miner_pub",
					User:             "alice",
					IntentID:         "intent_epoch",
					FileRoot:         "file_root",
					SegmentID:        0,
					SegmentRoot:      "segment_root",
					ShardIndex:       0,
					ShardHash:        "shard_hash",
					ShardSize:        1,
					SectorCommitment: "sector_root",
					MinerEndpoint:    "http://miner.local",
				},
			},
		},
	}
	store.data.Deals["deal_epoch"] = "intent_epoch"
}

func signedTransferTx(t *testing.T, id string, req wire.TransferRequest, privateKey *ecdsa.PrivateKey, chainID string) wire.Transaction {
	t.Helper()
	if err := wire.SignTransfer(&req, privateKey, chainID); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	payloadHash := chaincrypto.HashBytes(raw)
	return wire.Transaction{
		TxID:           chaincrypto.HashBytes([]byte("transfer:" + payloadHash)),
		Type:           "transfer",
		From:           wire.NormalizeAddress(req.From),
		Nonce:          req.Nonce,
		NonceProtected: true,
		Fee:            req.Fee,
		PayloadHash:    payloadHash,
		Payload:        append([]byte(nil), raw...),
		CreatedAtUnix:  time.Now().Unix(),
	}
}

func signTestBlockVote(t *testing.T, block wire.Block, identity *OperatorIdentity, power uint64) wire.BlockVote {
	t.Helper()
	vote := wire.BlockVote{
		Height:             block.Height,
		BlockHash:          block.Hash,
		ValidatorAddress:   identity.OwnerAddress,
		ValidatorPublicKey: identity.OperatorPublicKeyHex(),
		Power:              power,
	}
	if err := wire.SignBlockVote(&vote, identity.OperatorPrivateKey); err != nil {
		t.Fatal(err)
	}
	return vote
}

func signTestConsensusVote(t *testing.T, block wire.Block, identity *OperatorIdentity, voteType string, power uint64) wire.ConsensusVote {
	t.Helper()
	vote := wire.ConsensusVote{
		Height:             block.Height,
		Round:              block.Round,
		Type:               voteType,
		BlockHash:          block.Hash,
		ValidatorAddress:   identity.OwnerAddress,
		ValidatorPublicKey: identity.OperatorPublicKeyHex(),
		Power:              power,
	}
	if err := wire.SignConsensusVote(&vote, identity.OperatorPrivateKey); err != nil {
		t.Fatal(err)
	}
	return vote
}

type captureConsensusVoteBroadcaster struct {
	votes chan wire.ConsensusVote
}

func (b *captureConsensusVoteBroadcaster) BroadcastConsensusVote(vote wire.ConsensusVote) {
	b.votes <- vote
}
