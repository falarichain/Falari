package chain

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"chain/internal/wire"
)

var agentAllowedOps = []string{
	"create_intent",
	"batch_commit",
	"finalize",
	"renew",
	"retrieval",
	"collection_create",
	"append_record",
	"create_key_envelope",
	"create_share",
	"revoke_share",
	"share_create",
	"share_revoke",
	"private_read",
}

const maxAgentKeyNameLen = 64
const maxAgentKeyPermissions = 10

type registerAgentKeyTxPayload struct {
	Request wire.RegisterAgentKeyRequest `json:"request"`
	Key     wire.AgentKey                `json:"key"`
}

type revokeAgentKeyTxPayload struct {
	Request wire.RevokeAgentKeyRequest `json:"request"`
}

func (s *Store) RegisterAgentKey(req wire.RegisterAgentKeyRequest) (wire.RegisterAgentKeyResponse, error) {
	req.Master = wire.NormalizeAddress(req.Master)
	req.Name = strings.TrimSpace(req.Name)
	var err error
	req.Permissions, err = normalizeAgentPermissions(req.Permissions)
	if err != nil {
		return wire.RegisterAgentKeyResponse{}, err
	}
	if err := validateRegisterAgentKeyRequest(req, true); err != nil {
		return wire.RegisterAgentKeyResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	master := wire.NormalizeAddress(req.Master)
	if err := s.verifyAccountRequestLocked(req.ChainID, master, req.Nonce, func() error {
		return wire.VerifyRegisterAgentKey(req)
	}); err != nil {
		return wire.RegisterAgentKeyResponse{}, err
	}
	nonce := req.Nonce
	s.consumeAccountNonceLocked(master)

	keyID := agentKeyID(master, nonce)
	now := time.Now().Unix()
	dayStart := startOfNextDay()

	key := wire.AgentKey{
		KeyID:       keyID,
		Name:        req.Name,
		Master:      master,
		AgentPub:    req.AgentPub,
		Nonce:       0,
		Permissions: req.Permissions,
		DailyLimit:  req.DailyLimit,
		TotalLimit:  req.TotalLimit,
		UsedToday:   0,
		UsedTotal:   0,
		DayResetAt:  dayStart,
		CreatedAt:   now,
		ExpiresAt:   req.ExpiresAt,
	}
	s.data.AgentKeys[keyID] = &key
	s.recordTxLocked("register_agent_key", master, registerAgentKeyTxPayload{Request: req, Key: key})

	if err := s.saveLocked(); err != nil {
		return wire.RegisterAgentKeyResponse{}, err
	}
	return wire.RegisterAgentKeyResponse{Key: key}, nil
}

func (s *Store) RevokeAgentKey(req wire.RevokeAgentKeyRequest) error {
	req.Master = wire.NormalizeAddress(req.Master)
	if req.KeyID == "" {
		return errors.New("key id is required")
	}
	if req.Master == "" {
		return errors.New("master address is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.data.AgentKeys[req.KeyID]
	if !ok {
		return errors.New("agent key not found")
	}
	if key.Master != req.Master {
		return errors.New("only the master can revoke this key")
	}
	if key.Revoked {
		return errors.New("agent key already revoked")
	}

	if err := s.verifyAccountRequestLocked(req.ChainID, req.Master, req.Nonce, func() error {
		return wire.VerifyRevokeAgentKey(req)
	}); err != nil {
		return err
	}
	s.consumeAccountNonceLocked(req.Master)

	key.Revoked = true
	s.recordTxLocked("revoke_agent_key", req.Master, revokeAgentKeyTxPayload{Request: req})
	return s.saveLocked()
}

func (s *Store) ListAgentKeys(master string) ([]wire.AgentKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalized := wire.NormalizeAddress(master)
	keys := make([]wire.AgentKey, 0)
	for _, key := range s.data.AgentKeys {
		if key.Master == normalized {
			keys = append(keys, *key)
		}
	}
	return keys, nil
}

func (s *Store) validateAgentKeyTxLocked(tx wire.Transaction) error {
	if tx.AgentKeyID == "" {
		return nil
	}

	key, ok := s.data.AgentKeys[tx.AgentKeyID]
	if !ok {
		return errors.New("agent key not found")
	}
	if key.Revoked {
		return errors.New("agent key has been revoked")
	}
	if key.ExpiresAt > 0 && time.Now().Unix() > key.ExpiresAt {
		return errors.New("agent key expired")
	}

	if !agentKeyAllowsOperation(key.Permissions, tx.Type) {
		return errors.New("agent key lacks permission: " + tx.Type)
	}

	nowUnix := time.Now().Unix()
	if nowUnix > key.DayResetAt {
		key.UsedToday = 0
		key.DayResetAt = startOfNextDay()
	}

	if key.DailyLimit > 0 && key.UsedToday+tx.Fee > key.DailyLimit {
		return errors.New("agent key daily limit exceeded")
	}
	if key.TotalLimit > 0 && key.UsedTotal+tx.Fee > key.TotalLimit {
		return errors.New("agent key total limit exceeded")
	}

	master := s.accountLocked(key.Master)
	if master.Balance < tx.Fee {
		return errors.New("master account insufficient balance")
	}

	if tx.AgentNonce != key.Nonce {
		return errors.New("invalid agent nonce")
	}

	key.Nonce++
	key.UsedToday += tx.Fee
	key.UsedTotal += tx.Fee
	return nil
}

func agentKeyID(master string, nonce uint64) string {
	payload, _ := json.Marshal(struct {
		Master string `json:"master"`
		Nonce  uint64 `json:"nonce"`
	}{Master: wire.NormalizeAddress(master), Nonce: nonce})
	hash := sha256.Sum256(payload)
	return "key_" + base64.RawURLEncoding.EncodeToString(hash[:12])
}

func normalizeAgentPermissions(permissions []string) ([]string, error) {
	if len(permissions) == 0 || len(permissions) > maxAgentKeyPermissions {
		return nil, errors.New("permissions must have between 1 and 10 entries")
	}
	seen := map[string]bool{}
	normalized := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		p := strings.TrimSpace(permission)
		if p == "" || !slices.Contains(agentAllowedOps, p) {
			return nil, errors.New("invalid agent permission: " + p)
		}
		if seen[p] {
			return nil, errors.New("duplicate agent permission: " + p)
		}
		seen[p] = true
		normalized = append(normalized, p)
	}
	return normalized, nil
}

func validateRegisterAgentKeyRequest(req wire.RegisterAgentKeyRequest, requireFreshExpiry bool) error {
	if req.Master == "" {
		return errors.New("master address is required")
	}
	if req.AgentPub == "" {
		return errors.New("agent public key is required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return errors.New("name is required")
	}
	if len(req.Name) > maxAgentKeyNameLen {
		return errors.New("name exceeds maximum length")
	}
	if requireFreshExpiry && req.ExpiresAt > 0 && req.ExpiresAt <= time.Now().Unix() {
		return errors.New("expires_at must be in the future")
	}
	return nil
}

func (s *Store) applyRegisterAgentKeyLocked(payload registerAgentKeyTxPayload) error {
	req := payload.Request
	req.Master = wire.NormalizeAddress(req.Master)
	req.Name = strings.TrimSpace(req.Name)
	permissions, err := normalizeAgentPermissions(req.Permissions)
	if err != nil {
		return err
	}
	req.Permissions = permissions
	if err := validateRegisterAgentKeyRequest(req, false); err != nil {
		return err
	}
	expectedKeyID := agentKeyID(req.Master, req.Nonce)
	if payload.Key.KeyID != expectedKeyID {
		return errors.New("replay register agent key id mismatch")
	}
	if _, exists := s.data.AgentKeys[expectedKeyID]; exists {
		return nil
	}
	if err := s.verifyAccountRequestLocked(req.ChainID, req.Master, req.Nonce, func() error {
		return wire.VerifyRegisterAgentKey(req)
	}); err != nil {
		return err
	}
	if err := validateAgentKeyMatchesRequest(payload.Key, req); err != nil {
		return err
	}
	s.consumeAccountNonceLocked(req.Master)
	key := payload.Key
	key.Master = req.Master
	key.Name = req.Name
	key.Permissions = append([]string(nil), req.Permissions...)
	s.data.AgentKeys[expectedKeyID] = &key
	return nil
}

func (s *Store) applyRevokeAgentKeyLocked(payload revokeAgentKeyTxPayload) error {
	req := payload.Request
	req.Master = wire.NormalizeAddress(req.Master)
	if req.KeyID == "" {
		return errors.New("key id is required")
	}
	if req.Master == "" {
		return errors.New("master address is required")
	}
	key, ok := s.data.AgentKeys[req.KeyID]
	if !ok {
		return errors.New("replay revoke agent key not found")
	}
	if key.Master != req.Master {
		return errors.New("replay revoke agent key master mismatch")
	}
	if key.Revoked {
		return nil
	}
	if err := s.verifyAccountRequestLocked(req.ChainID, req.Master, req.Nonce, func() error {
		return wire.VerifyRevokeAgentKey(req)
	}); err != nil {
		return err
	}
	s.consumeAccountNonceLocked(req.Master)
	key.Revoked = true
	return nil
}

func validateAgentKeyMatchesRequest(key wire.AgentKey, req wire.RegisterAgentKeyRequest) error {
	if key.Master != req.Master || key.Name != req.Name || key.AgentPub != req.AgentPub {
		return errors.New("replay register agent key metadata mismatch")
	}
	if key.DailyLimit != req.DailyLimit || key.TotalLimit != req.TotalLimit || key.ExpiresAt != req.ExpiresAt {
		return errors.New("replay register agent key limits mismatch")
	}
	if key.Nonce != 0 || key.UsedToday != 0 || key.UsedTotal != 0 || key.Revoked {
		return errors.New("replay register agent key state mismatch")
	}
	if key.CreatedAt == 0 || key.DayResetAt == 0 {
		return errors.New("replay register agent key timestamps missing")
	}
	if len(key.Permissions) != len(req.Permissions) {
		return errors.New("replay register agent key permissions mismatch")
	}
	for i := range key.Permissions {
		if key.Permissions[i] != req.Permissions[i] {
			return errors.New("replay register agent key permissions mismatch")
		}
	}
	return nil
}

func startOfNextDay() int64 {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()).Unix()
}
