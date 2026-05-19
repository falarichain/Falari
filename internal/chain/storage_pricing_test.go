package chain

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestStorageQuoteUsesErasureDurationAndUtilization(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Miners["miner_busy"] = wire.MinerStats{
		MinerAddress:  "miner_busy",
		CapacityBytes: 10 * (1 << 30),
		UsedBytes:     9 * (1 << 30),
		Status:        "active",
	}

	quote, err := store.StorageQuote(wire.StorageQuoteRequest{
		FileSize: 1 << 30,
		Erasure:  wire.ErasurePolicy{DataShards: 2, ParityShards: 1},
		Policy:   wire.StoragePolicy{Duration: int64(30 * 24 * time.Hour / time.Second)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if quote.RedundantBytes != 1610612736 {
		t.Fatalf("expected 1.5 GiB redundant bytes, got %d", quote.RedundantBytes)
	}
	if quote.BillableGiBMonths != 2 {
		t.Fatalf("expected 2 billable GiB-months, got %d", quote.BillableGiBMonths)
	}
	if quote.UtilizationBPS != 9000 || quote.UtilizationMultiplier != 20000 {
		t.Fatalf("unexpected utilization quote: %+v", quote)
	}
	if quote.RequiredFee != 4 {
		t.Fatalf("expected fee 4, got %d", quote.RequiredFee)
	}
}

func TestCreateIntentAutoLocksRequiredStorageFee(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", Balance: 10}

	resp, err := store.CreateIntent(wire.CreateIntentRequest{
		User:         "alice",
		FileName:     "file.bin",
		FileSize:     1,
		SegmentSize:  1,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments:     []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment-root", ShardHashes: []string{"shard-root"}}},
		Erasure:      wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(30 * 24 * time.Hour / time.Second)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.RequiredFee != 1 || resp.LockedFee != 1 {
		t.Fatalf("expected required and locked fee 1, got %+v", resp)
	}
	account := store.accountLocked("alice")
	if account.Balance != 9 || account.LockedStorage != 1 {
		t.Fatalf("unexpected account after intent: %+v", account)
	}
}

func TestCreateIntentRejectsStorageFeeBelowQuote(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", Balance: 10}

	_, err = store.CreateIntent(wire.CreateIntentRequest{
		User:         "alice",
		FileName:     "file.bin",
		FileSize:     1 << 30,
		SegmentSize:  1 << 20,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments:     []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment-root", ShardHashes: []string{"shard-root"}}},
		Erasure:      wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(60 * 24 * time.Hour / time.Second)},
		LockedFee:    1,
	})
	if err == nil {
		t.Fatal("expected underpriced storage intent to be rejected")
	}
}
