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

	// 1 GiB file, 2+1 erasure → 1536 MiB redundant, 30 days (year 1, full price)
	// tieredFee = ceil(1536 * 30 / 300) = 154
	// utilization 90% → multiplier 2x → fee = ceil(154 * 2) = 308
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
	if quote.TotalMiBDays != 1536*30 {
		t.Fatalf("expected %d total MiB-days, got %d", 1536*30, quote.TotalMiBDays)
	}
	if quote.UtilizationBPS != 9000 || quote.UtilizationMultiplier != 20000 {
		t.Fatalf("unexpected utilization quote: %+v", quote)
	}
	if quote.RequiredFee != 308 {
		t.Fatalf("expected fee 308, got %d", quote.RequiredFee)
	}
}

func TestStorageQuoteTieredDiscount(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	// 1000 MiB file, 1+0 erasure, 3 years (1080 days)
	// Year 1 (360d): ceil(1000*360/300) = 1200
	// Year 2 (360d): ceil(1000*1*360*9000/(300*10000)) = 1080
	// Year 3 (360d): ceil(1000*1*360*8000/(300*10000)) = 960
	// Total: 3240
	quote, err := store.StorageQuote(wire.StorageQuoteRequest{
		FileSize: 1000 * (1 << 20),
		Erasure:  wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:   wire.StoragePolicy{Duration: 1080 * 24 * 60 * 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	if quote.RequiredFee != 3240 {
		t.Fatalf("expected tiered fee 3240, got %d", quote.RequiredFee)
	}
}

func TestStorageQuoteTieredDiscountMaxCap(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	// 1000 MiB file, 1+0 erasure, 10 years (3600 days)
	// Year 1:  ceil(1000*360/300) = 1200
	// Year 2:  ceil(1000*360*9000/(300*10000)) = 1080
	// Year 3:  960, Year 4: 840, Year 5: 720, Year 6: 600, Year 7: 480
	// Year 8:  360, Year 9: 240
	// Year 10: ceil(1000*360*1000/(300*10000)) = 120 (90% discount cap)
	// Total: 1200+1080+960+840+720+600+480+360+240+120 = 6600
	quote, err := store.StorageQuote(wire.StorageQuoteRequest{
		FileSize: 1000 * (1 << 20),
		Erasure:  wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:   wire.StoragePolicy{Duration: 3600 * 24 * 60 * 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	if quote.RequiredFee != 6600 {
		t.Fatalf("expected tiered fee 6600, got %d", quote.RequiredFee)
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
	// fee = ceil(1*30/300) = 1 (minimum fee)
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

	// 1 GiB file, 1 year → fee = ceil(1024*360/300) = 1024
	_, err = store.CreateIntent(wire.CreateIntentRequest{
		User:         "alice",
		FileName:     "file.bin",
		FileSize:     1 << 30,
		SegmentSize:  1 << 20,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments:     []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment-root", ShardHashes: []string{"shard-root"}}},
		Erasure:      wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(360 * 24 * time.Hour / time.Second)},
		LockedFee:    1,
	})
	if err == nil {
		t.Fatal("expected underpriced storage intent to be rejected")
	}
}

func TestCreateIntentBurnsThreePercent(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", Balance: 10000}

	// 1000 MiB file, 1+0 erasure, 30000 days (fits in year 1)
	// fee = ceil(1000 * 30000 / 300) = 100000 → too large, use shorter
	// 1000 MiB, 30000 days → fee = ceil(1000*30000/300) = 100000. Too big.
	// Use 100 MiB, 300 days → fee = ceil(100*300/300) = 100
	// burn = 100*300/10000 = 3, minerPortion = 97
	resp, err := store.CreateIntent(wire.CreateIntentRequest{
		User:         "alice",
		FileName:     "big.bin",
		FileSize:     100 * (1 << 20),
		SegmentSize:  1 << 20,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments:     []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment-root", ShardHashes: []string{"shard-root"}}},
		Erasure:      wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(300 * 24 * time.Hour / time.Second)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.RequiredFee != 100 {
		t.Fatalf("expected required fee 100, got %d", resp.RequiredFee)
	}
	if resp.LockedFee != 100 {
		t.Fatalf("expected locked fee 100, got %d", resp.LockedFee)
	}
	if resp.BurnedFee != 3 {
		t.Fatalf("expected burned fee 3, got %d", resp.BurnedFee)
	}

	account := store.accountLocked("alice")
	if account.Balance != 9900 {
		t.Fatalf("expected balance 9900, got %d", account.Balance)
	}
	// minerPortion = 100 - 3 = 97
	if account.LockedStorage != 97 {
		t.Fatalf("expected locked storage 97, got %d", account.LockedStorage)
	}

	intent := store.data.Intents[resp.IntentID]
	if intent.LockedFee != 97 {
		t.Fatalf("expected intent locked fee 97, got %d", intent.LockedFee)
	}
	if intent.BurnedFee != 3 {
		t.Fatalf("expected intent burned fee 3, got %d", intent.BurnedFee)
	}

	escrow := store.data.DealEscrows[resp.IntentID]
	if escrow.LockedFee != 97 {
		t.Fatalf("expected escrow locked fee 97, got %d", escrow.LockedFee)
	}
	if escrow.BurnedFee != 3 {
		t.Fatalf("expected escrow burned fee 3, got %d", escrow.BurnedFee)
	}

	if store.data.StorageFeePool.TotalLocked != 97 {
		t.Fatalf("expected pool total locked 97, got %d", store.data.StorageFeePool.TotalLocked)
	}
	if store.data.StorageFeePool.TotalBurned != 3 {
		t.Fatalf("expected pool total burned 3, got %d", store.data.StorageFeePool.TotalBurned)
	}
}

func TestCreateIntentSmallFeeNoBurn(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", Balance: 10}

	resp, err := store.CreateIntent(wire.CreateIntentRequest{
		User:         "alice",
		FileName:     "tiny.bin",
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
	// fee = 1 (minimum), burn = 1*300/10000 = 0 (integer division)
	if resp.BurnedFee != 0 {
		t.Fatalf("expected no burn for small fee, got %d", resp.BurnedFee)
	}
	account := store.accountLocked("alice")
	if account.Balance != 9 || account.LockedStorage != 1 {
		t.Fatalf("unexpected account: %+v", account)
	}
	if store.data.StorageFeePool.TotalBurned != 0 {
		t.Fatalf("expected pool total burned 0, got %d", store.data.StorageFeePool.TotalBurned)
	}
}
