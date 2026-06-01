package chain

import (
	"encoding/json"
	"fmt"
	"time"

	"chain/internal/wire"
)

// processWasmCronJobsLocked iterates all registered cron jobs and executes
// those that are due. Called during produceBlockLocked before user tx selection.
// Must be called with s.mu held.
func (s *Store) processWasmCronJobsLocked(blockTime int64) {
	if len(s.data.WasmCronJobs) == 0 {
		return
	}

	for contractAddr, jobs := range s.data.WasmCronJobs {
		contract, ok := s.data.WasmContracts[contractAddr]
		if !ok || contract.Status != wire.WasmContractStatusActive {
			continue
		}

		for i, job := range jobs {
			if !job.Enabled {
				continue
			}
			if job.NextDueAtUnix > blockTime {
				continue
			}

			// Check if contract has enough balance for gas.
			if contract.Balance < wire.WasmDefaultCronGasReserve {
				// Skip execution if contract can't afford gas.
				continue
			}

			// Build the cron input: {"block_time": <unix>, "cron_method": "<name>"}
			input, _ := json.Marshal(map[string]any{
				"block_time":  blockTime,
				"cron_method": job.MethodName,
			})

			// Execute the cron method.
			resultData, gasUsed, stateDelta, execErr := s.executeWasmMethodLocked(
				contractAddr, job.MethodName, input, blockTime,
			)

			// Re-fetch contract after WASM execution — host functions called
			// during execution (exec_transfer, exec_create_intent, etc.) may
			// have modified the contract's balance in the map.
			contract = s.data.WasmContracts[contractAddr]

			// Charge gas from contract balance.
			gasCharged := gasUsed
			if gasCharged > contract.Balance {
				gasCharged = contract.Balance
			}
			contract.Balance -= gasCharged
			s.data.WasmContracts[contractAddr] = contract

			// Update cron job state.
			success := execErr == nil
			if success {
				job.FailureCount = 0
				job.LastExecutedAtUnix = blockTime
			} else {
				job.FailureCount++
				if job.FailureCount >= wire.WasmCronAutoDisable {
					job.Enabled = false
				}
			}
			job.NextDueAtUnix = blockTime + job.IntervalSeconds
			jobs[i] = job

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

		s.data.WasmCronJobs[contractAddr] = jobs
	}
}

// WasmCronExecPayload is defined in wire/types.go. Verify fields match.
var _ = fmt.Sprintf
var _ = time.Now
