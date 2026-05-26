package chain

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestStatusSummarizesOperationalState(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	registerTestMiner(t, store, "miner_a", "http://miner-a", 100)
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(100))
	store.data.Blocks = append(store.data.Blocks, wire.Block{
		Height:   1,
		Hash:     "block_hash",
		TimeUnix: time.Now().Unix(),
		Finality: wire.BlockFinality{Finalized: true},
	})
	store.data.PendingTxs = append(store.data.PendingTxs, wire.Transaction{TxID: "tx_1"})
	store.data.Challenges["challenge_1"] = wire.StorageChallenge{ChallengeID: "challenge_1"}
	store.data.Epochs["epoch_1"] = wire.ProofEpoch{EpochID: "epoch_1", Status: "active"}
	store.data.RepairTasks["repair_1"] = wire.RepairTask{RepairID: "repair_1", Status: "pending"}

	resp, err := store.CreateIntent(testAssignedIntentRequest(t, store, alice, 0))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != wire.StatusUploading {
		t.Fatalf("expected uploading intent, got %s", resp.Status)
	}

	status := store.Status()
	if status.Status != "ok" || status.Height != 1 || status.LatestFinalizedHeight != 1 {
		t.Fatalf("unexpected chain height status: %+v", status)
	}
	if status.PendingTransactions != 3 || status.PendingChallenges != 1 || status.ActiveEpochs != 1 || status.PendingRepairTasks != 1 {
		t.Fatalf("unexpected pending counters: %+v", status)
	}
	if status.Miners != 1 || status.ActiveMiners != 1 || status.CapacityBytes != 100 {
		t.Fatalf("unexpected miner counters: %+v", status)
	}
	if status.Intents != 1 || status.UploadingIntents != 1 {
		t.Fatalf("unexpected intent counters: %+v", status)
	}
}
