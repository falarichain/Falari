package chain

import (
	"testing"

	"chain/internal/wire"
)

func TestDistributeRetrievalPoolRewardsByServiceWeight(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Miners["miner_a"] = wire.MinerStats{MinerAddress: "miner_a", Status: wire.MinerStatusActive, RetrievalBytes: 100, AntiSpamScore: 10000, SpeedScore: 10000}
	store.data.Miners["miner_b"] = wire.MinerStats{MinerAddress: "miner_b", Status: wire.MinerStatusActive, RetrievalBytes: 300, AntiSpamScore: 10000, SpeedScore: 10000}

	store.distributeRetrievalPoolRewardsLocked(40)

	if got := store.data.Accounts["miner_a"].Balance; got != 0 {
		t.Fatalf("expected miner_a balance 0 before vesting release, got %d", got)
	}
	if got := store.data.Accounts["miner_a"].PendingMiningRewards; got != 10 {
		t.Fatalf("expected miner_a pending reward 10, got %d", got)
	}
	if got := store.data.Accounts["miner_b"].Balance; got != 0 {
		t.Fatalf("expected miner_b balance 0 before vesting release, got %d", got)
	}
	if got := store.data.Accounts["miner_b"].PendingMiningRewards; got != 30 {
		t.Fatalf("expected miner_b pending reward 30, got %d", got)
	}
}

func TestDistributeValidatorRewardsSharesWithDelegators(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Validators["validator_a"] = wire.ValidatorInfo{
		Address:        "validator_a",
		PublicKey:      "pub",
		Stake:          10,
		SelfStake:      10,
		DelegatedStake: 10,
		Status:         wire.ValidatorStatusActive,
	}
	store.data.ConsensusValidators["validator_a"] = true
	store.data.StakeDelegations = map[string]wire.StakeDelegation{
		delegationKey("delegator_a", "validator_a"): {
			Delegator: "delegator_a",
			Validator: "validator_a",
			Amount:    10,
		},
	}

	store.distributeValidatorPoolRewardsLocked(100)

	if got := store.data.Accounts["validator_a"].Balance; got != 0 {
		t.Fatalf("expected validator balance 0 before vesting release, got %d", got)
	}
	if got := store.data.Accounts["validator_a"].PendingMiningRewards; got != 60 {
		t.Fatalf("expected validator pending reward 60 including commission, got %d", got)
	}
	if got := store.data.Accounts["delegator_a"].Balance; got != 0 {
		t.Fatalf("expected delegator balance 0 before vesting release, got %d", got)
	}
	if got := store.data.Accounts["delegator_a"].PendingMiningRewards; got != 40 {
		t.Fatalf("expected delegator pending reward 40, got %d", got)
	}
	validator := store.data.Validators["validator_a"]
	if validator.Rewards != 60 || validator.DelegationRewards != 40 {
		t.Fatalf("unexpected validator reward accounting: %+v", validator)
	}
}
