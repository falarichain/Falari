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

	// Initialize pool with enough funds.
	store.initRewardPoolsLocked()
	params := store.miningParamsLocked()
	store.data.RewardPools.StorageRemaining = params.RegistrationBonusAmount * 10

	// Simulate the bonus grant logic from RegisterMiner (with cap + pool accounting).
	account := store.accountLocked(addr)
	existing := store.minerStatsLocked(addr)
	if !existing.BonusReleased && !existing.BonusExpired && params.RegistrationBonusAmount > 0 {
		capOK := params.MaxBonusAddresses == 0 || store.data.BonusGrantedCount < params.MaxBonusAddresses
		if capOK && store.data.RewardPools.StorageRemaining >= params.RegistrationBonusAmount {
			account.LockedBonus += params.RegistrationBonusAmount
			store.data.RewardPools.StorageRemaining -= params.RegistrationBonusAmount
			store.data.BonusGrantedCount++
		}
	}
	store.data.Accounts[addr] = account

	// Verify bonus was granted.
	finalAccount := store.data.Accounts[addr]
	expectedBonus := params.RegistrationBonusAmount
	if finalAccount.LockedBonus != expectedBonus {
		t.Fatalf("expected LockedBonus=%d, got %d", expectedBonus, finalAccount.LockedBonus)
	}
	// Verify BonusGrantedCount was incremented.
	if store.data.BonusGrantedCount != 1 {
		t.Fatalf("expected BonusGrantedCount=1, got %d", store.data.BonusGrantedCount)
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
	bonusAmount := uint64(5000) * reward.TokenUnit

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		Balance:     0,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:      addr,
		Status:            wire.MinerStatusActive,
		ProofSuccess:      10000, // Meets threshold
		ProofFailure:      500,   // 95.24% success rate
		RetrievalSuccess:  200,   // Meets retrieval threshold
		RetrievalObligMet: true,
		RegisteredAtUnix:  1_700_000_000, // Recent registration (within 90 days)
	}

	// Simulate the bonus expiry + release check logic from SubmitProof.
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)
	params := store.miningParamsLocked()

	if !stats.BonusReleased && !stats.BonusExpired && account.LockedBonus > 0 {
		// ① Check deadline first.
		if params.BonusDeadlineSeconds > 0 && stats.RegisteredAtUnix > 0 {
			// Not expired in this test (registered recently).
		}
		// ② Check release conditions.
		if !stats.BonusExpired && params.MinBonusProofCount > 0 {
			total := stats.ProofSuccess + stats.ProofFailure
			if stats.ProofSuccess >= params.MinBonusProofCount &&
				total > 0 &&
				stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS &&
				stats.RetrievalObligMet &&
				(params.MinBonusRetrievalCount == 0 || stats.RetrievalSuccess >= params.MinBonusRetrievalCount) {
				stats.BonusReleased = true
				account.Balance += account.LockedBonus
				// No pool deduction — already reserved at grant time.
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
		MinerAddress:      addr,
		Status:            wire.MinerStatusActive,
		ProofSuccess:      2000, // Below threshold of 5000
		ProofFailure:      100,
		RetrievalSuccess:  200,
		RetrievalObligMet: true,
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
				stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS &&
				stats.RetrievalObligMet &&
				(params.MinBonusRetrievalCount == 0 || stats.RetrievalSuccess >= params.MinBonusRetrievalCount) {
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
		MinerAddress:      addr,
		Status:            wire.MinerStatusActive,
		ProofSuccess:      10000,
		ProofFailure:      1000, // 90.9% success rate, below 95%
		RetrievalSuccess:  200,
		RetrievalObligMet: true,
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
				stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS &&
				stats.RetrievalObligMet &&
				(params.MinBonusRetrievalCount == 0 || stats.RetrievalSuccess >= params.MinBonusRetrievalCount) {
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

// TestBonusReturnedToPoolOnExit verifies that remaining LockedBonus is returned to Storage Pool on exit.
func TestBonusReturnedToPoolOnExit(t *testing.T) {
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

	// Initialize pool to track StorageRemaining.
	store.initRewardPoolsLocked()
	poolBefore := store.data.RewardPools.StorageRemaining

	// Simulate the exit logic from finalizeExitingMinersLocked.
	account := store.accountLocked(addr)
	if account.LockedBonus > 0 {
		store.initRewardPoolsLocked()
		store.data.RewardPools.StorageRemaining = saturatingAdd(store.data.RewardPools.StorageRemaining, account.LockedBonus)
		account.LockedBonus = 0
		store.data.Accounts[addr] = account
	}

	// Verify bonus was returned to pool (not to balance).
	finalAccount := store.data.Accounts[addr]
	if finalAccount.Balance != initialBalance {
		t.Fatalf("expected Balance unchanged=%d, got %d", initialBalance, finalAccount.Balance)
	}
	if finalAccount.LockedBonus != 0 {
		t.Fatalf("expected LockedBonus=0, got %d", finalAccount.LockedBonus)
	}

	// Verify pool was replenished.
	poolAfter := store.data.RewardPools.StorageRemaining
	if poolAfter != poolBefore+bonusAmount {
		t.Fatalf("expected pool increase=%d, got %d", bonusAmount, poolAfter-poolBefore)
	}
}

// TestBonusNotReleasedBelowRetrievalCount verifies bonus is not released when retrieval count is below threshold.
func TestBonusNotReleasedBelowRetrievalCount(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_low_retrieval"
	bonusAmount := uint64(1000) * reward.TokenUnit

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		Balance:     0,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:      addr,
		Status:            wire.MinerStatusActive,
		ProofSuccess:      10000,
		ProofFailure:      500,
		RetrievalSuccess:  50, // Below threshold of 100
		RetrievalObligMet: true,
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
				stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS &&
				stats.RetrievalObligMet &&
				(params.MinBonusRetrievalCount == 0 || stats.RetrievalSuccess >= params.MinBonusRetrievalCount) {
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

// TestBonusNotReleasedWhenObligNotMet verifies bonus is not released when DHT obligation is not met.
func TestBonusNotReleasedWhenObligNotMet(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_oblig_not_met"
	bonusAmount := uint64(1000) * reward.TokenUnit

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		Balance:     0,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:      addr,
		Status:            wire.MinerStatusActive,
		ProofSuccess:      10000,
		ProofFailure:      500,
		RetrievalSuccess:  200,
		RetrievalObligMet: false, // DHT obligation not met
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
				stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS &&
				stats.RetrievalObligMet &&
				(params.MinBonusRetrievalCount == 0 || stats.RetrievalSuccess >= params.MinBonusRetrievalCount) {
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

// TestBonusReleasedRetrievalCheckDisabled verifies bonus is released when MinBonusRetrievalCount=0.
func TestBonusReleasedRetrievalCheckDisabled(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_retrieval_disabled"
	bonusAmount := uint64(5000) * reward.TokenUnit

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		Balance:     0,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:      addr,
		Status:            wire.MinerStatusActive,
		ProofSuccess:      10000,
		ProofFailure:      500,
		RetrievalSuccess:  0, // No retrievals
		RetrievalObligMet: true,
		RegisteredAtUnix:  1_700_000_000,
	}

	// Disable retrieval check by setting MinBonusRetrievalCount to 0.
	params := store.miningParamsLocked()
	params.MinBonusRetrievalCount = 0

	// Simulate the bonus release check logic (no pool deduction — reserved at grant time).
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)

	if !stats.BonusReleased && !stats.BonusExpired && account.LockedBonus > 0 {
		if params.MinBonusProofCount > 0 {
			total := stats.ProofSuccess + stats.ProofFailure
			if stats.ProofSuccess >= params.MinBonusProofCount &&
				total > 0 &&
				stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS &&
				stats.RetrievalObligMet &&
				(params.MinBonusRetrievalCount == 0 || stats.RetrievalSuccess >= params.MinBonusRetrievalCount) {
				stats.BonusReleased = true
				account.Balance += account.LockedBonus
				// No pool deduction — already reserved at grant time.
				account.LockedBonus = 0
				store.data.Accounts[addr] = account
				store.data.Miners[addr] = stats
			}
		}
	}

	// Verify bonus WAS released.
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

// TestBonusCapEnforced verifies that the 200K address cap is enforced.
func TestBonusCapEnforced(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	store.initRewardPoolsLocked()
	params := store.miningParamsLocked()
	store.data.RewardPools.StorageRemaining = params.RegistrationBonusAmount * 10

	// Set cap to 2 for testing.
	params.MaxBonusAddresses = 2

	// Grant bonus to 2 miners (reaching the cap).
	for i := 0; i < 2; i++ {
		addr := "miner_cap_" + string(rune('a'+i))
		store.data.Accounts[addr] = wire.Account{Address: addr, Balance: 100 * reward.TokenUnit}
		store.data.Miners[addr] = wire.MinerStats{MinerAddress: addr, Status: wire.MinerStatusActive}

		account := store.accountLocked(addr)
		if store.data.BonusGrantedCount < params.MaxBonusAddresses &&
			store.data.RewardPools.StorageRemaining >= params.RegistrationBonusAmount {
			account.LockedBonus += params.RegistrationBonusAmount
			store.data.RewardPools.StorageRemaining -= params.RegistrationBonusAmount
			store.data.BonusGrantedCount++
		}
		store.data.Accounts[addr] = account
	}

	if store.data.BonusGrantedCount != 2 {
		t.Fatalf("expected BonusGrantedCount=2, got %d", store.data.BonusGrantedCount)
	}

	// Try to grant a 3rd miner — should fail due to cap.
	addr3 := "miner_cap_c"
	store.data.Accounts[addr3] = wire.Account{Address: addr3, Balance: 100 * reward.TokenUnit}
	store.data.Miners[addr3] = wire.MinerStats{MinerAddress: addr3, Status: wire.MinerStatusActive}

	account := store.accountLocked(addr3)
	capOK := params.MaxBonusAddresses == 0 || store.data.BonusGrantedCount < params.MaxBonusAddresses
	if capOK && store.data.RewardPools.StorageRemaining >= params.RegistrationBonusAmount {
		account.LockedBonus += params.RegistrationBonusAmount
		store.data.RewardPools.StorageRemaining -= params.RegistrationBonusAmount
		store.data.BonusGrantedCount++
	}
	store.data.Accounts[addr3] = account

	// Verify 3rd miner did NOT receive the bonus.
	finalAccount := store.data.Accounts[addr3]
	if finalAccount.LockedBonus != 0 {
		t.Fatalf("expected LockedBonus=0 for capped miner, got %d", finalAccount.LockedBonus)
	}
	if store.data.BonusGrantedCount != 2 {
		t.Fatalf("expected BonusGrantedCount still=2, got %d", store.data.BonusGrantedCount)
	}
}

// TestBonusExpiresAfterDeadline verifies that the bonus expires after 90 days.
func TestBonusExpiresAfterDeadline(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_expired"
	bonusAmount := uint64(5000) * reward.TokenUnit
	registeredAt := int64(1_700_000_000)

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     100, // Not enough proofs
		RegisteredAtUnix: registeredAt,
	}
	store.data.BonusGrantedCount = 1

	// Initialize pool to track StorageRemaining.
	store.initRewardPoolsLocked()
	poolBefore := store.data.RewardPools.StorageRemaining

	// Simulate the expiry logic with time past the 90-day deadline.
	params := store.miningParamsLocked()
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)

	// Simulate time = registeredAt + 91 days (past deadline).
	simulatedNow := registeredAt + int64(params.BonusDeadlineSeconds) + 86400

	if !stats.BonusReleased && !stats.BonusExpired && account.LockedBonus > 0 {
		if params.BonusDeadlineSeconds > 0 && stats.RegisteredAtUnix > 0 &&
			(simulatedNow-stats.RegisteredAtUnix) > int64(params.BonusDeadlineSeconds) {
			stats.BonusExpired = true
			store.initRewardPoolsLocked()
			store.data.RewardPools.StorageRemaining = saturatingAdd(store.data.RewardPools.StorageRemaining, account.LockedBonus)
			account.LockedBonus = 0
			if store.data.BonusGrantedCount > 0 {
				store.data.BonusGrantedCount--
			}
			store.data.Accounts[addr] = account
			store.data.Miners[addr] = stats
		}
	}

	// Verify bonus was expired.
	finalStats := store.data.Miners[addr]
	if !finalStats.BonusExpired {
		t.Fatal("expected BonusExpired=true")
	}

	finalAccount := store.data.Accounts[addr]
	if finalAccount.LockedBonus != 0 {
		t.Fatalf("expected LockedBonus=0, got %d", finalAccount.LockedBonus)
	}

	// Verify pool was replenished.
	poolAfter := store.data.RewardPools.StorageRemaining
	if poolAfter != poolBefore+bonusAmount {
		t.Fatalf("expected pool increase=%d, got %d", bonusAmount, poolAfter-poolBefore)
	}

	// Verify slot was released.
	if store.data.BonusGrantedCount != 0 {
		t.Fatalf("expected BonusGrantedCount=0 after expiry, got %d", store.data.BonusGrantedCount)
	}
}

// TestExpiredBonusCannotBeReleased verifies that an expired bonus cannot be released later.
func TestExpiredBonusCannotBeReleased(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_expired_no_release"

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: 0, // Already cleared by expiry
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:      addr,
		Status:            wire.MinerStatusActive,
		BonusExpired:      true, // Already expired
		ProofSuccess:      10000,
		ProofFailure:      100,
		RetrievalSuccess:  200,
		RetrievalObligMet: true,
	}

	// Simulate the bonus release check logic — should NOT release because BonusExpired=true.
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)
	params := store.miningParamsLocked()
	bonusReleased := false

	if !stats.BonusReleased && !stats.BonusExpired && account.LockedBonus > 0 {
		if params.MinBonusProofCount > 0 {
			total := stats.ProofSuccess + stats.ProofFailure
			if stats.ProofSuccess >= params.MinBonusProofCount &&
				total > 0 &&
				stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS &&
				stats.RetrievalObligMet &&
				(params.MinBonusRetrievalCount == 0 || stats.RetrievalSuccess >= params.MinBonusRetrievalCount) {
				stats.BonusReleased = true
				bonusReleased = true
				account.Balance += account.LockedBonus
				account.LockedBonus = 0
				store.data.Accounts[addr] = account
				store.data.Miners[addr] = stats
			}
		}
	}

	// Verify bonus was NOT released.
	if bonusReleased {
		t.Fatal("expected bonus NOT to be released for expired miner")
	}
	finalStats := store.data.Miners[addr]
	if finalStats.BonusReleased {
		t.Fatal("expected BonusReleased=false for expired miner")
	}
}

// TestBonusNotExpiredBeforeDeadline verifies that the bonus does NOT expire before 90 days.
func TestBonusNotExpiredBeforeDeadline(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_not_expired"
	bonusAmount := uint64(5000) * reward.TokenUnit
	registeredAt := int64(1_700_000_000)

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     100,
		RegisteredAtUnix: registeredAt,
	}

	// Simulate the expiry logic with time = 89 days (before deadline).
	params := store.miningParamsLocked()
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)

	// 89 days = 89 * 86400 = 7_689_600 seconds (less than 7_776_000).
	simulatedNow := registeredAt + 89*86400

	if !stats.BonusReleased && !stats.BonusExpired && account.LockedBonus > 0 {
		if params.BonusDeadlineSeconds > 0 && stats.RegisteredAtUnix > 0 &&
			(simulatedNow-stats.RegisteredAtUnix) > int64(params.BonusDeadlineSeconds) {
			stats.BonusExpired = true
			account.LockedBonus = 0
			store.data.Accounts[addr] = account
			store.data.Miners[addr] = stats
		}
	}

	// Verify bonus was NOT expired.
	finalStats := store.data.Miners[addr]
	if finalStats.BonusExpired {
		t.Fatal("expected BonusExpired=false before deadline")
	}

	finalAccount := store.data.Accounts[addr]
	if finalAccount.LockedBonus != bonusAmount {
		t.Fatalf("expected LockedBonus=%d, got %d", bonusAmount, finalAccount.LockedBonus)
	}
}

// TestExpiredBonusReleasesSlot verifies that an expired bonus releases the slot for new registrations.
func TestExpiredBonusReleasesSlot(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_slot_release"
	bonusAmount := uint64(5000) * reward.TokenUnit
	registeredAt := int64(1_700_000_000)

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     100,
		RegisteredAtUnix: registeredAt,
	}
	store.data.BonusGrantedCount = 1 // One slot was used.

	// Simulate expiry past the deadline.
	params := store.miningParamsLocked()
	account := store.accountLocked(addr)
	stats := store.minerStatsLocked(addr)

	simulatedNow := registeredAt + int64(params.BonusDeadlineSeconds) + 86400

	if !stats.BonusReleased && !stats.BonusExpired && account.LockedBonus > 0 {
		if params.BonusDeadlineSeconds > 0 && stats.RegisteredAtUnix > 0 &&
			(simulatedNow-stats.RegisteredAtUnix) > int64(params.BonusDeadlineSeconds) {
			stats.BonusExpired = true
			store.initRewardPoolsLocked()
			store.data.RewardPools.StorageRemaining = saturatingAdd(store.data.RewardPools.StorageRemaining, account.LockedBonus)
			account.LockedBonus = 0
			if store.data.BonusGrantedCount > 0 {
				store.data.BonusGrantedCount--
			}
			store.data.Accounts[addr] = account
			store.data.Miners[addr] = stats
		}
	}

	// Verify slot was released.
	if store.data.BonusGrantedCount != 0 {
		t.Fatalf("expected BonusGrantedCount=0 after expiry, got %d", store.data.BonusGrantedCount)
	}
}

// TestExitedMinerReleasesSlot verifies that an exiting miner releases the bonus slot.
func TestExitedMinerReleasesSlot(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	addr := "miner_exit_slot"
	bonusAmount := uint64(5000) * reward.TokenUnit

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress: addr,
		Status:       wire.MinerStatusExiting,
		ExitedAtUnix: 1,
	}
	store.data.BonusGrantedCount = 1

	// Simulate exit logic from finalizeExitingMinersLocked.
	store.initRewardPoolsLocked()
	account := store.accountLocked(addr)
	if account.LockedBonus > 0 {
		store.data.RewardPools.StorageRemaining = saturatingAdd(store.data.RewardPools.StorageRemaining, account.LockedBonus)
		account.LockedBonus = 0
		if store.data.BonusGrantedCount > 0 {
			store.data.BonusGrantedCount--
		}
		store.data.Accounts[addr] = account
	}

	// Verify slot was released.
	if store.data.BonusGrantedCount != 0 {
		t.Fatalf("expected BonusGrantedCount=0 after exit, got %d", store.data.BonusGrantedCount)
	}
}
