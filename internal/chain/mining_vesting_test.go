package chain

import "testing"

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

	released, total := store.releaseVestedMiningRewardsLocked(day + miningRewardVestingDaySeconds)
	if released != 1 || total != 3 {
		t.Fatalf("expected day-one release of 3, got buckets=%d total=%d", released, total)
	}
	account = store.accountLocked("miner_a")
	if account.Balance != 3 || account.PendingMiningRewards != 267 {
		t.Fatalf("unexpected account after day-one release: %+v", account)
	}

	released, total = store.releaseVestedMiningRewardsLocked(day + 30*miningRewardVestingDaySeconds)
	if released != 1 || total != 87 {
		t.Fatalf("expected day-thirty catch-up release of 87, got buckets=%d total=%d", released, total)
	}
	account = store.accountLocked("miner_a")
	if account.Balance != 90 || account.PendingMiningRewards != 180 {
		t.Fatalf("unexpected account after day-thirty release: %+v", account)
	}

	released, total = store.releaseVestedMiningRewardsLocked(day + 90*miningRewardVestingDaySeconds)
	if released != 1 || total != 180 {
		t.Fatalf("expected final release of 180, got buckets=%d total=%d", released, total)
	}
	account = store.accountLocked("miner_a")
	if account.Balance != 270 || account.PendingMiningRewards != 0 {
		t.Fatalf("unexpected account after final release: %+v", account)
	}
	if len(store.data.MiningRewardVestings) != 0 {
		t.Fatalf("expected completed bucket to be removed, got %d", len(store.data.MiningRewardVestings))
	}
}
