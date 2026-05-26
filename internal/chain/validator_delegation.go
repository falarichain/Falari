package chain

import (
	"errors"
	"time"

	"chain/internal/wire"
)

type delegateStakeTxPayload struct {
	Request         wire.DelegateStakeRequest `json:"request"`
	DelegatedBefore uint64                    `json:"delegated_before"`
	DelegatedAfter  uint64                    `json:"delegated_after"`
}

type undelegateStakeTxPayload struct {
	Request         wire.UndelegateStakeRequest `json:"request"`
	DelegatedBefore uint64                      `json:"delegated_before"`
	DelegatedAfter  uint64                      `json:"delegated_after"`
}

func (s *Store) DelegateStake(req wire.DelegateStakeRequest) (wire.DelegateStakeResponse, error) {
	req.Delegator = wire.NormalizeAddress(req.Delegator)
	req.Validator = wire.NormalizeAddress(req.Validator)

	if req.Delegator == "" || req.Validator == "" {
		return wire.DelegateStakeResponse{}, errors.New("delegator and validator addresses are required")
	}
	if req.Amount == 0 {
		return wire.DelegateStakeResponse{}, errors.New("delegate amount must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.verifyAccountRequestLocked(req.ChainID, req.Delegator, req.Nonce, func() error {
		return wire.VerifyDelegateStake(req)
	}); err != nil {
		return wire.DelegateStakeResponse{}, err
	}

	validator, ok := s.data.Validators[req.Validator]
	if !ok || validator.Status != wire.ValidatorStatusActive {
		return wire.DelegateStakeResponse{}, errors.New("validator is not active")
	}

	account := s.accountLocked(req.Delegator)
	if account.Balance < req.Amount {
		return wire.DelegateStakeResponse{}, errors.New("insufficient balance for delegation")
	}
	s.consumeAccountNonceLocked(req.Delegator)
	account = s.accountLocked(req.Delegator)

	if s.data.StakeDelegations == nil {
		s.data.StakeDelegations = map[string]wire.StakeDelegation{}
	}

	key := delegationKey(req.Delegator, req.Validator)
	existing, hadBefore := s.data.StakeDelegations[key]
	delegatedBefore := existing.Amount
	if hadBefore {
		existing.Amount += req.Amount
	} else {
		existing = wire.StakeDelegation{
			Delegator: req.Delegator,
			Validator: req.Validator,
			Amount:    req.Amount,
			SinceUnix: time.Now().Unix(),
		}
	}
	s.data.StakeDelegations[key] = existing

	account.Balance -= req.Amount
	s.data.Accounts[req.Delegator] = account

	validator.DelegatedStake = saturatingAdd(validator.DelegatedStake, req.Amount)
	if !hadBefore {
		validator.DelegatorCount++
	}
	s.data.Validators[req.Validator] = validator
	s.syncMinerDelegatorCountLocked(req.Validator, validator.DelegatorCount)

	s.recordTxLocked("delegate_stake", req.Delegator, delegateStakeTxPayload{
		Request:         req,
		DelegatedBefore: delegatedBefore,
		DelegatedAfter:  existing.Amount,
	})

	if err := s.saveLocked(); err != nil {
		return wire.DelegateStakeResponse{}, err
	}
	return wire.DelegateStakeResponse{
		Delegator:      req.Delegator,
		Validator:      req.Validator,
		Amount:         req.Amount,
		DelegatedStake: existing.Amount,
	}, nil
}

func (s *Store) UndelegateStake(req wire.UndelegateStakeRequest) (wire.UndelegateStakeResponse, error) {
	req.Delegator = wire.NormalizeAddress(req.Delegator)
	req.Validator = wire.NormalizeAddress(req.Validator)

	if req.Delegator == "" || req.Validator == "" {
		return wire.UndelegateStakeResponse{}, errors.New("delegator and validator addresses are required")
	}
	if req.Amount == 0 {
		return wire.UndelegateStakeResponse{}, errors.New("undelegate amount must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.verifyAccountRequestLocked(req.ChainID, req.Delegator, req.Nonce, func() error {
		return wire.VerifyUndelegateStake(req)
	}); err != nil {
		return wire.UndelegateStakeResponse{}, err
	}

	if s.data.StakeDelegations == nil {
		return wire.UndelegateStakeResponse{}, errors.New("no delegation found")
	}

	key := delegationKey(req.Delegator, req.Validator)
	existing, ok := s.data.StakeDelegations[key]
	if !ok || existing.Amount < req.Amount {
		return wire.UndelegateStakeResponse{}, errors.New("insufficient delegated stake")
	}

	delegatedBefore := existing.Amount
	released := req.Amount
	existing.Amount -= released
	if existing.Amount == 0 {
		delete(s.data.StakeDelegations, key)
	} else {
		s.data.StakeDelegations[key] = existing
	}

	s.consumeAccountNonceLocked(req.Delegator)
	account := s.accountLocked(req.Delegator)
	account.Balance += released
	s.data.Accounts[req.Delegator] = account

	validator := s.validatorLocked(req.Validator)
	if validator.DelegatedStake >= released {
		validator.DelegatedStake -= released
	} else {
		validator.DelegatedStake = 0
	}
	if existing.Amount == 0 && validator.DelegatorCount > 0 {
		validator.DelegatorCount--
	}
	s.data.Validators[req.Validator] = validator
	s.syncMinerDelegatorCountLocked(req.Validator, validator.DelegatorCount)

	s.recordTxLocked("undelegate_stake", req.Delegator, undelegateStakeTxPayload{
		Request:         req,
		DelegatedBefore: delegatedBefore,
		DelegatedAfter:  existing.Amount,
	})

	if err := s.saveLocked(); err != nil {
		return wire.UndelegateStakeResponse{}, err
	}
	return wire.UndelegateStakeResponse{
		Delegator:      req.Delegator,
		Validator:      req.Validator,
		Released:       released,
		DelegatedStake: existing.Amount,
	}, nil
}

func (s *Store) applyDelegateStakeLocked(payload delegateStakeTxPayload) error {
	req := payload.Request
	req.Delegator = wire.NormalizeAddress(req.Delegator)
	req.Validator = wire.NormalizeAddress(req.Validator)
	if err := s.verifyAccountRequestLocked(req.ChainID, req.Delegator, req.Nonce, func() error {
		return wire.VerifyDelegateStake(req)
	}); err != nil {
		return err
	}
	validator, ok := s.data.Validators[req.Validator]
	if !ok || validator.Status != wire.ValidatorStatusActive {
		return errors.New("replay delegate validator is not active")
	}
	account := s.accountLocked(req.Delegator)
	if account.Balance < req.Amount {
		return errors.New("replay delegate has insufficient balance")
	}
	s.consumeAccountNonceLocked(req.Delegator)
	account = s.accountLocked(req.Delegator)
	if s.data.StakeDelegations == nil {
		s.data.StakeDelegations = map[string]wire.StakeDelegation{}
	}
	key := delegationKey(req.Delegator, req.Validator)
	existing, hadBefore := s.data.StakeDelegations[key]
	if existing.Amount != payload.DelegatedBefore {
		return errors.New("replay delegate stake before mismatch")
	}
	existing.Amount = payload.DelegatedAfter
	if !hadBefore {
		existing.Delegator = req.Delegator
		existing.Validator = req.Validator
		existing.SinceUnix = time.Now().Unix()
	}
	s.data.StakeDelegations[key] = existing
	account.Balance -= req.Amount
	s.data.Accounts[req.Delegator] = account
	validator.DelegatedStake = saturatingAdd(validator.DelegatedStake, req.Amount)
	if !hadBefore {
		validator.DelegatorCount++
	}
	s.data.Validators[req.Validator] = validator
	s.syncMinerDelegatorCountLocked(req.Validator, validator.DelegatorCount)
	return nil
}

func (s *Store) applyUndelegateStakeLocked(payload undelegateStakeTxPayload) error {
	req := payload.Request
	req.Delegator = wire.NormalizeAddress(req.Delegator)
	req.Validator = wire.NormalizeAddress(req.Validator)
	if err := s.verifyAccountRequestLocked(req.ChainID, req.Delegator, req.Nonce, func() error {
		return wire.VerifyUndelegateStake(req)
	}); err != nil {
		return err
	}
	if s.data.StakeDelegations == nil {
		return errors.New("replay undelegate no delegation found")
	}
	key := delegationKey(req.Delegator, req.Validator)
	existing, ok := s.data.StakeDelegations[key]
	if !ok || existing.Amount != payload.DelegatedBefore || payload.DelegatedBefore < req.Amount {
		return errors.New("replay undelegate stake before mismatch")
	}
	if payload.DelegatedAfter != payload.DelegatedBefore-req.Amount {
		return errors.New("replay undelegate stake after mismatch")
	}
	s.consumeAccountNonceLocked(req.Delegator)
	existing.Amount = payload.DelegatedAfter
	if existing.Amount == 0 {
		delete(s.data.StakeDelegations, key)
	} else {
		s.data.StakeDelegations[key] = existing
	}
	account := s.accountLocked(req.Delegator)
	account.Balance += req.Amount
	s.data.Accounts[req.Delegator] = account
	validator := s.validatorLocked(req.Validator)
	if validator.DelegatedStake >= req.Amount {
		validator.DelegatedStake -= req.Amount
	} else {
		validator.DelegatedStake = 0
	}
	if existing.Amount == 0 && validator.DelegatorCount > 0 {
		validator.DelegatorCount--
	}
	s.data.Validators[req.Validator] = validator
	s.syncMinerDelegatorCountLocked(req.Validator, validator.DelegatorCount)
	return nil
}

func (s *Store) Delegation(delegator string, validator string) wire.StakeDelegation {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data.StakeDelegations == nil {
		return wire.StakeDelegation{}
	}
	return s.data.StakeDelegations[delegationKey(delegator, validator)]
}

func delegationKey(delegator string, validator string) string {
	return wire.NormalizeAddress(delegator) + ":" + wire.NormalizeAddress(validator)
}

func (s *Store) syncMinerDelegatorCountLocked(address string, count int) {
	miner, ok := s.data.Miners[address]
	if !ok {
		return
	}
	miner.DelegatorCount = count
	s.data.Miners[address] = miner
}
