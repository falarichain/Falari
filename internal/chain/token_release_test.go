package chain

import (
	"testing"
	"time"

	"chain/internal/reward"
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

func TestStoragePerBlockReleaseAccruesEstimatedRewards(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.initRewardPoolsLocked()
	store.data.Miners["miner_a"] = wire.MinerStats{
		MinerAddress:       "miner_a",
		Status:             wire.MinerStatusActive,
		UsedBytes:          1,
		EffectiveWeight:    1,
		StorageRewardIndex: store.data.StorageRewardIndex,
	}
	now := int64(1_700_000_000)
	store.releaseStoragePerBlockLocked(now)

	expected := uint64(50) * reward.TokenUnit // default StorageRewardPerBlock
	if got := store.data.Accounts["miner_a"].PendingMiningRewards; got != 0 {
		t.Fatalf("expected no direct pending mining rewards before proof settlement, got %d", got)
	}
	stats, err := store.MinerStats("miner_a")
	if err != nil {
		t.Fatal(err)
	}
	if got := stats.UnsettledStorageRewards; got != expected {
		t.Fatalf("expected unsettled storage reward %d, got %d", expected, got)
	}
	if store.data.RewardPools.StorageRemaining != reward.StoragePoolInitial-expected {
		t.Fatalf("expected storage pool remaining %d, got %d",
			reward.StoragePoolInitial-expected, store.data.RewardPools.StorageRemaining)
	}

	settled := store.settleStorageRewardForMinerLocked("miner_a", now+60)
	if settled != expected {
		t.Fatalf("expected settled storage reward %d, got %d", expected, settled)
	}
	if got := store.data.Accounts["miner_a"].PendingMiningRewards; got != expected {
		t.Fatalf("expected settled pending mining rewards %d, got %d", expected, got)
	}
}

func TestFoundationPerBlockReleaseReleasesFixedAmount(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.initRewardPoolsLocked()
	store.data.FoundationAddress = "foundation_addr"
	now := int64(1_700_000_000)

	store.releaseFoundationPerBlockLocked(now)

	expected := uint64(16) * reward.TokenUnit // default FoundationRewardPerBlock
	if store.data.RewardPools.FoundationRemaining != reward.FoundationPoolInitial-expected {
		t.Fatalf("expected foundation pool remaining %d, got %d",
			reward.FoundationPoolInitial-expected, store.data.RewardPools.FoundationRemaining)
	}
	if got := store.data.Accounts["foundation_addr"].Balance; got != expected {
		t.Fatalf("expected foundation address balance %d, got %d", expected, got)
	}
}

func TestRetrievalPerBlockReleaseReleasesFixedAmount(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.initRewardPoolsLocked()
	store.data.RetrievalAddress = "retrieval_addr"
	now := int64(1_700_000_000)

	store.releaseRetrievalPerBlockLocked(now)

	expected := uint64(10) * reward.TokenUnit // default RetrievalRewardPerBlock
	if store.data.RewardPools.RetrievalRemaining != reward.RetrievalPoolInitial-expected {
		t.Fatalf("expected retrieval pool remaining %d, got %d",
			reward.RetrievalPoolInitial-expected, store.data.RewardPools.RetrievalRemaining)
	}
	if got := store.data.Accounts["retrieval_addr"].Balance; got != expected {
		t.Fatalf("expected retrieval address balance %d, got %d", expected, got)
	}
}

func TestFoundationPerBlockReleaseCapsAtPoolRemaining(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.initRewardPoolsLocked()
	store.data.FoundationAddress = "foundation_addr"
	store.data.RewardPools.FoundationRemaining = 100 // nearly depleted
	now := int64(1_700_000_000)

	store.releaseFoundationPerBlockLocked(now)

	if store.data.RewardPools.FoundationRemaining != 0 {
		t.Fatalf("expected foundation pool fully depleted, got %d", store.data.RewardPools.FoundationRemaining)
	}
	if got := store.data.Accounts["foundation_addr"].Balance; got != 100 {
		t.Fatalf("expected foundation address balance 100, got %d", got)
	}
}

func TestRetrievalPerBlockReleaseCapsAtPoolRemaining(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.initRewardPoolsLocked()
	store.data.RetrievalAddress = "retrieval_addr"
	store.data.RewardPools.RetrievalRemaining = 50 // nearly depleted
	now := int64(1_700_000_000)

	store.releaseRetrievalPerBlockLocked(now)

	if store.data.RewardPools.RetrievalRemaining != 0 {
		t.Fatalf("expected retrieval pool fully depleted, got %d", store.data.RewardPools.RetrievalRemaining)
	}
	if got := store.data.Accounts["retrieval_addr"].Balance; got != 50 {
		t.Fatalf("expected retrieval address balance 50, got %d", got)
	}
}
