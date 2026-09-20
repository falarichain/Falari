package chain

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"chain/internal/wire"
)

// H7: Per-block time budget for all WASM system execution (cron + events).
const wasmSystemBlockBudget = 5 * time.Second

// processWasmCronJobsLocked iterates all registered cron jobs and executes
// those that are due. Called during produceBlockLocked before user tx selection.
// Must be called with s.mu held. deadline caps total WASM system time per block.
func (s *Store) processWasmCronJobsLocked(blockTime int64, deadline time.Time) {
	if len(s.data.WasmCronJobs) == 0 {
		return
	}

	// C1: Snapshot contract addresses to avoid map iteration while WASM host
	// functions (register_cron / unregister_cron) may modify the map.
	addrs := make([]string, 0, len(s.data.WasmCronJobs))
	for addr := range s.data.WasmCronJobs {
		addrs = append(addrs, addr)
	}

	for _, contractAddr := range addrs {
		contract, ok := s.data.WasmContracts[contractAddr]
		if !ok || contract.Status != wire.WasmContractStatusActive {
			continue
		}

		jobs := s.data.WasmCronJobs[contractAddr]
		for i := range jobs {
			job := &jobs[i]
			// H7: Check per-block time budget before each execution.
			if time.Now().After(deadline) {
				log.Printf("wasm cron: per-block time budget exceeded, deferring remaining jobs")
				return
			}
			if !job.Enabled {
				continue
			}
			if job.NextDueAtUnix > blockTime {
				continue
			}

			// Check if contract has enough balance for gas.
			if contract.Balance < wire.WasmDefaultCronGasReserve {
				continue
			}

			// Build the cron input.
			input, _ := json.Marshal(map[string]any{
				"block_time":  blockTime,
				"cron_method": job.MethodName,
			})

			// Execute the cron method.
			resultData, gasUsed, stateDelta, execErr := s.executeWasmMethodLocked(
				contractAddr, job.MethodName, input, blockTime,
			)

			// Re-fetch contract after WASM execution.
			contract = s.data.WasmContracts[contractAddr]

			// Re-fetch jobs — host functions may have added/removed cron entries.
			liveJobs := s.data.WasmCronJobs[contractAddr]
			// C1: only update this specific job if it still exists at the same index.
			if i < len(liveJobs) && liveJobs[i].MethodName == job.MethodName {
				success := execErr == nil
				if success {
					liveJobs[i].FailureCount = 0
					liveJobs[i].LastExecutedAtUnix = blockTime
				} else {
					liveJobs[i].FailureCount++
					if liveJobs[i].FailureCount >= wire.WasmCronAutoDisable {
						liveJobs[i].Enabled = false
					}
				}
				liveJobs[i].NextDueAtUnix = blockTime + job.IntervalSeconds
			}

			// Charge gas from contract balance.
			gasCharged := gasUsed
			if gasCharged > contract.Balance {
				gasCharged = contract.Balance
			}
			contract.Balance -= gasCharged
			s.data.WasmContracts[contractAddr] = contract

			success := execErr == nil

			// Record the cron execution as a system transaction.
			payload := wasmCronExecTxPayload{
				Payload: wire.WasmCronExecPayload{
					ContractAddress: contractAddr,
					MethodName:      job.MethodName,
					BlockTimeUnix:   blockTime,
					GasUsed:         gasUsed,
					GasCharged:      gasCharged,
					Success:         success,
					Result:          resultData,
				},
				StateDelta: stateDelta,
			}
			if execErr != nil {
				payload.Payload.FailReason = execErr.Error()
			}

			s.recordTxLocked("wasm_cron_exec", contractAddr, payload)

			// Emit chain-level event.
			eventType := wire.EventContractCronExecuted
			if !success {
				eventType = wire.EventContractCronFailed
			}
			s.emitEventWithEmitterLocked(eventType, map[string]any{
				"contract_address": contractAddr,
				"method":           job.MethodName,
				"gas_used":         gasUsed,
				"success":          success,
			}, contractAddr, "", "", int64(len(s.data.Blocks)), contractAddr)
		}
	}
}

// WasmCronExecPayload is defined in wire/types.go. Verify fields match.
var _ = fmt.Sprintf
var _ = time.Now
