package chain

import (
	"errors"
	"time"

	"chain/internal/wire"
)

type permanentFundTopUpTxPayload struct {
	Request        wire.PermanentFundTopUpRequest  `json:"request"`
	Response       wire.PermanentFundTopUpResponse `json:"response"`
	ToppedUpAtUnix int64                           `json:"topped_up_at_unix"`
}

func (s *Store) TopUpPermanentFund(req wire.PermanentFundTopUpRequest) (wire.PermanentFundTopUpResponse, error) {
	req.User = wire.NormalizeAddress(req.User)
	if req.IntentID == "" || req.User == "" {
		return wire.PermanentFundTopUpResponse{}, errors.New("intent id and user are required")
	}
	if req.Amount == 0 {
		return wire.PermanentFundTopUpResponse{}, errors.New("amount must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.PermanentFundTopUpResponse{}, errors.New("intent not found")
	}
	if intent.User != req.User {
		return wire.PermanentFundTopUpResponse{}, errors.New("intent user mismatch")
	}
	if !isPermanentIntent(intent) {
		return wire.PermanentFundTopUpResponse{}, errors.New("intent is not permanent storage")
	}
	if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
		return wire.VerifyPermanentFundTopUp(req)
	}); err != nil {
		return wire.PermanentFundTopUpResponse{}, err
	}
	account := s.accountLocked(req.User)
	if account.Balance < req.Amount {
		return wire.PermanentFundTopUpResponse{}, errors.New("insufficient balance")
	}
	s.consumeAccountNonceLocked(req.User)
	account = s.accountLocked(req.User)
	account.Balance -= req.Amount
	account.LockedStorage = saturatingAdd(account.LockedStorage, req.Amount)
	s.data.Accounts[req.User] = account

	now := time.Now().Unix()
	fund := s.ensurePermanentFundLocked(intent, now)
	fund.Balance = saturatingAdd(fund.Balance, req.Amount)
	fund.Contributed = saturatingAdd(fund.Contributed, req.Amount)
	fund.SustainableDailyRate = permanentFundDailyRate(fund.Balance)
	// Reset the baseline rate on top-up so the ratio-based takeover
	// threshold is measured against the new (higher) daily rate.
	fund.InitialDailyRate = fund.SustainableDailyRate
	fund.UpdatedAtUnix = now
	fund.Closed = false
	fund.ClosedReason = ""
	fund.ClosedAtUnix = 0
	fund.TransferredToPool = 0
	s.data.PermanentStorageFunds[intent.IntentID] = fund
	intent.LockedFee = saturatingAdd(intent.LockedFee, req.Amount)
	s.addDealEscrowFundsLocked(intent, req.Amount, now)
	s.data.StorageFeePool.PermanentFundBalance = saturatingAdd(s.data.StorageFeePool.PermanentFundBalance, req.Amount)
	intent.PermanentFundBalance = fund.Balance
	intent.PermanentFundPaid = fund.Paid
	intent.UpdatedAt = now

	resp := wire.PermanentFundTopUpResponse{Fund: fund}
	s.recordTxLocked("permanent_fund_topup", req.User, permanentFundTopUpTxPayload{
		Request:        req,
		Response:       resp,
		ToppedUpAtUnix: now,
	})
	if err := s.saveLocked(); err != nil {
		return wire.PermanentFundTopUpResponse{}, err
	}
	return resp, nil
}

func (s *Store) applyPermanentFundTopUpLocked(payload permanentFundTopUpTxPayload) error {
	req := payload.Request
	req.User = wire.NormalizeAddress(req.User)
	if req.IntentID == "" || req.User == "" {
		return errors.New("intent id and user are required")
	}
	if req.Amount == 0 {
		return errors.New("amount must be positive")
	}
	toppedUpAt := payload.ToppedUpAtUnix
	if toppedUpAt <= 0 {
		return errors.New("replay permanent fund topup missing timestamp")
	}
	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return errors.New("intent not found")
	}
	if intent.User != req.User {
		return errors.New("intent user mismatch")
	}
	if !isPermanentIntent(intent) {
		return errors.New("intent is not permanent storage")
	}
	if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
		return wire.VerifyPermanentFundTopUp(req)
	}); err != nil {
		return err
	}
	fund := s.ensurePermanentFundLocked(intent, toppedUpAt)
	fund.Balance = saturatingAdd(fund.Balance, req.Amount)
	fund.Contributed = saturatingAdd(fund.Contributed, req.Amount)
	fund.SustainableDailyRate = permanentFundDailyRate(fund.Balance)
	fund.InitialDailyRate = fund.SustainableDailyRate
	fund.UpdatedAtUnix = toppedUpAt
	fund.Closed = false
	fund.ClosedReason = ""
	fund.ClosedAtUnix = 0
	fund.TransferredToPool = 0
	expectedResp := wire.PermanentFundTopUpResponse{Fund: fund}
	if payload.Response != expectedResp {
		return errors.New("replay permanent fund topup response mismatch")
	}
	account := s.accountLocked(req.User)
	if account.Balance < req.Amount {
		return errors.New("replay permanent fund topup has insufficient balance")
	}
	s.consumeAccountNonceLocked(req.User)
	account = s.accountLocked(req.User)
	account.Balance -= req.Amount
	account.LockedStorage = saturatingAdd(account.LockedStorage, req.Amount)
	s.data.Accounts[req.User] = account
	s.data.PermanentStorageFunds[intent.IntentID] = fund
	intent.LockedFee = saturatingAdd(intent.LockedFee, req.Amount)
	s.addDealEscrowFundsLocked(intent, req.Amount, toppedUpAt)
	s.data.StorageFeePool.PermanentFundBalance = saturatingAdd(s.data.StorageFeePool.PermanentFundBalance, req.Amount)
	intent.PermanentFundBalance = fund.Balance
	intent.PermanentFundPaid = fund.Paid
	intent.UpdatedAt = toppedUpAt
	return nil
}

func (s *Store) ensurePermanentFundLocked(intent *Intent, now int64) wire.PermanentStorageFund {
	fund, ok := s.data.PermanentStorageFunds[intent.IntentID]
	if !ok {
		remaining := remainingIntentEscrow(intent)
		dailyRate := permanentFundDailyRate(remaining)
		fund = wire.PermanentStorageFund{
			IntentID:             intent.IntentID,
			User:                 intent.User,
			Balance:              remaining,
			Contributed:          intent.LockedFee,
			Paid:                 intent.PaidFee,
			SustainableDailyRate: dailyRate,
			InitialDailyRate:     dailyRate,
			CreatedAtUnix:        firstNonZero(intent.CreatedAt, now),
			UpdatedAtUnix:        now,
		}
	}
	if fund.User == "" {
		fund.User = intent.User
	}
	if fund.CreatedAtUnix == 0 {
		fund.CreatedAtUnix = firstNonZero(intent.CreatedAt, now)
	}
	if fund.UpdatedAtUnix == 0 {
		fund.UpdatedAtUnix = now
	}
	// Ensure InitialDailyRate is set for funds created before this field existed.
	if fund.InitialDailyRate == 0 && fund.SustainableDailyRate > 0 {
		fund.InitialDailyRate = fund.SustainableDailyRate
	}
	return fund
}

func (s *Store) createPermanentFundLocked(intent *Intent, now int64) {
	if intent == nil || !isPermanentIntent(intent) {
		return
	}
	fund := s.ensurePermanentFundLocked(intent, now)
	s.data.PermanentStorageFunds[intent.IntentID] = fund
	s.data.StorageFeePool.PermanentFundBalance = saturatingAdd(s.data.StorageFeePool.PermanentFundBalance, fund.Balance)
	intent.PermanentFundBalance = fund.Balance
	intent.PermanentFundPaid = fund.Paid
}

func (s *Store) spendPermanentFundLocked(intent *Intent, amount uint64, now int64) uint64 {
	if intent == nil || amount == 0 || !isPermanentIntent(intent) {
		return 0
	}
	fund := s.ensurePermanentFundLocked(intent, now)
	if fund.Closed {
		return 0
	}
	if fund.SustainableDailyRate == 0 {
		fund.SustainableDailyRate = permanentFundDailyRate(fund.Balance)
	}
	lastPayout := fund.LastPayoutUnix
	if lastPayout == 0 {
		lastPayout = firstNonZero(fund.CreatedAtUnix, intent.CreatedAt, now)
	}
	elapsedDays := (now - lastPayout) / miningRewardVestingDaySeconds
	if elapsedDays <= 0 {
		fund.UpdatedAtUnix = now
		s.data.PermanentStorageFunds[intent.IntentID] = fund
		intent.PermanentFundBalance = fund.Balance
		intent.PermanentFundPaid = fund.Paid
		return 0
	}
	spendLimit := fund.SustainableDailyRate * uint64(elapsedDays)
	if amount > spendLimit {
		amount = spendLimit
	}
	if amount > fund.Balance {
		amount = fund.Balance
	}
	user := s.accountLocked(intent.User)
	if amount > user.LockedStorage {
		amount = user.LockedStorage
	}
	if amount == 0 {
		return 0
	}
	user.LockedStorage -= amount
	s.data.Accounts[intent.User] = user
	fund.Balance -= amount
	fund.Paid = saturatingAdd(fund.Paid, amount)
	fund.LastPayoutUnix = now
	fund.UpdatedAtUnix = now
	s.data.PermanentStorageFunds[intent.IntentID] = fund
	intent.PermanentFundBalance = fund.Balance
	intent.PermanentFundPaid = fund.Paid
	intent.PaidFee = saturatingAdd(intent.PaidFee, amount)
	escrow := s.dealEscrowLocked(intent)
	escrow.PaidFee = saturatingAdd(escrow.PaidFee, amount)
	escrow.AccruedFee = saturatingAdd(escrow.AccruedFee, amount)
	escrow.LastAccruedAtUnix = now
	s.data.DealEscrows[intent.IntentID] = escrow
	s.data.StorageFeePool.TotalPaid = saturatingAdd(s.data.StorageFeePool.TotalPaid, amount)
	if s.data.StorageFeePool.PermanentFundBalance >= amount {
		s.data.StorageFeePool.PermanentFundBalance -= amount
	} else {
		s.data.StorageFeePool.PermanentFundBalance = 0
	}
	return amount
}

func (s *Store) closePermanentFundLocked(intent *Intent, reason string, now int64) uint64 {
	if intent == nil || !isPermanentIntent(intent) {
		return 0
	}
	fund := s.ensurePermanentFundLocked(intent, now)
	if fund.Closed {
		return 0
	}
	remaining := fund.Balance
	if remaining > 0 {
		user := s.accountLocked(intent.User)
		if user.LockedStorage < remaining {
			remaining = user.LockedStorage
		}
		// Burn: deduct from user's LockedStorage but do NOT credit to any address.
		// This is a deflationary mechanism — tokens are permanently removed from
		// circulation (sent to the black-hole address).
		user.LockedStorage -= remaining
		s.data.Accounts[intent.User] = user
		fund.Burned = saturatingAdd(fund.Burned, remaining)
		intent.BurnedFee = saturatingAdd(intent.BurnedFee, remaining)
		s.data.StorageFeePool.TotalBurned = saturatingAdd(s.data.StorageFeePool.TotalBurned, remaining)
	}
	fund.Balance = 0
	fund.Closed = true
	fund.ClosedReason = reason
	fund.ClosedAtUnix = now
	fund.UpdatedAtUnix = now
	s.data.PermanentStorageFunds[intent.IntentID] = fund
	if s.data.StorageFeePool.PermanentFundBalance >= remaining {
		s.data.StorageFeePool.PermanentFundBalance -= remaining
	} else {
		s.data.StorageFeePool.PermanentFundBalance = 0
	}
	intent.PermanentFundBalance = 0
	intent.PermanentFundPaid = fund.Paid
	return remaining
}

func isPermanentIntent(intent *Intent) bool {
	return intent != nil && intent.Policy.Duration <= 0
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
