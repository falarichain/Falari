package chain

import (
	"errors"
	"fmt"

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
	multiplierBPS := s.transactionFeeMultiplierBPS(tx.Type)
	requiredFee := s.data.FeeMarket.BaseFee * multiplierBPS / 10000
	if tx.Fee < requiredFee {
		return fmt.Errorf("transaction fee %d below required %d (base %d x %d bps)",
			tx.Fee, requiredFee, s.data.FeeMarket.BaseFee, multiplierBPS)
	}
	return nil
}

// transactionFeeMultiplierBPS returns the fee multiplier for the given
// transaction type in basis points. 10000 = 1.0x (no multiplier).
func (s *Store) transactionFeeMultiplierBPS(txType string) uint64 {
	m := s.data.FeeMarket.Multipliers
	switch txType {
	case "bridge_out":
		if m.BridgeOut > 0 {
			return m.BridgeOut
		}
	case "create_intent":
		if m.CreateIntent > 0 {
			return m.CreateIntent
		}
	case "upload_nft_template":
		if m.UploadNFTTemplate > 0 {
			return m.UploadNFTTemplate
		}
	case "register_validator":
		if m.RegisterValidator > 0 {
			return m.RegisterValidator
		}
	case "batch_commit":
		if m.BatchCommit > 0 {
			return m.BatchCommit
		}
	}
	return 10000
}

// GetFeeMarket returns the current fee market state.
func (s *Store) GetFeeMarket() wire.FeeMarket {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.FeeMarket
}

// SetFeeMarket updates fee market parameters. Must be operator-authenticated.
func (s *Store) SetFeeMarket(req wire.SetFeeMarketRequest) (wire.FeeMarket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.BaseFee != nil {
		if *req.BaseFee == 0 {
			return wire.FeeMarket{}, errors.New("base_fee must be > 0")
		}
		s.data.FeeMarket.BaseFee = *req.BaseFee
	}
	if req.TargetBlockTxs != nil {
		if *req.TargetBlockTxs <= 0 {
			return wire.FeeMarket{}, errors.New("target_block_txs must be > 0")
		}
		s.data.FeeMarket.TargetBlockTxs = *req.TargetBlockTxs
	}
	if req.Multipliers != nil {
		if err := applyFeeMultiplierUpdate(&s.data.FeeMarket.Multipliers, req.Multipliers); err != nil {
			return wire.FeeMarket{}, err
		}
	}

	if err := s.saveLocked(); err != nil {
		return wire.FeeMarket{}, err
	}
	return s.data.FeeMarket, nil
}

// applyFeeMultiplierUpdate applies non-zero multiplier values from the request
// onto the existing multipliers. Each value must be in [1000, 100000] BPS.
func applyFeeMultiplierUpdate(dst *wire.FeeMultipliers, src *wire.FeeMultipliers) error {
	applyField := func(name string, val uint64, dstField *uint64) error {
		if val == 0 {
			return nil
		}
		if val < 1000 || val > 100000 {
			return fmt.Errorf("%s multiplier %d out of range [1000, 100000]", name, val)
		}
		*dstField = val
		return nil
	}
	if err := applyField("bridge_out", src.BridgeOut, &dst.BridgeOut); err != nil {
		return err
	}
	if err := applyField("create_intent", src.CreateIntent, &dst.CreateIntent); err != nil {
		return err
	}
	if err := applyField("upload_nft_template", src.UploadNFTTemplate, &dst.UploadNFTTemplate); err != nil {
		return err
	}
	if err := applyField("register_validator", src.RegisterValidator, &dst.RegisterValidator); err != nil {
		return err
	}
	if err := applyField("batch_commit", src.BatchCommit, &dst.BatchCommit); err != nil {
		return err
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
	// Single account copy ensures correct accounting when payer == producer.
	// Previously, two separate copies caused the producer credit to overwrite
	// the payer debit, effectively creating tokens instead of a no-op.
	acct := s.accountLocked(payerAddress)
	if acct.Balance < fee {
		return errors.New("insufficient balance for transaction fee")
	}
	if payerAddress != producerAddress {
		acct.Balance -= fee
		s.data.Accounts[acct.Address] = acct
		producer := s.accountLocked(producerAddress)
		producer.Balance += fee
		s.data.Accounts[producer.Address] = producer
	}
	// When payer == producer the fee is a no-op: balance stays the same.
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
	// System-generated transactions: validators produce these automatically
	// during block production. Exempt from gas fee to avoid circular dependency
	// (validators would need to pay themselves).
	case "faucet", "genesis_credit", "validator_rotation",
		"submit_proof", "submit_delete_receipt", "submit_retrieval_receipt",
		"generate_challenges", "create_repair_tasks",
		"start_epoch", "finalize_epoch", "validator_evidence",
		"governance_deal_action", "committee_freeze_deal", "governance_block_deal",
		"direct_governance_action", "direct_action_review_vote":
		return false
	}
	// All other transaction types are user-submitted and MUST include a gas
	// fee (tx.Fee >= baseFee) to prevent fee-less DoS.  Storage transactions
	// charge gas (tx.Fee) separately from storage costs (payload.LockedFee).
	return true
}

func transferTotalCost(amount uint64, fee uint64) (uint64, error) {
	total := amount + fee
	if total < amount {
		return 0, errors.New("transfer total cost overflows")
	}
	return total, nil
}
