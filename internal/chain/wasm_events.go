package chain

import (
	"encoding/json"

	"chain/internal/wire"
)

// deliverWasmPendingEventsLocked processes all pending WASM events that are due
// for delivery at the current block. For each subscriber, it calls the
// subscriber's "handle_event" method with the event data as input.
// Must be called with s.mu held.
func (s *Store) deliverWasmPendingEventsLocked(blockTime int64) {
	if len(s.data.WasmPendingEvents) == 0 {
		return
	}

	blockHeight := uint64(len(s.data.Blocks))
	remaining := make([]wire.WasmPendingEventDelivery, 0)

	for _, pending := range s.data.WasmPendingEvents {
		// Only deliver events that are due at or before the current block.
		if pending.DeliveryBlock > blockHeight {
			remaining = append(remaining, pending)
			continue
		}

		// Deliver to each subscriber.
		for _, subAddr := range pending.Subscribers {
			contract, ok := s.data.WasmContracts[subAddr]
			if !ok || contract.Status != wire.WasmContractStatusActive {
				continue // skip inactive subscribers
			}

			// Skip if contract can't afford gas.
			if contract.Balance < wire.WasmDefaultCronGasReserve {
				continue
			}

			// Build event input for the subscriber's handle_event method.
			eventInput, _ := json.Marshal(map[string]any{
				"emitter_address": pending.Event.EmitterAddress,
				"event_type":      pending.Event.EventType,
				"attributes":      pending.Event.Attributes,
				"emitted_at_block": pending.Event.EmittedAtBlock,
			})

			// Execute handle_event on the subscriber contract.
			resultData, gasUsed, stateDelta, execErr := s.executeWasmMethodLocked(
				subAddr, "handle_event", eventInput, blockTime,
			)

			// Re-fetch contract after WASM execution — host functions called
			// during execution may have modified the contract's balance.
			contract = s.data.WasmContracts[subAddr]

			// Charge gas from subscriber balance.
			gasCharged := gasUsed
			if gasCharged > contract.Balance {
				gasCharged = contract.Balance
			}
			contract.Balance -= gasCharged
			s.data.WasmContracts[subAddr] = contract

			success := execErr == nil

			// Record the event delivery as a system transaction.
			deliveryPayload := wasmEventDeliveryTxPayload{
				Payload: wire.WasmEventDeliveryPayload{
					SubscriberAddress: subAddr,
					Event:             pending.Event,
					Result:            resultData,
					GasUsed:           gasUsed,
					GasCharged:        gasCharged,
					Success:           success,
				},
				StateDelta: stateDelta,
			}
			if execErr != nil {
				deliveryPayload.Payload.FailReason = execErr.Error()
			}

			s.recordTxLocked("wasm_event_delivery", subAddr, deliveryPayload)

			// Emit chain-level event for indexing.
			chainEventType := wire.EventContractEventDelivered
			if !success {
				chainEventType = wire.EventContractEventDeliveryFailed
			}
			s.emitEventWithEmitterLocked(chainEventType, map[string]any{
				"subscriber_address": subAddr,
				"emitter_address":    pending.Event.EmitterAddress,
				"event_type":         pending.Event.EventType,
				"success":            success,
				"gas_used":           gasUsed,
			}, subAddr, "", pending.Event.EmitterAddress, int64(blockHeight), subAddr)
		}
	}

	s.data.WasmPendingEvents = remaining
}
