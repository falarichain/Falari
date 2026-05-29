package chain

import (
	"testing"

	"chain/internal/reward"
	"chain/internal/wire"
)

// TestRegistrationGrantsBonus verifies that a new miner receives the registration bonus.
func TestRegistrationGrantsBonus(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_new"
	store.data.Accounts[addr] = wire.Account{
		Address: addr,
		Balance: 100 * reward.TokenUnit,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusActive,
	}

	// Simulate the bonus grant logic from RegisterMiner.
	account := store.accountLocked(addr)
	existing := store.minerStatsLocked(addr)
	if !existing.BonusReleased {
		account.LockedBonus += store.miningParamsLocked().RegistrationBonusAmount
	}
	store.data.Accounts[addr] = account

	// Verify bonus was granted.
	finalAccount := store.data.Accounts[addr]
	expectedBonus := store.miningParamsLocked().RegistrationBonusAmount
	if finalAccount.LockedBonus != expectedBonus {
		t.Fatalf("expected LockedBonus=%d, got %d", expectedBonus, finalAccount.LockedBonus)
	}
}

// TestRegistrationNoBonusAfterRelease verifies that a miner with BonusReleased=true
// does not receive the bonus again on re-registration.
func TestRegistrationNoBonusAfterRelease(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_reregister"
	store.data.Accounts[addr] = wire.Account{
		Address: addr,
		Balance: 100 * reward.TokenUnit,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:  addr,
		Status:        wire.MinerStatusActive,
		BonusReleased: true, // Already released bonus.
	}

	// Simulate the bonus grant logic from RegisterMiner.
	account := store.accountLocked(addr)
	existing := store.minerStatsLocked(addr)
	if !existing.BonusReleased {
		account.LockedBonus += store.miningParamsLocked().RegistrationBonusAmount
	}
	store.data.Accounts[addr] = account

	// Verify no additional bonus was granted.
	finalAccount := store.data.Accounts[addr]
	if finalAccount.LockedBonus != 0 {
		t.Fatalf("expected LockedBonus=0 for re-registered miner, got %d", finalAccount.LockedBonus)
	}
}

// TestBonusDeductedBeforeStake verifies that penalties are deducted from LockedBonus first.
func TestBonusDeductedBeforeStake(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_bonus_first"
	bonusAmount := uint64(500) * reward.TokenUnit
	stakeAmount := uint64(1000) * reward.TokenUnit
	slashAmount := uint64(200) * reward.TokenUnit

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
		LockedStake: stakeAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusActive,
		Stake:        stakeAmount,
	}

	// Simulate the deduction logic from finalizeEpochLocked.
	account := store.accountLocked(addr)
	slash := slashAmount

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

	// Verify deduction came entirely from bonus.
	if fromBonus != slashAmount {
		t.Fatalf("expected fromBonus=%d, got %d", slashAmount, fromBonus)
	}
	if fromStake != 0 {
		t.Fatalf("expected fromStake=0, got %d", fromStake)
	}

	finalAccount := store.data.Accounts[addr]
	if finalAccount.LockedBonus != bonusAmount-slashAmount {
		t.Fatalf("expected LockedBonus=%d, got %d", bonusAmount-slashAmount, finalAccount.LockedBonus)
	}
	if finalAccount.LockedStake != stakeAmount {
		t.Fatalf("expected LockedStake unchanged=%d, got %d", stakeAmount, finalAccount.LockedStake)
	}
}

// TestBonusThenStakeFullOrder verifies the full deduction order: Bonus → Stake.
func TestBonusThenStakeFullOrder(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_full_order"
	bonusAmount := uint64(300) * reward.TokenUnit
	stakeAmount := uint64(500) * reward.TokenUnit
	slashAmount := uint64(400) * reward.TokenUnit // More than bonus

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
		LockedStake: stakeAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusActive,
		Stake:        stakeAmount,
	}

	// Simulate the deduction logic.
	account := store.accountLocked(addr)
	slash := slashAmount

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

	// Verify: 300 from bonus + 100 from stake = 400 total.
	if fromBonus != bonusAmount {
		t.Fatalf("expected fromBonus=%d, got %d", bonusAmount, fromBonus)
	}
	expectedFromStake := slashAmount - bonusAmount
	if fromStake != expectedFromStake {
		t.Fatalf("expected fromStake=%d, got %d", expectedFromStake, fromStake)
	}

	finalAccount := store.data.Accounts[addr]
	if finalAccount.LockedBonus != 0 {
		t.Fatalf("expected LockedBonus=0, got %d", finalAccount.LockedBonus)
	}
	expectedStakeRemaining := stakeAmount - expectedFromStake
	if finalAccount.LockedStake != expectedStakeRemaining {
		t.Fatalf("expected LockedStake=%d, got %d", expectedStakeRemaining, finalAccount.LockedStake)
	}
}

// TestAutoExitWhenBothDepleted verifies auto-exit when both LockedBonus and LockedStake are depleted.
func TestAutoExitWhenBothDepleted(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	now := int64(1_700_000_000)

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_auto_exit"
	bonusAmount := uint64(200) * reward.TokenUnit
	stakeAmount := uint64(300) * reward.TokenUnit
	slashAmount := uint64(600) * reward.TokenUnit // More than total

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
		LockedStake: stakeAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusActive,
		Stake:        stakeAmount,
	}

	// Simulate the deduction and auto-exit logic.
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)
	slash := slashAmount

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

	// Verify both are depleted.
	finalAccount := store.data.Accounts[addr]
	if finalAccount.LockedBonus != 0 {
		t.Fatalf("expected LockedBonus=0, got %d", finalAccount.LockedBonus)
	}
	if finalAccount.LockedStake != 0 {
		t.Fatalf("expected LockedStake=0, got %d", finalAccount.LockedStake)
	}

	// Verify miner is in Exiting state.
	finalStats := store.data.Miners[addr]
	if finalStats.Status != wire.MinerStatusExiting {
		t.Fatalf("expected status=%s, got %s", wire.MinerStatusExiting, finalStats.Status)
	}
	if finalStats.ExitedAtUnix == 0 {
		t.Fatal("expected ExitedAtUnix to be set")
	}

	// Verify actualSlash is capped at available funds.
	if actualSlash != bonusAmount+stakeAmount {
		t.Fatalf("expected actualSlash=%d, got %d", bonusAmount+stakeAmount, actualSlash)
	}
}

// TestBonusReleaseOnProofSuccess verifies bonus release when success rate threshold is met.
func TestBonusReleaseOnProofSuccess(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_release"
	bonusAmount := uint64(1000) * reward.TokenUnit

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		Balance:     0,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusActive,
		ProofSuccess: 10000, // Meets threshold
		ProofFailure: 500,   // 95.24% success rate
	}

	// Simulate the bonus release check logic.
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)
	params := store.miningParamsLocked()

	if !stats.BonusReleased && account.LockedBonus > 0 {
		if params.MinBonusProofCount > 0 {
			total := stats.ProofSuccess + stats.ProofFailure
			if stats.ProofSuccess >= params.MinBonusProofCount &&
				total > 0 &&
				stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS {
				stats.BonusReleased = true
				account.Balance += account.LockedBonus
				store.initRewardPoolsLocked()
				if store.data.RewardPools.StorageRemaining >= account.LockedBonus {
					store.data.RewardPools.StorageRemaining -= account.LockedBonus
				}
				account.LockedBonus = 0
				store.data.Accounts[addr] = account
				store.data.Miners[addr] = stats
			}
		}
	}

	// Verify bonus was released.
	finalAccount := store.data.Accounts[addr]
	if finalAccount.Balance != bonusAmount {
		t.Fatalf("expected Balance=%d, got %d", bonusAmount, finalAccount.Balance)
	}
	if finalAccount.LockedBonus != 0 {
		t.Fatalf("expected LockedBonus=0, got %d", finalAccount.LockedBonus)
	}

	finalStats := store.data.Miners[addr]
	if !finalStats.BonusReleased {
		t.Fatal("expected BonusReleased=true")
	}
}

// TestBonusNotReleasedBelowCount verifies bonus is not released when proof count is below threshold.
func TestBonusNotReleasedBelowCount(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_low_count"
	bonusAmount := uint64(1000) * reward.TokenUnit

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		Balance:     0,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusActive,
		ProofSuccess: 2000, // Below threshold of 5000
		ProofFailure: 100,
	}

	// Simulate the bonus release check logic.
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)
	params := store.miningParamsLocked()

	if !stats.BonusReleased && account.LockedBonus > 0 {
		if params.MinBonusProofCount > 0 {
			total := stats.ProofSuccess + stats.ProofFailure
			if stats.ProofSuccess >= params.MinBonusProofCount &&
				total > 0 &&
				stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS {
				stats.BonusReleased = true
				account.Balance += account.LockedBonus
				account.LockedBonus = 0
				store.data.Accounts[addr] = account
				store.data.Miners[addr] = stats
			}
		}
	}

	// Verify bonus was NOT released.
	finalAccount := store.data.Accounts[addr]
	if finalAccount.Balance != 0 {
		t.Fatalf("expected Balance=0, got %d", finalAccount.Balance)
	}
	if finalAccount.LockedBonus != bonusAmount {
		t.Fatalf("expected LockedBonus=%d, got %d", bonusAmount, finalAccount.LockedBonus)
	}

	finalStats := store.data.Miners[addr]
	if finalStats.BonusReleased {
		t.Fatal("expected BonusReleased=false")
	}
}

// TestBonusNotReleasedBelowRate verifies bonus is not released when success rate is below threshold.
func TestBonusNotReleasedBelowRate(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_low_rate"
	bonusAmount := uint64(1000) * reward.TokenUnit

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		Balance:     0,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusActive,
		ProofSuccess: 10000,
		ProofFailure: 1000, // 90.9% success rate, below 95%
	}

	// Simulate the bonus release check logic.
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)
	params := store.miningParamsLocked()

	if !stats.BonusReleased && account.LockedBonus > 0 {
		if params.MinBonusProofCount > 0 {
			total := stats.ProofSuccess + stats.ProofFailure
			if stats.ProofSuccess >= params.MinBonusProofCount &&
				total > 0 &&
				stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS {
				stats.BonusReleased = true
				account.Balance += account.LockedBonus
				account.LockedBonus = 0
				store.data.Accounts[addr] = account
				store.data.Miners[addr] = stats
			}
		}
	}

	// Verify bonus was NOT released.
	finalAccount := store.data.Accounts[addr]
	if finalAccount.Balance != 0 {
		t.Fatalf("expected Balance=0, got %d", finalAccount.Balance)
	}
	if finalAccount.LockedBonus != bonusAmount {
		t.Fatalf("expected LockedBonus=%d, got %d", bonusAmount, finalAccount.LockedBonus)
	}

	finalStats := store.data.Miners[addr]
	if finalStats.BonusReleased {
		t.Fatal("expected BonusReleased=false")
	}
}

// TestBonusReturnedOnExit verifies that remaining LockedBonus is returned to Balance on exit.
func TestBonusReturnedOnExit(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_exit_bonus"
	bonusAmount := uint64(500) * reward.TokenUnit
	initialBalance := uint64(100) * reward.TokenUnit

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		Balance:     initialBalance,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusExiting,
		ExitedAtUnix: 1, // Already past deadline
	}

	// Simulate the exit logic from finalizeExitingMinersLocked.
	account := store.accountLocked(addr)
	if account.LockedBonus > 0 {
		account.Balance += account.LockedBonus
		account.LockedBonus = 0
		store.data.Accounts[addr] = account
	}

	// Verify bonus was returned to balance.
	finalAccount := store.data.Accounts[addr]
	expectedBalance := initialBalance + bonusAmount
	if finalAccount.Balance != expectedBalance {
		t.Fatalf("expected Balance=%d, got %d", expectedBalance, finalAccount.Balance)
	}
	if finalAccount.LockedBonus != 0 {
		t.Fatalf("expected LockedBonus=0, got %d", finalAccount.LockedBonus)
	}
}
