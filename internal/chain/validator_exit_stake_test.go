package chain

import (
	"testing"

	"chain/internal/wire"
)

func TestFinalizeExitingValidatorsReturnsSelfStakeViaUnbonding(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	selfStake := uint64(1_000_000) * wire.TokenUnit

	store.mu.Lock()
	// Validator A: exiting with self-stake — should transition to exited and return stake.
	store.data.Validators["val_a"] = wire.ValidatorInfo{
		OwnerAddress: "val_a",
		Status:       wire.ValidatorStatusExiting,
		SelfStake:    selfStake,
	}
	store.data.Accounts["val_a"] = wire.Account{
		Address:     "val_a",
		LockedStake: selfStake,
		Balance:     500 * wire.TokenUnit,
	}

	// Validator B: exiting with zero self-stake — should transition but no unbonding.
	store.data.Validators["val_b"] = wire.ValidatorInfo{
		OwnerAddress: "val_b",
		Status:       wire.ValidatorStatusExiting,
		SelfStake:    0,
	}
	store.data.Accounts["val_b"] = wire.Account{
		Address: "val_b",
		Balance: 200 * wire.TokenUnit,
	}

	// Validator C: active — should not be affected.
	store.data.Validators["val_c"] = wire.ValidatorInfo{
		OwnerAddress: "val_c",
		Status:       wire.ValidatorStatusActive,
		SelfStake:    selfStake,
	}
	store.data.Accounts["val_c"] = wire.Account{
		Address:     "val_c",
		LockedStake: selfStake,
	}

	// Validator D: slashed — should transition to exiting (not exited).
	store.data.Validators["val_d"] = wire.ValidatorInfo{
		OwnerAddress: "val_d",
		Status:       wire.ValidatorStatusSlashed,
		SelfStake:    selfStake,
	}
	store.data.Accounts["val_d"] = wire.Account{
		Address:     "val_d",
		LockedStake: selfStake,
	}

	store.finalizeExitingValidatorsLocked()
	store.mu.Unlock()

	store.mu.Lock()
	defer store.mu.Unlock()

	// Validator A: exited, stake returned via unbonding.
	valA := store.data.Validators["val_a"]
	if valA.Status != wire.ValidatorStatusExited {
		t.Fatalf("val_a: expected exited, got %s", valA.Status)
	}
	if valA.SelfStake != 0 {
		t.Fatalf("val_a: expected SelfStake=0 after exit, got %d", valA.SelfStake)
	}
	accountA := store.data.Accounts["val_a"]
	if accountA.LockedStake != 0 {
		t.Fatalf("val_a: expected LockedStake=0, got %d", accountA.LockedStake)
	}
	if accountA.UnbondingBalance != selfStake {
		t.Fatalf("val_a: expected UnbondingBalance=%d, got %d", selfStake, accountA.UnbondingBalance)
	}
	if accountA.Balance != 500*wire.TokenUnit {
		t.Fatalf("val_a: Balance should be untouched, got %d", accountA.Balance)
	}
	// Check unbonding entry.
	foundA := false
	for _, entry := range store.data.UnbondingEntries {
		if entry.Delegator == "val_a" && entry.Amount == selfStake {
			foundA = true
			if entry.MaturesAtUnix <= entry.CreatedAtUnix {
				t.Fatal("val_a: unbonding entry should mature after creation")
			}
			break
		}
	}
	if !foundA {
		t.Fatal("val_a: expected unbonding entry")
	}

	// Validator B: exited, no unbonding entry (zero self-stake).
	valB := store.data.Validators["val_b"]
	if valB.Status != wire.ValidatorStatusExited {
		t.Fatalf("val_b: expected exited, got %s", valB.Status)
	}
	for _, entry := range store.data.UnbondingEntries {
		if entry.Delegator == "val_b" {
			t.Fatal("val_b: unexpected unbonding entry for zero-stake validator")
		}
	}

	// Validator C: still active.
	valC := store.data.Validators["val_c"]
	if valC.Status != wire.ValidatorStatusActive {
		t.Fatalf("val_c: expected active, got %s", valC.Status)
	}
	accountC := store.data.Accounts["val_c"]
	if accountC.LockedStake != selfStake {
		t.Fatalf("val_c: LockedStake should be unchanged")
	}

	// Validator D: slashed → exiting (NOT exited, stake still locked).
	valD := store.data.Validators["val_d"]
	if valD.Status != wire.ValidatorStatusExiting {
		t.Fatalf("val_d: expected exiting (from slashed), got %s", valD.Status)
	}
	accountD := store.data.Accounts["val_d"]
	if accountD.LockedStake != selfStake {
		t.Fatalf("val_d: LockedStake should still be locked during exiting")
	}
}
