package chain

import (
	"errors"

	"chain/internal/wire"
)

func (s *Store) BaseFee() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.FeeMarket.BaseFee
}

func (s *Store) validateTransactionFeeLocked(tx wire.Transaction) error {
	if !transactionRequiresBaseFee(tx) {
		return nil
	}
	if tx.Fee < s.data.FeeMarket.BaseFee {
		return errors.New("transaction fee below current base fee")
	}
	return nil
}

func (s *Store) chargeTransactionFeeLocked(tx wire.Transaction, producerAddress string) error {
	if tx.Fee == 0 || s.data.FeeChargedTxs[tx.TxID] {
		return nil
	}
	fee := s.chargeableTransactionFeeLocked(tx)
	if fee == 0 {
		s.data.FeeChargedTxs[tx.TxID] = true
		return nil
	}
	payerAddress := wire.NormalizeAddress(tx.From)
	if payerAddress == "" {
		return errors.New("fee payer is required")
	}
	payer := s.accountLocked(payerAddress)
	if payer.Balance < fee {
		return errors.New("insufficient balance for transaction fee")
	}
	producer := s.accountLocked(producerAddress)
	payer.Balance -= fee
	producer.Balance += fee
	s.data.Accounts[payer.Address] = payer
	s.data.Accounts[producer.Address] = producer
	s.data.FeeChargedTxs[tx.TxID] = true
	return nil
}

func (s *Store) chargeableTransactionFeeLocked(tx wire.Transaction) uint64 {
	return tx.Fee
}

func (s *Store) adjustFeeMarketAfterBlockLocked(block wire.Block) {
	market := s.data.FeeMarket
	if market.BaseFee == 0 {
		market = defaultFeeMarket()
	}
	if market.TargetBlockTxs <= 0 {
		market.TargetBlockTxs = defaultTargetBlockTxs
	}
	txCount := len(block.Transactions)
	switch {
	case txCount > market.TargetBlockTxs:
		step := market.BaseFee / 8
		if step == 0 {
			step = 1
		}
		market.BaseFee += step
	case txCount < market.TargetBlockTxs && market.BaseFee > defaultBaseFee:
		step := market.BaseFee / 8
		if step == 0 {
			step = 1
		}
		if market.BaseFee <= defaultBaseFee+step {
			market.BaseFee = defaultBaseFee
		} else {
			market.BaseFee -= step
		}
	}
	market.LastBlockTxs = txCount
	market.UpdatedAtHeight = block.Height
	s.data.FeeMarket = market
}

func transactionRequiresBaseFee(tx wire.Transaction) bool {
	switch tx.Type {
	// Types that already have Fee fields in their request structs
	// and are properly handled by enrichTransactionMetadata.
	case "transfer", "multisig_exec", "bridge_out":
		return true
	}
	// TODO(H-04): The following transaction types SHOULD also require a base
	// fee to prevent fee-less DoS, but their request structs currently lack a
	// Fee field.  Adding fee support requires:
	//   1. Add a `Fee uint64` field to each request struct in wire/types.go
	//   2. Add a case in enrichTransactionMetadata to extract Fee from payload
	//   3. Add the type to the switch above
	//
	// Pending types:
	//   create_intent, batch_commit, finalize_deal, settle_intent,
	//   renew_deal, terminate_deal, set_access_policy,
	//   register_miner, deregister_miner, adjust_capacity,
	//   register_validator, deregister_validator,
	//   delegate_stake, undelegate_stake,
	//   governance_create_proposal, governance_cast_vote, governance_execute_proposal,
	//   create_collection, append_record,
	//   register_agent_key, revoke_agent_key, extend_agent_key, topup_agent_key,
	//   create_key_envelope, create_share, revoke_share
	return false
}

func transferTotalCost(amount uint64, fee uint64) (uint64, error) {
	total := amount + fee
	if total < amount {
		return 0, errors.New("transfer total cost overflows")
	}
	return total, nil
}
