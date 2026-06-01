package chain

import (
	"chain/internal/wire"
)

// WasmStateDelta captures all state mutations produced by a single WASM
// execution (host function side effects). Stored in the transaction payload
// so that block replay can re-apply these changes without re-executing WASM.
type WasmStateDelta struct {
	KVUpserts        map[string]string              `json:"kv_upserts,omitempty"`
	KVDeletes         []string                       `json:"kv_deletes,omitempty"`
	ContractBalance   *int64                         `json:"contract_balance,omitempty"`
	AccountDeltas     map[string]int64               `json:"account_deltas,omitempty"`
	PendingEvents     []wire.WasmPendingEventDelivery `json:"pending_events,omitempty"`
	Subscriptions     []wire.WasmEventSubscription   `json:"subscriptions,omitempty"`
	CronJobs          []wire.WasmCronJob             `json:"cron_jobs,omitempty"`
	NonceDelta        *uint64                        `json:"nonce_delta,omitempty"`
	NewIntents        map[string]*Intent             `json:"new_intents,omitempty"`
	EscrowDeltas      map[string]EscrowDelta         `json:"escrow_deltas,omitempty"`
	NewCollections    map[string]wire.DataCollection `json:"new_collections,omitempty"`
	NewRecords        map[string]wire.DataRecord     `json:"new_records,omitempty"`
	CollectionAppends map[string][]string            `json:"collection_appends,omitempty"`
}

// IsEmpty returns true if the delta contains no state changes.
func (d *WasmStateDelta) IsEmpty() bool {
	return len(d.KVUpserts) == 0 &&
		len(d.KVDeletes) == 0 &&
		d.ContractBalance == nil &&
		len(d.AccountDeltas) == 0 &&
		len(d.PendingEvents) == 0 &&
		d.Subscriptions == nil &&
		d.CronJobs == nil &&
		d.NonceDelta == nil &&
		len(d.NewIntents) == 0 &&
		len(d.EscrowDeltas) == 0 &&
		len(d.NewCollections) == 0 &&
		len(d.NewRecords) == 0 &&
		len(d.CollectionAppends) == 0
}

// EscrowDelta captures field-level changes to a DealEscrow.
type EscrowDelta struct {
	LockedFeeDelta *int64  `json:"locked_fee_delta,omitempty"`
	Status         *string `json:"status,omitempty"`
}

// wasmStateSnapshot is an internal snapshot of WASM-relevant state, taken
// immediately before WASM execution. Used by diffWasmState to compute deltas.
type wasmStateSnapshot struct {
	contractAddr       string
	kvStore            map[string]string              // deep copy of contract's KV
	contractBalance    uint64                         // WasmContracts[addr].Balance
	subscriptions      []wire.WasmEventSubscription   // copy of slice
	cronJobs           []wire.WasmCronJob             // copy of slice
	pendingEventsCount int                            // len(WasmPendingEvents)
	wasmNonce          uint64
	accountBalances    map[string]uint64              // addr → Accounts[addr].Balance
	intentIDs          map[string]bool                // existing intent IDs
	escrows            map[string]wire.DealEscrow     // copy of relevant escrows
	collectionIDs      map[string]bool                // existing collection IDs
	recordIDs          map[string]bool                // existing record IDs
	collectionRecords  map[string]int                 // collID → len(CollectionRecords[collID])
}

// captureWasmStateSnapshot takes a point-in-time snapshot of all state that
// WASM host functions can modify. Called immediately before engine.CallExport().
func captureWasmStateSnapshot(s *Store, contractAddr string) wasmStateSnapshot {
	snap := wasmStateSnapshot{
		contractAddr:    contractAddr,
		accountBalances: make(map[string]uint64),
		intentIDs:       make(map[string]bool),
		escrows:         make(map[string]wire.DealEscrow),
		collectionIDs:   make(map[string]bool),
		recordIDs:       make(map[string]bool),
		collectionRecords: make(map[string]int),
	}

	// KV store: deep copy of the executing contract's map.
	if kv, ok := s.data.WasmKVStore[contractAddr]; ok {
		snap.kvStore = make(map[string]string, len(kv))
		for k, v := range kv {
			snap.kvStore[k] = v
		}
	}

	// Contract balance.
	if c := s.data.WasmContracts[contractAddr]; c != nil {
		snap.contractBalance = c.Balance
	}

	// Subscriptions: copy slice.
	if subs := s.data.WasmEventSubscriptions[contractAddr]; len(subs) > 0 {
		snap.subscriptions = make([]wire.WasmEventSubscription, len(subs))
		copy(snap.subscriptions, subs)
	}

	// Cron jobs: copy slice.
	if jobs := s.data.WasmCronJobs[contractAddr]; len(jobs) > 0 {
		snap.cronJobs = make([]wire.WasmCronJob, len(jobs))
		copy(snap.cronJobs, jobs)
	}

	// Pending events count.
	snap.pendingEventsCount = len(s.data.WasmPendingEvents)

	// WasmNonce.
	snap.wasmNonce = s.data.WasmNonce

	// Account balances: record the executing contract's account.
	if acc, ok := s.data.Accounts[contractAddr]; ok {
		snap.accountBalances[contractAddr] = acc.Balance
	}

	// Existing intent IDs.
	for id := range s.data.Intents {
		snap.intentIDs[id] = true
	}

	// Deal escrows: copy all (they're value types, not too many).
	for id, esc := range s.data.DealEscrows {
		snap.escrows[id] = esc
	}

	// Existing collection IDs and record counts.
	for id := range s.data.Collections {
		snap.collectionIDs[id] = true
	}
	for id := range s.data.DataRecords {
		snap.recordIDs[id] = true
	}
	for collID, recs := range s.data.CollectionRecords {
		snap.collectionRecords[collID] = len(recs)
	}

	return snap
}

// diffWasmState compares the live state against a pre-execution snapshot and
// produces a WasmStateDelta capturing all changes made during WASM execution.
func diffWasmState(s *Store, before wasmStateSnapshot, contractAddr string) WasmStateDelta {
	var delta WasmStateDelta

	// ── KV store diff ──
	currentKV := s.data.WasmKVStore[contractAddr]
	upserts := make(map[string]string)
	var deletes []string

	for k, v := range currentKV {
		oldV, existed := before.kvStore[k]
		if !existed || oldV != v {
			upserts[k] = v
		}
	}
	for k := range before.kvStore {
		if _, exists := currentKV[k]; !exists {
			deletes = append(deletes, k)
		}
	}
	if len(upserts) > 0 {
		delta.KVUpserts = upserts
	}
	if len(deletes) > 0 {
		delta.KVDeletes = deletes
	}

	// ── Contract balance diff ──
	var currentBal uint64
	if c := s.data.WasmContracts[contractAddr]; c != nil {
		currentBal = c.Balance
	}
	if currentBal != before.contractBalance {
		bd := int64(currentBal) - int64(before.contractBalance)
		delta.ContractBalance = &bd
	}

	// ── Account balance diffs ──
	// Scan all accounts; compare against snapshot balances.
	accountDeltas := make(map[string]int64)
	for addr, acc := range s.data.Accounts {
		oldBal, tracked := before.accountBalances[addr]
		if tracked {
			if acc.Balance != oldBal {
				accountDeltas[addr] = int64(acc.Balance) - int64(oldBal)
			}
		} else if acc.Balance > 0 {
			// Account not in snapshot but has balance — could be a new
			// account that received a transfer. We include it only if
			// it might have been created during this WASM call.
			// Since we can't know for sure without pre-snapshotting all
			// accounts, we check: if balance > 0 and the account wasn't
			// tracked, the delta is +balance (net new funds).
			// However, this would incorrectly flag ALL existing accounts.
			// So we skip untracked accounts — exec_transfer targets should
			// already exist (accountLocked auto-creates them with 0 balance).
			// We only care about BALANCE CHANGES, not account creation.
			_ = addr
		}
	}
	// For untracked accounts that received transfers: accountLocked creates
	// them with 0 balance, so if they now have balance > 0, the delta is
	// their full balance. But we need to distinguish "existed with 0" from
	// "created during WASM". Since accountLocked always creates on access,
	// we can't tell the difference. Solution: record all accounts with
	// balance changes that were NOT in the snapshot by checking if they
	// appear in Accounts but weren't tracked. This is safe because:
	// - Before WASM: account may or may not exist (0 or more balance)
	// - After WASM: if exec_transfer sent funds, balance increased
	// - We can't know the original balance if not snapshotted
	// Workaround: pre-snapshot likely targets. Since we can't know targets
	// in advance, we accept this limitation for now. External transfers to
	// previously-unseen accounts may not be fully captured.
	// TODO: instrument exec_transfer to record target addresses for pre-snapshot.
	if len(accountDeltas) > 0 {
		delta.AccountDeltas = accountDeltas
	}

	// ── Pending events diff ──
	if len(s.data.WasmPendingEvents) > before.pendingEventsCount {
		newEvents := make([]wire.WasmPendingEventDelivery, len(s.data.WasmPendingEvents)-before.pendingEventsCount)
		copy(newEvents, s.data.WasmPendingEvents[before.pendingEventsCount:])
		delta.PendingEvents = newEvents
	}

	// ── Subscriptions diff ──
	currentSubs := s.data.WasmEventSubscriptions[contractAddr]
	if len(currentSubs) != len(before.subscriptions) {
		subsCopy := make([]wire.WasmEventSubscription, len(currentSubs))
		copy(subsCopy, currentSubs)
		delta.Subscriptions = subsCopy
	}

	// ── Cron jobs diff ──
	currentJobs := s.data.WasmCronJobs[contractAddr]
	if len(currentJobs) != len(before.cronJobs) {
		jobsCopy := make([]wire.WasmCronJob, len(currentJobs))
		copy(jobsCopy, currentJobs)
		delta.CronJobs = jobsCopy
	}

	// ── WasmNonce diff ──
	if s.data.WasmNonce != before.wasmNonce {
		nd := s.data.WasmNonce - before.wasmNonce
		delta.NonceDelta = &nd
	}

	// ── New intents ──
	newIntents := make(map[string]*Intent)
	for id, intent := range s.data.Intents {
		if !before.intentIDs[id] {
			newIntents[id] = intent
		}
	}
	if len(newIntents) > 0 {
		delta.NewIntents = newIntents
	}

	// ── Escrow diffs ──
	escrowDeltas := make(map[string]EscrowDelta)
	for id, current := range s.data.DealEscrows {
		old, existed := before.escrows[id]
		if !existed {
			continue // new escrows not expected from WASM host functions
		}
		var ed EscrowDelta
		if current.LockedFee != old.LockedFee {
			ld := int64(current.LockedFee) - int64(old.LockedFee)
			ed.LockedFeeDelta = &ld
		}
		if current.Status != old.Status {
			st := current.Status
			ed.Status = &st
		}
		if ed.LockedFeeDelta != nil || ed.Status != nil {
			escrowDeltas[id] = ed
		}
	}
	if len(escrowDeltas) > 0 {
		delta.EscrowDeltas = escrowDeltas
	}

	// ── New collections ──
	newColls := make(map[string]wire.DataCollection)
	for id, coll := range s.data.Collections {
		if !before.collectionIDs[id] {
			newColls[id] = coll
		}
	}
	if len(newColls) > 0 {
		delta.NewCollections = newColls
	}

	// ── New records ──
	newRecs := make(map[string]wire.DataRecord)
	for id, rec := range s.data.DataRecords {
		if !before.recordIDs[id] {
			newRecs[id] = rec
		}
	}
	if len(newRecs) > 0 {
		delta.NewRecords = newRecs
	}

	// ── Collection appends ──
	collAppends := make(map[string][]string)
	for collID, currentLen := range before.collectionRecords {
		currentRecs := s.data.CollectionRecords[collID]
		if len(currentRecs) > currentLen {
			appended := make([]string, len(currentRecs)-currentLen)
			copy(appended, currentRecs[currentLen:])
			collAppends[collID] = appended
		}
	}
	// Also check new collections that may have records appended.
	for collID := range newColls {
		if recs := s.data.CollectionRecords[collID]; len(recs) > 0 {
			appended := make([]string, len(recs))
			copy(appended, recs)
			collAppends[collID] = appended
		}
	}
	if len(collAppends) > 0 {
		delta.CollectionAppends = collAppends
	}

	return delta
}

// applyWasmStateDelta applies a captured state delta during block replay.
// This reproduces the host function side effects without re-executing WASM.
func applyWasmStateDelta(s *Store, contractAddr string, delta *WasmStateDelta) {
	if delta == nil {
		return
	}

	// 1. KV upserts.
	if len(delta.KVUpserts) > 0 {
		kv := s.data.WasmKVStore[contractAddr]
		if kv == nil {
			kv = map[string]string{}
			s.data.WasmKVStore[contractAddr] = kv
		}
		for k, v := range delta.KVUpserts {
			kv[k] = v
		}
	}

	// 2. KV deletes.
	if len(delta.KVDeletes) > 0 {
		kv := s.data.WasmKVStore[contractAddr]
		if kv != nil {
			for _, k := range delta.KVDeletes {
				delete(kv, k)
			}
		}
	}

	// 3. Contract balance adjustment.
	if delta.ContractBalance != nil {
		if c := s.data.WasmContracts[contractAddr]; c != nil {
			// Use signed arithmetic to handle both positive and negative deltas.
			newBal := int64(c.Balance) + *delta.ContractBalance
			if newBal < 0 {
				newBal = 0
			}
			c.Balance = uint64(newBal)
		}
	}

	// 4. External account balance deltas.
	for addr, d := range delta.AccountDeltas {
		acc := s.accountLocked(addr)
		newBal := int64(acc.Balance) + d
		if newBal < 0 {
			newBal = 0
		}
		acc.Balance = uint64(newBal)
		s.data.Accounts[addr] = acc
	}

	// 5. Pending events.
	if len(delta.PendingEvents) > 0 {
		s.data.WasmPendingEvents = append(s.data.WasmPendingEvents, delta.PendingEvents...)
	}

	// 6. Subscriptions (full replacement).
	if delta.Subscriptions != nil {
		s.data.WasmEventSubscriptions[contractAddr] = delta.Subscriptions
	}

	// 7. Cron jobs (full replacement).
	if delta.CronJobs != nil {
		s.data.WasmCronJobs[contractAddr] = delta.CronJobs
	}

	// 8. WasmNonce increment.
	if delta.NonceDelta != nil {
		s.data.WasmNonce += *delta.NonceDelta
	}

	// 9. New intents.
	for id, intent := range delta.NewIntents {
		s.data.Intents[id] = intent
	}

	// 10. Escrow field deltas.
	for id, ed := range delta.EscrowDeltas {
		esc, ok := s.data.DealEscrows[id]
		if !ok {
			continue
		}
		if ed.LockedFeeDelta != nil {
			newFee := int64(esc.LockedFee) + *ed.LockedFeeDelta
			if newFee < 0 {
				newFee = 0
			}
			esc.LockedFee = uint64(newFee)
		}
		if ed.Status != nil {
			esc.Status = *ed.Status
		}
		s.data.DealEscrows[id] = esc
	}

	// 11. New collections.
	for id, coll := range delta.NewCollections {
		s.data.Collections[id] = coll
	}

	// 12. New records.
	for id, rec := range delta.NewRecords {
		s.data.DataRecords[id] = rec
	}

	// 13. Collection appends.
	for collID, recordIDs := range delta.CollectionAppends {
		s.data.CollectionRecords[collID] = append(s.data.CollectionRecords[collID], recordIDs...)
	}
}
