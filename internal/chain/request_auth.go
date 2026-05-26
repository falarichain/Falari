package chain

import (
	"errors"

	"chain/internal/wire"
)

func (s *Store) verifyAccountRequestLocked(chainID string, address string, nonce uint64, verify func() error) error {
	if chainID == "" {
		return errors.New("chain_id is required")
	}
	if chainID != s.data.ChainID {
		return errors.New("request chain_id mismatch")
	}
	if err := verify(); err != nil {
		return err
	}
	account := s.accountLocked(wire.NormalizeAddress(address))
	if nonce != account.Nonce {
		return errors.New("invalid request nonce")
	}
	return nil
}

func (s *Store) consumeAccountNonceLocked(address string) {
	account := s.accountLocked(wire.NormalizeAddress(address))
	account.Nonce++
	s.data.Accounts[account.Address] = account
}
