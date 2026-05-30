package chain

import (
	"testing"
	"time"

	"chain/internal/reward"
	"chain/internal/wire"
)

// TestActivationExpiry_NoProof verifies that a miner who registered more than
// 7 days ago and never submitted a proof is expired and enters exit flow.
func TestActivationExpiry_NoProof(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	store.initRewardPoolsLocked()
	params := store.miningParamsLocked()
	store.data.RewardPools.StorageRemaining = params.RegistrationBonusAmount * 10

	bonusAmount := params.RegistrationBonusAmount
	addr := "miner_ghost"
	registeredAt := time.Now().Unix() - int64(params.ActivationWindowSeconds) - 86400 // 8 days ago

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     0,
		RegisteredAtUnix: registeredAt,
	}
	store.data.BonusGrantedCount = 1

	poolBefore := store.data.RewardPools.StorageRemaining

	store.expireInactiveMinersLocked()

	// Verify miner state.
	stats := store.data.Miners[addr]
	if !stats.BonusExpired {
		t.Fatal("expected BonusExpired=true")
	}
	if stats.Status != wire.MinerStatusExiting {
		t.Fatalf("expected Status=%q, got %q", wire.MinerStatusExiting, stats.Status)
	}
	if stats.ExitedAtUnix == 0 {
		t.Fatal("expected ExitedAtUnix > 0")
	}

	// Verify account state.
	account := store.data.Accounts[addr]
	if account.LockedBonus != 0 {
		t.Fatalf("expected LockedBonus=0, got %d", account.LockedBonus)
	}

	// Verify pool was replenished.
	poolAfter := store.data.RewardPools.StorageRemaining
	if poolAfter-poolBefore != bonusAmount {
		t.Fatalf("expected pool increase of %d, got %d", bonusAmount, poolAfter-poolBefore)
	}

	// Verify slot was released.
	if store.data.BonusGrantedCount != 0 {
		t.Fatalf("expected BonusGrantedCount=0, got %d", store.data.BonusGrantedCount)
	}
}

// TestActivationExpiry_WithProof verifies that a miner who has submitted at
// least one proof is not expired even if the activation window has passed.
func TestActivationExpiry_WithProof(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	params := store.miningParamsLocked()
	addr := "miner_active"
	bonusAmount := params.RegistrationBonusAmount

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     1, // Has submitted a proof.
		RegisteredAtUnix: time.Now().Unix() - int64(params.ActivationWindowSeconds) - 86400,
	}
	store.data.BonusGrantedCount = 1

	store.expireInactiveMinersLocked()

	stats := store.data.Miners[addr]
	if stats.BonusExpired {
		t.Fatal("expected BonusExpired=false for active miner")
	}
	if stats.Status != wire.MinerStatusActive {
		t.Fatalf("expected Status=%q, got %q", wire.MinerStatusActive, stats.Status)
	}
	account := store.data.Accounts[addr]
	if account.LockedBonus != bonusAmount {
		t.Fatalf("expected LockedBonus=%d, got %d", bonusAmount, account.LockedBonus)
	}
	if store.data.BonusGrantedCount != 1 {
		t.Fatalf("expected BonusGrantedCount=1, got %d", store.data.BonusGrantedCount)
	}
}

// TestActivationExpiry_BeforeWindow verifies that a miner within the
// activation window is not expired even without proofs.
func TestActivationExpiry_BeforeWindow(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	params := store.miningParamsLocked()
	addr := "miner_young"
	bonusAmount := params.RegistrationBonusAmount
	// Registered 6 days ago — within the 7-day window.
	registeredAt := time.Now().Unix() - 6*86400

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     0,
		RegisteredAtUnix: registeredAt,
	}

	store.expireInactiveMinersLocked()

	stats := store.data.Miners[addr]
	if stats.BonusExpired {
		t.Fatal("expected BonusExpired=false for young miner")
	}
	if stats.Status != wire.MinerStatusActive {
		t.Fatalf("expected Status=%q, got %q", wire.MinerStatusActive, stats.Status)
	}
}

// TestActivationExpiry_ExactlyAtBoundary verifies that the activation check
// uses strict greater-than comparison (elapsed == window → not expired).
func TestActivationExpiry_ExactlyAtBoundary(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	params := store.miningParamsLocked()
	addr := "miner_boundary"
	bonusAmount := params.RegistrationBonusAmount
	// Registered exactly ActivationWindowSeconds ago.
	registeredAt := time.Now().Unix() - int64(params.ActivationWindowSeconds)

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     0,
		RegisteredAtUnix: registeredAt,
	}

	store.expireInactiveMinersLocked()

	stats := store.data.Miners[addr]
	if stats.BonusExpired {
		t.Fatal("expected BonusExpired=false at exact boundary (strict >)")
	}
	if stats.Status != wire.MinerStatusActive {
		t.Fatalf("expected Status=%q, got %q", wire.MinerStatusActive, stats.Status)
	}
}

// TestActivationExpiry_DisabledByZero verifies that setting
// ActivationWindowSeconds=0 disables the activation check.
func TestActivationExpiry_DisabledByZero(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	// Override params to disable activation window.
	store.data.MiningParams = store.miningParamsLocked()
	store.data.MiningParams.ActivationWindowSeconds = 0

	addr := "miner_nocheck"
	bonusAmount := uint64(5000) * reward.TokenUnit
	registeredAt := time.Now().Unix() - 30*86400 // 30 days ago

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     0,
		RegisteredAtUnix: registeredAt,
	}

	store.expireInactiveMinersLocked()

	stats := store.data.Miners[addr]
	if stats.BonusExpired {
		t.Fatal("expected BonusExpired=false when activation window disabled")
	}
	if stats.Status != wire.MinerStatusActive {
		t.Fatalf("expected Status=%q, got %q", wire.MinerStatusActive, stats.Status)
	}
}

// TestActivationExpiry_AlreadyExpired verifies idempotency: a miner already
// marked BonusExpired is skipped.
func TestActivationExpiry_AlreadyExpired(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	params := store.miningParamsLocked()
	addr := "miner_already_expired"
	registeredAt := time.Now().Unix() - int64(params.ActivationWindowSeconds) - 86400

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: 0, // Already zeroed.
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusExiting,
		ProofSuccess:     0,
		RegisteredAtUnix: registeredAt,
		BonusExpired:     true, // Already processed.
	}

	store.expireInactiveMinersLocked()

	// Should remain unchanged.
	stats := store.data.Miners[addr]
	if stats.Status != wire.MinerStatusExiting {
		t.Fatalf("expected Status=%q, got %q", wire.MinerStatusExiting, stats.Status)
	}
}

// TestActivationExpiry_BonusReleased verifies that a miner whose bonus was
// already released is not affected by activation expiry.
func TestActivationExpiry_BonusReleased(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	params := store.miningParamsLocked()
	addr := "miner_released"
	registeredAt := time.Now().Unix() - int64(params.ActivationWindowSeconds) - 86400

	store.data.Accounts[addr] = wire.Account{
		Address: addr,
		Balance: uint64(5000) * reward.TokenUnit, // Bonus already in balance.
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     5000,
		RegisteredAtUnix: registeredAt,
		BonusReleased:    true,
	}

	store.expireInactiveMinersLocked()

	stats := store.data.Miners[addr]
	if stats.BonusExpired {
		t.Fatal("expected BonusExpired=false for released miner")
	}
	if stats.Status != wire.MinerStatusActive {
		t.Fatalf("expected Status=%q, got %q", wire.MinerStatusActive, stats.Status)
	}
}

// TestActivationExpiry_ExitingMiner verifies that miners already in Exiting
// state are skipped even if they have no proofs.
func TestActivationExpiry_ExitingMiner(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	params := store.miningParamsLocked()
	addr := "miner_exiting"
	registeredAt := time.Now().Unix() - int64(params.ActivationWindowSeconds) - 86400

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: 0,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusExiting,
		ProofSuccess:     0,
		RegisteredAtUnix: registeredAt,
	}

	store.expireInactiveMinersLocked()

	stats := store.data.Miners[addr]
	if stats.BonusExpired {
		t.Fatal("expected BonusExpired=false for exiting miner")
	}
}

// TestActivationExpiry_DegradedMiner verifies that a degraded miner with no
// successful proofs is still expired by the activation window.
func TestActivationExpiry_DegradedMiner(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	store.initRewardPoolsLocked()
	params := store.miningParamsLocked()
	store.data.RewardPools.StorageRemaining = params.RegistrationBonusAmount * 10

	addr := "miner_degraded"
	bonusAmount := params.RegistrationBonusAmount
	registeredAt := time.Now().Unix() - int64(params.ActivationWindowSeconds) - 86400

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusDegraded,
		ProofSuccess:     0,
		ProofFailure:     100, // Failed proofs but never succeeded.
		RegisteredAtUnix: registeredAt,
	}
	store.data.BonusGrantedCount = 1

	store.expireInactiveMinersLocked()

	stats := store.data.Miners[addr]
	if !stats.BonusExpired {
		t.Fatal("expected BonusExpired=true for degraded miner with no successful proofs")
	}
	if stats.Status != wire.MinerStatusExiting {
		t.Fatalf("expected Status=%q, got %q", wire.MinerStatusExiting, stats.Status)
	}
}

// TestActivationExpiry_NoBonus verifies that a miner with LockedBonus=0 is
// skipped (no bonus slot to release).
func TestActivationExpiry_NoBonus(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	params := store.miningParamsLocked()
	addr := "miner_nobonus"
	registeredAt := time.Now().Unix() - int64(params.ActivationWindowSeconds) - 86400

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: 0, // No bonus (e.g., pool was depleted at registration).
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     0,
		RegisteredAtUnix: registeredAt,
	}

	store.expireInactiveMinersLocked()

	stats := store.data.Miners[addr]
	// Miner still gets expired and exits (they occupy a slot even without bonus).
	// But since LockedBonus=0, BonusGrantedCount should not change.
	if stats.Status != wire.MinerStatusExiting {
		t.Fatalf("expected Status=%q, got %q", wire.MinerStatusExiting, stats.Status)
	}
	if !stats.BonusExpired {
		t.Fatal("expected BonusExpired=true")
	}
}

// TestActivationExpiry_ReleasesBonusSlot verifies that the bonus slot is
// freed when an inactive miner is expired, allowing new registrations.
func TestActivationExpiry_ReleasesBonusSlot(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	store.initRewardPoolsLocked()
	params := store.miningParamsLocked()
	bonusAmount := params.RegistrationBonusAmount

	// Set cap to 3.
	params.MaxBonusAddresses = 3
	store.data.MiningParams = params
	store.data.RewardPools.StorageRemaining = bonusAmount * 10

	registeredAt := time.Now().Unix() - int64(params.ActivationWindowSeconds) - 86400

	// Miner 1: active with proofs.
	store.data.Accounts["m1"] = wire.Account{Address: "m1", LockedBonus: bonusAmount}
	store.data.Miners["m1"] = wire.MinerStats{
		MinerAddress: "m1", Status: wire.MinerStatusActive,
		ProofSuccess: 100, RegisteredAtUnix: registeredAt,
	}
	// Miner 2: ghost (no proofs, past window).
	store.data.Accounts["m2"] = wire.Account{Address: "m2", LockedBonus: bonusAmount}
	store.data.Miners["m2"] = wire.MinerStats{
		MinerAddress: "m2", Status: wire.MinerStatusActive,
		ProofSuccess: 0, RegisteredAtUnix: registeredAt,
	}
	// Miner 3: ghost (no proofs, past window).
	store.data.Accounts["m3"] = wire.Account{Address: "m3", LockedBonus: bonusAmount}
	store.data.Miners["m3"] = wire.MinerStats{
		MinerAddress: "m3", Status: wire.MinerStatusActive,
		ProofSuccess: 0, RegisteredAtUnix: registeredAt,
	}

	store.data.BonusGrantedCount = 3 // All slots used.

	store.expireInactiveMinersLocked()

	// Verify only the 2 ghosts were expired.
	if store.data.Miners["m1"].BonusExpired {
		t.Fatal("active miner should not be expired")
	}
	if !store.data.Miners["m2"].BonusExpired {
		t.Fatal("ghost miner m2 should be expired")
	}
	if !store.data.Miners["m3"].BonusExpired {
		t.Fatal("ghost miner m3 should be expired")
	}

	// Verify slots were released.
	if store.data.BonusGrantedCount != 1 {
		t.Fatalf("expected BonusGrantedCount=1, got %d", store.data.BonusGrantedCount)
	}
}

// TestActivationExpiry_FullExitCycle verifies the complete lifecycle:
// register → 7 days inactive → exiting → 7 days → exited.
func TestActivationExpiry_FullExitCycle(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	store.initRewardPoolsLocked()
	params := store.miningParamsLocked()
	bonusAmount := params.RegistrationBonusAmount
	store.data.RewardPools.StorageRemaining = bonusAmount * 10

	stakeAmount := uint64(1000) * reward.TokenUnit
	addr := "miner_full_cycle"
	registeredAt := time.Now().Unix() - int64(params.ActivationWindowSeconds) - 86400

	store.data.Accounts[addr] = wire.Account{
		Address:     addr,
		LockedBonus: bonusAmount,
		LockedStake: stakeAmount,
	}
	store.data.Miners[addr] = wire.MinerStats{
		MinerAddress:     addr,
		Status:           wire.MinerStatusActive,
		ProofSuccess:     0,
		RegisteredAtUnix: registeredAt,
		Stake:            stakeAmount,
	}
	store.data.BonusGrantedCount = 1

	// Step 1: Activation expiry triggers.
	store.expireInactiveMinersLocked()

	stats := store.data.Miners[addr]
	if stats.Status != wire.MinerStatusExiting {
		t.Fatalf("step 1: expected Status=%q, got %q", wire.MinerStatusExiting, stats.Status)
	}
	if !stats.BonusExpired {
		t.Fatal("step 1: expected BonusExpired=true")
	}
	account := store.data.Accounts[addr]
	if account.LockedBonus != 0 {
		t.Fatalf("step 1: expected LockedBonus=0, got %d", account.LockedBonus)
	}
	store.mu.Unlock()

	// Step 2: Simulate exit deadline passing by setting ExitedAtUnix to the past.
	store.mu.Lock()
	stats = store.data.Miners[addr]
	stats.ExitedAtUnix = 1 // Force past deadline.
	store.data.Miners[addr] = stats
	store.mu.Unlock()

	// Step 3: Finalize exit.
	store.mu.Lock()
	store.finalizeExitingMinersLocked()
	store.mu.Unlock()

	// Verify final state.
	store.mu.Lock()
	defer store.mu.Unlock()

	stats = store.data.Miners[addr]
	if stats.Status != wire.MinerStatusExited {
		t.Fatalf("step 3: expected Status=%q, got %q", wire.MinerStatusExited, stats.Status)
	}

	// Verify stake was moved to unbonding.
	account = store.data.Accounts[addr]
	if account.LockedStake != 0 {
		t.Fatalf("step 3: expected LockedStake=0, got %d", account.LockedStake)
	}
	if account.UnbondingBalance < stakeAmount {
		t.Fatalf("step 3: expected UnbondingBalance>=%d, got %d", stakeAmount, account.UnbondingBalance)
	}
}

// TestActivationExpiry_MultipleMiners verifies correct behavior with a mix
// of miner states.
func TestActivationExpiry_MultipleMiners(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	store.initRewardPoolsLocked()
	params := store.miningParamsLocked()
	bonusAmount := params.RegistrationBonusAmount
	store.data.RewardPools.StorageRemaining = bonusAmount * 10

	pastWindow := time.Now().Unix() - int64(params.ActivationWindowSeconds) - 86400
	withinWindow := time.Now().Unix() - 3*86400

	// Miner 1: active with proofs, past window.
	store.data.Accounts["m1"] = wire.Account{Address: "m1", LockedBonus: bonusAmount}
	store.data.Miners["m1"] = wire.MinerStats{
		MinerAddress: "m1", Status: wire.MinerStatusActive,
		ProofSuccess: 10, RegisteredAtUnix: pastWindow,
	}

	// Miner 2: active with proofs, within window.
	store.data.Accounts["m2"] = wire.Account{Address: "m2", LockedBonus: bonusAmount}
	store.data.Miners["m2"] = wire.MinerStats{
		MinerAddress: "m2", Status: wire.MinerStatusActive,
		ProofSuccess: 5, RegisteredAtUnix: withinWindow,
	}

	// Miner 3: ghost, past window.
	store.data.Accounts["m3"] = wire.Account{Address: "m3", LockedBonus: bonusAmount}
	store.data.Miners["m3"] = wire.MinerStats{
		MinerAddress: "m3", Status: wire.MinerStatusActive,
		ProofSuccess: 0, RegisteredAtUnix: pastWindow,
	}

	// Miner 4: ghost, past window.
	store.data.Accounts["m4"] = wire.Account{Address: "m4", LockedBonus: bonusAmount}
	store.data.Miners["m4"] = wire.MinerStats{
		MinerAddress: "m4", Status: wire.MinerStatusActive,
		ProofSuccess: 0, RegisteredAtUnix: pastWindow,
	}

	// Miner 5: already exited.
	store.data.Accounts["m5"] = wire.Account{Address: "m5", LockedBonus: 0}
	store.data.Miners["m5"] = wire.MinerStats{
		MinerAddress: "m5", Status: wire.MinerStatusExited,
		ProofSuccess: 0, RegisteredAtUnix: pastWindow,
	}

	store.data.BonusGrantedCount = 4 // m1-m4 have slots.

	store.expireInactiveMinersLocked()

	// m1, m2 should be unaffected.
	if store.data.Miners["m1"].BonusExpired {
		t.Fatal("m1 (active with proofs) should not be expired")
	}
	if store.data.Miners["m2"].BonusExpired {
		t.Fatal("m2 (active within window) should not be expired")
	}

	// m3, m4 should be expired.
	if !store.data.Miners["m3"].BonusExpired {
		t.Fatal("m3 (ghost) should be expired")
	}
	if store.data.Miners["m3"].Status != wire.MinerStatusExiting {
		t.Fatalf("m3: expected Status=%q, got %q", wire.MinerStatusExiting, store.data.Miners["m3"].Status)
	}
	if !store.data.Miners["m4"].BonusExpired {
		t.Fatal("m4 (ghost) should be expired")
	}
	if store.data.Miners["m4"].Status != wire.MinerStatusExiting {
		t.Fatalf("m4: expected Status=%q, got %q", wire.MinerStatusExiting, store.data.Miners["m4"].Status)
	}

	// m5 should be unaffected.
	if store.data.Miners["m5"].BonusExpired {
		t.Fatal("m5 (already exited) should not be expired")
	}

	// 2 slots released.
	if store.data.BonusGrantedCount != 2 {
		t.Fatalf("expected BonusGrantedCount=2, got %d", store.data.BonusGrantedCount)
	}
}
