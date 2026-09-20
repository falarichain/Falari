package chain

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"chain/internal/wire"
)

// The tests here pin the invariant behind runBlockHousekeepingLocked: miner and
// validator status transitions feed the StateRoot, so they must happen once per block,
// at the same relative point, on every node. A node that performs them at a node-local
// moment (an RPC poll, a scheduler tick, wall-clock maths) publishes a root its peers
// cannot reproduce.

func testStateRoot(t *testing.T, store *Store) string {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.fullStateRootLocked()
}

// followerStoreFrom returns a store holding a copy of the producer's committed state,
// which is what a peer node has after booting from the same database. It carries no
// operator identity, so it only ever advances by replaying blocks.
func followerStoreFrom(t *testing.T, producer *Store) *Store {
	t.Helper()
	producer.mu.Lock()
	cloned, err := cloneStateForRollback(producer.data)
	producer.mu.Unlock()
	if err != nil {
		t.Fatalf("clone producer state: %v", err)
	}
	follower, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = follower.Close() })
	follower.data = cloned
	return follower
}

// pendingTransitions names the seeded accounts whose status a block is expected to move.
type pendingTransitions struct {
	exitingMiner     string
	inactiveMiner    string
	exitingValidator string
	maturedDelegator string
}

// seedPendingTransitions queues one of each per-block transition: a miner past its exit
// deadline, a miner whose activation window lapsed, a slashed validator, and a matured
// unbonding entry. Every one of them rewrites Accounts, Miners or Validators.
func seedPendingTransitions(t *testing.T, store *Store) pendingTransitions {
	t.Helper()
	now := time.Now().Unix()
	out := pendingTransitions{
		exitingMiner:     "miner_exiting",
		inactiveMiner:    "miner_inactive",
		exitingValidator: "0x00000000000000000000000000000000000000b1",
		maturedDelegator: "unbond_user",
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.initRewardPoolsLocked()
	params := store.miningParamsLocked()

	stake := uint64(40) * wire.TokenUnit
	store.data.Accounts[out.exitingMiner] = wire.Account{
		Address:     out.exitingMiner,
		Balance:     wire.TokenUnit,
		LockedStake: stake,
	}
	store.data.Miners[out.exitingMiner] = wire.MinerStats{
		MinerAddress:     out.exitingMiner,
		Status:           wire.MinerStatusExiting,
		ExitedAtUnix:     now - 60,
		Stake:            stake,
		RegisteredAtUnix: now - 10*86400,
	}

	store.data.Accounts[out.inactiveMiner] = wire.Account{
		Address:     out.inactiveMiner,
		LockedBonus: params.RegistrationBonusAmount,
	}
	store.data.Miners[out.inactiveMiner] = wire.MinerStats{
		MinerAddress:     out.inactiveMiner,
		Status:           wire.MinerStatusActive,
		RegisteredAtUnix: now - int64(params.ActivationWindowSeconds) - 86400,
	}
	store.data.BonusGrantedCount++

	// Slashed validators move to exiting in one block and release their self-stake in
	// the next, so the seeded entry spans two heights.
	store.data.Validators[out.exitingValidator] = wire.ValidatorInfo{
		OwnerAddress:      out.exitingValidator,
		OperatorAddress:   out.exitingValidator,
		OperatorPublicKey: "0x" + strings.Repeat("11", 33),
		Status:            wire.ValidatorStatusSlashed,
		Stake:             uint64(20) * wire.TokenUnit,
		SelfStake:         uint64(20) * wire.TokenUnit,
	}
	store.data.Accounts[out.exitingValidator] = wire.Account{
		Address:     out.exitingValidator,
		LockedStake: uint64(20) * wire.TokenUnit,
	}

	store.data.Accounts[out.maturedDelegator] = wire.Account{
		Address:          out.maturedDelegator,
		UnbondingBalance: 7 * wire.TokenUnit,
	}
	if store.data.UnbondingEntries == nil {
		store.data.UnbondingEntries = map[string]wire.UnbondingEntry{}
	}
	store.data.UnbondingEntries["unbond_matured"] = wire.UnbondingEntry{
		ID:            "unbond_matured",
		Delegator:     out.maturedDelegator,
		Validator:     out.exitingValidator,
		Amount:        7 * wire.TokenUnit,
		CreatedAtUnix: now - 8*86400,
		MaturesAtUnix: now - 60,
	}
	return out
}

func TestReadAPIsLeaveConsensusStateUntouched(t *testing.T) {
	store, identity := registeredTestValidator(t, MinValidatorStake)
	store.SetOperatorIdentity(identity)
	seeded := seedPendingTransitions(t, store)

	before := testStateRoot(t, store)
	store.Status()
	if got := testStateRoot(t, store); got != before {
		t.Fatalf("Status() changed the StateRoot: %s -> %s", before, got)
	}
	store.Validators()
	if got := testStateRoot(t, store); got != before {
		t.Fatalf("Validators() changed the StateRoot: %s -> %s", before, got)
	}
	if _, err := store.MinerStats(seeded.exitingMiner); err != nil {
		t.Fatal(err)
	}
	if got := testStateRoot(t, store); got != before {
		t.Fatalf("MinerStats() changed the StateRoot: %s -> %s", before, got)
	}

	// The queries must stay read-only in the detail, not just in the root: had any of
	// them still run housekeeping, the exiting miner would already be exited.
	store.mu.Lock()
	miner := store.data.Miners[seeded.exitingMiner]
	unbonding := len(store.data.UnbondingEntries)
	store.mu.Unlock()
	if miner.Status != wire.MinerStatusExiting {
		t.Fatalf("a read API finalized a miner: %s", miner.Status)
	}
	if unbonding != 1 {
		t.Fatalf("a read API settled unbonding entries: %d left", unbonding)
	}
}

// TestNodeLocalReadsDoNotForkTheNextBlock is the regression for the read-path triggers:
// one node serves status queries while its peer does not, then both must still agree on
// the next block's StateRoot. The follower accepting a second block is what proves the
// two nodes held identical state after the first one.
func TestNodeLocalReadsDoNotForkTheNextBlock(t *testing.T) {
	producer, identity := registeredTestValidator(t, MinValidatorStake)
	producer.SetOperatorIdentity(identity)
	seeded := seedPendingTransitions(t, producer)

	follower := followerStoreFrom(t, producer)
	if testStateRoot(t, follower) != testStateRoot(t, producer) {
		t.Fatal("follower did not start from the producer's state")
	}

	producer.Status()
	producer.Validators()
	if _, err := producer.MinerStats(seeded.exitingMiner); err != nil {
		t.Fatal(err)
	}

	for height := uint64(1); height <= 2; height++ {
		produced, err := producer.ProduceBlock()
		if err != nil {
			t.Fatal(err)
		}
		if !produced.Produced {
			t.Fatalf("expected block at height %d", height)
		}
		accepted, err := follower.AcceptBlock(produced.Block)
		if err != nil {
			t.Fatalf("follower rejected honest block %d: %v", height, err)
		}
		if !accepted {
			t.Fatalf("follower ignored block %d", height)
		}
		if got, want := testStateRoot(t, follower), testStateRoot(t, producer); got != want {
			t.Fatalf("nodes diverged after block %d: follower %s, producer %s", height, got, want)
		}
	}

	// The transitions have to have actually happened, or the agreement above is vacuous.
	producer.mu.Lock()
	defer producer.mu.Unlock()
	if got := producer.data.Miners[seeded.exitingMiner].Status; got != wire.MinerStatusExited {
		t.Fatalf("exiting miner reached %s", got)
	}
	if got := producer.data.Miners[seeded.inactiveMiner].Status; got != wire.MinerStatusExiting {
		t.Fatalf("inactive miner reached %s", got)
	}
	if got := producer.data.Validators[seeded.exitingValidator].Status; got != wire.ValidatorStatusExited {
		t.Fatalf("slashed validator reached %s", got)
	}
	if got := producer.accountLocked(seeded.maturedDelegator).Balance; got != 7*wire.TokenUnit {
		t.Fatalf("matured unbonding credited %d", got)
	}
	if _, stillLocked := producer.data.UnbondingEntries["unbond_matured"]; stillLocked {
		t.Fatal("matured unbonding entry survived the block")
	}
}

// TestEpochAutoExitDeadlineMatchesOnReplay covers the depleted-stake exit: the recording
// node used to stamp wall-clock time while replay derived the deadline from the epoch, so
// the two nodes exited the same miner at different heights.
func TestEpochAutoExitDeadlineMatchesOnReplay(t *testing.T) {
	producer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = producer.Close() })
	identity := testOperatorIdentity(t)
	registration, err := identity.RegistrationRequest(producer.ChainID(), producer.AccountNonce(identity.OwnerAddress), "http://validator-a", MinValidatorStake, 0)
	if err != nil {
		t.Fatal(err)
	}
	fundValidatorForTest(t, producer, identity, MinValidatorStake)
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetOperatorIdentity(identity)
	testRegisterEpochOperator(producer, identity.OperatorAddress)

	seedFinalizedDealForEpochTest(producer)
	// Slashing the whole stake pushes the miner into the auto-exit branch.
	stake := producer.data.Miners["miner_epoch"].Stake
	startResp, err := producer.StartEpoch(wire.StartEpochRequest{
		IntentID:            "intent_epoch",
		ChallengesPerDeal:   1,
		DurationSeconds:     600,
		RewardPerProof:      wire.TokenUnit,
		SlashPerMissedProof: stake + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.FinalizeEpoch(wire.FinalizeEpochRequest{EpochID: startResp.Epoch.EpochID}); err != nil {
		t.Fatal(err)
	}

	follower := followerStoreFrom(t, producer)
	produced, err := producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected the finalize block")
	}
	if accepted, err := follower.AcceptBlock(produced.Block); err != nil || !accepted {
		t.Fatalf("follower rejected the epoch block: accepted=%t err=%v", accepted, err)
	}

	wantExit := startResp.Epoch.DeadlineUnix + wire.UnbondingPeriodSeconds
	producer.mu.Lock()
	producerMiner := producer.data.Miners["miner_epoch"]
	producer.mu.Unlock()
	follower.mu.Lock()
	followerMiner := follower.data.Miners["miner_epoch"]
	follower.mu.Unlock()

	if producerMiner.Status != wire.MinerStatusExiting {
		t.Fatalf("expected the depleted miner to be exiting, got %s", producerMiner.Status)
	}
	if producerMiner.ExitedAtUnix != wantExit {
		t.Fatalf("producer stamped exit %d, epoch deadline gives %d", producerMiner.ExitedAtUnix, wantExit)
	}
	if !reflect.DeepEqual(producerMiner, followerMiner) {
		t.Fatalf("replay settled a different exit for the same epoch:\n producer %+v\n follower %+v", producerMiner, followerMiner)
	}
}

// TestFinalizeExpiredEpochsSettlesInRoundOrder pins the settle order: each finalize
// consumes the next operator nonce, so iterating the epoch map in Go's random order
// made the nonce-to-epoch pairing unreproducible for replaying peers.
func TestFinalizeExpiredEpochsSettlesInRoundOrder(t *testing.T) {
	store := testEpochStore(t)
	testEpochOperatorIdentity(t, store)

	now := time.Now().Unix()
	rounds := map[string]uint64{}
	// Seeded out of order so a map iteration cannot accidentally look like a sort.
	for i, round := range []uint64{4, 1, 5, 2, 3} {
		epochID := "epoch_" + string(rune('a'+i))
		rounds[epochID] = round
		store.mu.Lock()
		store.data.Epochs[epochID] = wire.ProofEpoch{
			EpochID:             epochID,
			EpochRound:          round,
			StartedAtUnix:       now - 7200,
			DeadlineUnix:        now - 3600,
			Status:              "active",
			RewardPerProof:      wire.TokenUnit,
			SlashPerMissedProof: 1,
		}
		store.mu.Unlock()
	}

	responses, err := store.FinalizeExpiredEpochs()
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != len(rounds) {
		t.Fatalf("expected %d finalized epochs, got %d", len(rounds), len(responses))
	}
	var settled []uint64
	for _, resp := range responses {
		settled = append(settled, rounds[resp.EpochID])
	}
	for i := 1; i < len(settled); i++ {
		if settled[i-1] >= settled[i] {
			t.Fatalf("epochs settled out of round order: %v", settled)
		}
	}
}

func TestAgentKeyQuotaRollsOverFromBlockClock(t *testing.T) {
	const spend = uint64(5)
	// Two spends on either side of a UTC midnight, at a wall-clock-independent base.
	dayOne := time.Date(2026, time.September, 15, 23, 0, 0, 0, time.UTC).Unix()
	dayTwo := time.Date(2026, time.September, 16, 0, 30, 0, 0, time.UTC).Unix()

	seedLister := func(t *testing.T) *Store {
		t.Helper()
		store, err := OpenStore("")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		key := wire.AgentKey{
			KeyID:      "agent_key_1",
			DailyLimit: 100,
			DayResetAt: startOfNextDayAt(dayOne),
		}
		store.mu.Lock()
		store.data.AgentKeys[key.KeyID] = &key
		store.mu.Unlock()
		return store
	}

	producer := seedLister(t)
	follower := seedLister(t)

	consume := func(store *Store, blockTime int64) {
		store.mu.Lock()
		defer store.mu.Unlock()
		store.withBlockTimeLocked(blockTime, func() {
			if err := store.consumeAgentRequestLocked("agent_key_1", spend); err != nil {
				t.Fatal(err)
			}
		})
	}
	replay := func(store *Store, blockTime int64) {
		store.mu.Lock()
		defer store.mu.Unlock()
		store.withBlockTimeLocked(blockTime, func() {
			store.replayAgentKeyMutationLocked("agent_key_1", spend)
		})
	}

	consume(producer, dayOne)
	replay(follower, dayOne)
	consume(producer, dayTwo)
	replay(follower, dayTwo)

	producer.mu.Lock()
	producerKey := *producer.data.AgentKeys["agent_key_1"]
	producer.mu.Unlock()
	follower.mu.Lock()
	followerKey := *follower.data.AgentKeys["agent_key_1"]
	follower.mu.Unlock()

	if producerKey.UsedToday != spend {
		t.Fatalf("the second day should reopen the quota, used today %d", producerKey.UsedToday)
	}
	if producerKey.UsedTotal != 2*spend {
		t.Fatalf("unexpected total usage %d", producerKey.UsedTotal)
	}
	if want := startOfNextDayAt(dayTwo); producerKey.DayResetAt != want {
		t.Fatalf("quota window reset at %d, block clock gives %d", producerKey.DayResetAt, want)
	}
	if !reflect.DeepEqual(producerKey, followerKey) {
		t.Fatalf("replay drifted from the producing node:\n producer %+v\n follower %+v", producerKey, followerKey)
	}
}
