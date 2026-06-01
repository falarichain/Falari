package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"chain/internal/wasm"
	"chain/internal/wire"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Local constants for host API limits not defined in wire package.
const (
	maxWasmLogLen       = 1024
	maxWasmKVIterLimit  = 500
	maxWasmEventDataLen = 4096
)

// Gas costs for host function calls. These are charged from the per-call
// GasMeter; exceeding the limit traps the WASM execution.
const (
	// Simple read-only queries — O(1) map lookups.
	gasReadSimple uint64 = 1000

	// Heavier queries that iterate or build larger responses.
	gasReadHeavy uint64 = 3000

	// KV operations.
	gasKVGet    uint64 = 2000
	gasKVSet    uint64 = 5000
	gasKVSetBPS uint64 = 10 // per byte of (key+value)
	gasKVDelete uint64 = 1000
	gasKVKeys   uint64 = 3000
	gasKVHas    uint64 = 1000

	// State-changing operations.
	gasTransfer        uint64 = 10000
	gasCreateIntent    uint64 = 8000
	gasRenewDeal       uint64 = 10000
	gasTerminateDeal   uint64 = 8000
	gasCreateColl      uint64 = 8000
	gasAppendRecord    uint64 = 5000
	gasAppendRecordBPS uint64 = 10 // per byte of metadata

	// Event / subscription / cron.
	gasEmitEvent    uint64 = 5000
	gasLogMsg       uint64 = 1000
	gasSubscribe    uint64 = 3000
	gasRegisterCron uint64 = 3000
	gasUnregCron    uint64 = 1000

	// Memory write cost (per byte written to WASM memory).
	gasWriteBPS uint64 = 5
)

// ── Context helpers for WASM host functions ──

type wasmContractAddrKey struct{}
type wasmBlockTimeKey struct{}
type wasmEventCounterKey struct{}

// withContractAddress returns a context carrying the calling contract address.
func withContractAddress(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, wasmContractAddrKey{}, addr)
}

// contractAddressFromContext extracts the calling contract address from context.
func contractAddressFromContext(ctx context.Context) string {
	if addr, ok := ctx.Value(wasmContractAddrKey{}).(string); ok {
		return addr
	}
	return ""
}

// withBlockTime returns a context carrying the block timestamp.
func withBlockTime(ctx context.Context, t int64) context.Context {
	return context.WithValue(ctx, wasmBlockTimeKey{}, t)
}

// blockTimeFromContext extracts the block timestamp from context.
func blockTimeFromContext(ctx context.Context) int64 {
	if t, ok := ctx.Value(wasmBlockTimeKey{}).(int64); ok {
		return t
	}
	return 0
}

// wasmEventCounter tracks per-call event emission count (mutable via pointer).
type wasmEventCounter struct {
	count int
	max   int
}

// withEventCounter returns a context carrying a fresh event counter.
func withEventCounter(ctx context.Context, max int) context.Context {
	return context.WithValue(ctx, wasmEventCounterKey{}, &wasmEventCounter{max: max})
}

// eventCounterFromContext extracts the event counter from context.
func eventCounterFromContext(ctx context.Context) *wasmEventCounter {
	if c, ok := ctx.Value(wasmEventCounterKey{}).(*wasmEventCounter); ok {
		return c
	}
	return nil
}

// consumeGas charges the given amount from the context's GasMeter.
// If gas is exceeded, it panics — wazero catches panics in host functions
// and converts them to WASM traps, cleanly aborting the contract call.
func consumeGas(ctx context.Context, amount uint64) {
	gm := wasm.GasMeterFromContext(ctx)
	if gm == nil {
		return
	}
	if err := gm.Consume(amount); err != nil {
		panic(err.Error())
	}
}

// ── registerHostFunctions implements the HostFunctionRegistrar callback ──

// registerHostFunctions registers all host API functions on the wazero "env"
// module builder. These functions are available to WASM contracts as imports
// from the "env" module.
//
// IMPORTANT: all host functions execute while s.mu is held by the caller
// (DeployContract, CallContract, or executeWasmMethodLocked). They must NOT
// call any Store method that re-acquires the mutex.
func (s *Store) registerHostFunctions(builder wazero.HostModuleBuilder) {

	// ── Read-only queries ──

	// self_address() -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module) uint64 {
			consumeGas(ctx, gasReadSimple)
			addr := contractAddressFromContext(ctx)
			return writeResult(ctx, mod, []byte(addr))
		}).
		Export("self_address")

	// self_balance() -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module) uint64 {
			consumeGas(ctx, gasReadSimple)
			addr := contractAddressFromContext(ctx)
			contract := s.data.WasmContracts[addr]
			var bal uint64
			if contract != nil {
				bal = contract.Balance
			}
			data, _ := json.Marshal(map[string]uint64{"balance": bal})
			return writeResult(ctx, mod, data)
		}).
		Export("self_balance")

	// query_account_balance(addr_ptr, addr_len) -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasReadSimple)
			addr := readString(mod, uint32(ptr), uint32(length))
			addr = wire.NormalizeAddress(addr)
			account := s.accountLocked(addr)
			data, _ := json.Marshal(map[string]any{
				"address":                account.Address,
				"balance":                account.Balance,
				"nonce":                  account.Nonce,
				"locked_stake":           account.LockedStake,
				"locked_storage":         account.LockedStorage,
				"unbonding_balance":      account.UnbondingBalance,
				"pending_mining_rewards": account.PendingMiningRewards,
			})
			return writeResult(ctx, mod, data)
		}).
		Export("query_account_balance")

	// query_intent(intent_id_ptr, intent_id_len) -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasReadSimple)
			intentID := readString(mod, uint32(ptr), uint32(length))
			intent, ok := s.data.Intents[intentID]
			if !ok {
				return writeResult(ctx, mod, errorJSON("intent not found"))
			}
			data, _ := json.Marshal(intent.IntentView)
			return writeResult(ctx, mod, data)
		}).
		Export("query_intent")

	// query_miner(addr_ptr, addr_len) -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasReadSimple)
			addr := readString(mod, uint32(ptr), uint32(length))
			addr = wire.NormalizeAddress(addr)
			miner, ok := s.data.Miners[addr]
			if !ok {
				return writeResult(ctx, mod, errorJSON("miner not found"))
			}
			data, _ := json.Marshal(map[string]any{
				"miner_address":             miner.MinerAddress,
				"capacity_bytes":            miner.CapacityBytes,
				"used_bytes":                miner.UsedBytes,
				"stake":                     miner.Stake,
				"status":                    miner.Status,
				"proof_success":             miner.ProofSuccess,
				"proof_failure":             miner.ProofFailure,
				"rewards":                   miner.Rewards,
				"endpoint":                  miner.Endpoint,
				"access_service_required":   miner.AccessServiceRequired,
				"upload_service_enabled":    miner.UploadServiceEnabled,
				"download_service_enabled":  miner.DownloadServiceEnabled,
			})
			return writeResult(ctx, mod, data)
		}).
		Export("query_miner")

	// query_epoch(epoch_id_ptr, epoch_id_len) -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasReadSimple)
			epochID := readString(mod, uint32(ptr), uint32(length))
			epoch, ok := s.data.Epochs[epochID]
			if !ok {
				return writeResult(ctx, mod, errorJSON("epoch not found"))
			}
			data, _ := json.Marshal(map[string]any{
				"epoch_id":         epoch.EpochID,
				"epoch_round":      epoch.EpochRound,
				"intent_id":        epoch.IntentID,
				"started_at_unix":  epoch.StartedAtUnix,
				"deadline_unix":    epoch.DeadlineUnix,
				"status":           epoch.Status,
				"reward_per_proof": epoch.RewardPerProof,
				"challenge_count":  len(epoch.ChallengeIDs),
			})
			return writeResult(ctx, mod, data)
		}).
		Export("query_epoch")

	// query_storage_pricing() -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module) uint64 {
			consumeGas(ctx, gasReadSimple)
			sp := s.data.StoragePricing
			data, _ := json.Marshal(map[string]uint64{
				"base_price":         sp.BasePrice,
				"minimum_fee":        sp.MinimumFee,
				"permanent_duration": uint64(sp.PermanentDuration),
			})
			return writeResult(ctx, mod, data)
		}).
		Export("query_storage_pricing")

	// query_fee_market() -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module) uint64 {
			consumeGas(ctx, gasReadSimple)
			fm := s.data.FeeMarket
			data, _ := json.Marshal(map[string]any{
				"base_fee":          fm.BaseFee,
				"target_block_txs":  fm.TargetBlockTxs,
				"last_block_txs":    fm.LastBlockTxs,
				"updated_at_height": fm.UpdatedAtHeight,
			})
			return writeResult(ctx, mod, data)
		}).
		Export("query_fee_market")

	// query_block_height() -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module) uint64 {
			consumeGas(ctx, gasReadSimple)
			height := uint64(len(s.data.Blocks))
			data, _ := json.Marshal(map[string]uint64{"height": height})
			return writeResult(ctx, mod, data)
		}).
		Export("query_block_height")

	// query_block_time() -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module) uint64 {
			consumeGas(ctx, gasReadSimple)
			t := blockTimeFromContext(ctx)
			if t == 0 && len(s.data.Blocks) > 0 {
				t = s.data.Blocks[len(s.data.Blocks)-1].TimeUnix
			}
			data, _ := json.Marshal(map[string]int64{"block_time": t})
			return writeResult(ctx, mod, data)
		}).
		Export("query_block_time")

	// query_contract(addr_ptr, addr_len) -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasReadSimple)
			addr := readString(mod, uint32(ptr), uint32(length))
			addr = wire.NormalizeAddress(addr)
			contract, ok := s.data.WasmContracts[addr]
			if !ok {
				return writeResult(ctx, mod, errorJSON("contract not found"))
			}
			data, _ := json.Marshal(map[string]any{
				"address":         contract.Address,
				"code_hash":       contract.CodeHash,
				"admin":           contract.Admin,
				"label":           contract.Label,
				"balance":         contract.Balance,
				"status":          contract.Status,
				"created_at_unix": contract.CreatedAtUnix,
			})
			return writeResult(ctx, mod, data)
		}).
		Export("query_contract")

	// log_msg(msg_ptr, msg_len) -> void
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) {
			consumeGas(ctx, gasLogMsg)
			// Enforce per-call event/log emission limit.
			counter := eventCounterFromContext(ctx)
			if counter != nil {
				if counter.count >= counter.max {
					return // silently drop if over limit
				}
				counter.count++
			}
			msg := readString(mod, uint32(ptr), uint32(length))
			addr := contractAddressFromContext(ctx)
			if len(msg) > maxWasmLogLen {
				msg = msg[:maxWasmLogLen]
			}
			s.emitEventWithEmitterLocked("contract_log", map[string]any{
				"contract_address": addr,
				"message":          msg,
			}, addr, "", "", int64(len(s.data.Blocks)), addr)
		}).
		Export("log_msg")

	// ── Extended read-only queries (public on-chain data) ──

	// query_collection(coll_id_ptr, coll_id_len) -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasReadSimple)
			collID := readString(mod, uint32(ptr), uint32(length))
			coll, ok := s.data.Collections[collID]
			if !ok {
				return writeResult(ctx, mod, errorJSON("collection not found"))
			}
			data, _ := json.Marshal(map[string]any{
				"collection_id":  coll.CollectionID,
				"user":           coll.User,
				"name":           coll.Name,
				"description":    coll.Description,
				"metadata":       coll.Metadata,
				"created_at_unix": coll.CreatedAtUnix,
				"record_count":   len(s.data.CollectionRecords[collID]),
			})
			return writeResult(ctx, mod, data)
		}).
		Export("query_collection")

	// query_collection_records(coll_id_ptr, coll_id_len) -> packed_i64
	// Returns a list of record summaries for the given collection.
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasReadHeavy)
			collID := readString(mod, uint32(ptr), uint32(length))
			recordIDs := s.data.CollectionRecords[collID]
			if recordIDs == nil {
				return writeResult(ctx, mod, []byte("[]"))
			}

			// Build record summaries (cap at 200 to avoid oversized results).
			type recordSummary struct {
				RecordID      string            `json:"record_id"`
				Kind          string            `json:"kind,omitempty"`
				Key           string            `json:"key,omitempty"`
				User          string            `json:"user"`
				FileRoot      string            `json:"file_root"`
				Metadata      map[string]string `json:"metadata,omitempty"`
				CreatedAtUnix int64             `json:"created_at_unix"`
			}
			limit := len(recordIDs)
			if limit > 200 {
				limit = 200
			}
			summaries := make([]recordSummary, 0, limit)
			for i := 0; i < limit; i++ {
				rec, ok := s.data.DataRecords[recordIDs[i]]
				if !ok {
					continue
				}
				summaries = append(summaries, recordSummary{
					RecordID:      rec.RecordID,
					Kind:          rec.Kind,
					Key:           rec.Key,
					User:          rec.User,
					FileRoot:      rec.FileRoot,
					Metadata:      rec.Metadata,
					CreatedAtUnix: rec.CreatedAtUnix,
				})
			}
			data, _ := json.Marshal(summaries)
			return writeResult(ctx, mod, data)
		}).
		Export("query_collection_records")

	// query_record(record_id_ptr, record_id_len) -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasReadSimple)
			recordID := readString(mod, uint32(ptr), uint32(length))
			rec, ok := s.data.DataRecords[recordID]
			if !ok {
				return writeResult(ctx, mod, errorJSON("record not found"))
			}
			data, _ := json.Marshal(map[string]any{
				"record_id":       rec.RecordID,
				"collection_id":   rec.CollectionID,
				"user":            rec.User,
				"intent_id":       rec.IntentID,
				"deal_id":         rec.DealID,
				"parent_record":   rec.ParentRecord,
				"kind":            rec.Kind,
				"key":             rec.Key,
				"file_root":       rec.FileRoot,
				"manifest_root":   rec.ManifestRoot,
				"metadata":        rec.Metadata,
				"created_at_unix": rec.CreatedAtUnix,
			})
			return writeResult(ctx, mod, data)
		}).
		Export("query_record")

	// query_contract_kv(addr_ptr, addr_len, key_ptr, key_len) -> packed_i64
	// Reads a key from another contract's KV store (public read access).
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, aPtr, aLen, kPtr, kLen uint64) uint64 {
			consumeGas(ctx, gasReadSimple)
			targetAddr := readString(mod, uint32(aPtr), uint32(aLen))
			targetAddr = wire.NormalizeAddress(targetAddr)
			key := readString(mod, uint32(kPtr), uint32(kLen))

			// Target must be an active contract with public KV.
			contract, ok := s.data.WasmContracts[targetAddr]
			if !ok || contract.Status != wire.WasmContractStatusActive {
				return writeResult(ctx, mod, errorJSON("contract not found"))
			}
			if !contract.PublicKV {
				return writeResult(ctx, mod, errorJSON("contract KV is not public"))
			}

			kv := s.data.WasmKVStore[targetAddr]
			if kv == nil {
				return 0
			}
			val, ok := kv[key]
			if !ok {
				return 0
			}
			return writeResult(ctx, mod, []byte(val))
		}).
		Export("query_contract_kv")

	// query_deal_escrow(deal_id_ptr, deal_id_len) -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasReadSimple)
			dealID := readString(mod, uint32(ptr), uint32(length))
			escrow, ok := s.data.DealEscrows[dealID]
			if !ok {
				return writeResult(ctx, mod, errorJSON("deal escrow not found"))
			}
			data, _ := json.Marshal(map[string]any{
				"intent_id":           escrow.IntentID,
				"user":                escrow.User,
				"locked_fee":          escrow.LockedFee,
				"paid_fee":            escrow.PaidFee,
				"accrued_fee":         escrow.AccruedFee,
				"status":              escrow.Status,
				"permanent":           escrow.Permanent,
				"start_at_unix":       escrow.StartAtUnix,
				"expires_at_unix":     escrow.ExpiresAtUnix,
				"last_accrued_at_unix": escrow.LastAccruedAtUnix,
			})
			return writeResult(ctx, mod, data)
		}).
		Export("query_deal_escrow")

	// query_storage_providers(intent_id_ptr, intent_id_len) -> packed_i64
	// Returns the miner addresses and endpoints storing the given intent's data.
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasReadHeavy)
			intentID := readString(mod, uint32(ptr), uint32(length))
			intent, ok := s.data.Intents[intentID]
			if !ok {
				return writeResult(ctx, mod, errorJSON("intent not found"))
			}

			type providerInfo struct {
				MinerAddress string `json:"miner_address"`
				Endpoint     string `json:"endpoint,omitempty"`
			}

			// Collect unique providers from intent assignments and receipts.
			seen := map[string]bool{}
			providers := make([]providerInfo, 0)

			// From assignments.
			for _, a := range intent.Assignments {
				if a.MinerAddress != "" && !seen[a.MinerAddress] {
					seen[a.MinerAddress] = true
					ep := ""
					if miner, ok := s.data.Miners[a.MinerAddress]; ok {
						ep = miner.Endpoint
					}
					providers = append(providers, providerInfo{
						MinerAddress: a.MinerAddress,
						Endpoint:     ep,
					})
				}
			}

			// From receipts.
			for _, shardReceipts := range intent.Receipts {
				for _, r := range shardReceipts {
					if r.MinerAddress != "" && !seen[r.MinerAddress] {
						seen[r.MinerAddress] = true
						providers = append(providers, providerInfo{
							MinerAddress: r.MinerAddress,
							Endpoint:     r.MinerEndpoint,
						})
					}
				}
			}

			// Cap at 50 providers.
			if len(providers) > 50 {
				providers = providers[:50]
			}

			data, _ := json.Marshal(map[string]any{
				"intent_id": intentID,
				"status":    intent.Status,
				"providers": providers,
			})
			return writeResult(ctx, mod, data)
		}).
		Export("query_storage_providers")

	// ── KV operations ──

	// kv_get(key_ptr, key_len) -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasKVGet)
			addr := contractAddressFromContext(ctx)
			key := readString(mod, uint32(ptr), uint32(length))
			kv := s.data.WasmKVStore[addr]
			if kv == nil {
				return 0 // null = key not found
			}
			val, ok := kv[key]
			if !ok {
				return 0
			}
			return writeResult(ctx, mod, []byte(val))
		}).
		Export("kv_get")

	// kv_set(key_ptr, key_len, val_ptr, val_len) -> i32
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, kPtr, kLen, vPtr, vLen uint64) uint32 {
			consumeGas(ctx, gasKVSet)
			addr := contractAddressFromContext(ctx)
			key := readString(mod, uint32(kPtr), uint32(kLen))
			val := readString(mod, uint32(vPtr), uint32(vLen))

			if len(key) == 0 || len(key) > wire.MaxWasmKVKeyBytes {
				return 1
			}
			if len(val) > wire.MaxWasmKVValueBytes {
				return 1
			}

			kv := s.data.WasmKVStore[addr]
			if kv == nil {
				kv = map[string]string{}
				s.data.WasmKVStore[addr] = kv
			}

			// Check entry limit (only for new keys).
			if _, exists := kv[key]; !exists {
				if len(kv) >= wire.MaxWasmKVEntries {
					return 2 // over limit
				}
			}

			// Charge per-byte cost for the write.
			consumeGas(ctx, gasKVSetBPS*uint64(len(key)+len(val)))
			kv[key] = val
			return 0
		}).
		Export("kv_set")

	// kv_delete(key_ptr, key_len) -> i32
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint32 {
			consumeGas(ctx, gasKVDelete)
			addr := contractAddressFromContext(ctx)
			key := readString(mod, uint32(ptr), uint32(length))
			kv := s.data.WasmKVStore[addr]
			if kv == nil {
				return 0
			}
			delete(kv, key)
			return 0
		}).
		Export("kv_delete")

	// kv_keys(prefix_ptr, prefix_len) -> packed_i64
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint64 {
			consumeGas(ctx, gasKVKeys)
			addr := contractAddressFromContext(ctx)
			prefix := readString(mod, uint32(ptr), uint32(length))
			kv := s.data.WasmKVStore[addr]
			if kv == nil {
				return writeResult(ctx, mod, []byte("[]"))
			}

			keys := make([]string, 0)
			for k := range kv {
				if strings.HasPrefix(k, prefix) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)

			if len(keys) > maxWasmKVIterLimit {
				keys = keys[:maxWasmKVIterLimit]
			}

			data, _ := json.Marshal(keys)
			return writeResult(ctx, mod, data)
		}).
		Export("kv_keys")

	// kv_has(key_ptr, key_len) -> i32
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint32 {
			consumeGas(ctx, gasKVHas)
			addr := contractAddressFromContext(ctx)
			key := readString(mod, uint32(ptr), uint32(length))
			kv := s.data.WasmKVStore[addr]
			if kv == nil {
				return 0
			}
			if _, ok := kv[key]; ok {
				return 1
			}
			return 0
		}).
		Export("kv_has")

	// ── Controlled writes ──

	// exec_transfer(to_ptr, to_len, amount) -> i32
	// Transfers FAL from the calling contract's balance to a target address.
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length, amount uint64) uint32 {
			consumeGas(ctx, gasTransfer)
			addr := contractAddressFromContext(ctx)
			contract := s.data.WasmContracts[addr]
			if contract == nil || contract.Status != wire.WasmContractStatusActive {
				return 1
			}
			if amount == 0 {
				return 2 // zero transfer not allowed
			}
			if amount > contract.Balance {
				return 2 // insufficient balance
			}

			to := readString(mod, uint32(ptr), uint32(length))
			to = wire.NormalizeAddress(to)
			if to == "" {
				return 3
			}
			if !wire.IsValidAddress(to) {
				return 3 // invalid hex address
			}
			if strings.EqualFold(to, addr) {
				return 0 // self-transfer is no-op
			}

			contract.Balance -= amount
			s.data.WasmContracts[addr] = contract

			target := s.accountLocked(to)
			target.Balance += amount
			s.data.Accounts[target.Address] = target

			return 0
		}).
		Export("exec_transfer")

	// emit_event(event_type_ptr, event_type_len, attrs_json_ptr, attrs_json_len) -> i32
	// Emits a typed contract event with key-value attributes.
	// Other contracts can subscribe to events by emitter address and event type.
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, tPtr, tLen, aPtr, aLen uint64) uint32 {
			consumeGas(ctx, gasEmitEvent)
			addr := contractAddressFromContext(ctx)
			eventType := readString(mod, uint32(tPtr), uint32(tLen))
			attrsJSON := readString(mod, uint32(aPtr), uint32(aLen))

			if eventType == "" || len(eventType) > wire.MaxWasmEventTypeLen {
				return 1
			}
			if len(attrsJSON) > maxWasmEventDataLen {
				return 2
			}

			// Enforce per-call event emission limit via context counter.
			counter := eventCounterFromContext(ctx)
			if counter != nil {
				if counter.count >= counter.max {
					return 4 // too many events per call
				}
				counter.count++
			}

			// Parse attributes (must be a JSON object with string values).
			attributes := map[string]string{}
			if attrsJSON != "" {
				if err := json.Unmarshal([]byte(attrsJSON), &attributes); err != nil {
					return 3 // invalid attributes JSON
				}
				if len(attributes) > wire.MaxWasmEventAttributes {
					return 5 // too many attributes
				}
				for k, v := range attributes {
					if len(k) > wire.MaxWasmEventAttrKeyLen || len(v) > wire.MaxWasmEventAttrValLen {
						return 6 // attribute key/value too long
					}
				}
			}

			blockHeight := uint64(len(s.data.Blocks))

			event := wire.WasmContractEvent{
				EmitterAddress: addr,
				EventType:      eventType,
				Attributes:     attributes,
				EmittedAtBlock: blockHeight,
				DeliveryBlock:  blockHeight + 1, // deliver in next block
			}

			// Queue for delivery to subscribers in the next block.
			s.enqueueWasmEventLocked(event)

			// Emit chain-level event for indexing.
			s.emitEventWithEmitterLocked(wire.EventContractEventEmitted, map[string]any{
				"contract_address": addr,
				"event_type":       eventType,
			}, addr, "", "", int64(blockHeight), addr)

			return 0
		}).
		Export("emit_event")

	// subscribe_event(emitter_ptr, emitter_len, event_type_ptr, event_type_len) -> i32
	// Subscribes the calling contract to events from the given emitter and type.
	// When a matching event is emitted, the subscriber's "handle_event" method
	// is called in the next block with the event JSON as input.
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ePtr, eLen, tPtr, tLen uint64) uint32 {
			consumeGas(ctx, gasSubscribe)
			addr := contractAddressFromContext(ctx)
			emitterAddr := readString(mod, uint32(ePtr), uint32(eLen))
			eventType := readString(mod, uint32(tPtr), uint32(tLen))

			emitterAddr = wire.NormalizeAddress(emitterAddr)
			if emitterAddr == "" || eventType == "" {
				return 1
			}
			if len(eventType) > wire.MaxWasmEventTypeLen {
				return 2
			}

			subs := s.data.WasmEventSubscriptions[addr]
			if len(subs) >= wire.MaxWasmSubscriptions {
				return 3 // over limit
			}

			// Check duplicate.
			for _, sub := range subs {
				if strings.EqualFold(sub.EmitterAddress, emitterAddr) &&
					sub.EventTypeFilter == eventType {
					return 4 // already subscribed
				}
			}

			blockTime := blockTimeFromContext(ctx)
			if blockTime == 0 && len(s.data.Blocks) > 0 {
				blockTime = s.data.Blocks[len(s.data.Blocks)-1].TimeUnix
			}

			sub := wire.WasmEventSubscription{
				SubscriberAddress: addr,
				EmitterAddress:    emitterAddr,
				EventTypeFilter:   eventType,
				CreatedAtUnix:     blockTime,
			}
			s.data.WasmEventSubscriptions[addr] = append(subs, sub)
			return 0
		}).
		Export("subscribe_event")

	// register_cron(method_ptr, method_len, interval_secs) -> i32
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length, intervalSecs uint64) uint32 {
			consumeGas(ctx, gasRegisterCron)
			addr := contractAddressFromContext(ctx)
			method := readString(mod, uint32(ptr), uint32(length))

			if method == "" || len(method) > wire.MaxWasmMethodNameLen {
				return 1
			}
			if intervalSecs < wire.MinWasmCronIntervalSecs {
				return 2
			}
			// Guard against uint64 → int64 overflow.
			if intervalSecs > uint64(math.MaxInt64) {
				return 2
			}

			blockTime := blockTimeFromContext(ctx)
			if blockTime == 0 && len(s.data.Blocks) > 0 {
				blockTime = s.data.Blocks[len(s.data.Blocks)-1].TimeUnix
			}
			if blockTime == 0 {
				blockTime = 1
			}

			spec := wire.WasmCronJobSpec{
				MethodName:      method,
				IntervalSeconds: int64(intervalSecs),
			}
			err := s.registerWasmCronLocked(addr, spec, blockTime)
			if err != nil {
				if strings.Contains(err.Error(), "max") {
					return 3
				}
				if strings.Contains(err.Error(), "duplicate") {
					return 4
				}
				return 5
			}
			return 0
		}).
		Export("register_cron")

	// unregister_cron(method_ptr, method_len) -> i32
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint32 {
			consumeGas(ctx, gasUnregCron)
			addr := contractAddressFromContext(ctx)
			method := readString(mod, uint32(ptr), uint32(length))

			jobs := s.data.WasmCronJobs[addr]
			found := false
			filtered := make([]wire.WasmCronJob, 0, len(jobs))
			for _, j := range jobs {
				if j.MethodName == method {
					found = true
					continue
				}
				filtered = append(filtered, j)
			}
			if !found {
				return 1
			}
			s.data.WasmCronJobs[addr] = filtered
			return 0
		}).
		Export("unregister_cron")

	// ── Chain operations (simplified implementations) ──

	// exec_create_intent creates a minimal storage intent from contract funds.
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, paramsPtr, paramsLen uint64) uint64 {
			consumeGas(ctx, gasCreateIntent)
			addr := contractAddressFromContext(ctx)
			contract := s.data.WasmContracts[addr]
			if contract == nil || contract.Status != wire.WasmContractStatusActive {
				return writeResult(ctx, mod, errorJSON("contract not active"))
			}

			paramsRaw := readString(mod, uint32(paramsPtr), uint32(paramsLen))
			var params struct {
				FileName  string `json:"file_name"`
				FileSize  int64  `json:"file_size"`
				LockedFee uint64 `json:"locked_fee"`
			}
			if err := json.Unmarshal([]byte(paramsRaw), &params); err != nil {
				return writeResult(ctx, mod, errorJSON("invalid params"))
			}
			if params.LockedFee > contract.Balance {
				return writeResult(ctx, mod, errorJSON("insufficient balance"))
			}

			contract.Balance -= params.LockedFee
			s.data.WasmContracts[addr] = contract

			intentID := fmt.Sprintf("wasm-%s-%d", addr, s.data.WasmNonce)
			s.data.WasmNonce++
			s.data.Intents[intentID] = &Intent{
				IntentView: wire.IntentView{
					IntentID:  intentID,
					User:      addr,
					FileName:  params.FileName,
					FileSize:  params.FileSize,
					LockedFee: params.LockedFee,
					Status:    wire.StatusUploading,
				},
			}

			data, _ := json.Marshal(map[string]string{
				"intent_id": intentID,
				"status":    wire.StatusUploading,
			})
			return writeResult(ctx, mod, data)
		}).
		Export("exec_create_intent")

	// exec_renew_deal extends a storage deal by adding funds to its escrow.
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, dealPtr, dealLen, amount uint64) uint32 {
			consumeGas(ctx, gasRenewDeal)
			addr := contractAddressFromContext(ctx)
			contract := s.data.WasmContracts[addr]
			if contract == nil || contract.Status != wire.WasmContractStatusActive {
				return 1
			}
			if amount > contract.Balance {
				return 2
			}

			dealID := readString(mod, uint32(dealPtr), uint32(dealLen))
			escrow, ok := s.data.DealEscrows[dealID]
			if !ok {
				return 3
			}

			contract.Balance -= amount
			s.data.WasmContracts[addr] = contract
			escrow.LockedFee += amount
			s.data.DealEscrows[dealID] = escrow
			return 0
		}).
		Export("exec_renew_deal")

	// exec_terminate_deal terminates a storage deal.
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint64) uint32 {
			consumeGas(ctx, gasTerminateDeal)
			addr := contractAddressFromContext(ctx)
			dealID := readString(mod, uint32(ptr), uint32(length))

			escrow, ok := s.data.DealEscrows[dealID]
			if !ok {
				return 1
			}
			if !strings.EqualFold(escrow.User, addr) {
				return 2
			}
			escrow.Status = "terminated"
			s.data.DealEscrows[dealID] = escrow
			return 0
		}).
		Export("exec_terminate_deal")

	// exec_create_collection creates a new data collection owned by the contract.
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, paramsPtr, paramsLen uint64) uint64 {
			consumeGas(ctx, gasCreateColl)
			addr := contractAddressFromContext(ctx)
			contract := s.data.WasmContracts[addr]
			if contract == nil || contract.Status != wire.WasmContractStatusActive {
				return writeResult(ctx, mod, errorJSON("contract not active"))
			}

			paramsRaw := readString(mod, uint32(paramsPtr), uint32(paramsLen))
			var params struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal([]byte(paramsRaw), &params); err != nil {
				return writeResult(ctx, mod, errorJSON("invalid params"))
			}

			collID := fmt.Sprintf("wasm-col-%s-%d", addr, s.data.WasmNonce)
			s.data.WasmNonce++

			blockTime := blockTimeFromContext(ctx)
			if blockTime == 0 && len(s.data.Blocks) > 0 {
				blockTime = s.data.Blocks[len(s.data.Blocks)-1].TimeUnix
			}

			s.data.Collections[collID] = wire.DataCollection{
				CollectionID:  collID,
				User:          addr,
				Name:          params.Name,
				Description:   params.Description,
				CreatedAtUnix: blockTime,
				UpdatedAtUnix: blockTime,
			}

			data, _ := json.Marshal(map[string]string{
				"collection_id": collID,
			})
			return writeResult(ctx, mod, data)
		}).
		Export("exec_create_collection")

	// exec_append_record appends a record to a data collection.
	builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, collPtr, collLen, metaPtr, metaLen uint64) uint32 {
			consumeGas(ctx, gasAppendRecord)
			addr := contractAddressFromContext(ctx)
			collID := readString(mod, uint32(collPtr), uint32(collLen))

			coll, ok := s.data.Collections[collID]
			if !ok {
				return 1
			}
			// Only the collection owner can append.
			if !strings.EqualFold(coll.User, addr) {
				return 2
			}

			metaRaw := readString(mod, uint32(metaPtr), uint32(metaLen))
			metadata := map[string]string{}
			if metaRaw != "" {
				if err := json.Unmarshal([]byte(metaRaw), &metadata); err != nil {
					return 3
				}
			}

			blockTime := blockTimeFromContext(ctx)
			if blockTime == 0 && len(s.data.Blocks) > 0 {
				blockTime = s.data.Blocks[len(s.data.Blocks)-1].TimeUnix
			}

			recordID := fmt.Sprintf("wasm-rec-%s-%d", collID, s.data.WasmNonce)
			s.data.WasmNonce++
			s.data.DataRecords[recordID] = wire.DataRecord{
				RecordID:      recordID,
				CollectionID:  collID,
				User:          addr,
				Kind:          "wasm",
				Metadata:      metadata,
				CreatedAtUnix: blockTime,
			}
			s.data.CollectionRecords[collID] = append(s.data.CollectionRecords[collID], recordID)
			return 0
		}).
		Export("exec_append_record")
}

// ── Internal helpers for host functions ──

// readString reads a UTF-8 string from WASM linear memory.
func readString(mod api.Module, ptr, length uint32) string {
	if length == 0 {
		return ""
	}
	data, ok := mod.Memory().Read(ptr, length)
	if !ok {
		return ""
	}
	return string(data)
}

// writeResult allocates memory in the WASM module via its "alloc" export,
// writes the given data, and returns the packed (ptr<<32|len) result.
// Returns 0 on failure (null result).
func writeResult(ctx context.Context, mod api.Module, data []byte) uint64 {
	if len(data) == 0 {
		return 0
	}
	ptr, length, err := wasm.WriteToMemory(ctx, mod, data)
	if err != nil {
		return 0
	}
	return wasm.PackPtrLen(ptr, length)
}

// errorJSON creates a simple JSON error payload.
func errorJSON(msg string) []byte {
	data, _ := json.Marshal(map[string]string{"error": msg})
	return data
}

// enqueueWasmEventLocked adds a contract event to the pending delivery queue,
// collecting all matching subscribers into a single delivery entry.
// Must be called with s.mu held.
func (s *Store) enqueueWasmEventLocked(event wire.WasmContractEvent) {
	if len(s.data.WasmPendingEvents) >= wire.MaxWasmPendingEvents {
		return // drop event if queue is full
	}

	var subscribers []string
	for subAddr, subs := range s.data.WasmEventSubscriptions {
		for _, sub := range subs {
			if strings.EqualFold(sub.EmitterAddress, event.EmitterAddress) &&
				(sub.EventTypeFilter == event.EventType || sub.EventTypeFilter == "*") {
				subscribers = append(subscribers, subAddr)
				break // one match per subscriber is enough
			}
		}
	}
	if len(subscribers) == 0 {
		return
	}

	pending := wire.WasmPendingEventDelivery{
		Event:         event,
		Subscribers:   subscribers,
		DeliveryBlock: event.DeliveryBlock,
	}
	s.data.WasmPendingEvents = append(s.data.WasmPendingEvents, pending)
}
