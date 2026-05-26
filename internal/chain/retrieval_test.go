package chain

import (
	"testing"
	"time"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestSubmitRetrievalReceiptRecordsAccessTelemetryWithoutPayingStorageMiner(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	user := wire.AccountAddress(&clientKey.PublicKey)
	minerKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	minerAddress := wire.AccountAddress(&minerKey.PublicKey)
	minerPublicKey := wire.EncodeHex(ethcrypto.CompressPubkey(&minerKey.PublicKey))
	shardHash := "shard_retrieval"

	store.data.Accounts[user] = wire.Account{Address: user, LockedStorage: 5}
	store.data.Accounts[minerAddress] = wire.Account{Address: minerAddress, LockedStake: 10}
	store.data.Miners[minerAddress] = wire.MinerStats{
		MinerAddress: minerAddress,
		PublicKey:    minerPublicKey,
		Stake:        10,
		Status:       "active",
	}
	store.data.Intents["intent_retrieval"] = &Intent{
		IntentView: wire.IntentView{
			IntentID:         "intent_retrieval",
			User:             user,
			Status:           wire.StatusFinalized,
			StorageStatus:    wire.StorageStatusActive,
			AccessStatus:     wire.AccessStatusPublic,
			ModerationStatus: wire.ModerationStatusNone,
			LockedFee:        5,
			Policy:           wire.StoragePolicy{Duration: 10},
		},
		CreatedAt: time.Now().Add(-20 * time.Second).Unix(),
		UpdatedAt: time.Now().Add(-20 * time.Second).Unix(),
		Receipts: map[int]map[int]wire.MinerReceipt{
			0: {
				0: {
					IntentID:       "intent_retrieval",
					ShardHash:      shardHash,
					MinerAddress:   "storage_miner",
					MinerPublicKey: minerPublicKey,
					ShardSize:      1024,
				},
			},
		},
	}

	receipt := wire.RetrievalReceipt{
		ReceiptID:      "retrieval_receipt_1",
		RequestID:      "retrieval_request_1",
		IntentID:       "intent_retrieval",
		ShardHash:      shardHash,
		MinerAddress:   minerAddress,
		MinerPublicKey: minerPublicKey,
		BytesServed:    2*1024*1024 + 1,
		ServedAtUnix:   time.Now().Unix(),
	}
	if err := wire.SignRetrievalClientReceipt(&receipt, clientKey); err != nil {
		t.Fatal(err)
	}
	if err := wire.SignRetrievalReceiptMiner(&receipt, minerKey); err != nil {
		t.Fatal(err)
	}

	resp, err := store.SubmitRetrievalReceipt(wire.SubmitRetrievalReceiptRequest{Receipt: receipt})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "accepted" || resp.Reward != 0 {
		t.Fatalf("unexpected retrieval response %+v", resp)
	}
	if account := store.accountLocked(user); account.LockedStorage != 5 {
		t.Fatalf("expected user locked storage unchanged, got %d", account.LockedStorage)
	}
	if account := store.accountLocked(minerAddress); account.Balance != 0 {
		t.Fatalf("expected miner balance unchanged (0) before release, got %d", account.Balance)
	} else if account.PendingMiningRewards != 0 {
		t.Fatalf("expected miner pending mining rewards 0, got %d", account.PendingMiningRewards)
	}
	stats := store.minerStatsLocked(minerAddress)
	if stats.RetrievalSuccess != 1 || stats.RetrievalBytes != receipt.BytesServed || stats.RetrievalRewards != 0 || stats.Rewards != 0 {
		t.Fatalf("unexpected retrieval miner stats %+v", stats)
	}
	if len(store.data.MiningRewardVestings) != 0 {
		t.Fatalf("expected no mining vesting bucket for raw retrieval telemetry, got %d", len(store.data.MiningRewardVestings))
	}

	replay, err := store.SubmitRetrievalReceipt(wire.SubmitRetrievalReceiptRequest{Receipt: receipt})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Reward != 0 {
		t.Fatalf("duplicate receipt should not pay again: %+v", replay)
	}
}

func TestSubmitRetrievalReceiptRejectsBlockedIntent(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	user := wire.AccountAddress(&clientKey.PublicKey)
	minerKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	minerAddress := wire.AccountAddress(&minerKey.PublicKey)
	minerPublicKey := wire.EncodeHex(ethcrypto.CompressPubkey(&minerKey.PublicKey))
	store.data.Accounts[minerAddress] = wire.Account{Address: minerAddress, LockedStake: 10}
	store.data.Miners[minerAddress] = wire.MinerStats{
		MinerAddress: minerAddress,
		PublicKey:    minerPublicKey,
		Stake:        10,
		Status:       "active",
	}
	store.data.Intents["intent_blocked_retrieval"] = &Intent{
		IntentView: wire.IntentView{
			IntentID:         "intent_blocked_retrieval",
			User:             user,
			Status:           wire.StatusFinalized,
			StorageStatus:    wire.StorageStatusActive,
			AccessStatus:     wire.AccessStatusBlocked,
			ModerationStatus: wire.ModerationStatusBlocked,
			LockedFee:        5,
		},
		Receipts: map[int]map[int]wire.MinerReceipt{
			0: {0: {IntentID: "intent_blocked_retrieval", ShardHash: "blocked_shard"}},
		},
	}

	receipt := wire.RetrievalReceipt{
		ReceiptID:      "retrieval_receipt_blocked",
		RequestID:      "retrieval_request_blocked",
		IntentID:       "intent_blocked_retrieval",
		ShardHash:      "blocked_shard",
		MinerAddress:   minerAddress,
		MinerPublicKey: minerPublicKey,
		BytesServed:    1,
		ServedAtUnix:   time.Now().Unix(),
	}
	if err := wire.SignRetrievalClientReceipt(&receipt, clientKey); err != nil {
		t.Fatal(err)
	}
	if err := wire.SignRetrievalReceiptMiner(&receipt, minerKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SubmitRetrievalReceipt(wire.SubmitRetrievalReceiptRequest{Receipt: receipt}); err == nil {
		t.Fatal("expected blocked retrieval to be rejected")
	}
}
