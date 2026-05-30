package chain

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestFiniteDealEscrowAccruesByServiceTime(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	intent := &Intent{
		IntentView: wire.IntentView{
			IntentID:      "intent_fee_accrual",
			User:          "alice",
			LockedFee:     90,
			Status:        wire.StatusFinalized,
			StorageStatus: wire.StorageStatusActive,
			Policy:        wire.StoragePolicy{Duration: 90 * miningRewardVestingDaySeconds},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", LockedStorage: 90}
	store.data.Intents[intent.IntentID] = intent
	store.createDealEscrowLocked(intent, now)
	store.activateDealEscrowLocked(intent, now)

	paid := store.spendFiniteStorageFeeLocked(intent, 90, now+30*miningRewardVestingDaySeconds)
	if paid != 30 {
		t.Fatalf("expected 30 accrued tokens to pay, got %d", paid)
	}
	if account := store.accountLocked("alice"); account.LockedStorage != 60 {
		t.Fatalf("expected locked storage 60 after payment, got %d", account.LockedStorage)
	}
	escrow := store.data.DealEscrows[intent.IntentID]
	if escrow.AccruedFee != 30 || escrow.PaidFee != 30 {
		t.Fatalf("unexpected escrow after accrual payment: %+v", escrow)
	}
	if store.data.StorageFeePool.TotalLocked != 90 || store.data.StorageFeePool.TotalPaid != 30 {
		t.Fatalf("unexpected storage fee pool: %+v", store.data.StorageFeePool)
	}
}

func TestPermanentFundPaysFullRequestedAmount(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	intent := &Intent{
		IntentView: wire.IntentView{
			IntentID:      "intent_permanent_rate",
			User:          "alice",
			LockedFee:     36500,
			Status:        wire.StatusFinalized,
			StorageStatus: wire.StorageStatusActive,
			Policy:        wire.StoragePolicy{Duration: 0},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", LockedStorage: 36500}
	store.data.Intents[intent.IntentID] = intent
	store.createPermanentFundLocked(intent, now)
	store.createDealEscrowLocked(intent, now)

	paid := store.spendPermanentFundLocked(intent, 1000, now+miningRewardVestingDaySeconds)
	if paid != 1000 {
		t.Fatalf("expected permanent fund to pay full requested amount 1000, got %d", paid)
	}
	fund := store.data.PermanentStorageFunds[intent.IntentID]
	if fund.Balance != 35500 || fund.Paid != 1000 {
		t.Fatalf("unexpected permanent fund after payment: %+v", fund)
	}
	if account := store.accountLocked("alice"); account.LockedStorage != 35500 {
		t.Fatalf("expected locked storage 35500 after payment, got %d", account.LockedStorage)
	}
}
