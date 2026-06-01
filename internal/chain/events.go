package chain

import (
	"encoding/json"
	"sort"
	"time"

	"chain/internal/wire"
)

// maxChainEvents caps the append-only event log to prevent unbounded state growth.
const maxChainEvents = 100_000

// currentHeightLocked returns the current chain height derived from block count.
// Must be called while holding s.mu.
func (s *Store) currentHeightLocked() int64 {
	return int64(len(s.data.Blocks))
}

// emitEventWithEmitterLocked is the primary event emission function.
// It enriches the event with transient Store context (TransactionHash, LogIndex)
// and stamps the emitter module identifier.
// Must be called while holding s.mu.
func (s *Store) emitEventWithEmitterLocked(eventType string, payload any, relatedAddress, relatedIntentID, counterpartyAddress string, blockHeight int64, emitter string) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			raw = json.RawMessage("{}")
		} else {
			raw = b
		}
	}
	s.data.NextEventID++
	logIdx := s.blockLogIndex
	s.blockLogIndex++
	evt := wire.ChainEvent{
		EventID:             s.data.NextEventID,
		EventType:           eventType,
		Payload:             raw,
		CreatedAtUnix:       time.Now().Unix(),
		RelatedAddress:      relatedAddress,
		RelatedIntentID:     relatedIntentID,
		CounterpartyAddress: counterpartyAddress,
		BlockHeight:         blockHeight,
		TransactionHash:     s.currentTxHash,
		LogIndex:            logIdx,
		Emitter:             emitter,
	}
	s.data.ChainEvents = append(s.data.ChainEvents, evt)
	if len(s.data.ChainEvents) > maxChainEvents {
		s.data.ChainEvents = s.data.ChainEvents[len(s.data.ChainEvents)-maxChainEvents:]
	}
	if s.eventBus != nil {
		s.eventBus.Publish(evt)
	}
}

// emitEventFullLocked appends a system event with all fields to the chain event log.
// Convenience wrapper that delegates to emitEventWithEmitterLocked.
// Must be called while holding s.mu.
func (s *Store) emitEventFullLocked(eventType string, payload any, relatedAddress, relatedIntentID, counterpartyAddress string, blockHeight int64) {
	s.emitEventWithEmitterLocked(eventType, payload, relatedAddress, relatedIntentID, counterpartyAddress, blockHeight, "")
}

// emitEventLocked appends a system event to the chain event log.
// Convenience wrapper that delegates to emitEventFullLocked with auto-derived block height.
// Must be called while holding s.mu.
func (s *Store) emitEventLocked(eventType string, payload any, relatedAddress string, relatedIntentID string) {
	s.emitEventFullLocked(eventType, payload, relatedAddress, relatedIntentID, "", s.currentHeightLocked())
}

// backfillBlockMetadataLocked sets BlockHash and BlockTimestamp on events
// that were emitted during block processing (matching block height, empty hash).
// Also attaches events to their corresponding transaction receipts as Logs.
// Must be called while holding s.mu.
func (s *Store) backfillBlockMetadataLocked(block wire.Block) {
	height := int64(block.Height)
	txEvents := map[string][]wire.ChainEvent{}
	for i := len(s.data.ChainEvents) - 1; i >= 0; i-- {
		e := &s.data.ChainEvents[i]
		if e.BlockHeight < height {
			break
		}
		if e.BlockHeight == height && e.BlockHash == "" {
			e.BlockHash = block.Hash
			e.BlockTimestamp = block.TimeUnix
		}
		if e.BlockHeight == height && e.TransactionHash != "" {
			txEvents[e.TransactionHash] = append(txEvents[e.TransactionHash], *e)
		}
	}
	// Attach events to transaction receipts as Logs.
	for txHash, logs := range txEvents {
		if r, ok := s.data.Receipts[txHash]; ok {
			r.Logs = logs
			s.data.Receipts[txHash] = r
		}
	}
}

// EventFilter defines query parameters for filtering chain events.
type EventFilter struct {
	Type            string
	Address         string
	IntentID        string
	Since           int64
	Limit           int
	Counterparty    string
	MinHeight       int64
	MaxHeight       int64
	AfterEventID    uint64
	BeforeEventID   uint64
	TransactionHash string
	ExactHeight     int64
}

// eventMatchesFilter returns true if the event satisfies all active filter criteria.
func eventMatchesFilter(e wire.ChainEvent, f EventFilter) bool {
	if f.Type != "" && e.EventType != f.Type {
		return false
	}
	if f.Address != "" && e.RelatedAddress != f.Address && e.CounterpartyAddress != f.Address {
		return false
	}
	if f.IntentID != "" && e.RelatedIntentID != f.IntentID {
		return false
	}
	if f.Since > 0 && e.CreatedAtUnix < f.Since {
		return false
	}
	if f.Counterparty != "" && e.CounterpartyAddress != f.Counterparty {
		return false
	}
	if f.MinHeight > 0 && e.BlockHeight > 0 && e.BlockHeight < f.MinHeight {
		return false
	}
	if f.MaxHeight > 0 && e.BlockHeight > 0 && e.BlockHeight > f.MaxHeight {
		return false
	}
	if f.AfterEventID > 0 && e.EventID <= f.AfterEventID {
		return false
	}
	if f.BeforeEventID > 0 && e.EventID >= f.BeforeEventID {
		return false
	}
	if f.TransactionHash != "" && e.TransactionHash != f.TransactionHash {
		return false
	}
	if f.ExactHeight > 0 && e.BlockHeight != f.ExactHeight {
		return false
	}
	return true
}

// QueryEvents returns events matching the given filter, newest first.
func (s *Store) QueryEvents(f EventFilter) wire.ChainEventsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	// Collect matching events in reverse order (newest first).
	var matched []wire.ChainEvent
	events := s.data.ChainEvents
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if !eventMatchesFilter(e, f) {
			continue
		}
		matched = append(matched, e)
	}

	hasMore := len(matched) > limit
	if hasMore {
		matched = matched[:limit]
	}

	// Sort result by EventID ascending for stable output.
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].EventID < matched[j].EventID
	})

	if matched == nil {
		matched = []wire.ChainEvent{}
	}

	var nextCursor uint64
	if hasMore && len(matched) > 0 {
		// Cursor is the oldest event's ID in the batch. Client passes this
		// as BeforeEventID to paginate to older events (EventID < cursor).
		nextCursor = matched[0].EventID
	}

	return wire.ChainEventsResponse{
		Events:     matched,
		Total:      len(matched),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}
}
