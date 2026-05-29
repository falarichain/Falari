package chain

import (
	"testing"

	"chain/internal/wire"
)

func TestFinalizeExitingMinersTransitionsAfterDeadline(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	// Miner A: exiting with deadline in the past — should transition to exited.
	store.data.Miners["miner_a"] = wire.MinerStats{
		MinerAddress: "miner_a",
		Status:       wire.MinerStatusExiting,
		ExitedAtUnix: 1, // epoch second 1 — always in the past
	}
	// Miner B: exiting with deadline in the future — should stay exiting.
	store.data.Miners["miner_b"] = wire.MinerStats{
		MinerAddress: "miner_b",
		Status:       wire.MinerStatusExiting,
		ExitedAtUnix: 9_999_999_999, // year 2286 — always in the future
	}
	// Miner C: active — should not be affected.
	store.data.Miners["miner_c"] = wire.MinerStats{
		MinerAddress: "miner_c",
		Status:       wire.MinerStatusActive,
	}
	// Miner D: already exited — should not be affected.
	store.data.Miners["miner_d"] = wire.MinerStats{
		MinerAddress: "miner_d",
		Status:       wire.MinerStatusExited,
	}
	// Miner E: exiting but ExitedAtUnix is zero — should NOT transition (safety guard).
	store.data.Miners["miner_e"] = wire.MinerStats{
		MinerAddress: "miner_e",
		Status:       wire.MinerStatusExiting,
		ExitedAtUnix: 0,
	}
	store.mu.Unlock()

	store.mu.Lock()
	store.finalizeExitingMinersLocked()
	store.mu.Unlock()

	store.mu.Lock()
	defer store.mu.Unlock()

	checks := map[string]string{
		"miner_a": wire.MinerStatusExited,  // past deadline → exited
		"miner_b": wire.MinerStatusExiting, // future deadline → still exiting
		"miner_c": wire.MinerStatusActive,  // unaffected
		"miner_d": wire.MinerStatusExited,  // unaffected (already exited)
		"miner_e": wire.MinerStatusExiting, // zero deadline → stays exiting
	}
	for addr, want := range checks {
		got := store.data.Miners[addr].Status
		if got != want {
			t.Errorf("miner %s: got status %q, want %q", addr, got, want)
		}
	}
}

func TestFinalizeExitingMinersReturnsStakeViaUnbonding(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	stakeAmount := uint64(5000) * wire.TokenUnit

	store.mu.Lock()
	// Set up miner with stake in exiting state, deadline already passed.
	store.data.Miners["miner_exit"] = wire.MinerStats{
		MinerAddress: "miner_exit",
		Status:       wire.MinerStatusExiting,
		ExitedAtUnix: 1,
		Stake:        stakeAmount,
	}
	store.data.Accounts["miner_exit"] = wire.Account{
		Address:     "miner_exit",
		LockedStake: stakeAmount,
		Balance:     100 * wire.TokenUnit,
	}

	// Set up miner with zero stake — should transition but no unbonding entry.
	store.data.Miners["miner_zero"] = wire.MinerStats{
		MinerAddress: "miner_zero",
		Status:       wire.MinerStatusExiting,
		ExitedAtUnix: 1,
		Stake:        0,
	}
	store.data.Accounts["miner_zero"] = wire.Account{
		Address: "miner_zero",
		Balance: 50 * wire.TokenUnit,
	}

	store.finalizeExitingMinersLocked()
	store.mu.Unlock()

	store.mu.Lock()
	defer store.mu.Unlock()

	// Verify miner_exit: status = exited, stake = 0.
	stats := store.data.Miners["miner_exit"]
	if stats.Status != wire.MinerStatusExited {
		t.Fatalf("expected exited, got %s", stats.Status)
	}
	if stats.Stake != 0 {
		t.Fatalf("expected stake=0 after exit, got %d", stats.Stake)
	}

	// Verify account: LockedStake → 0, UnbondingBalance → stakeAmount.
	account := store.data.Accounts["miner_exit"]
	if account.LockedStake != 0 {
		t.Fatalf("expected LockedStake=0, got %d", account.LockedStake)
	}
	if account.UnbondingBalance != stakeAmount {
		t.Fatalf("expected UnbondingBalance=%d, got %d", stakeAmount, account.UnbondingBalance)
	}
	// Balance should be untouched.
	if account.Balance != 100*wire.TokenUnit {
		t.Fatalf("expected Balance unchanged, got %d", account.Balance)
	}

	// Verify unbonding entry exists.
	found := false
	for _, entry := range store.data.UnbondingEntries {
		if entry.Delegator == "miner_exit" && entry.Amount == stakeAmount {
			found = true
			if entry.MaturesAtUnix <= entry.CreatedAtUnix {
				t.Fatalf("unbonding entry should mature after creation")
			}
			break
		}
	}
	if !found {
		t.Fatal("expected unbonding entry for miner_exit")
	}

	// Verify miner_zero: exited, no unbonding entry.
	statsZero := store.data.Miners["miner_zero"]
	if statsZero.Status != wire.MinerStatusExited {
		t.Fatalf("expected miner_zero exited, got %s", statsZero.Status)
	}
	for _, entry := range store.data.UnbondingEntries {
		if entry.Delegator == "miner_zero" {
			t.Fatal("unexpected unbonding entry for zero-stake miner")
		}
	}
}
