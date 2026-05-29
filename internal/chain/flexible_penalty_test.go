package chain

import (
	"testing"

	"chain/internal/reward"
	"chain/internal/wire"
)

func TestSlashVestingBucketsDeductsOldestFirst(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	now := int64(1_700_000_000)
	day1 := miningRewardVestingDayStart(now)
	day2 := day1 + miningRewardVestingDaySeconds
	day3 := day2 + miningRewardVestingDaySeconds

	store.mu.Lock()
	defer store.mu.Unlock()

	// Create 3 vesting buckets for the same miner.
	store.vestMiningRewardLocked("miner_x", 100, "storage_pool", day1)
	store.vestMiningRewardLocked("miner_x", 200, "storage_pool", day2)
	store.vestMiningRewardLocked("miner_x", 300, "storage_pool", day3)

	account := store.accountLocked("miner_x")
	if account.PendingMiningRewards != 600 {
		t.Fatalf("expected pending=600, got %d", account.PendingMiningRewards)
	}

	// Slash 150: should take 100 from day1 bucket (deleting it) + 50 from day2.
	slashed := store.slashVestingBucketsLocked("miner_x", 150)
	if slashed != 150 {
		t.Fatalf("expected slashed=150, got %d", slashed)
	}

	account = store.accountLocked("miner_x")
	if account.PendingMiningRewards != 450 {
		t.Fatalf("expected pending=450, got %d", account.PendingMiningRewards)
	}

	// Day1 bucket should be deleted (100 fully slashed).
	day1ID := miningRewardVestingBucketID("miner_x", day1)
	if _, ok := store.data.MiningRewardVestings[day1ID]; ok {
		t.Fatal("day1 bucket should be deleted")
	}

	// Day2 bucket should have Total=150 (was 200, slashed 50).
	day2ID := miningRewardVestingBucketID("miner_x", day2)
	b2 := store.data.MiningRewardVestings[day2ID]
	if b2.Total != 150 {
		t.Fatalf("day2 bucket total=%d, want 150", b2.Total)
	}

	// Day3 bucket untouched.
	day3ID := miningRewardVestingBucketID("miner_x", day3)
	b3 := store.data.MiningRewardVestings[day3ID]
	if b3.Total != 300 {
		t.Fatalf("day3 bucket total=%d, want 300", b3.Total)
	}
}

func TestSlashVestingBucketsCappedAtAvailable(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	now := int64(1_700_000_000)

	store.mu.Lock()
	defer store.mu.Unlock()

	store.vestMiningRewardLocked("miner_y", 50, "storage_pool", now)

	// Try to slash 1000, only 50 available.
	slashed := store.slashVestingBucketsLocked("miner_y", 1000)
	if slashed != 50 {
		t.Fatalf("expected slashed=50, got %d", slashed)
	}

	account := store.accountLocked("miner_y")
	if account.PendingMiningRewards != 0 {
		t.Fatalf("expected pending=0, got %d", account.PendingMiningRewards)
	}
}

func TestSlashVestingBucketsZeroWhenEmpty(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	slashed := store.slashVestingBucketsLocked("no_such_miner", 100)
	if slashed != 0 {
		t.Fatalf("expected slashed=0, got %d", slashed)
	}
}

func TestFlexibleSlashingBonusFirstThenStake(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	slashPerProof := uint64(10) * reward.TokenUnit

	store.mu.Lock()
	defer store.mu.Unlock()

	// Miner with bonus + stake.
	addr := "miner_flex"
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusActive,
		Stake:        4 * reward.TokenUnit,
	}
	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: 8 * reward.TokenUnit,
		LockedStake: 4 * reward.TokenUnit,
	}

	// Simulate missed proof slashing (slash=10, bonus=8, stake=4).
	slash := slashPerProof
	account := store.accountLocked(addr)

	// 1. Slash from LockedBonus first.
	fromBonus := slash
	if fromBonus > account.LockedBonus {
		fromBonus = account.LockedBonus
	}
	account.LockedBonus -= fromBonus

	// 2. Then from LockedStake.
	fromStake := slash - fromBonus
	if fromStake > account.LockedStake {
		fromStake = account.LockedStake
	}
	account.LockedStake -= fromStake
	store.data.Accounts[addr] = account

	actualSlash := fromBonus + fromStake

	// Verify: 8 from bonus + 2 from stake = 10 total.
	if fromBonus != 8*reward.TokenUnit {
		t.Fatalf("expected fromBonus=%d, got %d", 8*reward.TokenUnit, fromBonus)
	}
	if fromStake != 2*reward.TokenUnit {
		t.Fatalf("expected fromStake=%d, got %d", 2*reward.TokenUnit, fromStake)
	}
	if actualSlash != slashPerProof {
		t.Fatalf("expected actualSlash=%d, got %d", slashPerProof, actualSlash)
	}

	account = store.accountLocked(addr)
	if account.LockedBonus != 0 {
		t.Fatalf("expected LockedBonus=0, got %d", account.LockedBonus)
	}
	if account.LockedStake != 2*reward.TokenUnit {
		t.Fatalf("expected LockedStake=%d, got %d", 2*reward.TokenUnit, account.LockedStake)
	}
}

func TestFlexibleSlashingAutoExitWhenDepleted(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	now := int64(1_700_000_000)
	slashPerProof := uint64(10) * reward.TokenUnit

	store.mu.Lock()
	defer store.mu.Unlock()

	// Miner with small bonus and stake, will be depleted.
	addr := "miner_depleted"
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusActive,
		Stake:        3 * reward.TokenUnit,
	}
	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: 2 * reward.TokenUnit,
		LockedStake: 3 * reward.TokenUnit,
	}

	// Simulate missed proof slashing (slash=10, bonus=2, stake=3 → total=5).
	slash := slashPerProof
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)

	fromBonus := slash
	if fromBonus > account.LockedBonus {
		fromBonus = account.LockedBonus
	}
	account.LockedBonus -= fromBonus

	fromStake := slash - fromBonus
	if fromStake > account.LockedStake {
		fromStake = account.LockedStake
	}
	account.LockedStake -= fromStake
	store.data.Accounts[addr] = account

	actualSlash := fromBonus + fromStake
	stats.Stake = account.LockedStake
	stats.Slashed += actualSlash

	// Auto-exit: bonus and stake both depleted.
	if account.LockedBonus == 0 && account.LockedStake == 0 && actualSlash > 0 {
		stats.Status = wire.MinerStatusExiting
		stats.ExitedAtUnix = now + 7*24*60*60
	}
	store.data.Miners[addr] = stats

	// Verify deduction: 2 from bonus + 3 from stake = 5 total.
	if fromBonus != 2*reward.TokenUnit {
		t.Fatalf("expected fromBonus=%d, got %d", 2*reward.TokenUnit, fromBonus)
	}
	if fromStake != 3*reward.TokenUnit {
		t.Fatalf("expected fromStake=%d, got %d", 3*reward.TokenUnit, fromStake)
	}
	if actualSlash != 5*reward.TokenUnit {
		t.Fatalf("expected actualSlash=%d, got %d", 5*reward.TokenUnit, actualSlash)
	}

	// Miner should now be in Exiting state.
	final := store.data.Miners[addr]
	if final.Status != wire.MinerStatusExiting {
		t.Fatalf("expected status=%s, got %s", wire.MinerStatusExiting, final.Status)
	}
	if final.ExitedAtUnix == 0 {
		t.Fatal("expected ExitedAtUnix to be set")
	}
}

func TestFlexibleSlashingNoBonusNoStake(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_nothing"
	store.data.Accounts[addr] = wire.Account{Address: addr}

	slash := uint64(10) * reward.TokenUnit
	account := store.accountLocked(addr)

	fromBonus := slash
	if fromBonus > account.LockedBonus {
		fromBonus = account.LockedBonus
	}
	account.LockedBonus -= fromBonus

	fromStake := slash - fromBonus
	if fromStake > account.LockedStake {
		fromStake = account.LockedStake
	}
	account.LockedStake -= fromStake

	actualSlash := fromBonus + fromStake
	store.data.Accounts[addr] = account

	if actualSlash != 0 {
		t.Fatalf("expected actualSlash=0 when no bonus and no stake, got %d", actualSlash)
	}
}
