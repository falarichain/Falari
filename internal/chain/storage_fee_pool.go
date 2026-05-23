package chain

import "chain/internal/wire"

func ensureDealEscrowForState(state *State, intent *Intent) {
	if state == nil || intent == nil || intent.IntentID == "" || intent.LockedFee == 0 {
		return
	}
	if state.DealEscrows == nil {
		state.DealEscrows = map[string]wire.DealEscrow{}
	}
	escrow, ok := state.DealEscrows[intent.IntentID]
	if !ok {
		escrow = wire.DealEscrow{
			IntentID:    intent.IntentID,
			User:        intent.User,
			LockedFee:   intent.LockedFee,
			PaidFee:     intent.PaidFee,
			RefundedFee: intent.RefundedFee,
			Status:      intent.StorageStatus,
			Permanent:   isPermanentIntent(intent),
		}
	}
	escrow.User = intent.User
	escrow.LockedFee = intent.LockedFee
	escrow.PaidFee = intent.PaidFee
	escrow.RefundedFee = intent.RefundedFee
	escrow.Status = intent.StorageStatus
	escrow.Permanent = isPermanentIntent(intent)
	if intent.Status == wire.StatusFinalized {
		if escrow.StartAtUnix == 0 {
			escrow.StartAtUnix = firstNonZero(intent.UpdatedAt, intent.CreatedAt)
		}
		if intent.ExpiresAtUnix > 0 {
			escrow.ExpiresAtUnix = intent.ExpiresAtUnix
		} else if intent.Policy.Duration > 0 && escrow.StartAtUnix > 0 {
			escrow.ExpiresAtUnix = escrow.StartAtUnix + intent.Policy.Duration
		}
	}
	state.DealEscrows[intent.IntentID] = escrow
}

func rebuildStorageFeePoolForState(state *State) {
	if state == nil {
		return
	}
	var pool wire.StorageFeePool
	for _, escrow := range state.DealEscrows {
		pool.TotalLocked = saturatingAdd(pool.TotalLocked, escrow.LockedFee)
		pool.TotalPaid = saturatingAdd(pool.TotalPaid, escrow.PaidFee)
		pool.TotalRefunded = saturatingAdd(pool.TotalRefunded, escrow.RefundedFee)
	}
	for _, fund := range state.PermanentStorageFunds {
		if !fund.Closed {
			pool.PermanentFundBalance = saturatingAdd(pool.PermanentFundBalance, fund.Balance)
		}
		pool.TransferredToRewardPool = saturatingAdd(pool.TransferredToRewardPool, fund.TransferredToPool)
	}
	pool.InsuranceReserve = state.StorageFeePool.InsuranceReserve
	state.StorageFeePool = pool
}

func (s *Store) createDealEscrowLocked(intent *Intent, now int64) {
	if intent == nil || intent.IntentID == "" || intent.LockedFee == 0 {
		return
	}
	if s.data.DealEscrows == nil {
		s.data.DealEscrows = map[string]wire.DealEscrow{}
	}
	_, exists := s.data.DealEscrows[intent.IntentID]
	ensureDealEscrowForState(&s.data, intent)
	if !exists {
		s.data.StorageFeePool.TotalLocked = saturatingAdd(s.data.StorageFeePool.TotalLocked, intent.LockedFee)
	}
}

func (s *Store) activateDealEscrowLocked(intent *Intent, now int64) {
	if intent == nil || intent.IntentID == "" {
		return
	}
	escrow := s.dealEscrowLocked(intent)
	if escrow.StartAtUnix == 0 {
		escrow.StartAtUnix = now
	}
	if intent.Policy.Duration > 0 {
		escrow.ExpiresAtUnix = now + intent.Policy.Duration
	}
	escrow.Status = wire.StorageStatusActive
	s.data.DealEscrows[intent.IntentID] = escrow
}

func (s *Store) addDealEscrowFundsLocked(intent *Intent, amount uint64, now int64) {
	if intent == nil || amount == 0 {
		return
	}
	escrow := s.dealEscrowLocked(intent)
	escrow.LockedFee = intent.LockedFee
	escrow.Status = intent.StorageStatus
	escrow.Permanent = isPermanentIntent(intent)
	if escrow.StartAtUnix == 0 && intent.Status == wire.StatusFinalized {
		escrow.StartAtUnix = firstNonZero(intent.UpdatedAt, now)
	}
	if intent.ExpiresAtUnix > 0 {
		escrow.ExpiresAtUnix = intent.ExpiresAtUnix
	}
	s.data.DealEscrows[intent.IntentID] = escrow
	s.data.StorageFeePool.TotalLocked = saturatingAdd(s.data.StorageFeePool.TotalLocked, amount)
}

func (s *Store) dealEscrowLocked(intent *Intent) wire.DealEscrow {
	if s.data.DealEscrows == nil {
		s.data.DealEscrows = map[string]wire.DealEscrow{}
	}
	ensureDealEscrowForState(&s.data, intent)
	return s.data.DealEscrows[intent.IntentID]
}

func (s *Store) spendFiniteStorageFeeLocked(intent *Intent, requested uint64, now int64) uint64 {
	if intent == nil || requested == 0 || isPermanentIntent(intent) {
		return 0
	}
	escrow := s.dealEscrowLocked(intent)
	available := finiteDealEscrowAvailable(escrow, intent, now)
	if requested > available {
		requested = available
	}
	remaining := remainingIntentEscrow(intent)
	if requested > remaining {
		requested = remaining
	}
	user := s.accountLocked(intent.User)
	if requested > user.LockedStorage {
		requested = user.LockedStorage
	}
	if requested == 0 {
		escrow.AccruedFee = finiteDealEscrowAccrued(escrow, intent, now)
		escrow.LastAccruedAtUnix = now
		s.data.DealEscrows[intent.IntentID] = escrow
		return 0
	}
	user.LockedStorage -= requested
	s.data.Accounts[intent.User] = user
	intent.PaidFee = saturatingAdd(intent.PaidFee, requested)
	escrow.PaidFee = saturatingAdd(escrow.PaidFee, requested)
	escrow.AccruedFee = finiteDealEscrowAccrued(escrow, intent, now)
	escrow.LastAccruedAtUnix = now
	s.data.DealEscrows[intent.IntentID] = escrow
	s.data.StorageFeePool.TotalPaid = saturatingAdd(s.data.StorageFeePool.TotalPaid, requested)
	return requested
}

func (s *Store) recordStorageFeeRefundLocked(intent *Intent, amount uint64) {
	if intent == nil || amount == 0 {
		return
	}
	escrow := s.dealEscrowLocked(intent)
	escrow.RefundedFee = saturatingAdd(escrow.RefundedFee, amount)
	escrow.Status = intent.StorageStatus
	s.data.DealEscrows[intent.IntentID] = escrow
	s.data.StorageFeePool.TotalRefunded = saturatingAdd(s.data.StorageFeePool.TotalRefunded, amount)
}

func finiteDealEscrowAvailable(escrow wire.DealEscrow, intent *Intent, now int64) uint64 {
	accrued := finiteDealEscrowAccrued(escrow, intent, now)
	if accrued <= escrow.PaidFee {
		return 0
	}
	return accrued - escrow.PaidFee
}

func finiteDealEscrowAccrued(escrow wire.DealEscrow, intent *Intent, now int64) uint64 {
	if escrow.LockedFee == 0 || intent == nil || intent.Policy.Duration <= 0 {
		return 0
	}
	start := escrow.StartAtUnix
	if start == 0 {
		start = firstNonZero(intent.UpdatedAt, intent.CreatedAt)
	}
	if now <= start {
		return 0
	}
	duration := intent.Policy.Duration
	expires := escrow.ExpiresAtUnix
	if expires == 0 {
		expires = start + duration
	}
	if expires > start {
		duration = expires - start
	}
	if duration <= 0 {
		return escrow.LockedFee
	}
	elapsed := now - start
	if elapsed >= duration {
		return escrow.LockedFee
	}
	return escrow.LockedFee * uint64(elapsed) / uint64(duration)
}

func permanentFundDailyRate(balance uint64) uint64 {
	if balance == 0 {
		return 0
	}
	rate := balance * defaultPermanentFundAnnualSpendBPS / 10000 / 365
	if rate == 0 {
		rate = 1
	}
	return rate
}
