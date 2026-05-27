package chain

import (
	"errors"
	"log"
	"time"

	"chain/internal/wire"
)

const defaultGracePeriod = int64(7 * 24 * 60 * 60)

type renewDealTxPayload struct {
	Request       wire.RenewDealRequest  `json:"request"`
	Response      wire.RenewDealResponse `json:"response"`
	RenewedAtUnix int64                  `json:"renewed_at_unix"`
}

func (s *Store) RenewDeal(req wire.RenewDealRequest) (wire.RenewDealResponse, error) {
	req.User = wire.NormalizeAddress(req.User)
	if req.IntentID == "" || req.User == "" {
		return wire.RenewDealResponse{}, errors.New("intent id and user are required")
	}
	if req.Duration <= 0 {
		return wire.RenewDealResponse{}, errors.New("renewal duration must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.RenewDealResponse{}, errors.New("intent not found")
	}
	if intent.User != req.User {
		return wire.RenewDealResponse{}, errors.New("user mismatch")
	}
	if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
		return wire.VerifyRenewDeal(req)
	}); err != nil {
		return wire.RenewDealResponse{}, err
	}
	if !intent.Policy.Renewable {
		return wire.RenewDealResponse{}, errors.New("deal is not renewable")
	}

	normalizeIntentLifecycle(intent)
	now := time.Now().Unix()
	graceUsed := false

	switch intent.Status {
	case wire.StatusFinalized:
		if intent.ExpiresAtUnix > now {
		} else {
			graceExpiresAt := intent.ExpiresAtUnix + defaultGracePeriod
			if now > graceExpiresAt {
				return wire.RenewDealResponse{}, errors.New("grace period has expired")
			}
			intent.Status = wire.StatusFinalized
			intent.StorageStatus = wire.StorageStatusActive
			graceUsed = true
		}
	case wire.StatusExpired:
		graceExpiresAt := intent.ExpiresAtUnix + defaultGracePeriod
		if now > graceExpiresAt {
			return wire.RenewDealResponse{}, errors.New("grace period has expired")
		}
		intent.Status = wire.StatusFinalized
		intent.StorageStatus = wire.StorageStatusActive
		graceUsed = true
	default:
		return wire.RenewDealResponse{}, errors.New("intent is not in a renewable state")
	}

	newExpiry := now + req.Duration
	if intent.Policy.Duration > 0 && req.Duration > intent.Policy.Duration {
		return wire.RenewDealResponse{}, errors.New("renewal duration exceeds policy duration")
	}

	price := s.estimateRenewalPriceLocked(intent, req.Duration)
	account := s.accountLocked(req.User)
	if account.Balance < price {
		return wire.RenewDealResponse{}, errors.New("insufficient balance for renewal")
	}
	s.consumeAccountNonceLocked(req.User)
	account = s.accountLocked(req.User)
	account.Balance -= price
	account.LockedStorage += price
	s.data.Accounts[req.User] = account

	intent.LockedFee = saturatingAdd(intent.LockedFee, price)
	intent.UpdatedAt = now
	intent.ExpiresAtUnix = newExpiry
	intent.Policy.Duration = req.Duration
	s.addDealEscrowFundsLocked(intent, price, now)

	resp := wire.RenewDealResponse{
		IntentID:      intent.IntentID,
		Status:        intent.Status,
		ExpiresAtUnix: newExpiry,
		NewLockedFee:  intent.LockedFee,
		PaidAmount:    price,
		GraceUsed:     graceUsed,
	}

	s.recordTxLocked("renew_deal", req.User, renewDealTxPayload{
		Request:       req,
		Response:      resp,
		RenewedAtUnix: now,
	})

	if err := s.saveLocked(); err != nil {
		return wire.RenewDealResponse{}, err
	}
	return resp, nil
}

func (s *Store) estimateRenewalPriceLocked(intent *Intent, duration int64) uint64 {
	basePrice := s.data.StoragePricing.BasePrice
	if basePrice == 0 {
		basePrice = defaultStorageBasePrice
	}
	redundantBytes, err := redundantStorageBytes(intent.FileSize, intent.Erasure)
	if err != nil {
		redundantBytes = uint64(intent.FileSize)
	}
	fee, err := quoteTieredFee(redundantBytes, duration, basePrice)
	if err != nil || fee == 0 {
		return defaultStorageMinimumFee
	}
	if fee < defaultStorageMinimumFee {
		fee = defaultStorageMinimumFee
	}
	return fee
}

func (s *Store) applyRenewDealLocked(payload renewDealTxPayload) error {
	req := payload.Request
	req.User = wire.NormalizeAddress(req.User)
	if req.IntentID == "" || req.User == "" {
		return errors.New("intent id and user are required")
	}
	if req.Duration <= 0 {
		return errors.New("renewal duration must be positive")
	}
	renewedAt := payload.RenewedAtUnix
	if renewedAt <= 0 {
		return errors.New("replay renew deal missing renewal timestamp")
	}
	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return errors.New("intent not found")
	}
	if intent.User != req.User {
		return errors.New("user mismatch")
	}
	if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
		return wire.VerifyRenewDeal(req)
	}); err != nil {
		return err
	}
	intentView := *intent
	if !intentView.Policy.Renewable {
		return errors.New("deal is not renewable")
	}
	normalizeIntentLifecycleAt(&intentView, renewedAt)
	graceUsed := false
	switch intentView.Status {
	case wire.StatusFinalized:
		if intentView.ExpiresAtUnix <= renewedAt {
			graceExpiresAt := intentView.ExpiresAtUnix + defaultGracePeriod
			if renewedAt > graceExpiresAt {
				return errors.New("grace period has expired")
			}
			intentView.Status = wire.StatusFinalized
			intentView.StorageStatus = wire.StorageStatusActive
			graceUsed = true
		}
	case wire.StatusExpired:
		graceExpiresAt := intentView.ExpiresAtUnix + defaultGracePeriod
		if renewedAt > graceExpiresAt {
			return errors.New("grace period has expired")
		}
		intentView.Status = wire.StatusFinalized
		intentView.StorageStatus = wire.StorageStatusActive
		graceUsed = true
	default:
		return errors.New("intent is not in a renewable state")
	}
	if intentView.Policy.Duration > 0 && req.Duration > intentView.Policy.Duration {
		return errors.New("renewal duration exceeds policy duration")
	}
	price := s.estimateRenewalPriceLocked(&intentView, req.Duration)
	expectedResp := wire.RenewDealResponse{
		IntentID:      intentView.IntentID,
		Status:        intentView.Status,
		ExpiresAtUnix: renewedAt + req.Duration,
		NewLockedFee:  saturatingAdd(intentView.LockedFee, price),
		PaidAmount:    price,
		GraceUsed:     graceUsed,
	}
	if payload.Response != expectedResp {
		return errors.New("replay renew deal response mismatch")
	}
	account := s.accountLocked(req.User)
	if account.Balance < price {
		return errors.New("replay renew deal has insufficient balance")
	}
	s.consumeAccountNonceLocked(req.User)
	account = s.accountLocked(req.User)
	account.Balance -= price
	account.LockedStorage = saturatingAdd(account.LockedStorage, price)
	s.data.Accounts[req.User] = account
	intent.LockedFee = expectedResp.NewLockedFee
	intent.Status = expectedResp.Status
	intent.StorageStatus = wire.StorageStatusActive
	intent.ExpiresAtUnix = expectedResp.ExpiresAtUnix
	intent.Policy.Duration = req.Duration
	intent.UpdatedAt = renewedAt
	s.addDealEscrowFundsLocked(intent, price, renewedAt)
	return nil
}

func (s *Store) autoRenewDealsLocked(now int64) (renewed int) {
	for _, intent := range s.data.Intents {
		if !intent.Policy.AutoRenew || !intent.Policy.Renewable {
			continue
		}
		if intent.Status != wire.StatusFinalized {
			continue
		}
		if intent.StorageStatus != wire.StorageStatusActive {
			continue
		}
		if intent.ExpiresAtUnix > now {
			continue
		}
		if now > intent.ExpiresAtUnix+defaultGracePeriod {
			continue
		}
		duration := intent.Policy.Duration
		if duration <= 0 {
			duration = 30 * 86400
		}
		price := s.estimateRenewalPriceLocked(intent, duration)
		account := s.accountLocked(intent.User)
		if account.Balance < price {
			continue
		}
		account.Balance -= price
		account.LockedStorage += price
		s.data.Accounts[intent.User] = account
		intent.LockedFee = saturatingAdd(intent.LockedFee, price)
		intent.UpdatedAt = now
		intent.ExpiresAtUnix = now + duration
		s.addDealEscrowFundsLocked(intent, price, now)
		renewed++
	}
	return renewed
}

func (s *Store) StartAutoRenewScheduler(interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			s.mu.Lock()
			renewed := s.autoRenewDealsLocked(time.Now().Unix())
			var err error
			if renewed > 0 {
				err = s.saveLocked()
			}
			s.mu.Unlock()
			if renewed > 0 {
				log.Printf("auto renewed %d deals", renewed)
			}
			if err != nil {
				log.Printf("auto renew save failed: %v", err)
			}
		}
	}()
}
