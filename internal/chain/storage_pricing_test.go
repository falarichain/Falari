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
	// With basePrice=10^7 (0.01 Token/MiB/30天):
	// tieredFee = 1536 * 10^7 * 30 * 10000 / (300 * 10000) = 1_536_000_000
	// utilization 70%+ → multiplier 1.5x → fee = 1_536_000_000 * 15000 / 10000 = 2_304_000_000
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
	if quote.UtilizationBPS != 9000 || quote.UtilizationMultiplier != 15000 {
		t.Fatalf("unexpected utilization quote: %+v", quote)
	}
	if quote.RequiredFee != 2_304_000_000 {
		t.Fatalf("expected fee 2_304_000_000, got %d", quote.RequiredFee)
	}
}

func TestStorageQuoteTieredDiscount(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	// 1000 MiB file, 1+0 erasure, 3 years (1080 days)
	// Year 1 (360d): 1000 * 10^7 * 360 * 10000 / (300 * 10000) = 120 * 10^8
	// Year 2 (360d): 1000 * 10^7 * 360 * 9000  / (300 * 10000) = 108 * 10^8
	// Year 3 (360d): 1000 * 10^7 * 360 * 8000  / (300 * 10000) =  96 * 10^8
	// Total: 324 * 10^8
	quote, err := store.StorageQuote(wire.StorageQuoteRequest{
		FileSize: 1000 * (1 << 20),
		Erasure:  wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:   wire.StoragePolicy{Duration: 1080 * 24 * 60 * 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	if quote.RequiredFee != gfTokens(324) {
		t.Fatalf("expected tiered fee 324, got %d", quote.RequiredFee)
	}
}

func TestStorageQuoteTieredDiscountMaxCap(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	// 1000 MiB file, 1+0 erasure, 10 years (3600 days)
	// Year 1:  120, Year 2:  108, Year 3:  96, Year 4:  84
	// Year 5:   72, Year 6:   60, Year 7:  48, Year 8:  36
	// Year 9:   24, Year 10:  12 (90% discount cap)
	// Total: 660 * 10^8
	quote, err := store.StorageQuote(wire.StorageQuoteRequest{
		FileSize: 1000 * (1 << 20),
		Erasure:  wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:   wire.StoragePolicy{Duration: 3600 * 24 * 60 * 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	if quote.RequiredFee != gfTokens(660) {
		t.Fatalf("expected tiered fee 660, got %d", quote.RequiredFee)
	}
}

func TestCreateIntentAutoLocksRequiredStorageFee(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(10))

	req := wire.CreateIntentRequest{
		User:         alice.Addr,
		FileName:     "file.bin",
		FileSize:     1,
		SegmentSize:  1,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments:     []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment-root", ShardHashes: []string{"shard-root"}}},
		Erasure:      wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(30 * 24 * time.Hour / time.Second)},
	}
	signCreateIntent(t, store, &req, alice)
	if err := wire.VerifyCreateIntent(req); err != nil {
		t.Fatalf("direct verify after sign failed: %v", err)
	}
	resp, err := store.CreateIntent(req)
	if err != nil {
		t.Fatal(err)
	}
	// Calculated fee = 1_000_000 (equals minimum 0.01 GF). No retrieval/foundation → burn = 9%.
	// burn = 30_000, miner portion = 910_000.
	if resp.RequiredFee != 1_000_000 || resp.LockedFee != 1_000_000 {
		t.Fatalf("expected required and locked fee 1_000_000, got %+v", resp)
	}
	account := store.accountLocked(alice.Addr)
	if account.Balance != gfTokens(10)-1_000_000 || account.LockedStorage != 910_000 {
		t.Fatalf("unexpected account after intent: %+v", account)
	}
}

func TestCreateIntentRejectsStorageFeeBelowQuote(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, 10)

	req := wire.CreateIntentRequest{
		User:         alice.Addr,
		FileName:     "file.bin",
		FileSize:     1 << 30,
		SegmentSize:  1 << 20,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments:     []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment-root", ShardHashes: []string{"shard-root"}}},
		Erasure:      wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(360 * 24 * time.Hour / time.Second)},
		LockedFee:    1,
	}
	signCreateIntent(t, store, &req, alice)
	_, err = store.CreateIntent(req)
	if err == nil {
		t.Fatal("expected underpriced storage intent to be rejected")
	}
}

func TestCreateIntentBurnsNinePercentWithoutAddresses(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(10000))

	req := wire.CreateIntentRequest{
		User:         alice.Addr,
		FileName:     "big.bin",
		FileSize:     100 * (1 << 20),
		SegmentSize:  1 << 20,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments:     []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment-root", ShardHashes: []string{"shard-root"}}},
		Erasure:      wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(300 * 24 * time.Hour / time.Second)},
	}
	signCreateIntent(t, store, &req, alice)
	resp, err := store.CreateIntent(req)
	if err != nil {
		t.Fatal(err)
	}
	// 100 MiB, 300 days, basePrice=10^7 → fee = 100*10^7*300*10000/(300*10000) = 10^9 = 10 tokens
	if resp.RequiredFee != gfTokens(10) {
		t.Fatalf("expected required fee 10, got %d", resp.RequiredFee)
	}
	if resp.BurnedFee != 90_000_000 {
		t.Fatalf("expected burned fee 90_000_000, got %d", resp.BurnedFee)
	}
	if resp.RetrievalFee != 0 {
		t.Fatalf("expected retrieval fee 0 (no address), got %d", resp.RetrievalFee)
	}
	if resp.FoundationFee != 0 {
		t.Fatalf("expected foundation fee 0 (no address), got %d", resp.FoundationFee)
	}

	account := store.accountLocked(alice.Addr)
	if account.Balance != gfTokens(10000)-gfTokens(10) {
		t.Fatalf("expected balance %d, got %d", gfTokens(10000)-gfTokens(10), account.Balance)
	}
	if account.LockedStorage != 910_000_000 {
		t.Fatalf("expected locked storage 910_000_000, got %d", account.LockedStorage)
	}

	intent := store.data.Intents[resp.IntentID]
	if intent.LockedFee != 910_000_000 {
		t.Fatalf("expected intent locked fee 910_000_000, got %d", intent.LockedFee)
	}
	if intent.BurnedFee != 90_000_000 {
		t.Fatalf("expected intent burned fee 90_000_000, got %d", intent.BurnedFee)
	}

	if store.data.StorageFeePool.TotalBurned != 90_000_000 {
		t.Fatalf("expected pool total burned 90_000_000, got %d", store.data.StorageFeePool.TotalBurned)
	}
	if store.data.StorageFeePool.TotalLocked != 910_000_000 {
		t.Fatalf("expected pool total locked 910_000_000, got %d", store.data.StorageFeePool.TotalLocked)
	}
}

func TestCreateIntentFourWaySplit(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(10000))
	store.data.RetrievalAddress = "retrieval_multisig"
	store.data.FoundationAddress = "foundation_multisig"

	req := wire.CreateIntentRequest{
		User:         alice.Addr,
		FileName:     "big.bin",
		FileSize:     100 * (1 << 20),
		SegmentSize:  1 << 20,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments:     []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment-root", ShardHashes: []string{"shard-root"}}},
		Erasure:      wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(300 * 24 * time.Hour / time.Second)},
	}
	signCreateIntent(t, store, &req, alice)
	resp, err := store.CreateIntent(req)
	if err != nil {
		t.Fatal(err)
	}
	// 100 MiB, 300 days → 10 tokens (basePrice=10^7)
	if resp.BurnedFee != 30_000_000 {
		t.Fatalf("expected burned fee 30_000_000, got %d", resp.BurnedFee)
	}
	if resp.RetrievalFee != 30_000_000 {
		t.Fatalf("expected retrieval fee 30_000_000, got %d", resp.RetrievalFee)
	}
	if resp.FoundationFee != 30_000_000 {
		t.Fatalf("expected foundation fee 30_000_000, got %d", resp.FoundationFee)
	}

	// User pays 10 tokens
	aliceAcc := store.accountLocked(alice.Addr)
	if aliceAcc.Balance != gfTokens(10000)-gfTokens(10) {
		t.Fatalf("expected alice balance %d, got %d", gfTokens(10000)-gfTokens(10), aliceAcc.Balance)
	}
	if aliceAcc.LockedStorage != 910_000_000 {
		t.Fatalf("expected alice locked storage 910_000_000, got %d", aliceAcc.LockedStorage)
	}

	// Retrieval address receives 3%
	retAcc := store.accountLocked("retrieval_multisig")
	if retAcc.Balance != 30_000_000 {
		t.Fatalf("expected retrieval balance 30_000_000, got %d", retAcc.Balance)
	}

	// Foundation address receives 3%
	fndAcc := store.accountLocked("foundation_multisig")
	if fndAcc.Balance != 30_000_000 {
		t.Fatalf("expected foundation balance 30_000_000, got %d", fndAcc.Balance)
	}

	// Pool tracking
	if store.data.StorageFeePool.TotalBurned != 30_000_000 {
		t.Fatalf("expected pool burned 30_000_000, got %d", store.data.StorageFeePool.TotalBurned)
	}
	if store.data.StorageFeePool.TotalToRetrieval != 30_000_000 {
		t.Fatalf("expected pool to retrieval 30_000_000, got %d", store.data.StorageFeePool.TotalToRetrieval)
	}
	if store.data.StorageFeePool.TotalToFoundation != 30_000_000 {
		t.Fatalf("expected pool to foundation 30_000_000, got %d", store.data.StorageFeePool.TotalToFoundation)
	}
	if store.data.StorageFeePool.TotalLocked != 910_000_000 {
		t.Fatalf("expected pool locked 910_000_000, got %d", store.data.StorageFeePool.TotalLocked)
	}
}

func TestCreateIntentSmallFeeNoBurn(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(10))

	req := wire.CreateIntentRequest{
		User:         alice.Addr,
		FileName:     "tiny.bin",
		FileSize:     1,
		SegmentSize:  1,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments:     []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment-root", ShardHashes: []string{"shard-root"}}},
		Erasure:      wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(30 * 24 * time.Hour / time.Second)},
	}
	signCreateIntent(t, store, &req, alice)
	resp, err := store.CreateIntent(req)
	if err != nil {
		t.Fatal(err)
	}
	// Calculated fee = 1_000_000 (equals minimum 0.01 GF). No retrieval/foundation → burn = 9%.
	// burn = 90_000, miner portion = 910_000.
	if resp.BurnedFee != 90_000 {
		t.Fatalf("expected burn 90_000 for calculated fee, got %d", resp.BurnedFee)
	}
	account := store.accountLocked(alice.Addr)
	if account.Balance != gfTokens(10)-1_000_000 || account.LockedStorage != 910_000 {
		t.Fatalf("unexpected account: %+v", account)
	}
	if store.data.StorageFeePool.TotalBurned != 90_000 {
		t.Fatalf("expected pool total burned 90_000, got %d", store.data.StorageFeePool.TotalBurned)
	}
}
