package chain

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestVerifyAgentRequestDoesNotConsumeNonceOrLimits(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	key := &wire.AgentKey{
		KeyID:       "key_test",
		Master:      "0x1111111111111111111111111111111111111111",
		AgentPub:    "agent_pub",
		Permissions: []string{"create_intent"},
		Nonce:       7,
		DailyLimit:  100,
		TotalLimit:  1000,
		UsedToday:   10,
		UsedTotal:   20,
		DayResetAt:  time.Now().Add(time.Hour).Unix(),
	}
	store.data.AgentKeys[key.KeyID] = key

	store.mu.Lock()
	err = store.verifyAgentRequestLocked(store.data.ChainID, key.KeyID, key.Nonce, key.Master, "create_intent", 5, func(agentPub string) error {
		if agentPub != key.AgentPub {
			t.Fatalf("unexpected agent pub: %s", agentPub)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if key.Nonce != 7 || key.UsedToday != 10 || key.UsedTotal != 20 {
		t.Fatalf("verify mutated agent key: %+v", key)
	}
	if err := store.consumeAgentRequestLocked(key.KeyID, 5); err != nil {
		t.Fatal(err)
	}
	store.mu.Unlock()

	if key.Nonce != 8 || key.UsedToday != 15 || key.UsedTotal != 25 {
		t.Fatalf("consume did not update agent key correctly: %+v", key)
	}
}
