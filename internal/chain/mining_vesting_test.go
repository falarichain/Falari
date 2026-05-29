package chain

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestMiningRewardVestingAggregatesByDayAndReleasesLinearly(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	now := int64(1_700_000_000)
	day := miningRewardVestingDayStart(now)

	store.vestMiningRewardLocked("miner_a", 90, miningRewardSourceStorageProof, now)
	store.vestMiningRewardLocked("miner_a", 180, miningRewardSourceRetrievalPool, now+3600)

	if len(store.data.MiningRewardVestings) != 1 {
		t.Fatalf("expected one daily bucket, got %d", len(store.data.MiningRewardVestings))
	}
	account := store.accountLocked("miner_a")
	if account.Balance != 0 || account.PendingMiningRewards != 270 {
		t.Fatalf("unexpected account after vesting: %+v", account)
	}
	pending, vesting, claimable := store.miningRewardVestingSummaryLocked("miner_a", day+miningRewardVestingDaySeconds)
	if pending != 270 || vesting != 261 || claimable != 9 {
		t.Fatalf("unexpected vesting summary pending=%d vesting=%d claimable=%d", pending, vesting, claimable)
	}

	released, total := store.releaseVestedMiningRewardsLocked(day + miningRewardVestingDaySeconds)
	if released != 1 || total != 9 {
		t.Fatalf("expected day-one release of 9, got buckets=%d total=%d", released, total)
	}
	account = store.accountLocked("miner_a")
	if account.Balance != 9 || account.PendingMiningRewards != 261 {
		t.Fatalf("unexpected account after day-one release: %+v", account)
	}

	released, total = store.releaseVestedMiningRewardsLocked(day + 30*miningRewardVestingDaySeconds)
	if released != 1 || total != 261 {
		t.Fatalf("expected final release of 261, got buckets=%d total=%d", released, total)
	}
	account = store.accountLocked("miner_a")
	if account.Balance != 270 || account.PendingMiningRewards != 0 {
		t.Fatalf("unexpected account after final release: %+v", account)
	}
	if len(store.data.MiningRewardVestings) != 0 {
		t.Fatalf("expected completed bucket to be removed, got %d", len(store.data.MiningRewardVestings))
	}
}

func TestClaimMiningRewardsRequiresSignedActiveClaim(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	miner := newTestUser(t)
	now := time.Now().Unix()
	store.vestMiningRewardLocked(miner.Addr, 300, miningRewardSourceStoragePool, now)

	req := wire.ClaimMiningRewardsRequest{
		MinerAddress: miner.Addr,
		ChainID:      store.data.ChainID,
		Nonce:        store.accountLocked(miner.Addr).Nonce,
	}
	if err := wire.SignClaimMiningRewards(&req, miner.Key); err != nil {
		t.Fatal(err)
	}
	resp, err := store.ClaimMiningRewards(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Claimed != 0 {
		t.Fatalf("expected no same-day claimable rewards, got %d", resp.Claimed)
	}
	if store.accountLocked(miner.Addr).Nonce != 1 {
		t.Fatalf("expected nonce consumed after signed claim")
	}

	req = wire.ClaimMiningRewardsRequest{
		MinerAddress: miner.Addr,
		ChainID:      store.data.ChainID,
		Nonce:        store.accountLocked(miner.Addr).Nonce,
	}
	if err := wire.SignClaimMiningRewards(&req, miner.Key); err != nil {
		t.Fatal(err)
	}
	store.data.MiningRewardVestings[miningRewardVestingBucketID(miner.Addr, miningRewardVestingDayStart(now))] = wire.MiningRewardVestingBucket{
		BucketID:      miningRewardVestingBucketID(miner.Addr, miningRewardVestingDayStart(now)),
		Address:       miner.Addr,
		DayUnix:       miningRewardVestingDayStart(now) - 30*miningRewardVestingDaySeconds,
		CreatedAtUnix: now,
		Total:         300,
		Sources:       map[string]uint64{miningRewardSourceStoragePool: 300},
	}
	resp, err = store.ClaimMiningRewards(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Claimed != 300 || resp.Balance != 300 || resp.PendingMiningRewards != 0 {
		t.Fatalf("unexpected claim response: %+v", resp)
	}
}

func TestDeregisterMinerDoesNotSettleUnprovedStorageRewards(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	miner := newTestUser(t)
	store.initRewardPoolsLocked()
	store.data.Accounts[miner.Addr] = wire.Account{Address: miner.Addr}
	store.data.Miners[miner.Addr] = wire.MinerStats{
		MinerAddress:       miner.Addr,
		Status:             wire.MinerStatusActive,
		UsedBytes:          1,
		EffectiveWeight:    1,
		StorageRewardIndex: store.data.StorageRewardIndex,
	}

	store.releaseStoragePerBlockLocked(time.Now().Unix())
	req := wire.DeregisterMinerRequest{
		MinerAddress: miner.Addr,
		ChainID:      store.data.ChainID,
		Nonce:        store.accountLocked(miner.Addr).Nonce,
	}
	if err := wire.SignDeregisterMiner(&req, miner.Key); err != nil {
		t.Fatal(err)
	}
	if err := store.DeregisterMiner(req); err != nil {
		t.Fatal(err)
	}
	stats := store.minerStatsLocked(miner.Addr)
	expected := uint64(50) * wire.TokenUnit
	if stats.Status != wire.MinerStatusExiting {
		t.Fatalf("expected miner exiting, got %s", stats.Status)
	}
	if stats.StorageRewardAccrued != expected {
		t.Fatalf("expected accrued storage reward %d, got %d", expected, stats.StorageRewardAccrued)
	}
	if stats.StorageRewards != 0 {
		t.Fatalf("expected no settled storage rewards, got %d", stats.StorageRewards)
	}
	if got := store.accountLocked(miner.Addr).PendingMiningRewards; got != 0 {
		t.Fatalf("deregister should not create pending mining rewards, got %d", got)
	}
}
