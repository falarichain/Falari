package chain

import (
	"testing"

	"chain/internal/wire"
)

func TestAvailabilityScoreNewValidatorGetsFullScore(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	score := store.availabilityScoreLocked("unknown_validator")
	if score != 10000 {
		t.Fatalf("expected new validator score 10000, got %d", score)
	}
}

func TestRecordProposerTurnAndScore(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		store.recordProposerTurnLocked("val_a", true)
	}
	for i := 0; i < 3; i++ {
		store.recordProposerTurnLocked("val_a", false)
	}

	score := store.availabilityScoreLocked("val_a")
	if score != 7000 {
		t.Fatalf("expected score 7000, got %d", score)
	}

	window := store.data.ProposerTurns["val_a"]
	if window.Successes != 7 {
		t.Fatalf("expected 7 successes, got %d", window.Successes)
	}
	if window.Misses != 3 {
		t.Fatalf("expected 3 misses, got %d", window.Misses)
	}
}

func TestRecordProposerTurnRingBufferWraps(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	params := store.miningParamsLocked()
	params.AvailabilityWindowSize = 5

	store.recordProposerTurnLocked("val_a", true)
	store.recordProposerTurnLocked("val_a", true)
	store.recordProposerTurnLocked("val_a", true)
	store.recordProposerTurnLocked("val_a", false)
	store.recordProposerTurnLocked("val_a", false)

	window := store.data.ProposerTurns["val_a"]
	if window.Successes != 3 || window.Misses != 2 {
		t.Fatalf("expected 3/2, got %d/%d", window.Successes, window.Misses)
	}

	// Overwrite oldest (true) with a miss.
	store.recordProposerTurnLocked("val_a", false)
	score := store.availabilityScoreLocked("val_a")
	if score != 4000 {
		t.Fatalf("expected score 4000 after wrap, got %d", score)
	}
	if window.Successes != 2 || window.Misses != 3 {
		t.Fatalf("expected 2/3 after wrap, got %d/%d", window.Successes, window.Misses)
	}

	// Overwrite oldest (true) with a success.
	store.recordProposerTurnLocked("val_a", true)
	if window.Successes+window.Misses != 5 {
		t.Fatalf("expected total 5, got %d", window.Successes+window.Misses)
	}
}

func TestEffectivePowerWithAvailability(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Validators["val_a"] = wire.ValidatorInfo{
		OwnerAddress:      "val_a",
		OperatorPublicKey: "pub_a",
		SelfStake:         1000,
		Status:            wire.ValidatorStatusActive,
	}

	effPower := store.effectivePowerLocked("val_a")
	rawPower := store.validatorPowerLocked("val_a")
	if effPower != rawPower {
		t.Fatalf("expected effective power %d (full score), got %d", rawPower, effPower)
	}

	// Record 50% availability.
	for i := 0; i < 5; i++ {
		store.recordProposerTurnLocked("val_a", true)
	}
	for i := 0; i < 5; i++ {
		store.recordProposerTurnLocked("val_a", false)
	}

	effPower = store.effectivePowerLocked("val_a")
	expected := rawPower * 5000 / 10000
	if effPower != expected {
		t.Fatalf("expected effective power %d (50%% score), got %d", expected, effPower)
	}
}

func TestEffectivePowerZeroForUnknownValidator(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	if got := store.effectivePowerLocked("nonexistent"); got != 0 {
		t.Fatalf("expected 0 effective power for unknown validator, got %d", got)
	}
}

func TestBlockProductionRewardSplit(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.initRewardPoolsLocked()

	store.data.Validators["producer"] = wire.ValidatorInfo{
		OwnerAddress:      "producer",
		OperatorPublicKey: "pub",
		SelfStake:         100,
		Status:            wire.ValidatorStatusActive,
	}
	store.data.ConsensusValidators["producer"] = true

	// Pre-set last release so the first call actually computes elapsed time.
	// Use 1 day elapsed to ensure integer math produces non-zero release.
	store.data.LastValidatorReleaseAtUnix = 1000 - 86400

	poolBefore := store.data.RewardPools.ValidatorRemaining

	store.releaseValidatorPerBlockLocked(1000, "producer")

	producerBal := store.data.Accounts["producer"].Balance
	if producerBal == 0 {
		t.Fatalf("expected producer to receive block reward, got 0")
	}

	poolAfter := store.data.RewardPools.ValidatorRemaining
	released := poolBefore - poolAfter
	if released == 0 {
		t.Fatalf("expected validator pool to release tokens, got 0")
	}

	expectedBlockReward := released * 3000 / 10000
	if producerBal != expectedBlockReward {
		t.Fatalf("expected block reward %d (30%%), got %d", expectedBlockReward, producerBal)
	}

	// Producer is also the only consensus validator, so it receives the 70%
	// staking portion via vesting (PendingMiningRewards).
	expectedStakingReward := released - expectedBlockReward
	pending := store.data.Accounts["producer"].PendingMiningRewards
	if pending != expectedStakingReward {
		t.Fatalf("expected pending staking reward %d, got %d", expectedStakingReward, pending)
	}
}
