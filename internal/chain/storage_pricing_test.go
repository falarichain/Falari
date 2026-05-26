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
	// With basePrice=10^8 the scaled arithmetic (big.Int, no intermediate ceil):
	// tieredFee = 1536 * 10^8 * 30 * 10000 / (300 * 10000) = 15_360_000_000
	// utilization 90% → multiplier 2x → fee = 15_360_000_000 * 20000 / 10000 = 30_720_000_000
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
	if quote.RequiredFee != 30_720_000_000 {
		t.Fatalf("expected fee 30_720_000_000, got %d", quote.RequiredFee)
	}
}

func TestStorageQuoteTieredDiscount(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	// 1000 MiB file, 1+0 erasure, 3 years (1080 days)
	// Year 1 (360d): 1000 * 10^8 * 360 * 10000 / (300 * 10000) = 1200 * 10^8
	// Year 2 (360d): 1000 * 10^8 * 360 * 9000  / (300 * 10000) = 1080 * 10^8
	// Year 3 (360d): 1000 * 10^8 * 360 * 8000  / (300 * 10000) =  960 * 10^8
	// Total: 3240 * 10^8
	quote, err := store.StorageQuote(wire.StorageQuoteRequest{
		FileSize: 1000 * (1 << 20),
		Erasure:  wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:   wire.StoragePolicy{Duration: 1080 * 24 * 60 * 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	if quote.RequiredFee != gfTokens(3240) {
		t.Fatalf("expected tiered fee 3240, got %d", quote.RequiredFee)
	}
}

func TestStorageQuoteTieredDiscountMaxCap(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	// 1000 MiB file, 1+0 erasure, 10 years (3600 days)
	// Year 1:  1200, Year 2:  1080, Year 3:  960, Year 4:  840
	// Year 5:   720, Year 6:   600, Year 7:  480, Year 8:  360
	// Year 9:   240, Year 10:  120 (90% discount cap)
	// Total: 6600 * 10^8
	quote, err := store.StorageQuote(wire.StorageQuoteRequest{
		FileSize: 1000 * (1 << 20),
		Erasure:  wire.ErasurePolicy{DataShards: 1, ParityShards: 0},
		Policy:   wire.StoragePolicy{Duration: 3600 * 24 * 60 * 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	if quote.RequiredFee != gfTokens(6600) {
		t.Fatalf("expected tiered fee 6600, got %d", quote.RequiredFee)
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
	// Minimum fee = 1 GF (10^8). No retrieval/foundation addresses → burn = 9%.
	// burn = 9_000_000, miner portion = 91_000_000.
	if resp.RequiredFee != gfTokens(1) || resp.LockedFee != gfTokens(1) {
		t.Fatalf("expected required and locked fee 1 GF, got %+v", resp)
	}
	account := store.accountLocked(alice.Addr)
	if account.Balance != gfTokens(9) || account.LockedStorage != 91_000_000 {
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
	if resp.RequiredFee != gfTokens(100) {
		t.Fatalf("expected required fee 100, got %d", resp.RequiredFee)
	}
	if resp.BurnedFee != gfTokens(9) {
		t.Fatalf("expected burned fee 9, got %d", resp.BurnedFee)
	}
	if resp.RetrievalFee != 0 {
		t.Fatalf("expected retrieval fee 0 (no address), got %d", resp.RetrievalFee)
	}
	if resp.FoundationFee != 0 {
		t.Fatalf("expected foundation fee 0 (no address), got %d", resp.FoundationFee)
	}

	account := store.accountLocked(alice.Addr)
	if account.Balance != gfTokens(9900) {
		t.Fatalf("expected balance 9900, got %d", account.Balance)
	}
	if account.LockedStorage != gfTokens(91) {
		t.Fatalf("expected locked storage 91, got %d", account.LockedStorage)
	}

	intent := store.data.Intents[resp.IntentID]
	if intent.LockedFee != gfTokens(91) {
		t.Fatalf("expected intent locked fee 91, got %d", intent.LockedFee)
	}
	if intent.BurnedFee != gfTokens(9) {
		t.Fatalf("expected intent burned fee 9, got %d", intent.BurnedFee)
	}

	if store.data.StorageFeePool.TotalBurned != gfTokens(9) {
		t.Fatalf("expected pool total burned 9, got %d", store.data.StorageFeePool.TotalBurned)
	}
	if store.data.StorageFeePool.TotalLocked != gfTokens(91) {
		t.Fatalf("expected pool total locked 91, got %d", store.data.StorageFeePool.TotalLocked)
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
	if resp.BurnedFee != gfTokens(3) {
		t.Fatalf("expected burned fee 3, got %d", resp.BurnedFee)
	}
	if resp.RetrievalFee != gfTokens(3) {
		t.Fatalf("expected retrieval fee 3, got %d", resp.RetrievalFee)
	}
	if resp.FoundationFee != gfTokens(3) {
		t.Fatalf("expected foundation fee 3, got %d", resp.FoundationFee)
	}

	// User pays full 100
	aliceAcc := store.accountLocked(alice.Addr)
	if aliceAcc.Balance != gfTokens(9900) {
		t.Fatalf("expected alice balance 9900, got %d", aliceAcc.Balance)
	}
	if aliceAcc.LockedStorage != gfTokens(91) {
		t.Fatalf("expected alice locked storage 91, got %d", aliceAcc.LockedStorage)
	}

	// Retrieval address receives 3
	retAcc := store.accountLocked("retrieval_multisig")
	if retAcc.Balance != gfTokens(3) {
		t.Fatalf("expected retrieval balance 3, got %d", retAcc.Balance)
	}

	// Foundation address receives 3
	fndAcc := store.accountLocked("foundation_multisig")
	if fndAcc.Balance != gfTokens(3) {
		t.Fatalf("expected foundation balance 3, got %d", fndAcc.Balance)
	}

	// Pool tracking
	if store.data.StorageFeePool.TotalBurned != gfTokens(3) {
		t.Fatalf("expected pool burned 3, got %d", store.data.StorageFeePool.TotalBurned)
	}
	if store.data.StorageFeePool.TotalToRetrieval != gfTokens(3) {
		t.Fatalf("expected pool to retrieval 3, got %d", store.data.StorageFeePool.TotalToRetrieval)
	}
	if store.data.StorageFeePool.TotalToFoundation != gfTokens(3) {
		t.Fatalf("expected pool to foundation 3, got %d", store.data.StorageFeePool.TotalToFoundation)
	}
	if store.data.StorageFeePool.TotalLocked != gfTokens(91) {
		t.Fatalf("expected pool locked 91, got %d", store.data.StorageFeePool.TotalLocked)
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
	// Minimum fee = 1 GF (10^8). No retrieval/foundation → burn = 9%.
	// burn = 9_000_000, miner portion = 91_000_000.
	if resp.BurnedFee != 9_000_000 {
		t.Fatalf("expected burn 9_000_000 for minimum fee, got %d", resp.BurnedFee)
	}
	account := store.accountLocked(alice.Addr)
	if account.Balance != gfTokens(9) || account.LockedStorage != 91_000_000 {
		t.Fatalf("unexpected account: %+v", account)
	}
	if store.data.StorageFeePool.TotalBurned != 9_000_000 {
		t.Fatalf("expected pool total burned 9_000_000, got %d", store.data.StorageFeePool.TotalBurned)
	}
}
