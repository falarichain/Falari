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
	alice := newTestUser(t)
	store.data.Accounts[alice.Addr] = wire.Account{Address: alice.Addr, Balance: gfTokens(10), LockedStorage: gfTokens(20)}
	intent := &Intent{
		IntentView: wire.IntentView{
			IntentID:  "intent_refund",
			User:      alice.Addr,
			FileName:  "refund.bin",
			FileSize:  1,
			LockedFee: gfTokens(20),
			Status:    wire.StatusPartial,
		},
		DeadlineUnix: time.Now().Add(-time.Minute).Unix(),
	}
	store.data.Intents[intent.IntentID] = intent

	siReq := wire.SettleIntentRequest{IntentID: intent.IntentID, User: alice.Addr}
	signSettleIntent(t, store, &siReq, alice)
	resp, err := store.SettleIntent(siReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp.RefundedFee != gfTokens(20) || resp.Status != wire.StatusExpired {
		t.Fatalf("unexpected settlement response %+v", resp)
	}
	account := store.accountLocked(alice.Addr)
	if account.Balance != gfTokens(30) || account.LockedStorage != 0 {
		t.Fatalf("unexpected account after refund %+v", account)
	}
}

func TestSettleIntentRefundsOnlyUnpaidEscrow(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	store.data.Accounts[alice.Addr] = wire.Account{Address: alice.Addr, LockedStorage: gfTokens(13)}
	intent := &Intent{
		IntentView: wire.IntentView{
			IntentID:      "intent_partial_refund",
			User:          alice.Addr,
			FileName:      "partial.bin",
			FileSize:      1,
			LockedFee:     gfTokens(20),
			PaidFee:       gfTokens(7),
			Status:        wire.StatusFinalized,
			Policy:        wire.StoragePolicy{Duration: 1},
			SegmentSize:   1,
			ExpiresAtUnix: time.Now().Add(-8 * 24 * time.Hour).Unix(),
		},
		DeadlineUnix: time.Now().Add(-time.Minute).Unix(),
		UpdatedAt:    time.Now().Add(-time.Hour).Unix(),
	}
	store.data.Intents[intent.IntentID] = intent

	siReq := wire.SettleIntentRequest{IntentID: intent.IntentID, User: alice.Addr}
	signSettleIntent(t, store, &siReq, alice)
	resp, err := store.SettleIntent(siReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp.RefundedFee != gfTokens(13) || resp.PaidFee != gfTokens(7) {
		t.Fatalf("unexpected settlement response %+v", resp)
	}
	account := store.accountLocked(alice.Addr)
	if account.Balance != gfTokens(13) || account.LockedStorage != 0 {
		t.Fatalf("unexpected account after partial refund %+v", account)
	}
}

func TestSettleExpiredIntentsAutomaticallySettlesDueOnly(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	store.data.Accounts["alice"] = wire.Account{Address: "alice", LockedStorage: gfTokens(10)}
	store.data.Accounts["bob"] = wire.Account{Address: "bob", LockedStorage: gfTokens(20)}
	store.data.Intents["intent_due"] = &Intent{
		IntentView: wire.IntentView{
			IntentID:  "intent_due",
			User:      "alice",
			FileSize:  1,
			LockedFee: gfTokens(10),
			Status:    wire.StatusPartial,
		},
		DeadlineUnix: now.Add(-time.Minute).Unix(),
	}
	store.data.Intents["intent_permanent"] = &Intent{
		IntentView: wire.IntentView{
			IntentID:  "intent_permanent",
			User:      "bob",
			FileSize:  1,
			LockedFee: gfTokens(20),
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
	if alice.Balance != gfTokens(10) || alice.LockedStorage != 0 {
		t.Fatalf("unexpected alice account %+v", alice)
	}
	bob := store.accountLocked("bob")
	if bob.Balance != 0 || bob.LockedStorage != gfTokens(20) {
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
	store.data.Accounts["alice"] = wire.Account{Address: "alice", LockedStorage: gfTokens(5)}
	store.data.Intents["intent_permanent_reject"] = &Intent{
		IntentView: wire.IntentView{
			IntentID:  "intent_permanent_reject",
			User:      "alice",
			FileSize:  1,
			LockedFee: gfTokens(5),
			Status:    wire.StatusFinalized,
			Policy:    wire.StoragePolicy{Class: "permanent"},
		},
		UpdatedAt: time.Now().Add(-time.Hour).Unix(),
	}
	if _, err := store.SettleIntent(wire.SettleIntentRequest{IntentID: "intent_permanent_reject"}); err == nil {
		t.Fatal("expected permanent finalized intent settlement to be rejected")
	}
}

func TestPermanentFundTopUpAndTerminateBurnsRemainingDonation(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	intent := testLifecycleIntentWithUser(alice.Addr)
	intent.Policy = wire.StoragePolicy{Class: "permanent", Duration: 0}
	intent.LockedFee = gfTokens(10)
	intent.PaidFee = gfTokens(2)
	intent.PermanentFundBalance = gfTokens(8)
	store.data.Intents[intent.IntentID] = intent
	store.data.Accounts[intent.User] = wire.Account{Address: intent.User, Balance: gfTokens(5), LockedStorage: gfTokens(8)}
	store.data.PermanentStorageFunds[intent.IntentID] = wire.PermanentStorageFund{
		IntentID:      intent.IntentID,
		User:          intent.User,
		Balance:       gfTokens(8),
		Contributed:   gfTokens(10),
		Paid:          gfTokens(2),
		CreatedAtUnix: time.Now().Unix(),
		UpdatedAtUnix: time.Now().Unix(),
	}

	topUpReq := wire.PermanentFundTopUpRequest{IntentID: intent.IntentID, User: intent.User, Amount: gfTokens(5)}
	signTopUpPermanentFund(t, store, &topUpReq, alice)
	topUp, err := store.TopUpPermanentFund(topUpReq)
	if err != nil {
		t.Fatal(err)
	}
	if topUp.Fund.Balance != gfTokens(13) || topUp.Fund.Contributed != gfTokens(15) {
		t.Fatalf("unexpected topup fund %+v", topUp.Fund)
	}
	account := store.accountLocked(intent.User)
	if account.Balance != 0 || account.LockedStorage != gfTokens(13) {
		t.Fatalf("unexpected account after topup %+v", account)
	}

	tdReq := wire.TerminateDealRequest{IntentID: intent.IntentID, User: intent.User}
	signTerminateDeal(t, store, &tdReq, alice)
	resp, err := store.TerminateDeal(tdReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp.RefundedFee != 0 {
		t.Fatalf("permanent fund termination should not refund, got %+v", resp)
	}
	if resp.BurnedFee != gfTokens(13) {
		t.Fatalf("expected 13 tokens burned, got %d", resp.BurnedFee)
	}
	account = store.accountLocked(intent.User)
	if account.LockedStorage != 0 {
		t.Fatalf("expected permanent donation to leave user locked storage, got %+v", account)
	}
	fund := store.data.PermanentStorageFunds[intent.IntentID]
	if !fund.Closed || fund.Burned != gfTokens(13) || fund.Balance != 0 {
		t.Fatalf("unexpected closed permanent fund %+v", fund)
	}
	if store.data.StorageFeePool.TotalBurned != gfTokens(13) {
		t.Fatalf("expected 13 tokens in TotalBurned, got %d", store.data.StorageFeePool.TotalBurned)
	}
}
