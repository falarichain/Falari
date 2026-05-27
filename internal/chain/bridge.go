package chain

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"chain/internal/wire"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// safeAddU64 returns a+b or an error if the addition overflows uint64.
func safeAddU64(a, b uint64) (uint64, error) {
	if a > math.MaxUint64-b {
		return 0, errors.New("uint64 overflow")
	}
	return a + b, nil
}

// BridgePoolAddress returns the deterministic system account that holds locked FAL.
// It has no associated private key — only the bridge module can debit/credit it.
func BridgePoolAddress() string {
	hash := ethcrypto.Keccak256([]byte("falari_bridge_pool"))
	return common.BytesToAddress(hash[12:]).Hex()
}

// ──────────────────────────────────────────────────────────────────────────────
// bridge_out  (user locks FAL into the bridge pool)
// ──────────────────────────────────────────────────────────────────────────────

func (s *Store) applyBridgeOutLocked(req wire.BridgeOutRequest) error {
	cfg := s.data.BridgeConfig
	if cfg == nil || !cfg.Enabled {
		return errors.New("bridge is not enabled")
	}
	if cfg.Paused {
		return errors.New("bridge is paused")
	}

	sender := wire.NormalizeAddress(req.Sender)
	if req.Amount < cfg.MinBridgeAmount {
		return errors.New("bridge amount below minimum")
	}

	// Daily rate-limit check.
	now := time.Now().Unix()
	if now-cfg.DayStartUnix >= 86400 {
		cfg.DayStartUnix = now
		cfg.CurrentDayAmount = 0
	}
	// Guard against underflow when governance lowers MaxAmountPerDay below CurrentDayAmount.
	if cfg.CurrentDayAmount >= cfg.MaxAmountPerDay {
		return errors.New("bridge daily limit already reached")
	}
	if req.Amount > cfg.MaxAmountPerDay-cfg.CurrentDayAmount {
		return errors.New("bridge daily limit exceeded")
	}

	total, err := safeAddU64(req.Amount, req.Fee)
	if err != nil {
		return errors.New("bridge amount + fee overflows")
	}
	account := s.accountLocked(sender)
	if account.Balance < total {
		return errors.New("insufficient balance for bridge out")
	}

	// Verify nonce.
	if req.Nonce != account.Nonce {
		return errors.New("invalid bridge out nonce")
	}

	// Verify signature.
	if err := wire.VerifyBridgeOutSignature(req, s.data.ChainID); err != nil {
		return err
	}

	// Pre-compute pool credit (validation only — no state writes yet).
	poolAddr := cfg.BridgePoolAddress
	pool := s.accountLocked(poolAddr)
	newPoolBal, err := safeAddU64(pool.Balance, total)
	if err != nil {
		return errors.New("bridge pool balance overflows")
	}

	// Pre-compute daily counter (validation only).
	newDayAmount, err := safeAddU64(cfg.CurrentDayAmount, req.Amount)
	if err != nil {
		return errors.New("daily bridge amount overflows")
	}

	// ── All checks passed — commit state atomically ──

	// Debit sender.
	account.Balance -= total
	account.Nonce++
	if req.PublicKey != "" {
		account.PublicKey = req.PublicKey
	}
	s.data.Accounts[sender] = account

	// Credit bridge pool.
	pool.Balance = newPoolBal
	s.data.Accounts[poolAddr] = pool

	// Record outbound.
	s.data.BridgeOutboundNonce++
	nonce := s.data.BridgeOutboundNonce
	s.data.BridgeOutbounds[nonce] = &wire.BridgeOutbound{
		Nonce:          nonce,
		TargetChainID:  req.TargetChainID,
		Sender:         sender,
		Recipient:      req.Recipient,
		Amount:         req.Amount,
		Fee:            req.Fee,
		Status:         wire.BridgeStatusLocked,
		LockedAtUnix:   now,
		ClaimableAfter: now + cfg.DelaySeconds,
	}

	// Update daily counter.
	cfg.CurrentDayAmount = newDayAmount

	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// bridge_in_claim  (relayer unlocks FAL from bridge pool after delay)
// ──────────────────────────────────────────────────────────────────────────────

func (s *Store) applyBridgeInClaimLocked(req wire.BridgeInClaimRequest) error {
	cfg := s.data.BridgeConfig
	if cfg == nil || !cfg.Enabled {
		return errors.New("bridge is not enabled")
	}
	if cfg.Paused {
		return errors.New("bridge is paused")
	}

	// Verify signature to authenticate the relayer.
	if err := wire.VerifyBridgeInClaimSignature(req, s.data.ChainID); err != nil {
		return err
	}

	// Recover signer address and verify it matches the configured relayer.
	signerAddr, err := wire.RecoverBridgeInClaimSigner(req, s.data.ChainID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(signerAddr, cfg.RelayerAddress) {
		return errors.New("bridge_in_claim may only be submitted by the configured relayer")
	}

	// Compute a unique message hash for replay protection.
	msgHash := bridgeInboundMessageHash(req)
	if _, consumed := s.data.BridgeConsumedMessages[msgHash]; consumed {
		return errors.New("bridge message already consumed")
	}

	// Look up the corresponding inbound record.
	inboundKey := req.SourceTxHash
	inbound, ok := s.data.BridgeInbounds[inboundKey]
	autoRegistered := false
	if !ok {
		// Build inbound record locally — do NOT write to state until all checks pass.
		now := time.Now().Unix()
		inbound = &wire.BridgeInbound{
			Nonce:             req.Nonce,
			SourceTxHash:      req.SourceTxHash,
			SourceBlockNumber: req.SourceBlockNumber,
			Recipient:         wire.NormalizeAddress(req.Recipient),
			Amount:            req.Amount,
			Status:            wire.BridgeStatusPending,
			DetectedAtUnix:    now,
			ClaimableAfter:    now + cfg.DelaySeconds,
		}
		autoRegistered = true
	}

	// Enforce delay — do NOT write state on failure (consensus safety).
	now := time.Now().Unix()
	if now < inbound.ClaimableAfter {
		return errors.New("bridge claim delay has not elapsed")
	}

	if inbound.Status == wire.BridgeStatusClaimed {
		return errors.New("bridge inbound already claimed")
	}

	// Verify amount consistency.
	if inbound.Amount != req.Amount {
		return errors.New("bridge claim amount mismatch")
	}

	recipient := wire.NormalizeAddress(inbound.Recipient)
	poolAddr := cfg.BridgePoolAddress
	pool := s.accountLocked(poolAddr)
	if pool.Balance < inbound.Amount {
		return errors.New("bridge pool has insufficient balance")
	}

	// Pre-compute recipient credit (validation only — no state writes yet).
	dest := s.accountLocked(recipient)
	newBal, err := safeAddU64(dest.Balance, inbound.Amount)
	if err != nil {
		return errors.New("recipient balance overflows")
	}

	// ── All checks passed — commit state atomically ──

	// Persist auto-registered inbound.
	if autoRegistered {
		s.data.BridgeInbounds[inboundKey] = inbound
	}

	// Debit bridge pool, credit recipient.
	pool.Balance -= inbound.Amount
	s.data.Accounts[poolAddr] = pool

	dest.Balance = newBal
	s.data.Accounts[recipient] = dest

	// Mark consumed.
	inbound.Status = wire.BridgeStatusClaimed
	s.data.BridgeConsumedMessages[msgHash] = &wire.BridgeConsumedMessage{
		MessageHash:    msgHash,
		ConsumedAtUnix: now,
	}

	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// bridge_set_config  (governance updates to bridge parameters)
// ──────────────────────────────────────────────────────────────────────────────

func (s *Store) applyBridgeSetConfigLocked(req wire.BridgeSetConfigRequest) error {
	// Verify signature from a governance operator.
	if err := wire.VerifyBridgeSetConfigSignature(req, s.data.ChainID); err != nil {
		return err
	}
	signerAddr, err := wire.RecoverBridgeSetConfigSigner(req, s.data.ChainID)
	if err != nil {
		return err
	}

	// Verify the signer is an enabled governance operator with admin permission.
	operator, ok := s.data.GovernanceOperators[wire.NormalizeAddress(signerAddr)]
	if !ok || !operator.Enabled {
		return errors.New("bridge_set_config requires an enabled governance operator")
	}
	if !hasAdminPermission(operator.Permissions) {
		return errors.New("bridge_set_config requires admin permission")
	}

	// Replay protection: reject requests with timestamps outside the allowed window.
	now := time.Now().Unix()
	skew := now - req.Timestamp
	if skew < 0 {
		skew = -skew
	}
	if skew > bridgeConfigTimestampSkew {
		return errors.New("bridge_set_config timestamp outside allowed window")
	}

	// Replay protection: reject already-consumed config messages.
	configHash := bridgeSetConfigMessageHash(req, s.data.ChainID)
	if _, consumed := s.data.BridgeConsumedMessages[configHash]; consumed {
		return errors.New("bridge_set_config message already consumed")
	}

	// ── Validate action and compute proposed state — no writes yet ──

	var newPaused *bool          // non-nil ⇒ pause/unpause succeeded
	var newConfig *wire.BridgeConfig // non-nil ⇒ update_config proposed config

	switch req.Action {
	case "pause":
		if s.data.BridgeConfig == nil {
			return errors.New("bridge is not configured")
		}
		paused := true
		newPaused = &paused

	case "unpause":
		if s.data.BridgeConfig == nil {
			return errors.New("bridge is not configured")
		}
		paused := false
		newPaused = &paused

	case "update_config":
		// Work on a local copy so validation failures don't mutate state.
		var proposed wire.BridgeConfig
		if s.data.BridgeConfig != nil {
			proposed = *s.data.BridgeConfig
		} else {
			proposed = wire.BridgeConfig{
				Enabled:           true,
				BridgePoolAddress: BridgePoolAddress(),
				DelaySeconds:      86400,
				MaxAmountPerDay:    1_000_000_000_000, // 10000 FAL (8 decimals)
				DayStartUnix:      time.Now().Unix(),
			}
		}
		if req.RelayerAddress != "" {
			proposed.RelayerAddress = wire.NormalizeAddress(req.RelayerAddress)
		}
		if req.MinBridgeAmount != nil {
			if *req.MinBridgeAmount == 0 {
				return errors.New("min_bridge_amount must be positive")
			}
			proposed.MinBridgeAmount = *req.MinBridgeAmount
		}
		if req.MaxAmountPerDay != nil {
			if *req.MaxAmountPerDay < proposed.MinBridgeAmount {
				return errors.New("max_amount_per_day must be >= min_bridge_amount")
			}
			proposed.MaxAmountPerDay = *req.MaxAmountPerDay
		}
		if req.DelaySeconds != nil {
			if *req.DelaySeconds < 3600 {
				return errors.New("delay_seconds must be at least 3600 (1 hour)")
			}
			proposed.DelaySeconds = *req.DelaySeconds
		}
		newConfig = &proposed

	default:
		return errors.New("unknown bridge_set_config action")
	}

	// ── All checks passed — commit state atomically ──

	s.data.BridgeConsumedMessages[configHash] = &wire.BridgeConsumedMessage{
		MessageHash:    configHash,
		ConsumedAtUnix: now,
	}
	if newPaused != nil {
		s.data.BridgeConfig.Paused = *newPaused
	}
	if newConfig != nil {
		s.data.BridgeConfig = newConfig
	}

	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func bridgeInboundMessageHash(req wire.BridgeInClaimRequest) string {
	raw, _ := json.Marshal(struct {
		SourceTxHash      string `json:"source_tx_hash"`
		SourceBlockNumber uint64 `json:"source_block_number"`
		Recipient         string `json:"recipient"`
		Amount            uint64 `json:"amount"`
		Nonce             uint64 `json:"nonce"`
		Direction         string `json:"direction"`
	}{
		SourceTxHash:      req.SourceTxHash,
		SourceBlockNumber: req.SourceBlockNumber,
		Recipient:         wire.NormalizeAddress(req.Recipient),
		Amount:            req.Amount,
		Nonce:             req.Nonce,
		Direction:         req.Direction,
	})
	return ethcrypto.Keccak256Hash(raw).Hex()
}

// bridgeConfigTimestampSkew is the maximum allowed age (in seconds) for a
// bridge_set_config request timestamp. Requests older or newer than this
// window are rejected to prevent replay attacks.
const bridgeConfigTimestampSkew = 300 // 5 minutes

// bridgeSetConfigMessageHash produces a unique hash for replay tracking of
// bridge_set_config requests, binding chain_id, action, timestamp, and all
// optional parameters.
func bridgeSetConfigMessageHash(req wire.BridgeSetConfigRequest, chainID string) string {
	raw, _ := json.Marshal(struct {
		ChainID         string  `json:"chain_id"`
		Action          string  `json:"action"`
		RelayerAddress  string  `json:"relayer_address,omitempty"`
		MinBridgeAmount *uint64 `json:"min_bridge_amount,omitempty"`
		MaxAmountPerDay *uint64 `json:"max_amount_per_day,omitempty"`
		DelaySeconds    *int64  `json:"delay_seconds,omitempty"`
		Timestamp       int64   `json:"timestamp"`
	}{
		ChainID:         chainID,
		Action:          req.Action,
		RelayerAddress:  req.RelayerAddress,
		MinBridgeAmount: req.MinBridgeAmount,
		MaxAmountPerDay: req.MaxAmountPerDay,
		DelaySeconds:    req.DelaySeconds,
		Timestamp:       req.Timestamp,
	})
	return "cfg:" + ethcrypto.Keccak256Hash(raw).Hex()
}

// ──────────────────────────────────────────────────────────────────────────────
// Store-level API methods (called by HTTP handlers)
// ──────────────────────────────────────────────────────────────────────────────

// BridgeConfig returns the current bridge configuration.
func (s *Store) BridgeConfig() *wire.BridgeConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.BridgeConfig
}

// BridgeOutbound returns a specific outbound record by nonce.
func (s *Store) BridgeOutbound(nonce uint64) (*wire.BridgeOutbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ob, ok := s.data.BridgeOutbounds[nonce]
	if !ok {
		return nil, errors.New("bridge outbound not found")
	}
	return ob, nil
}

// BridgeInbound returns a specific inbound record by source tx hash.
func (s *Store) BridgeInbound(hash string) (*wire.BridgeInbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ib, ok := s.data.BridgeInbounds[hash]
	if !ok {
		return nil, errors.New("bridge inbound not found")
	}
	return ib, nil
}

// BridgePending returns all pending (non-claimed) bridge operations.
func (s *Store) BridgePending() wire.BridgePendingResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	var outbounds []*wire.BridgeOutbound
	for _, ob := range s.data.BridgeOutbounds {
		if ob.Status == wire.BridgeStatusLocked || ob.Status == wire.BridgeStatusPending {
			outbounds = append(outbounds, ob)
		}
	}
	var inbounds []*wire.BridgeInbound
	for _, ib := range s.data.BridgeInbounds {
		if ib.Status != wire.BridgeStatusClaimed {
			inbounds = append(inbounds, ib)
		}
	}
	return wire.BridgePendingResponse{Outbounds: outbounds, Inbounds: inbounds}
}

// BridgeOut processes a user's bridge-out request (lock FAL into bridge pool).
func (s *Store) BridgeOut(req wire.BridgeOutRequest) (map[string]any, error) {
	if req.Sender == "" {
		return nil, errors.New("sender is required")
	}
	if req.Recipient == "" {
		return nil, errors.New("recipient is required")
	}
	if req.Amount == 0 {
		return nil, errors.New("amount must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	txID := s.recordTxLocked("bridge_out", req.Sender, req)
	if err := s.applyBridgeOutLocked(req); err != nil {
		s.removePendingTxLocked(txID)
		return nil, err
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return map[string]any{
		"nonce":    s.data.BridgeOutboundNonce,
		"sender":   req.Sender,
		"recipient": req.Recipient,
		"amount":   req.Amount,
		"fee":      req.Fee,
	}, nil
}

// BridgeClaim processes a relayer's bridge-in claim (unlock FAL from bridge pool).
func (s *Store) BridgeClaim(req wire.BridgeInClaimRequest) (map[string]any, error) {
	if req.SourceTxHash == "" {
		return nil, errors.New("source_tx_hash is required")
	}
	if req.Recipient == "" {
		return nil, errors.New("recipient is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	txID := s.recordTxLocked("bridge_in_claim", req.Recipient, req)
	if err := s.applyBridgeInClaimLocked(req); err != nil {
		s.removePendingTxLocked(txID)
		return nil, err
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return map[string]any{
		"source_tx_hash": req.SourceTxHash,
		"recipient":      req.Recipient,
		"amount":         req.Amount,
		"status":         "claimed",
	}, nil
}

// BridgeAdminSetConfig processes governance config updates (pause/unpause/update_config).
func (s *Store) BridgeAdminSetConfig(req wire.BridgeSetConfigRequest) (map[string]any, error) {
	if req.Action == "" {
		return nil, errors.New("action is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	txID := s.recordTxLocked("bridge_set_config", "governance", req)
	if err := s.applyBridgeSetConfigLocked(req); err != nil {
		s.removePendingTxLocked(txID)
		return nil, err
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return map[string]any{
		"action": req.Action,
		"config": s.data.BridgeConfig,
	}, nil
}
