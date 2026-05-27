package chain

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestRetrievalPoolIsReservedForGatewaySettlement(t *testing.T) {
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
	if got := store.data.Accounts["miner_a"].PendingMiningRewards; got != 0 {
		t.Fatalf("expected miner_a pending reward 0, got %d", got)
	}
	if got := store.data.Accounts["miner_b"].Balance; got != 0 {
		t.Fatalf("expected miner_b balance 0 before vesting release, got %d", got)
	}
	if got := store.data.Accounts["miner_b"].PendingMiningRewards; got != 0 {
		t.Fatalf("expected miner_b pending reward 0, got %d", got)
	}
	if got := store.data.RewardPools.RetrievalRemaining; got != wire.TokenRetrievalPoolInitial {
		t.Fatalf("expected retrieval pool to remain reserved, got %d", got)
	}
}

func TestDistributeValidatorRewardsSharesWithDelegators(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Validators["validator_a"] = wire.ValidatorInfo{
		OwnerAddress:      "validator_a",
		OperatorPublicKey: "pub",
		Stake:             gfTokens(10),
		SelfStake:         gfTokens(10),
		DelegatedStake:    gfTokens(10),
		Status:            wire.ValidatorStatusActive,
	}
	store.data.ConsensusValidators["validator_a"] = true
	store.data.StakeDelegations = map[string]wire.StakeDelegation{
		delegationKey("delegator_a", "validator_a"): {
			Delegator: "delegator_a",
			Validator: "validator_a",
			Amount:    gfTokens(10),
		},
	}

	store.distributeValidatorPoolRewardsLocked(100, time.Now().Unix())

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

func TestFoundationPoolDistributesDirectlyToAddress(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.FoundationAddress = "foundation_addr"

	store.distributeFoundationPoolRewardsLocked(1000)

	acct := store.data.Accounts["foundation_addr"]
	if acct.Balance != 1000 {
		t.Fatalf("expected foundation address balance 1000, got %d", acct.Balance)
	}
	// Foundation rewards should NOT go through vesting.
	if acct.PendingMiningRewards != 0 {
		t.Fatalf("expected no pending mining rewards for foundation, got %d", acct.PendingMiningRewards)
	}
}

func TestFoundationPoolReturnsWhenNoAddress(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.initRewardPoolsLocked()
	// Simulate a release that already decremented FoundationRemaining and incremented TokensReleased.
	store.data.RewardPools.FoundationRemaining = wire.TokenFoundationPoolInitial - 500
	store.data.RewardPools.TokensReleased = 500

	store.distributeFoundationPoolRewardsLocked(500)

	// Tokens should be returned to the pool since no address is set.
	if store.data.RewardPools.FoundationRemaining != wire.TokenFoundationPoolInitial {
		t.Fatalf("expected foundation pool restored to %d, got %d",
			wire.TokenFoundationPoolInitial, store.data.RewardPools.FoundationRemaining)
	}
	if store.data.RewardPools.TokensReleased != 0 {
		t.Fatalf("expected tokens released reset to 0, got %d", store.data.RewardPools.TokensReleased)
	}
}
