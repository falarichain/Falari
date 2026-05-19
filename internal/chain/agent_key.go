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
}

const maxAgentKeyNameLen = 64
const maxAgentKeyPermissions = 10

func (s *Store) RegisterAgentKey(req wire.RegisterAgentKeyRequest) (wire.RegisterAgentKeyResponse, error) {
	req.Master = wire.NormalizeAddress(req.Master)
	if req.Master == "" {
		return wire.RegisterAgentKeyResponse{}, errors.New("master address is required")
	}
	if req.AgentPub == "" {
		return wire.RegisterAgentKeyResponse{}, errors.New("agent public key is required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return wire.RegisterAgentKeyResponse{}, errors.New("name is required")
	}
	if len(req.Name) > maxAgentKeyNameLen {
		return wire.RegisterAgentKeyResponse{}, errors.New("name exceeds maximum length")
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Permissions) == 0 || len(req.Permissions) > maxAgentKeyPermissions {
		return wire.RegisterAgentKeyResponse{}, errors.New("permissions must have between 1 and 10 entries")
	}
	for _, p := range req.Permissions {
		p = strings.TrimSpace(p)
		if p == "" || !slices.Contains(agentAllowedOps, p) {
			return wire.RegisterAgentKeyResponse{}, errors.New("invalid agent permission: " + p)
		}
	}
	if req.ExpiresAt > 0 && req.ExpiresAt <= time.Now().Unix() {
		return wire.RegisterAgentKeyResponse{}, errors.New("expires_at must be in the future")
	}

	if err := wire.VerifyRegisterAgentKey(req); err != nil {
		return wire.RegisterAgentKeyResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	master := wire.NormalizeAddress(req.Master)
	account := s.accountLocked(master)
	nonce := account.Nonce
	account.Nonce++
	s.data.Accounts[master] = account

	keyID := agentKeyID(master, nonce)
	now := time.Now().Unix()
	dayStart := startOfNextDay()

	s.data.AgentKeys[keyID] = &wire.AgentKey{
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

	if err := s.saveLocked(); err != nil {
		return wire.RegisterAgentKeyResponse{}, err
	}
	return wire.RegisterAgentKeyResponse{Key: *s.data.AgentKeys[keyID]}, nil
}

func (s *Store) RevokeAgentKey(req wire.RevokeAgentKeyRequest) error {
	req.Master = wire.NormalizeAddress(req.Master)
	if req.KeyID == "" {
		return errors.New("key id is required")
	}
	if req.Master == "" {
		return errors.New("master address is required")
	}

	if err := wire.VerifyRevokeAgentKey(req); err != nil {
		return err
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

	if req.Nonce <= s.data.Accounts[req.Master].Nonce {
		acc := s.data.Accounts[req.Master]
		acc.Nonce = req.Nonce + 1
		s.data.Accounts[req.Master] = acc
	}

	key.Revoked = true
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

	if !slices.Contains(key.Permissions, tx.Type) {
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

func startOfNextDay() int64 {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()).Unix()
}
