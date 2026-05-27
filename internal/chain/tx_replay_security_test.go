package chain

import (
	"encoding/json"
	"testing"
	"time"

	"chain/internal/wire"
)

func TestReplayGovernanceCreateRejectsTamperedResponseProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)
	req := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	proposal := governanceProposalFromRequest(req, normalizeGovernanceOperator(addresses[0]), "gov_proposal_replay", req.CreatedAtUnix)
	proposal.ReasonHash = "tampered_reason"

	err := store.applyGovernanceCreateProposalLocked(governanceCreateProposalTxPayload{
		Request:  req,
		Response: wire.CreateGovernanceProposalResponse{Proposal: proposal},
	})
	if err == nil {
		t.Fatal("expected tampered proposal response to be rejected")
	}
	if _, ok := store.data.GovernanceProposals[proposal.ProposalID]; ok {
		t.Fatal("tampered proposal was written")
	}
	if nonce := store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])]; nonce != 0 {
		t.Fatalf("expected proposer nonce unchanged, got %d", nonce)
	}
}

func TestReplayGovernanceVoteRejectsTamperedCountsWithoutMutation(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)
	proposalReq := testGovernanceProposalReq(t, store, addresses[0], privKeys[0])
	proposalResp, err := store.CreateGovernanceProposal(proposalReq)
	if err != nil {
		t.Fatal(err)
	}

	voteReq := testGovernanceVoteReq(t, store, proposalResp.Proposal.ProposalID, addresses[1], true, privKeys[1])
	vote := wire.GovernanceVote{
		ProposalID:     voteReq.ProposalID,
		Voter:          normalizeGovernanceOperator(addresses[1]),
		VoterSignature: voteReq.Signature,
		Approve:        true,
		CreatedAtUnix:  voteReq.CreatedAtUnix,
	}
	err = store.applyGovernanceCastVoteLocked(governanceCastVoteTxPayload{
		Request: voteReq,
		Response: wire.CastGovernanceVoteResponse{
			Vote:         vote,
			ApproveCount: 99,
			RejectCount:  0,
			Threshold:    store.governanceThresholdLocked(proposalResp.Proposal.Action),
			Executed:     false,
		},
	})
	if err == nil {
		t.Fatal("expected tampered vote response to be rejected")
	}
	if got := len(store.data.GovernanceVotes[voteReq.ProposalID]); got != 0 {
		t.Fatalf("expected no replayed votes, got %d", got)
	}
	if nonce := store.data.OperatorNonces[normalizeGovernanceOperator(addresses[1])]; nonce != 0 {
		t.Fatalf("expected voter nonce unchanged, got %d", nonce)
	}
}

func TestReplayRenewDealRejectsTamperedResponseWithoutMutation(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(100))
	intent := testLifecycleIntentWithUser(alice.Addr)
	intent.Policy.Renewable = true
	store.data.Intents[intent.IntentID] = intent

	req := wire.RenewDealRequest{
		IntentID: intent.IntentID,
		User:     alice.Addr,
		Duration: int64(30 * 24 * time.Hour / time.Second),
	}
	signRenewDeal(t, store, &req, alice)

	renewedAt := time.Now().Unix()
	price := store.estimateRenewalPriceLocked(intent, req.Duration)
	resp := wire.RenewDealResponse{
		IntentID:      intent.IntentID,
		Status:        wire.StatusFinalized,
		ExpiresAtUnix: renewedAt + req.Duration,
		NewLockedFee:  saturatingAdd(intent.LockedFee, price+1),
		PaidAmount:    price,
	}

	err = store.applyRenewDealLocked(renewDealTxPayload{Request: req, Response: resp, RenewedAtUnix: renewedAt})
	if err == nil {
		t.Fatal("expected tampered renewal response to be rejected")
	}
	account := store.accountLocked(alice.Addr)
	if account.Nonce != 0 || account.Balance != gfTokens(100) || account.LockedStorage != 0 {
		t.Fatalf("account mutated after rejected replay: %+v", account)
	}
	if intent.LockedFee != gfTokens(10) {
		t.Fatalf("intent locked fee mutated to %d", intent.LockedFee)
	}
}

func TestReplayPermanentFundTopUpRejectsTamperedFundWithoutMutation(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(100))
	intent := testLifecycleIntentWithUser(alice.Addr)
	intent.Policy.Duration = 0
	store.data.Intents[intent.IntentID] = intent

	req := wire.PermanentFundTopUpRequest{
		IntentID: intent.IntentID,
		User:     alice.Addr,
		Amount:   gfTokens(5),
	}
	signTopUpPermanentFund(t, store, &req, alice)

	toppedUpAt := time.Now().Unix()
	fund := store.ensurePermanentFundLocked(intent, toppedUpAt)
	fund.Balance = saturatingAdd(fund.Balance, req.Amount+1)
	fund.Contributed = saturatingAdd(fund.Contributed, req.Amount)
	fund.SustainableDailyRate = permanentFundDailyRate(fund.Balance)
	fund.InitialDailyRate = fund.SustainableDailyRate
	fund.UpdatedAtUnix = toppedUpAt

	err = store.applyPermanentFundTopUpLocked(permanentFundTopUpTxPayload{
		Request:        req,
		Response:       wire.PermanentFundTopUpResponse{Fund: fund},
		ToppedUpAtUnix: toppedUpAt,
	})
	if err == nil {
		t.Fatal("expected tampered permanent fund response to be rejected")
	}
	account := store.accountLocked(alice.Addr)
	if account.Nonce != 0 || account.Balance != gfTokens(100) || account.LockedStorage != 0 {
		t.Fatalf("account mutated after rejected replay: %+v", account)
	}
	if _, ok := store.data.PermanentStorageFunds[intent.IntentID]; ok {
		t.Fatal("permanent fund was written after rejected replay")
	}
	if intent.LockedFee != gfTokens(10) {
		t.Fatalf("intent locked fee mutated to %d", intent.LockedFee)
	}
}

func TestApplyBlockTransactionsRollsBackWholeBlockOnReplayError(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	intent := testLifecycleIntentWithUser(alice.Addr)
	intent.Status = wire.StatusUploading
	intent.StorageStatus = wire.StorageStatusPending
	intent.DeadlineUnix = time.Now().Add(-time.Hour).Unix()
	store.data.Intents[intent.IntentID] = intent
	store.data.Accounts[alice.Addr] = wire.Account{Address: alice.Addr, LockedStorage: intent.LockedFee}
	store.createDealEscrowLocked(intent, intent.CreatedAt)

	req := wire.SettleIntentRequest{IntentID: intent.IntentID, User: alice.Addr}
	signSettleIntent(t, store, &req, alice)
	payload := settleIntentTxPayload{
		Request:       req,
		Response:      wire.SettleIntentResponse{IntentID: intent.IntentID, Status: wire.StatusFinalized},
		SettledAtUnix: time.Now().Unix(),
	}
	settleRaw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	creditRaw, err := json.Marshal(map[string]any{"address": "bob", "amount": uint64(123)})
	if err != nil {
		t.Fatal(err)
	}

	err = store.applyBlockTransactionsLocked(wire.Block{ProducerAddress: "producer", Transactions: []wire.Transaction{
		{TxID: "tx_credit", Type: "genesis_credit", Payload: creditRaw},
		{TxID: "tx_bad_settle", Type: "settle_intent", Payload: settleRaw},
	}})
	if err == nil {
		t.Fatal("expected block replay error")
	}
	if got := store.accountLocked("bob").Balance; got != 0 {
		t.Fatalf("expected credited balance rolled back, got %d", got)
	}
	if got := store.data.Intents[intent.IntentID].Status; got != wire.StatusUploading {
		t.Fatalf("expected intent status rolled back, got %s", got)
	}
	if got := store.accountLocked(alice.Addr).Nonce; got != 0 {
		t.Fatalf("expected account nonce rolled back, got %d", got)
	}
}

func TestApplyPendingTransactionsRollsBackRejectedCandidate(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	intent := testLifecycleIntentWithUser(alice.Addr)
	intent.Status = wire.StatusUploading
	intent.StorageStatus = wire.StorageStatusPending
	intent.DeadlineUnix = time.Now().Add(-time.Hour).Unix()
	store.data.Intents[intent.IntentID] = intent
	store.data.Accounts[alice.Addr] = wire.Account{Address: alice.Addr, LockedStorage: intent.LockedFee}
	store.createDealEscrowLocked(intent, intent.CreatedAt)

	req := wire.SettleIntentRequest{IntentID: intent.IntentID, User: alice.Addr}
	signSettleIntent(t, store, &req, alice)
	payload := settleIntentTxPayload{
		Request:       req,
		Response:      wire.SettleIntentResponse{IntentID: intent.IntentID, Status: wire.StatusFinalized},
		SettledAtUnix: time.Now().Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	applied, err := store.applyPendingTransactionsForBlockLocked([]wire.Transaction{
		{TxID: "tx_bad_settle", Type: "settle_intent", Payload: raw},
	}, "producer")
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("expected no applied txs, got %d", len(applied))
	}
	if got := store.data.Intents[intent.IntentID].Status; got != wire.StatusUploading {
		t.Fatalf("expected intent status rolled back, got %s", got)
	}
	if got := store.accountLocked(alice.Addr).Nonce; got != 0 {
		t.Fatalf("expected account nonce rolled back, got %d", got)
	}
}

func TestReplayCollectionRejectsTamperedPayloadBeforeNonceMutation(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(1))

	req := wire.CreateCollectionRequest{
		User: alice.Addr,
		Name: "photos",
	}
	signCreateCollection(t, store, &req, alice)
	payload := createCollectionTxPayload{
		Request: req,
		Collection: wire.DataCollection{
			CollectionID:  "collection_bad",
			User:          alice.Addr,
			Name:          "tampered",
			CreatedAtUnix: time.Now().Unix(),
			UpdatedAtUnix: time.Now().Unix(),
		},
		Nonce:     req.Nonce,
		PublicKey: req.PublicKey,
	}
	err = store.applyDataCollectionPayloadLocked(payload)
	if err == nil {
		t.Fatal("expected tampered collection payload to be rejected")
	}
	if got := store.accountLocked(alice.Addr).Nonce; got != 0 {
		t.Fatalf("expected nonce unchanged, got %d", got)
	}
	if _, ok := store.data.Collections[payload.Collection.CollectionID]; ok {
		t.Fatal("tampered collection was written")
	}
}
