package chain

import (
	"errors"
	"slices"
	"time"

	"chain/internal/wire"
)

func (s *Store) verifyAgentRequestLocked(chainID string, keyID string, agentNonce uint64, master string, operation string, spend uint64, verify func(agentPub string) error) error {
	if chainID == "" {
		return errors.New("chain_id is required")
	}
	if chainID != s.data.ChainID {
		return errors.New("request chain_id mismatch")
	}
	key, ok := s.data.AgentKeys[keyID]
	if !ok {
		return errors.New("agent key not found")
	}
	if key.Revoked {
		return errors.New("agent key has been revoked")
	}
	if key.ExpiresAt > 0 && time.Now().Unix() > key.ExpiresAt {
		return errors.New("agent key expired")
	}
	if !sameAddress(key.Master, master) {
		return errors.New("agent key master mismatch")
	}
	if !agentKeyAllowsOperation(key.Permissions, operation) {
		return errors.New("agent key lacks permission: " + operation)
	}
	if agentNonce != key.Nonce {
		return errors.New("invalid agent nonce")
	}
	if err := verify(key.AgentPub); err != nil {
		return err
	}
	usedToday := key.UsedToday
	if time.Now().Unix() > key.DayResetAt {
		usedToday = 0
	}
	if agentLimitExceeded(usedToday, spend, key.DailyLimit) {
		return errors.New("agent key daily limit exceeded")
	}
	if agentLimitExceeded(key.UsedTotal, spend, key.TotalLimit) {
		return errors.New("agent key total limit exceeded")
	}
	return nil
}

func (s *Store) consumeAgentRequestLocked(keyID string, spend uint64) error {
	key, ok := s.data.AgentKeys[keyID]
	if !ok {
		return errors.New("agent key not found")
	}
	nowUnix := time.Now().Unix()
	if nowUnix > key.DayResetAt {
		key.UsedToday = 0
		key.DayResetAt = startOfNextDay()
	}
	if agentLimitExceeded(key.UsedToday, spend, key.DailyLimit) {
		return errors.New("agent key daily limit exceeded")
	}
	if agentLimitExceeded(key.UsedTotal, spend, key.TotalLimit) {
		return errors.New("agent key total limit exceeded")
	}
	key.Nonce++
	// P2-H07: Saturating addition to prevent uint64 overflow on limit counters.
	if key.UsedToday > ^uint64(0)-spend {
		key.UsedToday = ^uint64(0)
	} else {
		key.UsedToday += spend
	}
	if key.UsedTotal > ^uint64(0)-spend {
		key.UsedTotal = ^uint64(0)
	} else {
		key.UsedTotal += spend
	}
	return nil
}

// replayAgentKeyMutationLocked applies the agent key state mutation (nonce
// increment + usage counters) without any validation. Used during block
// replay to keep agent key state consistent with the original execution.
func (s *Store) replayAgentKeyMutationLocked(keyID string, spend uint64) {
	key, ok := s.data.AgentKeys[keyID]
	if !ok {
		return // agent key may have been revoked; skip silently
	}
	key.Nonce++
	// Saturating addition to prevent uint64 overflow.
	if key.UsedToday > ^uint64(0)-spend {
		key.UsedToday = ^uint64(0)
	} else {
		key.UsedToday += spend
	}
	if key.UsedTotal > ^uint64(0)-spend {
		key.UsedTotal = ^uint64(0)
	} else {
		key.UsedTotal += spend
	}
}

func agentLimitExceeded(used uint64, spend uint64, limit uint64) bool {
	if limit == 0 {
		return false
	}
	if used > limit {
		return true
	}
	return spend > limit-used
}

func agentKeyAllowsOperation(permissions []string, operation string) bool {
	if slices.Contains(permissions, operation) {
		return true
	}
	for _, alias := range agentPermissionAliases(operation) {
		if slices.Contains(permissions, alias) {
			return true
		}
	}
	return false
}

func agentPermissionAliases(operation string) []string {
	switch operation {
	case "finalize_deal":
		return []string{"finalize"}
	case "terminate_deal":
		return []string{"terminate"}
	case "create_collection":
		return []string{"collection_create"}
	case "create_share":
		return []string{"share_create"}
	case "revoke_share":
		return []string{"share_revoke"}
	case "direct_governance_action":
		return []string{"governance_action"}
	case "direct_action_review_vote":
		return []string{"governance_review"}
	case "deploy_contract":
		return []string{"wasm_deploy"}
	case "call_contract":
		return []string{"wasm_call"}
	default:
		return nil
	}
}

func requestUsesAgent(keyID string) bool {
	return keyID != ""
}

func normalizeRequestUser(user string) string {
	return wire.NormalizeAddress(user)
}
