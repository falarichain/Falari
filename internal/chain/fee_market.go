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
	return tx.Type == "transfer" || tx.Type == "multisig_exec"
}

func transferTotalCost(amount uint64, fee uint64) (uint64, error) {
	total := amount + fee
	if total < amount {
		return 0, errors.New("transfer total cost overflows")
	}
	return total, nil
}
