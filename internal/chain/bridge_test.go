package chain

import (
	"math"
	"strings"
	"testing"
	"time"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// ── helpers ──

// setupBridgeStore creates an in-memory store with bridge enabled and a relayer configured.
func setupBridgeStore(t *testing.T) (*Store, testUser, testUser) {
	t.Helper()
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	sender := newTestUser(t)
	relayer := newTestUser(t)

	store.data.BridgeConfig = &wire.BridgeConfig{
		Enabled:           true,
		BridgePoolAddress: BridgePoolAddress(),
		RelayerAddress:    relayer.Addr,
		MinBridgeAmount:   100,
		DelaySeconds:      86400,
		MaxAmountPerDay:   1_000_000_000_000,
		DayStartUnix:      time.Now().Unix(),
	}

	// Fund the sender.
	fundAccount(store, sender.Addr, 1_000_000_000)
	return store, sender, relayer
}

// signBridgeOut is a test helper that fills ChainID, nonce, and signs.
func signBridgeOut(t *testing.T, store *Store, req *wire.BridgeOutRequest, u testUser) {
	t.Helper()
	if req.Sender == "" {
		req.Sender = u.Addr
	}
	req.Nonce = store.accountLocked(u.Addr).Nonce
	if err := wire.SignBridgeOut(req, u.Key, store.data.ChainID); err != nil {
		t.Fatal(err)
	}
}

// signBridgeClaim is a test helper for signing bridge_in_claim.
func signBridgeClaim(t *testing.T, store *Store, req *wire.BridgeInClaimRequest, u testUser) {
	t.Helper()
	if err := wire.SignBridgeInClaim(req, u.Key, store.data.ChainID); err != nil {
		t.Fatal(err)
	}
}

// signBridgeSetConfig is a test helper that fills Timestamp and signs.
func signBridgeSetConfig(t *testing.T, store *Store, req *wire.BridgeSetConfigRequest, u testUser) {
	t.Helper()
	if req.Timestamp == 0 {
		req.Timestamp = time.Now().Unix()
	}
	if err := wire.SignBridgeSetConfig(req, u.Key, store.data.ChainID); err != nil {
		t.Fatal(err)
	}
}

// addGovernanceOperator registers a testUser as an enabled governance operator.
func addGovernanceOperator(t *testing.T, store *Store, u testUser) {
	t.Helper()
	pubHex := wire.EncodeHex(ethcrypto.FromECDSAPub(&u.Key.PublicKey))
	store.data.GovernanceOperators[wire.NormalizeAddress(u.Addr)] = wire.GovernanceOperator{
		Operator:    u.Addr,
		PublicKey:   pubHex,
		Permissions: []string{"admin"},
		Enabled:     true,
	}
}

// ── bridge_out tests ──

func TestBridgeOutLocksFAL(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)

	req := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipientOnETH",
		TargetChainID: "1",
		Amount:        500,
		Fee:           10,
	}
	signBridgeOut(t, store, &req, sender)

	resp, err := store.BridgeOut(req)
	if err != nil {
		t.Fatalf("bridge out failed: %v", err)
	}

	nonce, _ := resp["nonce"].(uint64)
	if nonce == 0 {
		t.Fatal("expected nonce > 0")
	}

	// Sender debited.
	acct := store.accountLocked(sender.Addr)
	if acct.Balance != 1_000_000_000-510 {
		t.Fatalf("sender balance: got %d, want %d", acct.Balance, 1_000_000_000-510)
	}
	if acct.Nonce != 1 {
		t.Fatalf("sender nonce: got %d, want 1", acct.Nonce)
	}

	// Bridge pool credited.
	pool := store.accountLocked(store.data.BridgeConfig.BridgePoolAddress)
	if pool.Balance != 510 {
		t.Fatalf("pool balance: got %d, want 510", pool.Balance)
	}

	// Outbound recorded.
	ob, err := store.BridgeOutbound(nonce)
	if err != nil {
		t.Fatalf("outbound not found: %v", err)
	}
	if ob.Amount != 500 || ob.Fee != 10 {
		t.Fatalf("outbound amounts: got %d/%d, want 500/10", ob.Amount, ob.Fee)
	}
	if ob.Status != wire.BridgeStatusLocked {
		t.Fatalf("outbound status: got %s, want %s", ob.Status, wire.BridgeStatusLocked)
	}
}

func TestBridgeOutRejectsWhenPaused(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)
	store.data.BridgeConfig.Paused = true

	req := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        500,
	}
	signBridgeOut(t, store, &req, sender)

	_, err := store.BridgeOut(req)
	if err == nil {
		t.Fatal("expected error when bridge is paused")
	}
}

func TestBridgeOutRejectsBelowMinimum(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)

	req := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        50, // below MinBridgeAmount=100
	}
	signBridgeOut(t, store, &req, sender)

	_, err := store.BridgeOut(req)
	if err == nil {
		t.Fatal("expected error for amount below minimum")
	}
}

func TestBridgeOutRejectsDailyLimit(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)
	store.data.BridgeConfig.MaxAmountPerDay = 600

	// First bridge out: 500 — should succeed.
	req1 := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        500,
	}
	signBridgeOut(t, store, &req1, sender)
	if _, err := store.BridgeOut(req1); err != nil {
		t.Fatalf("first bridge out failed: %v", err)
	}

	// Second bridge out: 200 — should fail (500+200 > 600).
	req2 := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        200,
	}
	signBridgeOut(t, store, &req2, sender)

	_, err := store.BridgeOut(req2)
	if err == nil {
		t.Fatal("expected error for daily limit exceeded")
	}
}

func TestBridgeOutRejectsInsufficientBalance(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)

	req := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        2_000_000_000, // more than funded
	}
	signBridgeOut(t, store, &req, sender)

	_, err := store.BridgeOut(req)
	if err == nil {
		t.Fatal("expected error for insufficient balance")
	}
}

// ── bridge_in_claim tests ──

func TestBridgeInClaimUnlocksFAL(t *testing.T) {
	store, _, relayer := setupBridgeStore(t)

	recipient := newTestUser(t)
	fundAccount(store, recipient.Addr, 0) // ensure account exists

	// Pre-fund bridge pool.
	poolAddr := store.data.BridgeConfig.BridgePoolAddress
	fundAccount(store, poolAddr, 10_000)

	// Pre-register an inbound record that is already claimable.
	now := time.Now().Unix()
	sourceHash := "0xETHBurnTxHash123"
	store.data.BridgeInbounds[sourceHash] = &wire.BridgeInbound{
		Nonce:             1,
		SourceTxHash:      sourceHash,
		SourceBlockNumber: 100,
		Recipient:         recipient.Addr,
		Amount:            1000,
		Status:            wire.BridgeStatusPending,
		DetectedAtUnix:    now - 90000,
		ClaimableAfter:    now - 1, // already past delay
	}

	claimReq := wire.BridgeInClaimRequest{
		SourceTxHash:      sourceHash,
		SourceBlockNumber: 100,
		Recipient:         recipient.Addr,
		Amount:            1000,
		Nonce:             1,
		Direction:         "in",
	}
	signBridgeClaim(t, store, &claimReq, relayer)

	resp, err := store.BridgeClaim(claimReq)
	if err != nil {
		t.Fatalf("bridge claim failed: %v", err)
	}
	if resp["status"] != "claimed" {
		t.Fatalf("expected status claimed, got %v", resp["status"])
	}

	// Recipient credited.
	acct := store.accountLocked(recipient.Addr)
	if acct.Balance != 1000 {
		t.Fatalf("recipient balance: got %d, want 1000", acct.Balance)
	}

	// Pool debited.
	pool := store.accountLocked(poolAddr)
	if pool.Balance != 9000 {
		t.Fatalf("pool balance: got %d, want 9000", pool.Balance)
	}

	// Inbound marked claimed.
	ib, _ := store.BridgeInbound(sourceHash)
	if ib.Status != wire.BridgeStatusClaimed {
		t.Fatalf("inbound status: got %s, want %s", ib.Status, wire.BridgeStatusClaimed)
	}
}

func TestBridgeInClaimRejectsWrongRelayer(t *testing.T) {
	store, _, _ := setupBridgeStore(t)

	imposter := newTestUser(t)
	recipient := newTestUser(t)

	now := time.Now().Unix()
	sourceHash := "0xBurnTx"
	store.data.BridgeInbounds[sourceHash] = &wire.BridgeInbound{
		SourceTxHash:   sourceHash,
		Recipient:      recipient.Addr,
		Amount:         1000,
		Status:         wire.BridgeStatusPending,
		ClaimableAfter: now - 1,
	}

	claimReq := wire.BridgeInClaimRequest{
		SourceTxHash: sourceHash,
		Recipient:    recipient.Addr,
		Amount:       1000,
		Direction:    "in",
	}
	// Sign with imposter key, not the relayer.
	signBridgeClaim(t, store, &claimReq, imposter)

	_, err := store.BridgeClaim(claimReq)
	if err == nil {
		t.Fatal("expected error for wrong relayer")
	}
}

func TestBridgeInClaimRejectsReplay(t *testing.T) {
	store, _, relayer := setupBridgeStore(t)

	recipient := newTestUser(t)
	poolAddr := store.data.BridgeConfig.BridgePoolAddress
	fundAccount(store, poolAddr, 10_000)

	now := time.Now().Unix()
	sourceHash := "0xReplayTest"
	store.data.BridgeInbounds[sourceHash] = &wire.BridgeInbound{
		SourceTxHash:   sourceHash,
		Recipient:      recipient.Addr,
		Amount:         500,
		Status:         wire.BridgeStatusPending,
		ClaimableAfter: now - 1,
	}

	claimReq := wire.BridgeInClaimRequest{
		SourceTxHash: sourceHash,
		Recipient:    recipient.Addr,
		Amount:       500,
		Nonce:        1,
		Direction:    "in",
	}
	signBridgeClaim(t, store, &claimReq, relayer)

	// First claim succeeds.
	if _, err := store.BridgeClaim(claimReq); err != nil {
		t.Fatalf("first claim failed: %v", err)
	}

	// Re-sign for second attempt (nonce may differ, but message hash is same).
	claimReq2 := wire.BridgeInClaimRequest{
		SourceTxHash: sourceHash,
		Recipient:    recipient.Addr,
		Amount:       500,
		Nonce:        1,
		Direction:    "in",
	}
	signBridgeClaim(t, store, &claimReq2, relayer)

	// Second claim should fail (replay).
	_, err := store.BridgeClaim(claimReq2)
	if err == nil {
		t.Fatal("expected replay error")
	}
}

func TestBridgeInClaimRejectsBeforeDelay(t *testing.T) {
	store, _, relayer := setupBridgeStore(t)

	recipient := newTestUser(t)
	poolAddr := store.data.BridgeConfig.BridgePoolAddress
	fundAccount(store, poolAddr, 10_000)

	now := time.Now().Unix()
	sourceHash := "0xDelayTest"
	store.data.BridgeInbounds[sourceHash] = &wire.BridgeInbound{
		SourceTxHash:   sourceHash,
		Recipient:      recipient.Addr,
		Amount:         500,
		Status:         wire.BridgeStatusPending,
		ClaimableAfter: now + 86400, // 24h in the future
	}

	claimReq := wire.BridgeInClaimRequest{
		SourceTxHash: sourceHash,
		Recipient:    recipient.Addr,
		Amount:       500,
		Direction:    "in",
	}
	signBridgeClaim(t, store, &claimReq, relayer)

	_, err := store.BridgeClaim(claimReq)
	if err == nil {
		t.Fatal("expected error for delay not elapsed")
	}
}

// ── bridge_set_config tests ──

func TestBridgeSetConfigPauseUnpause(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	operator := newTestUser(t)
	addGovernanceOperator(t, store, operator)

	// Pause.
	pauseReq := wire.BridgeSetConfigRequest{Action: "pause"}
	signBridgeSetConfig(t, store, &pauseReq, operator)
	if _, err := store.BridgeAdminSetConfig(pauseReq); err != nil {
		t.Fatalf("pause failed: %v", err)
	}
	if !store.data.BridgeConfig.Paused {
		t.Fatal("expected bridge to be paused")
	}

	// Unpause.
	unpauseReq := wire.BridgeSetConfigRequest{Action: "unpause"}
	signBridgeSetConfig(t, store, &unpauseReq, operator)
	if _, err := store.BridgeAdminSetConfig(unpauseReq); err != nil {
		t.Fatalf("unpause failed: %v", err)
	}
	if store.data.BridgeConfig.Paused {
		t.Fatal("expected bridge to be unpaused")
	}
}

func TestBridgeSetConfigUpdateConfig(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	operator := newTestUser(t)
	addGovernanceOperator(t, store, operator)

	newRelayer := newTestUser(t)
	minAmt := uint64(200)
	maxAmt := uint64(5_000_000_000_000)
	delay := int64(43200)

	req := wire.BridgeSetConfigRequest{
		Action:          "update_config",
		RelayerAddress:  newRelayer.Addr,
		MinBridgeAmount: &minAmt,
		MaxAmountPerDay: &maxAmt,
		DelaySeconds:    &delay,
	}
	signBridgeSetConfig(t, store, &req, operator)

	resp, err := store.BridgeAdminSetConfig(req)
	if err != nil {
		t.Fatalf("update config failed: %v", err)
	}

	cfg := resp["config"].(*wire.BridgeConfig)
	if cfg.MinBridgeAmount != 200 {
		t.Fatalf("min bridge amount: got %d, want 200", cfg.MinBridgeAmount)
	}
	if cfg.MaxAmountPerDay != 5_000_000_000_000 {
		t.Fatalf("max amount per day: got %d, want 5000000000000", cfg.MaxAmountPerDay)
	}
	if cfg.DelaySeconds != 43200 {
		t.Fatalf("delay seconds: got %d, want 43200", cfg.DelaySeconds)
	}
	if cfg.RelayerAddress != wire.NormalizeAddress(newRelayer.Addr) {
		t.Fatalf("relayer address: got %s, want %s", cfg.RelayerAddress, wire.NormalizeAddress(newRelayer.Addr))
	}
}

func TestBridgeSetConfigRejectsNonOperator(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	nonOperator := newTestUser(t)

	req := wire.BridgeSetConfigRequest{Action: "pause"}
	signBridgeSetConfig(t, store, &req, nonOperator)

	_, err := store.BridgeAdminSetConfig(req)
	if err == nil {
		t.Fatal("expected error for non-operator")
	}
}

// ── bridge_pending tests ──

func TestBridgePendingReturnsActiveOperations(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)

	// Create an outbound.
	req := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        500,
	}
	signBridgeOut(t, store, &req, sender)
	if _, err := store.BridgeOut(req); err != nil {
		t.Fatalf("bridge out failed: %v", err)
	}

	// Add an inbound.
	store.data.BridgeInbounds["test_hash"] = &wire.BridgeInbound{
		SourceTxHash: "test_hash",
		Recipient:    sender.Addr,
		Amount:       200,
		Status:       wire.BridgeStatusPending,
	}

	resp := store.BridgePending()
	if len(resp.Outbounds) != 1 {
		t.Fatalf("expected 1 pending outbound, got %d", len(resp.Outbounds))
	}
	if len(resp.Inbounds) != 1 {
		t.Fatalf("expected 1 pending inbound, got %d", len(resp.Inbounds))
	}
}

// ── BridgePoolAddress determinism test ──

func TestBridgePoolAddressIsDeterministic(t *testing.T) {
	a := BridgePoolAddress()
	b := BridgePoolAddress()
	if a != b {
		t.Fatalf("bridge pool address not deterministic: %s != %s", a, b)
	}
	if len(a) != 42 { // 0x + 40 hex chars
		t.Fatalf("unexpected address length: %d", len(a))
	}
}

// ── security regression tests ──

// TestBridgeSetConfigRejectsReplay ensures the same signed config cannot be
// applied twice (Fix P1: nonce/timestamp replay protection).
func TestBridgeSetConfigRejectsReplay(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	operator := newTestUser(t)
	addGovernanceOperator(t, store, operator)

	req := wire.BridgeSetConfigRequest{Action: "pause"}
	signBridgeSetConfig(t, store, &req, operator)

	if _, err := store.BridgeAdminSetConfig(req); err != nil {
		t.Fatalf("first pause failed: %v", err)
	}

	// Replay the exact same signed request.
	_, err := store.BridgeAdminSetConfig(req)
	if err == nil {
		t.Fatal("expected replay rejection for duplicate bridge_set_config")
	}
}

// TestBridgeSetConfigRejectsExpiredTimestamp ensures old timestamps are rejected.
func TestBridgeSetConfigRejectsExpiredTimestamp(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	operator := newTestUser(t)
	addGovernanceOperator(t, store, operator)

	req := wire.BridgeSetConfigRequest{
		Action:    "pause",
		Timestamp: time.Now().Unix() - 600, // 10 minutes ago, exceeds 5-min skew
	}
	if err := wire.SignBridgeSetConfig(&req, operator.Key, store.data.ChainID); err != nil {
		t.Fatal(err)
	}

	_, err := store.BridgeAdminSetConfig(req)
	if err == nil {
		t.Fatal("expected error for expired timestamp")
	}
}

// TestBridgeClaimBeforeDelayDoesNotMutateState ensures that when a claim
// fails because the delay hasn't elapsed, no state mutation occurs.
// (P0 fix: failed transactions must never change consensus state.)
func TestBridgeClaimBeforeDelayDoesNotMutateState(t *testing.T) {
	store, _, relayer := setupBridgeStore(t)

	recipient := newTestUser(t)
	poolAddr := store.data.BridgeConfig.BridgePoolAddress
	fundAccount(store, poolAddr, 10_000)

	sourceHash := "0xNoStaleInbound"
	store.data.BridgeInbounds[sourceHash] = &wire.BridgeInbound{
		Nonce:             1,
		SourceTxHash:      sourceHash,
		SourceBlockNumber: 42,
		Recipient:         wire.NormalizeAddress(recipient.Addr),
		Amount:            500,
		Status:            wire.BridgeStatusPending,
		DetectedAtUnix:    time.Now().Unix(),
		ClaimableAfter:    time.Now().Unix() + 3600,
	}

	claimReq := wire.BridgeInClaimRequest{
		SourceTxHash:      sourceHash,
		SourceBlockNumber: 42,
		Recipient:         recipient.Addr,
		Amount:            500,
		Nonce:             1,
		Direction:         "in",
	}
	signBridgeClaim(t, store, &claimReq, relayer)

	// Claim should fail because the auto-created inbound has ClaimableAfter = now + 24h.
	_, err := store.BridgeClaim(claimReq)
	if err == nil {
		t.Fatal("expected delay error")
	}

	// Verify the pre-existing inbound was not marked claimed.
	inbound, lookupErr := store.BridgeInbound(sourceHash)
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	if inbound.Status != wire.BridgeStatusPending {
		t.Fatalf("expected inbound to remain pending, got %s", inbound.Status)
	}
}

// ── uint64 overflow protection tests ──

func TestSafeAddU64(t *testing.T) {
	// Normal addition.
	sum, err := safeAddU64(100, 200)
	if err != nil || sum != 300 {
		t.Fatalf("safeAddU64(100,200): got %d, %v", sum, err)
	}

	// Max + 0 should succeed.
	sum, err = safeAddU64(math.MaxUint64, 0)
	if err != nil || sum != math.MaxUint64 {
		t.Fatalf("safeAddU64(MaxUint64,0): got %d, %v", sum, err)
	}

	// Max + 1 should overflow.
	_, err = safeAddU64(math.MaxUint64, 1)
	if err == nil {
		t.Fatal("expected overflow for MaxUint64+1")
	}

	// Large + Large should overflow.
	_, err = safeAddU64(math.MaxUint64-10, 11)
	if err == nil {
		t.Fatal("expected overflow for near-max addition")
	}
}

func TestBridgeOutRejectsAmountFeeOverflow(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)
	// Fund sender with a huge balance so the only rejection is the overflow check.
	fundAccount(store, sender.Addr, math.MaxUint64)
	// Raise daily limit so the daily check doesn't trip before the overflow check.
	store.data.BridgeConfig.MaxAmountPerDay = math.MaxUint64

	req := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        math.MaxUint64,
		Fee:           1, // MaxUint64 + 1 → overflow
	}
	signBridgeOut(t, store, &req, sender)

	_, err := store.BridgeOut(req)
	if err == nil {
		t.Fatal("expected overflow error for Amount+Fee")
	}
	if !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("expected overflow error, got: %v", err)
	}
}

func TestBridgeOutRejectsDailyLimitOverflow(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)
	fundAccount(store, sender.Addr, math.MaxUint64)

	// Set daily limit to max so the limit check itself doesn't trip.
	store.data.BridgeConfig.MaxAmountPerDay = math.MaxUint64
	// Pre-fill the daily counter near max.
	store.data.BridgeConfig.CurrentDayAmount = math.MaxUint64 - 100

	req := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        200, // 200 > MaxUint64 - (MaxUint64-100) = 100, so daily check rejects
		Fee:           0,
	}
	signBridgeOut(t, store, &req, sender)

	_, err := store.BridgeOut(req)
	if err == nil {
		t.Fatal("expected daily limit error for near-overflow CurrentDayAmount+Amount")
	}
}

func TestBridgeOutRejectsPoolCreditOverflow(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)
	store.data.BridgeConfig.MaxAmountPerDay = math.MaxUint64

	// Set pool balance near max.
	poolAddr := store.data.BridgeConfig.BridgePoolAddress
	fundAccount(store, poolAddr, math.MaxUint64-100)
	fundAccount(store, sender.Addr, math.MaxUint64)

	req := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        200, // pool has MaxUint64-100, adding 200 overflows
		Fee:           0,
	}
	signBridgeOut(t, store, &req, sender)

	_, err := store.BridgeOut(req)
	if err == nil {
		t.Fatal("expected pool overflow error")
	}
	if !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("expected overflow error, got: %v", err)
	}
}

func TestBridgeInClaimRejectsRecipientOverflow(t *testing.T) {
	store, _, relayer := setupBridgeStore(t)

	recipient := newTestUser(t)
	// Give recipient a near-max balance.
	fundAccount(store, recipient.Addr, math.MaxUint64-100)

	poolAddr := store.data.BridgeConfig.BridgePoolAddress
	fundAccount(store, poolAddr, math.MaxUint64)

	now := time.Now().Unix()
	sourceHash := "0xOverflowClaim"
	store.data.BridgeInbounds[sourceHash] = &wire.BridgeInbound{
		Nonce:             1,
		SourceTxHash:      sourceHash,
		SourceBlockNumber: 100,
		Recipient:         recipient.Addr,
		Amount:            200, // MaxUint64-100 + 200 overflows
		Status:            wire.BridgeStatusPending,
		DetectedAtUnix:    now - 90000,
		ClaimableAfter:    now - 1,
	}

	claimReq := wire.BridgeInClaimRequest{
		SourceTxHash:      sourceHash,
		SourceBlockNumber: 100,
		Recipient:         recipient.Addr,
		Amount:            200,
		Nonce:             1,
		Direction:         "in",
	}
	signBridgeClaim(t, store, &claimReq, relayer)

	_, err := store.BridgeClaim(claimReq)
	if err == nil {
		t.Fatal("expected recipient balance overflow error")
	}
	if !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("expected overflow error, got: %v", err)
	}
}

// ── P1 regression: admin permission enforcement at state level ──

func TestBridgeSetConfigRejectsNonAdminOperator(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	limitedOp := newTestUser(t)

	// Register as enabled operator with limited (non-admin) permissions.
	pubHex := wire.EncodeHex(ethcrypto.FromECDSAPub(&limitedOp.Key.PublicKey))
	store.data.GovernanceOperators[wire.NormalizeAddress(limitedOp.Addr)] = wire.GovernanceOperator{
		Operator:    limitedOp.Addr,
		PublicKey:   pubHex,
		Enabled:     true,
		Permissions: []string{"bridge_claim_only"},
	}

	req := wire.BridgeSetConfigRequest{Action: "pause"}
	signBridgeSetConfig(t, store, &req, limitedOp)

	_, err := store.BridgeAdminSetConfig(req)
	if err == nil {
		t.Fatal("expected admin permission rejection for non-admin operator")
	}
	if !strings.Contains(err.Error(), "admin permission") {
		t.Fatalf("expected admin permission error, got: %v", err)
	}
}

// ── Regression: auto-register + claim succeeds when delay already elapsed ──

func TestBridgeClaimAutoRegisterSucceedsWhenDelayElapsed(t *testing.T) {
	store, _, relayer := setupBridgeStore(t)

	recipient := newTestUser(t)

	poolAddr := store.data.BridgeConfig.BridgePoolAddress
	fundAccount(store, poolAddr, 10_000)

	sourceHash := "0xAutoRegisterClaim"

	claimReq := wire.BridgeInClaimRequest{
		SourceTxHash:      sourceHash,
		SourceBlockNumber: 42,
		Recipient:         recipient.Addr,
		Amount:            500,
		Nonce:             1,
		Direction:         "in",
	}
	signBridgeClaim(t, store, &claimReq, relayer)

	resp, err := store.BridgeClaim(claimReq)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if resp["status"] != "claimed" {
		t.Fatalf("expected status claimed, got %v", resp["status"])
	}

	// Recipient credited.
	acct := store.accountLocked(recipient.Addr)
	if acct.Balance != 500 {
		t.Fatalf("recipient balance: got %d, want 500", acct.Balance)
	}

	// Inbound marked claimed.
	ib, _ := store.BridgeInbound(sourceHash)
	if ib.Status != wire.BridgeStatusClaimed {
		t.Fatalf("inbound status: got %s, want %s", ib.Status, wire.BridgeStatusClaimed)
	}
}

// ── P1 regression: update_config parameter validation ──

func TestBridgeSetConfigRejectsZeroDelay(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	operator := newTestUser(t)
	addGovernanceOperator(t, store, operator)

	delay := int64(0)
	req := wire.BridgeSetConfigRequest{
		Action:       "update_config",
		DelaySeconds: &delay,
	}
	signBridgeSetConfig(t, store, &req, operator)

	_, err := store.BridgeAdminSetConfig(req)
	if err == nil {
		t.Fatal("expected rejection for delay_seconds = 0")
	}
	if !strings.Contains(err.Error(), "delay_seconds must be at least 3600") {
		t.Fatalf("expected delay_seconds error, got: %v", err)
	}
}

func TestBridgeSetConfigRejectsShortDelay(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	operator := newTestUser(t)
	addGovernanceOperator(t, store, operator)

	delay := int64(3599)
	req := wire.BridgeSetConfigRequest{
		Action:       "update_config",
		DelaySeconds: &delay,
	}
	signBridgeSetConfig(t, store, &req, operator)

	_, err := store.BridgeAdminSetConfig(req)
	if err == nil {
		t.Fatal("expected rejection for delay_seconds = 3599")
	}
	if !strings.Contains(err.Error(), "delay_seconds must be at least 3600") {
		t.Fatalf("expected delay_seconds error, got: %v", err)
	}
}

func TestBridgeSetConfigRejectsZeroMinAmount(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	operator := newTestUser(t)
	addGovernanceOperator(t, store, operator)

	minAmt := uint64(0)
	req := wire.BridgeSetConfigRequest{
		Action:          "update_config",
		MinBridgeAmount: &minAmt,
	}
	signBridgeSetConfig(t, store, &req, operator)

	_, err := store.BridgeAdminSetConfig(req)
	if err == nil {
		t.Fatal("expected rejection for min_bridge_amount = 0")
	}
	if !strings.Contains(err.Error(), "min_bridge_amount must be positive") {
		t.Fatalf("expected min_bridge_amount error, got: %v", err)
	}
}

func TestBridgeSetConfigRejectsMaxBelowMin(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	operator := newTestUser(t)
	addGovernanceOperator(t, store, operator)

	// First set a valid MinBridgeAmount.
	store.data.BridgeConfig.MinBridgeAmount = 1000

	maxAmt := uint64(500) // less than MinBridgeAmount
	req := wire.BridgeSetConfigRequest{
		Action:          "update_config",
		MaxAmountPerDay: &maxAmt,
	}
	signBridgeSetConfig(t, store, &req, operator)

	_, err := store.BridgeAdminSetConfig(req)
	if err == nil {
		t.Fatal("expected rejection for max_amount_per_day < min_bridge_amount")
	}
	if !strings.Contains(err.Error(), "max_amount_per_day must be >= min_bridge_amount") {
		t.Fatalf("expected max_amount_per_day error, got: %v", err)
	}
}

// ── P1 regression: bridge_out no partial write on pool overflow ──

func TestBridgeOutNoPartialWriteOnPoolOverflow(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)
	store.data.BridgeConfig.MaxAmountPerDay = math.MaxUint64

	// Set pool balance near max so pool credit will overflow.
	poolAddr := store.data.BridgeConfig.BridgePoolAddress
	fundAccount(store, poolAddr, math.MaxUint64-100)
	fundAccount(store, sender.Addr, math.MaxUint64)

	balBefore := store.accountLocked(sender.Addr).Balance

	req := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        200, // pool has MaxUint64-100, adding 200 overflows
		Fee:           0,
	}
	signBridgeOut(t, store, &req, sender)

	_, err := store.BridgeOut(req)
	if err == nil {
		t.Fatal("expected pool overflow error")
	}

	// Verify sender balance was NOT debited — no partial writes on failure.
	balAfter := store.accountLocked(sender.Addr).Balance
	if balAfter != balBefore {
		t.Fatalf("sender balance changed after failed bridge_out: before=%d after=%d", balBefore, balAfter)
	}

	// Verify sender nonce was NOT incremented.
	nonceAfter := store.accountLocked(sender.Addr).Nonce
	if nonceAfter != 0 {
		t.Fatalf("sender nonce changed after failed bridge_out: got %d, want 0", nonceAfter)
	}
}

// ── P1 regression: daily limit underflow when config is lowered ──

func TestBridgeOutRejectsDailyLimitAfterConfigLowered(t *testing.T) {
	store, sender, _ := setupBridgeStore(t)
	fundAccount(store, sender.Addr, 1_000_000_000)

	// Simulate: governance lowers MaxAmountPerDay below CurrentDayAmount.
	store.data.BridgeConfig.CurrentDayAmount = 500_000_000_000
	store.data.BridgeConfig.MaxAmountPerDay = 100_000_000_000 // less than current

	req := wire.BridgeOutRequest{
		Sender:        sender.Addr,
		Recipient:     "0xRecipient",
		TargetChainID: "1",
		Amount:        100,
		Fee:           0,
	}
	signBridgeOut(t, store, &req, sender)

	_, err := store.BridgeOut(req)
	if err == nil {
		t.Fatal("expected daily limit rejection when CurrentDayAmount > MaxAmountPerDay")
	}
	if !strings.Contains(err.Error(), "daily limit") {
		t.Fatalf("expected daily limit error, got: %v", err)
	}
}

// ── P1 regression: set_config no partial state on failure ──

func TestBridgeSetConfigNoConsumedMessageOnFailure(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	operator := newTestUser(t)
	addGovernanceOperator(t, store, operator)

	minAmt := uint64(0) // invalid — will cause validation failure
	req := wire.BridgeSetConfigRequest{
		Action:          "update_config",
		MinBridgeAmount: &minAmt,
	}
	signBridgeSetConfig(t, store, &req, operator)

	_, err := store.BridgeAdminSetConfig(req)
	if err == nil {
		t.Fatal("expected validation error for min_bridge_amount = 0")
	}

	// Consumed message should NOT have been recorded.
	configHash := bridgeSetConfigMessageHash(req, store.data.ChainID)
	if _, consumed := store.data.BridgeConsumedMessages[configHash]; consumed {
		t.Fatal("consumed message was recorded on failed set_config — replay protection state leak")
	}
}

func TestBridgeSetConfigNoPartialUpdateOnFailure(t *testing.T) {
	store, _, _ := setupBridgeStore(t)
	operator := newTestUser(t)
	addGovernanceOperator(t, store, operator)

	// Record original values.
	origRelayer := store.data.BridgeConfig.RelayerAddress
	origMin := store.data.BridgeConfig.MinBridgeAmount

	newRelayer := newTestUser(t)
	minAmt := uint64(500)
	delay := int64(100) // invalid — less than 3600, will cause failure AFTER min is applied

	req := wire.BridgeSetConfigRequest{
		Action:          "update_config",
		RelayerAddress:  newRelayer.Addr,
		MinBridgeAmount: &minAmt,
		DelaySeconds:    &delay,
	}
	signBridgeSetConfig(t, store, &req, operator)

	_, err := store.BridgeAdminSetConfig(req)
	if err == nil {
		t.Fatal("expected validation error for delay_seconds < 3600")
	}

	// Verify relayer_address was NOT changed.
	if store.data.BridgeConfig.RelayerAddress != origRelayer {
		t.Fatalf("relayer_address was modified on failed set_config: got %s, want %s",
			store.data.BridgeConfig.RelayerAddress, origRelayer)
	}

	// Verify min_bridge_amount was NOT changed.
	if store.data.BridgeConfig.MinBridgeAmount != origMin {
		t.Fatalf("min_bridge_amount was modified on failed set_config: got %d, want %d",
			store.data.BridgeConfig.MinBridgeAmount, origMin)
	}
}
