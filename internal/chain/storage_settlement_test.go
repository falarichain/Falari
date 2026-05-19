package chain

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestSettleExpiredIntentRefundsLockedStorage(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", Balance: 10, LockedStorage: 20}
	intent := &Intent{
		IntentView: wire.IntentView{
			IntentID:  "intent_refund",
			User:      "alice",
			FileName:  "refund.bin",
			FileSize:  1,
			LockedFee: 20,
			Status:    wire.StatusPartial,
		},
		DeadlineUnix: time.Now().Add(-time.Minute).Unix(),
	}
	store.data.Intents[intent.IntentID] = intent

	resp, err := store.SettleIntent(wire.SettleIntentRequest{IntentID: intent.IntentID, User: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.RefundedFee != 20 || resp.Status != wire.StatusExpired {
		t.Fatalf("unexpected settlement response %+v", resp)
	}
	account := store.accountLocked("alice")
	if account.Balance != 30 || account.LockedStorage != 0 {
		t.Fatalf("unexpected account after refund %+v", account)
	}
}

func TestSettleIntentRefundsOnlyUnpaidEscrow(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", LockedStorage: 13}
	intent := &Intent{
		IntentView: wire.IntentView{
			IntentID:      "intent_partial_refund",
			User:          "alice",
			FileName:      "partial.bin",
			FileSize:      1,
			LockedFee:     20,
			PaidFee:       7,
			Status:        wire.StatusFinalized,
			Policy:        wire.StoragePolicy{Duration: 1},
			SegmentSize:   1,
			ExpiresAtUnix: time.Now().Add(-8 * 24 * time.Hour).Unix(),
		},
		DeadlineUnix: time.Now().Add(-time.Minute).Unix(),
		UpdatedAt:    time.Now().Add(-time.Hour).Unix(),
	}
	store.data.Intents[intent.IntentID] = intent

	resp, err := store.SettleIntent(wire.SettleIntentRequest{IntentID: intent.IntentID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.RefundedFee != 13 || resp.PaidFee != 7 {
		t.Fatalf("unexpected settlement response %+v", resp)
	}
	account := store.accountLocked("alice")
	if account.Balance != 13 || account.LockedStorage != 0 {
		t.Fatalf("unexpected account after partial refund %+v", account)
	}
}

func TestSettleExpiredIntentsAutomaticallySettlesDueOnly(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	store.data.Accounts["alice"] = wire.Account{Address: "alice", LockedStorage: 10}
	store.data.Accounts["bob"] = wire.Account{Address: "bob", LockedStorage: 20}
	store.data.Intents["intent_due"] = &Intent{
		IntentView: wire.IntentView{
			IntentID:  "intent_due",
			User:      "alice",
			FileSize:  1,
			LockedFee: 10,
			Status:    wire.StatusPartial,
		},
		DeadlineUnix: now.Add(-time.Minute).Unix(),
	}
	store.data.Intents["intent_permanent"] = &Intent{
		IntentView: wire.IntentView{
			IntentID:  "intent_permanent",
			User:      "bob",
			FileSize:  1,
			LockedFee: 20,
			Status:    wire.StatusFinalized,
			Policy:    wire.StoragePolicy{Class: "permanent"},
		},
		UpdatedAt: now.Add(-time.Hour).Unix(),
	}

	responses, err := store.SettleExpiredIntents()
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 1 || responses[0].IntentID != "intent_due" {
		t.Fatalf("expected only due intent to settle, got %+v", responses)
	}
	alice := store.accountLocked("alice")
	if alice.Balance != 10 || alice.LockedStorage != 0 {
		t.Fatalf("unexpected alice account %+v", alice)
	}
	bob := store.accountLocked("bob")
	if bob.Balance != 0 || bob.LockedStorage != 20 {
		t.Fatalf("permanent intent should remain locked, bob=%+v", bob)
	}
	if store.data.Intents["intent_permanent"].Status != wire.StatusFinalized {
		t.Fatalf("permanent intent should remain finalized")
	}
}

func TestSettlePermanentFinalizedIntentIsRejected(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", LockedStorage: 5}
	store.data.Intents["intent_permanent_reject"] = &Intent{
		IntentView: wire.IntentView{
			IntentID:  "intent_permanent_reject",
			User:      "alice",
			FileSize:  1,
			LockedFee: 5,
			Status:    wire.StatusFinalized,
			Policy:    wire.StoragePolicy{Class: "permanent"},
		},
		UpdatedAt: time.Now().Add(-time.Hour).Unix(),
	}
	if _, err := store.SettleIntent(wire.SettleIntentRequest{IntentID: "intent_permanent_reject"}); err == nil {
		t.Fatal("expected permanent finalized intent settlement to be rejected")
	}
}
