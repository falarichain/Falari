package chain

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://localhost:8080", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetBlockProducer(identity)

	if _, err := store.Faucet(wire.FaucetRequest{Address: "alice", Amount: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transfer(wire.TransferRequest{From: "alice", To: "bob", Amount: 25}); err != nil {
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
	if produced.Block.ProducerAddress != identity.Address {
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
	if empty.Produced {
		t.Fatal("empty mempool should not produce a block")
	}
}

func TestAcceptPeerBlock(t *testing.T) {
	producer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetBlockProducer(identity)
	if _, err := producer.Faucet(wire.FaucetRequest{Address: "alice", Amount: 100}); err != nil {
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
		t.Fatal("accepted block should replay faucet transaction")
	}
	if peer.data.Validators[identity.Address].ProducedBlocks != 1 {
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
		Amount: 10,
		Nonce:  0,
		Fee:    1,
	}, privateKey)
	if accepted, err := store.AcceptTransaction(lowFee); err != nil || !accepted {
		t.Fatalf("expected low fee tx accepted, accepted=%t err=%v", accepted, err)
	}

	replacement := signedTransferTx(t, "replacement", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000b0",
		Amount: 10,
		Nonce:  0,
		Fee:    3,
	}, privateKey)
	if accepted, err := store.AcceptTransaction(replacement); err != nil || !accepted {
		t.Fatalf("expected replacement tx accepted, accepted=%t err=%v", accepted, err)
	}
	mempool := store.Mempool()
	if len(mempool.Pending) != 1 || mempool.Pending[0].TxID != replacement.TxID || mempool.Pending[0].Fee != 3 {
		t.Fatalf("unexpected replacement mempool: %+v", mempool.Pending)
	}

	tooCheap := signedTransferTx(t, "too-cheap", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000b0",
		Amount: 10,
		Nonce:  0,
		Fee:    2,
	}, privateKey)
	if accepted, err := store.AcceptTransaction(tooCheap); err == nil || accepted {
		t.Fatalf("expected lower fee replacement rejected, accepted=%t err=%v", accepted, err)
	}
}

func TestMempoolProducesContiguousNonceOrder(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetBlockProducer(identity)

	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)
	store.data.Accounts[from] = wire.Account{Address: from, Balance: 200}

	nonceOneHighFee := signedTransferTx(t, "nonce-one", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000b1",
		Amount: 10,
		Nonce:  1,
		Fee:    100,
	}, privateKey)
	nonceZeroLowFee := signedTransferTx(t, "nonce-zero", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000b0",
		Amount: 10,
		Nonce:  0,
		Fee:    1,
	}, privateKey)
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
	if account.Nonce != 2 || account.Balance != 79 {
		t.Fatalf("unexpected sender account after block: %+v", account)
	}
}

func TestBlockProductionChargesFeesToProducer(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetBlockProducer(identity)

	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)
	to := "0x00000000000000000000000000000000000000f1"
	store.data.Accounts[from] = wire.Account{Address: from, Balance: 100}
	tx := signedTransferTx(t, "fee-transfer", wire.TransferRequest{
		From:   from,
		To:     to,
		Amount: 25,
		Nonce:  0,
		Fee:    5,
	}, privateKey)
	if accepted, err := store.AcceptTransaction(tx); err != nil || !accepted {
		t.Fatalf("expected tx accepted, accepted=%t err=%v", accepted, err)
	}

	produced, err := store.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected block to be produced")
	}
	fromAccount := store.accountLocked(from)
	toAccount := store.accountLocked(to)
	producerAccount := store.accountLocked(identity.Address)
	if fromAccount.Balance != 70 || toAccount.Balance != 25 || producerAccount.Balance != 5 {
		t.Fatalf("unexpected fee balances: from=%+v to=%+v producer=%+v", fromAccount, toAccount, producerAccount)
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
	}, privateKey)
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
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetBlockProducer(identity)

	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)
	store.data.Accounts[from] = wire.Account{Address: from, Balance: 100}
	tx := signedTransferTx(t, "busy-transfer", wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000f3",
		Amount: 10,
		Nonce:  0,
		Fee:    2,
	}, privateKey)
	if accepted, err := store.AcceptTransaction(tx); err != nil || !accepted {
		t.Fatalf("expected tx accepted, accepted=%t err=%v", accepted, err)
	}
	if _, err := store.ProduceBlock(); err != nil {
		t.Fatal(err)
	}
	if store.data.FeeMarket.BaseFee != 2 || store.data.FeeMarket.LastBlockTxs != 2 {
		t.Fatalf("expected base fee to increase after busy block, got %+v", store.data.FeeMarket)
	}
}

func TestSimplifiedBFTFinalizesAfterTwoThirdsVotes(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identities := make([]*ValidatorIdentity, 0, 3)
	for i := 0; i < 3; i++ {
		identity, err := LoadOrCreateValidatorIdentity("")
		if err != nil {
			t.Fatal(err)
		}
		identities = append(identities, identity)
		store.data.Validators[identity.Address] = wire.ValidatorInfo{
			Address:   identity.Address,
			PublicKey: identity.PublicKeyBase64(),
			Stake:     1,
			Status:    wire.ValidatorStatusActive,
		}
		store.data.ConsensusValidators[identity.Address] = true
	}
	proposerAddr, err := store.selectProposerLocked(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	var producer *ValidatorIdentity
	for _, identity := range identities {
		if identity.Address == proposerAddr {
			producer = identity
			break
		}
	}
	if producer == nil {
		t.Fatal("proposer identity not found")
	}
	store.SetBlockProducer(producer)
	if _, err := store.Faucet(wire.FaucetRequest{Address: "alice", Amount: 1}); err != nil {
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
		if identity.Address == producer.Address {
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
	identities := make([]*ValidatorIdentity, 0, 3)
	for i := 0; i < 3; i++ {
		identity, err := LoadOrCreateValidatorIdentity("")
		if err != nil {
			t.Fatal(err)
		}
		identities = append(identities, identity)
		store.data.Validators[identity.Address] = wire.ValidatorInfo{
			Address:   identity.Address,
			PublicKey: identity.PublicKeyBase64(),
			Stake:     1,
			Status:    wire.ValidatorStatusActive,
		}
		store.data.ConsensusValidators[identity.Address] = true
	}
	proposerAddr, err := store.selectProposerLocked(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	var producer *ValidatorIdentity
	for _, identity := range identities {
		if identity.Address == proposerAddr {
			producer = identity
			break
		}
	}
	if producer == nil {
		t.Fatal("proposer identity not found")
	}
	store.SetBlockProducer(producer)
	if _, err := store.Faucet(wire.FaucetRequest{Address: "alice", Amount: 1}); err != nil {
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
		if i < 2 && resp.Finalized {
			t.Fatal("expected finality only after quorum precommits")
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

func TestAcceptBlockRejectsInvalidFinalityCertificate(t *testing.T) {
	producer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetBlockProducer(identity)
	if _, err := producer.Faucet(wire.FaucetRequest{Address: "alice", Amount: 1}); err != nil {
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
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetBlockProducer(identity)
	if _, err := producer.Faucet(wire.FaucetRequest{Address: "alice", Amount: 1}); err != nil {
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
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	store.SetBlockProducer(identity)
	if _, err := store.Faucet(wire.FaucetRequest{Address: "alice", Amount: 100}); err != nil {
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
	if _, err := store.Faucet(wire.FaucetRequest{Address: "alice", Amount: 50}); err != nil {
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
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetBlockProducer(identity)
	seedFinalizedDealForEpochTest(producer)

	peer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	seedFinalizedDealForEpochTest(peer)
	peerRegistration, err := identity.RegistrationRequest("http://validator-a", 0)
	if err != nil {
		t.Fatal(err)
	}
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
	first, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Validators[first.Address] = wire.ValidatorInfo{
		Address:   first.Address,
		PublicKey: first.PublicKeyBase64(),
		Status:    "active",
	}
	store.data.Validators[second.Address] = wire.ValidatorInfo{
		Address:   second.Address,
		PublicKey: second.PublicKeyBase64(),
		Status:    "active",
	}
	store.data.ConsensusValidators[first.Address] = true
	store.data.ConsensusValidators[second.Address] = true

	expected := store.consensusValidatorAddressesLocked()[0]
	outOfTurn := first
	if expected == first.Address {
		outOfTurn = second
	}
	store.SetBlockProducer(outOfTurn)
	if _, err := store.Faucet(wire.FaucetRequest{Address: "alice", Amount: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProduceBlock(); err == nil {
		t.Fatal("expected out-of-turn producer to be rejected")
	}

	if expected == first.Address {
		store.SetBlockProducer(first)
	} else {
		store.SetBlockProducer(second)
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

func signedTransferTx(t *testing.T, id string, req wire.TransferRequest, privateKey *ecdsa.PrivateKey) wire.Transaction {
	t.Helper()
	if err := wire.SignTransfer(&req, privateKey); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return wire.Transaction{
		TxID:           fmt.Sprintf("tx_%s", id),
		Type:           "transfer",
		From:           wire.NormalizeAddress(req.From),
		Nonce:          req.Nonce,
		NonceProtected: true,
		Fee:            req.Fee,
		PayloadHash:    chaincrypto.HashBytes(raw),
		Payload:        append([]byte(nil), raw...),
		CreatedAtUnix:  time.Now().Unix(),
	}
}

func signTestBlockVote(t *testing.T, block wire.Block, identity *ValidatorIdentity, power uint64) wire.BlockVote {
	t.Helper()
	vote := wire.BlockVote{
		Height:             block.Height,
		BlockHash:          block.Hash,
		ValidatorAddress:   identity.Address,
		ValidatorPublicKey: identity.PublicKeyBase64(),
		Power:              power,
	}
	if err := wire.SignBlockVote(&vote, identity.PrivateKey); err != nil {
		t.Fatal(err)
	}
	return vote
}

func signTestConsensusVote(t *testing.T, block wire.Block, identity *ValidatorIdentity, voteType string, power uint64) wire.ConsensusVote {
	t.Helper()
	vote := wire.ConsensusVote{
		Height:             block.Height,
		Round:              block.Round,
		Type:               voteType,
		BlockHash:          block.Hash,
		ValidatorAddress:   identity.Address,
		ValidatorPublicKey: identity.PublicKeyBase64(),
		Power:              power,
	}
	if err := wire.SignConsensusVote(&vote, identity.PrivateKey); err != nil {
		t.Fatal(err)
	}
	return vote
}
