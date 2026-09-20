package chain

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"chain/internal/reward"
	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// replayReplicaStore returns an empty store that shares the operator set and
// intents of store, standing in for a second node replaying the block.
func replayReplicaStore(t *testing.T, store *Store) *Store {
	t.Helper()
	replica, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replica.Close() })
	replica.data.ChainID = store.data.ChainID
	for addr, op := range store.data.GovernanceOperators {
		replica.data.GovernanceOperators[addr] = op
	}
	for id, intent := range store.data.Intents {
		replica.data.Intents[id] = intent
	}
	for id, deal := range store.data.Deals {
		replica.data.Deals[id] = deal
	}
	return replica
}

func lastGovernanceCreateTx(t *testing.T, store *Store) governanceCreateProposalTxPayload {
	t.Helper()
	for i := len(store.data.PendingTxs) - 1; i >= 0; i-- {
		tx := store.data.PendingTxs[i]
		if tx.Type != "governance_create_proposal" {
			continue
		}
		var payload governanceCreateProposalTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			t.Fatalf("decode recorded governance_create_proposal: %v", err)
		}
		return payload
	}
	t.Fatal("no governance_create_proposal recorded")
	return governanceCreateProposalTxPayload{}
}

// TestReplayGovernanceCreateAcceptsHonestProposal covers three defects that each
// made a valid proposal fail replay on every other node: the operator snapshot
// was only set on the creating node, update_fee_market had no replay branch, and
// the activation window target was dropped when the proposal was built.
func TestReplayGovernanceCreateAcceptsHonestProposal(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	cases := []struct {
		name     string
		action   string
		intentID string
		tune     func(*wire.CreateGovernanceProposalRequest)
	}{
		{
			name:     "deal action",
			action:   "freeze",
			intentID: "intent_lifecycle",
		},
		{
			name:   "fee market",
			action: "update_fee_market",
			tune:   func(req *wire.CreateGovernanceProposalRequest) { req.TargetFeeMarketBaseFee = 5 * reward.TokenUnit },
		},
		{
			name:   "activation window",
			action: "update_mining_params",
			tune: func(req *wire.CreateGovernanceProposalRequest) {
				req.TargetActivationWindowSeconds = 3 * 24 * 60 * 60
			},
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			address, privKey := addresses[i], privKeys[i]
			req := wire.CreateGovernanceProposalRequest{
				Proposer:      address,
				ChainID:       store.data.ChainID,
				IntentID:      tc.intentID,
				Action:        tc.action,
				ReasonHash:    "reason_hash_test",
				ExpiresAtUnix: time.Now().Add(48 * time.Hour).Unix(),
				Nonce:         store.data.OperatorNonces[normalizeGovernanceOperator(address)],
				CreatedAtUnix: time.Now().Unix(),
			}
			if tc.tune != nil {
				tc.tune(&req)
			}
			if err := wire.SignGovernanceProposal(&req, privKey); err != nil {
				t.Fatalf("sign proposal: %v", err)
			}

			created, err := store.CreateGovernanceProposal(req)
			if err != nil {
				t.Fatalf("create proposal on originating node: %v", err)
			}

			replica := replayReplicaStore(t, store)
			payload := lastGovernanceCreateTx(t, store)
			if err := replica.applyGovernanceCreateProposalLocked(payload); err != nil {
				t.Fatalf("replay rejected an honest proposal: %v", err)
			}

			proposalID := created.Proposal.ProposalID
			stored, ok := replica.data.GovernanceProposals[proposalID]
			if !ok {
				t.Fatal("proposal not written on replica")
			}
			if !reflect.DeepEqual(stored, created.Proposal) {
				t.Fatalf("replica proposal differs from originating node:\n got %+v\nwant %+v", stored, created.Proposal)
			}
			if want := uint64(3 * 24 * 60 * 60); tc.name == "activation window" && stored.TargetActivationWindowSeconds != want {
				t.Fatalf("activation window target lost: got %d want %d", stored.TargetActivationWindowSeconds, want)
			}
			if len(stored.EnabledOperatorsSnapshot) != len(store.data.GovernanceOperators) {
				t.Fatalf("snapshot size %d, operators %d", len(stored.EnabledOperatorsSnapshot), len(store.data.GovernanceOperators))
			}
		})
	}
}

// TestReplayGovernanceVoteAfterOperatorJoin replays create+vote transactions into
// a second node whose operator set gained a member between the two votes. The
// local vote path derives the quorum from the proposal's operator snapshot, while
// replay used to derive it from the live set, so the replaying node reported a
// different Threshold than the vote it was replaying and rejected the block.
func TestReplayGovernanceVoteAfterOperatorJoin(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	joinedPriv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	joined := wire.AccountAddress(&joinedPriv.PublicKey)
	joinedOperator := wire.GovernanceOperator{
		Operator:    joined,
		PublicKey:   testEncodeHex(ethcrypto.FromECDSAPub(&joinedPriv.PublicKey)),
		Permissions: []string{"all"},
		Enabled:     true,
	}

	// update_mining_params is a config action, so its quorum is 2/3: two votes are
	// needed and the proposal stays pending after the first one.
	createReq := wire.CreateGovernanceProposalRequest{
		Proposer:                      addresses[0],
		ChainID:                       store.data.ChainID,
		Action:                        "update_mining_params",
		ReasonHash:                    "reason_hash_test",
		ExpiresAtUnix:                 time.Now().Add(48 * time.Hour).Unix(),
		Nonce:                         store.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])],
		CreatedAtUnix:                 time.Now().Unix(),
		TargetActivationWindowSeconds: 3 * 24 * 60 * 60,
	}
	if err := wire.SignGovernanceProposal(&createReq, privKeys[0]); err != nil {
		t.Fatalf("sign proposal: %v", err)
	}
	if _, err := store.CreateGovernanceProposal(createReq); err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	proposalID := lastGovernanceCreateTx(t, store).Response.Proposal.ProposalID

	// Captured while three operators exist, so the replica mirrors the operator set
	// the create transaction was produced against.
	replica := replayReplicaStore(t, store)

	if _, err := store.CastGovernanceVote(testGovernanceVoteReq(t, store, proposalID, addresses[0], true, privKeys[0])); err != nil {
		t.Fatalf("cast first vote: %v", err)
	}

	// A fourth operator joins after the proposal was created. Its vote is accepted
	// but excluded from the quorum, which stays pinned at three operators.
	store.data.GovernanceOperators[joined] = joinedOperator
	if _, err := store.CastGovernanceVote(testGovernanceVoteReq(t, store, proposalID, joined, true, joinedPriv)); err != nil {
		t.Fatalf("cast vote from late joiner: %v", err)
	}
	if store.data.GovernanceProposals[proposalID].Status != wire.GovProposalPending {
		t.Fatal("proposal should still be pending")
	}

	votes := governanceVotePayloads(t, store)
	if len(votes) != 2 {
		t.Fatalf("expected 2 recorded votes, got %d", len(votes))
	}

	// The replica applies the transactions in block order: create and the first
	// vote while three operators exist, then the join lands, then the second vote.
	if err := replica.applyGovernanceCreateProposalLocked(lastGovernanceCreateTx(t, store)); err != nil {
		t.Fatalf("replay create: %v", err)
	}
	if err := replica.applyGovernanceCastVoteLocked(votes[0]); err != nil {
		t.Fatalf("replay first vote: %v", err)
	}
	replica.data.GovernanceOperators[joined] = joinedOperator
	if err := replica.applyGovernanceCastVoteLocked(votes[1]); err != nil {
		t.Fatalf("replay vote from late joiner: %v", err)
	}

	stored, ok := replica.data.GovernanceProposals[proposalID]
	if !ok {
		t.Fatal("proposal missing on replica")
	}
	if len(stored.EnabledOperatorsSnapshot) != 3 {
		t.Fatalf("snapshot should stay pinned at creation: %v", stored.EnabledOperatorsSnapshot)
	}
	if got := replica.data.GovernanceVotes[proposalID]; len(got) != 2 {
		t.Fatalf("expected both votes replayed, got %d", len(got))
	}
	if replica.data.OperatorNonces[normalizeGovernanceOperator(addresses[0])] != 2 {
		t.Fatal("replica did not consume the replayed nonces")
	}
}

func governanceVotePayloads(t *testing.T, store *Store) []governanceCastVoteTxPayload {
	t.Helper()
	var out []governanceCastVoteTxPayload
	for _, tx := range store.data.PendingTxs {
		if tx.Type != "governance_cast_vote" {
			continue
		}
		var payload governanceCastVoteTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			t.Fatalf("decode recorded governance_cast_vote: %v", err)
		}
		out = append(out, payload)
	}
	if len(out) == 0 {
		t.Fatal("no governance_cast_vote recorded")
	}
	return out
}

// TestReplayGovernanceCreateIgnoresProducerSnapshotTampering pins the guarantee
// that a producer cannot shrink a quorum by publishing a smaller operator
// snapshot: the replica recomputes it from local state.
func TestReplayGovernanceCreateIgnoresProducerSnapshotTampering(t *testing.T) {
	store, privKeys, addresses := testGovernanceSetup(t)

	req := wire.CreateGovernanceProposalRequest{
		Proposer:      addresses[0],
		ChainID:       store.data.ChainID,
		IntentID:      "intent_lifecycle",
		Action:        "freeze",
		ReasonHash:    "reason_hash_test",
		ExpiresAtUnix: time.Now().Add(48 * time.Hour).Unix(),
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := wire.SignGovernanceProposal(&req, privKeys[0]); err != nil {
		t.Fatalf("sign proposal: %v", err)
	}
	if _, err := store.CreateGovernanceProposal(req); err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	payload := lastGovernanceCreateTx(t, store)
	payload.Response.Proposal.EnabledOperatorsSnapshot = []string{addresses[0]}

	replica := replayReplicaStore(t, store)
	if err := replica.applyGovernanceCreateProposalLocked(payload); err != nil {
		t.Fatalf("replay rejected honest proposal over shrunk snapshot: %v", err)
	}
	stored := replica.data.GovernanceProposals[payload.Response.Proposal.ProposalID]
	if len(stored.EnabledOperatorsSnapshot) != len(store.data.GovernanceOperators) {
		t.Fatalf("producer snapshot was trusted: %v", stored.EnabledOperatorsSnapshot)
	}
}
