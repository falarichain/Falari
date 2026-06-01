package chain

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore("")
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	return store
}

func TestEmitEvent_Basic(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventLocked(wire.EventMinerJailed, map[string]any{"miner": "addr1"}, "addr1", "")
	store.emitEventLocked(wire.EventPermanentFundClosed, map[string]any{"intent_id": "intent-1"}, "user1", "intent-1")
	store.mu.Unlock()

	if len(store.data.ChainEvents) != 2 {
		t.Fatalf("expected 2 events, got %d", len(store.data.ChainEvents))
	}
	if store.data.ChainEvents[0].EventID != 1 {
		t.Fatalf("expected event ID 1, got %d", store.data.ChainEvents[0].EventID)
	}
	if store.data.ChainEvents[1].EventID != 2 {
		t.Fatalf("expected event ID 2, got %d", store.data.ChainEvents[1].EventID)
	}
	if store.data.ChainEvents[0].EventType != wire.EventMinerJailed {
		t.Fatalf("expected %s, got %s", wire.EventMinerJailed, store.data.ChainEvents[0].EventType)
	}
	if store.data.ChainEvents[1].RelatedIntentID != "intent-1" {
		t.Fatalf("expected related intent 'intent-1', got %q", store.data.ChainEvents[1].RelatedIntentID)
	}
}

func TestEmitEvent_AutoIncrement(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	for i := 0; i < 10; i++ {
		store.emitEventLocked(wire.EventIntentSettled, nil, "", "")
	}
	store.mu.Unlock()

	if store.data.NextEventID != 10 {
		t.Fatalf("expected NextEventID 10, got %d", store.data.NextEventID)
	}
	for i, evt := range store.data.ChainEvents {
		if evt.EventID != uint64(i+1) {
			t.Fatalf("event %d: expected ID %d, got %d", i, i+1, evt.EventID)
		}
	}
}

func TestEmitEvent_CapAt10k(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	for i := 0; i < maxChainEvents+500; i++ {
		store.emitEventLocked(wire.EventIntentExpired, nil, "", "")
	}
	store.mu.Unlock()

	if len(store.data.ChainEvents) != maxChainEvents {
		t.Fatalf("expected %d events, got %d", maxChainEvents, len(store.data.ChainEvents))
	}
	// First remaining event should have ID 501.
	if store.data.ChainEvents[0].EventID != 501 {
		t.Fatalf("expected first event ID 501, got %d", store.data.ChainEvents[0].EventID)
	}
}

func TestQueryEvents_FilterByType(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventLocked(wire.EventMinerJailed, nil, "miner1", "")
	store.emitEventLocked(wire.EventIntentSettled, nil, "user1", "intent-1")
	store.emitEventLocked(wire.EventMinerJailed, nil, "miner2", "")
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{Type: wire.EventMinerJailed})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 jailed events, got %d", len(resp.Events))
	}
}

func TestQueryEvents_FilterByAddress(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventLocked(wire.EventMinerJailed, nil, "miner1", "")
	store.emitEventLocked(wire.EventMinerJailed, nil, "miner2", "")
	store.emitEventLocked(wire.EventIntentSettled, nil, "miner1", "intent-1")
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{Address: "miner1"})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events for miner1, got %d", len(resp.Events))
	}
}

func TestQueryEvents_FilterByIntentID(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventLocked(wire.EventIntentSettled, nil, "user1", "intent-1")
	store.emitEventLocked(wire.EventIntentExpired, nil, "user2", "intent-2")
	store.emitEventLocked(wire.EventIntentSettled, nil, "user1", "intent-3")
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{IntentID: "intent-2"})
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event for intent-2, got %d", len(resp.Events))
	}
	if resp.Events[0].EventType != wire.EventIntentExpired {
		t.Fatalf("expected intent_expired event, got %s", resp.Events[0].EventType)
	}
}

func TestQueryEvents_LimitAndHasMore(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	for i := 0; i < 10; i++ {
		store.emitEventLocked(wire.EventIntentSettled, nil, "user1", "")
	}
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{Limit: 3})
	if len(resp.Events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(resp.Events))
	}
	if !resp.HasMore {
		t.Fatal("expected HasMore=true")
	}

	resp2 := store.QueryEvents(EventFilter{Limit: 20})
	if len(resp2.Events) != 10 {
		t.Fatalf("expected 10 events, got %d", len(resp2.Events))
	}
	if resp2.HasMore {
		t.Fatal("expected HasMore=false")
	}
}

func TestQueryEvents_FilterBySince(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventLocked(wire.EventMinerJailed, nil, "miner1", "")
	store.data.ChainEvents[0].CreatedAtUnix = 1000
	store.emitEventLocked(wire.EventMinerJailed, nil, "miner2", "")
	store.data.ChainEvents[1].CreatedAtUnix = 2000
	store.emitEventLocked(wire.EventMinerJailed, nil, "miner3", "")
	store.data.ChainEvents[2].CreatedAtUnix = 3000
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{Since: 1500})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events since 1500, got %d", len(resp.Events))
	}
}

func TestQueryEvents_EmptyResult(t *testing.T) {
	store := newTestStore(t)
	resp := store.QueryEvents(EventFilter{Type: wire.EventMinerJailed})
	if len(resp.Events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(resp.Events))
	}
	if resp.Events == nil {
		t.Fatal("expected non-nil empty slice")
	}
}

func TestNormalizeState_ChainEvents(t *testing.T) {
	s := State{}
	normalizeState(&s)
	if s.ChainEvents == nil {
		t.Fatal("expected ChainEvents to be initialized, got nil")
	}
	if len(s.ChainEvents) != 0 {
		t.Fatalf("expected empty ChainEvents, got %d", len(s.ChainEvents))
	}
}

func TestEmitEventFull_CounterpartyAndBlockHeight(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.data.Blocks = append(store.data.Blocks, wire.Block{}, wire.Block{}, wire.Block{})
	height := store.currentHeightLocked()
	store.emitEventFullLocked(wire.EventTransfer, map[string]any{"amount": uint64(100)}, "sender1", "", "receiver1", height)
	store.mu.Unlock()

	if len(store.data.ChainEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(store.data.ChainEvents))
	}
	evt := store.data.ChainEvents[0]
	if evt.CounterpartyAddress != "receiver1" {
		t.Fatalf("expected counterparty 'receiver1', got %q", evt.CounterpartyAddress)
	}
	if evt.BlockHeight != height {
		t.Fatalf("expected block height %d, got %d", height, evt.BlockHeight)
	}
	if evt.RelatedAddress != "sender1" {
		t.Fatalf("expected related address 'sender1', got %q", evt.RelatedAddress)
	}
}

func TestEmitEvent_WrapperPopulatesBlockHeight(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.data.Blocks = append(store.data.Blocks, wire.Block{}, wire.Block{})
	store.emitEventLocked(wire.EventIntentSettled, nil, "user1", "intent-1")
	height := store.currentHeightLocked()
	store.mu.Unlock()

	evt := store.data.ChainEvents[0]
	if evt.BlockHeight != height {
		t.Fatalf("emitEventLocked should populate BlockHeight=%d, got %d", height, evt.BlockHeight)
	}
	if evt.CounterpartyAddress != "" {
		t.Fatalf("emitEventLocked should leave counterparty empty, got %q", evt.CounterpartyAddress)
	}
}

func TestQueryEvents_DualAddressMatching(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventFullLocked(wire.EventTransfer, map[string]any{"amount": uint64(500)}, "alice", "", "bob", 1)
	store.emitEventFullLocked(wire.EventTransfer, map[string]any{"amount": uint64(200)}, "bob", "", "charlie", 2)
	store.emitEventFullLocked(wire.EventTransfer, map[string]any{"amount": uint64(300)}, "charlie", "", "alice", 3)
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{Address: "alice"})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events for alice (sender+receiver), got %d", len(resp.Events))
	}

	resp = store.QueryEvents(EventFilter{Address: "bob"})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events for bob, got %d", len(resp.Events))
	}
}

func TestQueryEvents_CounterpartyFilter(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventFullLocked(wire.EventTransfer, nil, "alice", "", "bob", 1)
	store.emitEventFullLocked(wire.EventTransfer, nil, "charlie", "", "bob", 2)
	store.emitEventFullLocked(wire.EventTransfer, nil, "alice", "", "charlie", 3)
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{Counterparty: "bob"})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events with counterparty=bob, got %d", len(resp.Events))
	}
}

func TestQueryEvents_HeightRangeFilter(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventFullLocked(wire.EventTransfer, nil, "a", "", "b", 10)
	store.emitEventFullLocked(wire.EventTransfer, nil, "a", "", "c", 20)
	store.emitEventFullLocked(wire.EventTransfer, nil, "a", "", "d", 30)
	store.emitEventFullLocked(wire.EventTransfer, nil, "a", "", "e", 40)
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{MinHeight: 15, MaxHeight: 35})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events in height range [15,35], got %d", len(resp.Events))
	}
	for _, evt := range resp.Events {
		if evt.BlockHeight < 15 || evt.BlockHeight > 35 {
			t.Fatalf("event height %d outside range [15,35]", evt.BlockHeight)
		}
	}
}

func TestQueryEvents_CombinedFilters(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventFullLocked(wire.EventTransfer, nil, "alice", "", "bob", 10)
	store.emitEventFullLocked(wire.EventTransfer, nil, "alice", "", "charlie", 20)
	store.emitEventFullLocked(wire.EventIntentCreated, nil, "alice", "intent-1", "", 15)
	store.emitEventFullLocked(wire.EventTransfer, nil, "bob", "", "alice", 25)
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{Address: "alice", Type: wire.EventTransfer})
	if len(resp.Events) != 3 {
		t.Fatalf("expected 3 transfer events involving alice, got %d", len(resp.Events))
	}

	resp = store.QueryEvents(EventFilter{Address: "alice", Type: wire.EventTransfer, MinHeight: 15})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events with height>=15, got %d", len(resp.Events))
	}
}

// ── Phase 6: New EVM parity tests ──

func TestEmitEventWithEmitter_TransactionHash(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.currentTxHash = "tx_abc123"
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "alice", "", "bob", 1, "core")
	store.currentTxHash = "tx_def456"
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "charlie", "", "dave", 1, "core")
	store.currentTxHash = ""
	store.emitEventWithEmitterLocked(wire.EventIntentSettled, nil, "user1", "", "", 2, "system")
	store.mu.Unlock()

	if store.data.ChainEvents[0].TransactionHash != "tx_abc123" {
		t.Fatalf("expected tx_abc123, got %q", store.data.ChainEvents[0].TransactionHash)
	}
	if store.data.ChainEvents[1].TransactionHash != "tx_def456" {
		t.Fatalf("expected tx_def456, got %q", store.data.ChainEvents[1].TransactionHash)
	}
	if store.data.ChainEvents[2].TransactionHash != "" {
		t.Fatalf("expected empty tx hash, got %q", store.data.ChainEvents[2].TransactionHash)
	}
}

func TestEmitEventWithEmitter_LogIndex(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.blockLogIndex = 0
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "a", "", "b", 1, "core")
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "c", "", "d", 1, "core")
	store.emitEventWithEmitterLocked(wire.EventIntentCreated, nil, "e", "i1", "", 1, "core")
	store.mu.Unlock()

	for i, expected := range []int{0, 1, 2} {
		if store.data.ChainEvents[i].LogIndex != expected {
			t.Fatalf("event %d: expected LogIndex %d, got %d", i, expected, store.data.ChainEvents[i].LogIndex)
		}
	}
}

func TestEmitEventWithEmitter_EmitterField(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "a", "", "b", 1, "core")
	store.emitEventWithEmitterLocked(wire.EventBridgeOut, nil, "a", "", "b", 1, "bridge")
	store.emitEventWithEmitterLocked(wire.EventDelegateStake, nil, "a", "", "b", 1, "staking")
	store.emitEventWithEmitterLocked(wire.EventIntentRenewed, nil, "a", "", "", 1, "renewal")
	store.emitEventWithEmitterLocked(wire.EventGovProposalExecuted, nil, "a", "", "", 1, "governance")
	store.emitEventWithEmitterLocked(wire.EventMinerJailed, nil, "a", "", "", 1, "system")
	store.mu.Unlock()

	expected := []string{"core", "bridge", "staking", "renewal", "governance", "system"}
	for i, exp := range expected {
		if store.data.ChainEvents[i].Emitter != exp {
			t.Fatalf("event %d: expected emitter %q, got %q", i, exp, store.data.ChainEvents[i].Emitter)
		}
	}
}

func TestBackfillBlockMetadata_BlockHashAndTimestamp(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	// Emit events before block hash is known (simulating tx processing).
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "a", "", "b", 1, "core")
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "c", "", "d", 1, "core")
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "e", "", "f", 2, "core")

	// Verify events have empty BlockHash initially.
	if store.data.ChainEvents[0].BlockHash != "" {
		t.Fatal("expected empty BlockHash before backfill")
	}

	// Simulate block finalization with backfill.
	block := wire.Block{Height: 1, Hash: "blockhash_1", TimeUnix: 1234567890}
	store.backfillBlockMetadataLocked(block)
	store.mu.Unlock()

	// Events at height 1 should now have BlockHash and BlockTimestamp.
	for i := 0; i < 2; i++ {
		if store.data.ChainEvents[i].BlockHash != "blockhash_1" {
			t.Fatalf("event %d: expected blockhash_1, got %q", i, store.data.ChainEvents[i].BlockHash)
		}
		if store.data.ChainEvents[i].BlockTimestamp != 1234567890 {
			t.Fatalf("event %d: expected timestamp 1234567890, got %d", i, store.data.ChainEvents[i].BlockTimestamp)
		}
	}
	// Event at height 2 should remain untouched.
	if store.data.ChainEvents[2].BlockHash != "" {
		t.Fatalf("event at height 2 should not be backfilled, got %q", store.data.ChainEvents[2].BlockHash)
	}
}

func TestBackfillBlockMetadata_ReceiptLogs(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.currentTxHash = "tx_001"
	store.emitEventWithEmitterLocked(wire.EventTransfer, map[string]any{"amount": 100}, "a", "", "b", 1, "core")
	store.currentTxHash = "tx_002"
	store.emitEventWithEmitterLocked(wire.EventIntentCreated, nil, "c", "i1", "", 1, "core")

	// Create receipts before backfill.
	store.data.Receipts = map[string]wire.TransactionReceipt{
		"tx_001": {TransactionHash: "tx_001"},
		"tx_002": {TransactionHash: "tx_002"},
	}

	block := wire.Block{Height: 1, Hash: "bh_1", TimeUnix: 100}
	store.backfillBlockMetadataLocked(block)
	store.mu.Unlock()

	r1 := store.data.Receipts["tx_001"]
	if len(r1.Logs) != 1 {
		t.Fatalf("expected 1 log for tx_001, got %d", len(r1.Logs))
	}
	if r1.Logs[0].EventType != wire.EventTransfer {
		t.Fatalf("expected transfer log, got %s", r1.Logs[0].EventType)
	}

	r2 := store.data.Receipts["tx_002"]
	if len(r2.Logs) != 1 {
		t.Fatalf("expected 1 log for tx_002, got %d", len(r2.Logs))
	}
	if r2.Logs[0].EventType != wire.EventIntentCreated {
		t.Fatalf("expected intent_created log, got %s", r2.Logs[0].EventType)
	}
}

func TestQueryEvents_CursorPagination(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	for i := 0; i < 8; i++ {
		store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "a", "", "b", 1, "core")
	}
	store.mu.Unlock()

	// First page: get 2 events (newest).
	resp := store.QueryEvents(EventFilter{Limit: 2})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(resp.Events))
	}
	if !resp.HasMore {
		t.Fatal("expected HasMore=true")
	}
	if resp.NextCursor == 0 {
		t.Fatal("expected non-zero NextCursor")
	}

	// Second page: use BeforeEventID cursor to get next 2 older events.
	resp2 := store.QueryEvents(EventFilter{Limit: 2, BeforeEventID: resp.NextCursor})
	if len(resp2.Events) != 2 {
		t.Fatalf("expected 2 events on page 2, got %d", len(resp2.Events))
	}
	// Ensure no overlap: page 2 events should all have ID < page 1 min.
	page1Min := resp.Events[0].EventID
	for _, e := range resp2.Events {
		if e.EventID >= page1Min {
			t.Fatalf("page 2 event %d overlaps with page 1 (min %d)", e.EventID, page1Min)
		}
	}

	// Continue paginating until no more.
	allIDs := map[uint64]bool{}
	for _, e := range resp.Events {
		allIDs[e.EventID] = true
	}
	for _, e := range resp2.Events {
		allIDs[e.EventID] = true
	}
	cursor := resp2.NextCursor
	for resp2.HasMore {
		resp2 = store.QueryEvents(EventFilter{Limit: 2, BeforeEventID: cursor})
		for _, e := range resp2.Events {
			if allIDs[e.EventID] {
				t.Fatalf("duplicate event ID %d across pages", e.EventID)
			}
			allIDs[e.EventID] = true
		}
		cursor = resp2.NextCursor
	}
	if len(allIDs) != 8 {
		t.Fatalf("expected 8 unique events across pages, got %d", len(allIDs))
	}
}

func TestQueryEvents_TransactionHashFilter(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.currentTxHash = "tx_alpha"
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "a", "", "b", 1, "core")
	store.emitEventWithEmitterLocked(wire.EventIntentCreated, nil, "c", "i1", "", 1, "core")
	store.currentTxHash = "tx_beta"
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "d", "", "e", 1, "core")
	store.currentTxHash = ""
	store.emitEventWithEmitterLocked(wire.EventIntentSettled, nil, "f", "", "", 1, "system")
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{TransactionHash: "tx_alpha"})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events for tx_alpha, got %d", len(resp.Events))
	}
	for _, e := range resp.Events {
		if e.TransactionHash != "tx_alpha" {
			t.Fatalf("expected tx_alpha, got %q", e.TransactionHash)
		}
	}

	resp = store.QueryEvents(EventFilter{TransactionHash: "tx_beta"})
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event for tx_beta, got %d", len(resp.Events))
	}
}

func TestQueryEvents_ExactHeightFilter(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "a", "", "b", 10, "core")
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "c", "", "d", 20, "core")
	store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "e", "", "f", 30, "core")
	store.mu.Unlock()

	resp := store.QueryEvents(EventFilter{ExactHeight: 20})
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event at height 20, got %d", len(resp.Events))
	}
	if resp.Events[0].BlockHeight != 20 {
		t.Fatalf("expected height 20, got %d", resp.Events[0].BlockHeight)
	}
}

func TestEventBus_PubSub(t *testing.T) {
	bus := NewEventBus()

	// Subscribe two clients.
	ch1 := bus.Subscribe()
	ch2 := bus.Subscribe()

	evt := wire.ChainEvent{EventID: 1, EventType: wire.EventTransfer}
	bus.Publish(evt)

	// Both should receive the event.
	select {
	case received := <-ch1:
		if received.EventID != 1 {
			t.Fatalf("ch1: expected event ID 1, got %d", received.EventID)
		}
	case <-time.After(time.Second):
		t.Fatal("ch1: expected event, got nothing (timeout)")
	}

	select {
	case received := <-ch2:
		if received.EventID != 1 {
			t.Fatalf("ch2: expected event ID 1, got %d", received.EventID)
		}
	case <-time.After(time.Second):
		t.Fatal("ch2: expected event, got nothing (timeout)")
	}

	// Unsubscribe ch1, publish again.
	bus.Unsubscribe(ch1)
	bus.Publish(wire.ChainEvent{EventID: 2, EventType: wire.EventTransfer})

	// ch1 is closed after unsubscribe — verify by reading zero value.
	if _, ok := <-ch1; ok {
		t.Fatal("ch1 should be closed after unsubscribe")
	}

	select {
	case received := <-ch2:
		if received.EventID != 2 {
			t.Fatalf("ch2: expected event ID 2, got %d", received.EventID)
		}
	case <-time.After(time.Second):
		t.Fatal("ch2: expected event, got nothing (timeout)")
	}

	bus.Unsubscribe(ch2)
}

func TestEmitEventWithEmitter_NextCursorInResponse(t *testing.T) {
	store := newTestStore(t)
	store.mu.Lock()
	for i := 0; i < 5; i++ {
		store.emitEventWithEmitterLocked(wire.EventTransfer, nil, "a", "", "b", 1, "core")
	}
	store.mu.Unlock()

	// Request with limit < total: should get NextCursor.
	resp := store.QueryEvents(EventFilter{Limit: 2})
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(resp.Events))
	}
	if !resp.HasMore {
		t.Fatal("expected HasMore=true")
	}
	if resp.NextCursor == 0 {
		t.Fatal("expected non-zero NextCursor when HasMore=true")
	}

	// Request all: NextCursor should be 0.
	resp = store.QueryEvents(EventFilter{Limit: 100})
	if resp.HasMore {
		t.Fatal("expected HasMore=false when all events returned")
	}
	if resp.NextCursor != 0 {
		t.Fatalf("expected NextCursor=0 when no more events, got %d", resp.NextCursor)
	}
}
