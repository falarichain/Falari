package chain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"chain/internal/wire"
)

// ── Transaction payload wrappers (for recordTxLocked + block replay) ──

type createMultisigTxPayload struct {
	Request  wire.MultisigCreateRequest `json:"request"`
	Response wire.MultisigWallet        `json:"response"`
}

type multisigExecTxPayload struct {
	Request  wire.MultisigExecRequest  `json:"request"`
	Response wire.MultisigExecResponse `json:"response"`
}

// ── Store methods ──

// CreateMultisigWallet registers a new multisig wallet on-chain.
func (s *Store) CreateMultisigWallet(req wire.MultisigCreateRequest) (wire.MultisigWallet, error) {
	// Normalize and validate signers.
	for i, signer := range req.Signers {
		req.Signers[i] = wire.NormalizeAddress(signer)
	}
	if err := wire.ValidateMultisigSigners(req.Signers); err != nil {
		return wire.MultisigWallet{}, err
	}
	if req.Threshold < 1 || int(req.Threshold) > len(req.Signers) {
		return wire.MultisigWallet{}, errors.New("threshold must be between 1 and the number of signers")
	}

	// Compute deterministic address.
	address := wire.MultisigAddress(req.Signers, req.Threshold, req.Salt)

	// Verify creation signature from one of the signers.
	if err := wire.VerifyMultisigCreateSignature(req); err != nil {
		return wire.MultisigWallet{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify chain_id to prevent cross-chain replay.
	if req.ChainID != s.data.ChainID {
		return wire.MultisigWallet{}, errors.New("multisig create chain_id mismatch")
	}

	if _, exists := s.data.MultisigWallets[address]; exists {
		return wire.MultisigWallet{}, errors.New("multisig wallet already exists at this address")
	}

	now := time.Now().Unix()
	wallet := wire.MultisigWallet{
		Address:       address,
		Signers:       req.Signers,
		Threshold:     req.Threshold,
		Nonce:         0,
		Salt:          req.Salt,
		CreatedAtUnix: now,
	}
	s.data.MultisigWallets[address] = &wallet

	// Ensure an account entry exists (it may already exist if funds were sent to the address before registration).
	if _, ok := s.data.Accounts[address]; !ok {
		s.data.Accounts[address] = wire.Account{Address: address}
	}

	s.recordTxLocked("create_multisig", address, createMultisigTxPayload{
		Request:  req,
		Response: wallet,
	})
	if err := s.saveLocked(); err != nil {
		return wire.MultisigWallet{}, err
	}
	return wallet, nil
}

// MultisigExec executes a multisig operation after verifying M-of-N signatures.
func (s *Store) MultisigExec(req wire.MultisigExecRequest) (wire.MultisigExecResponse, error) {
	req.Wallet = wire.NormalizeAddress(req.Wallet)
	if req.Wallet == "" {
		return wire.MultisigExecResponse{}, errors.New("wallet address is required")
	}
	if req.Operation == "" {
		return wire.MultisigExecResponse{}, errors.New("operation is required")
	}

	// Normalize signer addresses in signatures.
	for i := range req.Signatures {
		req.Signatures[i].Signer = wire.NormalizeAddress(req.Signatures[i].Signer)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify chain_id to prevent cross-chain replay.
	if req.ChainID != s.data.ChainID {
		return wire.MultisigExecResponse{}, errors.New("multisig exec chain_id mismatch")
	}

	wallet, ok := s.data.MultisigWallets[req.Wallet]
	if !ok {
		return wire.MultisigExecResponse{}, errors.New("multisig wallet not found")
	}

	// Nonce check.
	if req.Nonce != wallet.Nonce {
		return wire.MultisigExecResponse{}, errors.New("invalid multisig nonce")
	}

	// Fee check.
	if req.Fee < s.data.FeeMarket.BaseFee {
		return wire.MultisigExecResponse{}, errors.New("multisig exec fee below current base fee")
	}

	// Verify M-of-N signatures.
	if err := wire.VerifyMultisigExecSignatures(req, *wallet); err != nil {
		return wire.MultisigExecResponse{}, err
	}

	// Execute the inner operation.
	var resp wire.MultisigExecResponse
	resp.Wallet = *wallet

	switch req.Operation {
	case "transfer":
		var inner wire.MultisigTransferPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return wire.MultisigExecResponse{}, errors.New("invalid transfer payload")
		}
		inner.To = wire.NormalizeAddress(inner.To)
		if inner.To == "" {
			return wire.MultisigExecResponse{}, errors.New("transfer recipient is required")
		}
		if inner.Amount == 0 {
			return wire.MultisigExecResponse{}, errors.New("transfer amount must be positive")
		}

		fromAccount := s.accountLocked(wallet.Address)
		total := inner.Amount + req.Fee
		if total < inner.Amount {
			return wire.MultisigExecResponse{}, errors.New("transfer total overflows")
		}
		if fromAccount.Balance < total {
			return wire.MultisigExecResponse{}, errors.New("multisig wallet insufficient balance")
		}

		toAccount := s.accountLocked(inner.To)
		fromAccount.Balance -= inner.Amount + req.Fee
		toAccount.Balance += inner.Amount
		s.data.Accounts[wallet.Address] = fromAccount
		s.data.Accounts[inner.To] = toAccount

		txResp := wire.TransferResponse{From: fromAccount, To: toAccount}
		resp.TransferResponse = &txResp

	case "update_signers":
		var inner wire.MultisigUpdateSignersPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return wire.MultisigExecResponse{}, errors.New("invalid update_signers payload")
		}
		for i, signer := range inner.NewSigners {
			inner.NewSigners[i] = wire.NormalizeAddress(signer)
		}
		if err := wire.ValidateMultisigSigners(inner.NewSigners); err != nil {
			return wire.MultisigExecResponse{}, err
		}
		if inner.NewThreshold < 1 || int(inner.NewThreshold) > len(inner.NewSigners) {
			return wire.MultisigExecResponse{}, errors.New("threshold must be between 1 and the number of signers")
		}
		wallet.Signers = inner.NewSigners
		wallet.Threshold = inner.NewThreshold

	case "update_threshold":
		var inner wire.MultisigUpdateThresholdPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return wire.MultisigExecResponse{}, errors.New("invalid update_threshold payload")
		}
		if inner.NewThreshold < 1 || int(inner.NewThreshold) > len(wallet.Signers) {
			return wire.MultisigExecResponse{}, errors.New("threshold must be between 1 and the number of signers")
		}
		wallet.Threshold = inner.NewThreshold

	case "create_intent":
		var inner wire.MultisigCreateIntentPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return wire.MultisigExecResponse{}, errors.New("invalid create_intent payload")
		}
		if inner.FileName == "" {
			return wire.MultisigExecResponse{}, errors.New("file_name is required")
		}
		if inner.LockedFee == 0 {
			return wire.MultisigExecResponse{}, errors.New("locked_fee must be positive")
		}
		fromAccount := s.accountLocked(wallet.Address)
		total := inner.LockedFee + req.Fee
		if total < inner.LockedFee {
			return wire.MultisigExecResponse{}, errors.New("create_intent total overflows")
		}
		if fromAccount.Balance < total {
			return wire.MultisigExecResponse{}, errors.New("multisig wallet insufficient balance")
		}
		fromAccount.Balance -= total
		s.data.Accounts[wallet.Address] = fromAccount

		intentID := fmt.Sprintf("ms-%s-%d", wallet.Address, wallet.Nonce)
		s.data.Intents[intentID] = &Intent{
			IntentView: wire.IntentView{
				IntentID:  intentID,
				User:      wallet.Address,
				FileName:  inner.FileName,
				FileSize:  inner.FileSize,
				LockedFee: inner.LockedFee,
				Status:    wire.StatusUploading,
			},
		}
		resp.IntentID = intentID

	case "batch_transfer":
		var inner wire.MultisigBatchTransferPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return wire.MultisigExecResponse{}, errors.New("invalid batch_transfer payload")
		}
		if len(inner.Transfers) == 0 {
			return wire.MultisigExecResponse{}, errors.New("batch_transfer requires at least one transfer")
		}
		var totalAmount uint64
		for i, t := range inner.Transfers {
			inner.Transfers[i].To = wire.NormalizeAddress(t.To)
			if inner.Transfers[i].To == "" {
				return wire.MultisigExecResponse{}, fmt.Errorf("batch_transfer: transfer %d missing recipient", i)
			}
			if t.Amount == 0 {
				return wire.MultisigExecResponse{}, fmt.Errorf("batch_transfer: transfer %d amount must be positive", i)
			}
			newTotal := totalAmount + t.Amount
			if newTotal < totalAmount {
				return wire.MultisigExecResponse{}, fmt.Errorf("batch_transfer: total amount overflows at transfer %d", i)
			}
			totalAmount = newTotal
		}
		fromAccount := s.accountLocked(wallet.Address)
		totalWithFee := totalAmount + req.Fee
		if totalWithFee < totalAmount {
			return wire.MultisigExecResponse{}, errors.New("batch_transfer total overflows")
		}
		if fromAccount.Balance < totalWithFee {
			return wire.MultisigExecResponse{}, errors.New("multisig wallet insufficient balance")
		}
		fromAccount.Balance -= totalWithFee
		for _, t := range inner.Transfers {
			toAccount := s.accountLocked(t.To)
			toAccount.Balance += t.Amount
			s.data.Accounts[t.To] = toAccount
		}
		s.data.Accounts[wallet.Address] = fromAccount

	default:
		return wire.MultisigExecResponse{}, errors.New("unsupported multisig operation: " + req.Operation)
	}

	// Increment wallet nonce.
	wallet.Nonce++
	resp.Wallet = *wallet

	s.recordTxLocked("multisig_exec", wallet.Address, multisigExecTxPayload{
		Request:  req,
		Response: resp,
	})
	if err := s.saveLocked(); err != nil {
		return wire.MultisigExecResponse{}, err
	}
	return resp, nil
}

// GetMultisigWallet returns a multisig wallet with its current balance.
func (s *Store) GetMultisigWallet(address string) (wire.MultisigWalletInfo, error) {
	address = wire.NormalizeAddress(address)
	s.mu.Lock()
	defer s.mu.Unlock()

	wallet, ok := s.data.MultisigWallets[address]
	if !ok {
		return wire.MultisigWalletInfo{}, errors.New("multisig wallet not found")
	}
	account := s.accountLocked(address)
	return wire.MultisigWalletInfo{
		Wallet:  *wallet,
		Balance: account.Balance,
	}, nil
}

// ListMultisigWallets returns all multisig wallets, optionally filtered by signer.
func (s *Store) ListMultisigWallets(signer string) ([]wire.MultisigWalletInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	signerLower := ""
	if signer != "" {
		signerLower = strings.ToLower(wire.NormalizeAddress(signer))
	}

	result := make([]wire.MultisigWalletInfo, 0)
	for _, wallet := range s.data.MultisigWallets {
		if signerLower != "" {
			found := false
			for _, s := range wallet.Signers {
				if strings.ToLower(wire.NormalizeAddress(s)) == signerLower {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		account := s.accountLocked(wallet.Address)
		result = append(result, wire.MultisigWalletInfo{
			Wallet:  *wallet,
			Balance: account.Balance,
		})
	}
	return result, nil
}

// ── Replay-safe apply functions (called during block processing) ──

func (s *Store) applyCreateMultisigLocked(payload createMultisigTxPayload) error {
	wallet := payload.Response
	if wallet.Address == "" {
		return errors.New("replay create multisig missing address")
	}
	if _, exists := s.data.MultisigWallets[wallet.Address]; exists {
		return nil // Idempotent: wallet already registered.
	}
	// Verify the deterministic address.
	computed := wire.MultisigAddress(wallet.Signers, wallet.Threshold, wallet.Salt)
	if !strings.EqualFold(computed, wallet.Address) {
		return errors.New("replay create multisig address mismatch")
	}
	w := wallet // copy
	s.data.MultisigWallets[wallet.Address] = &w
	if _, ok := s.data.Accounts[wallet.Address]; !ok {
		s.data.Accounts[wallet.Address] = wire.Account{Address: wallet.Address}
	}
	return nil
}

func (s *Store) applyMultisigExecLocked(payload multisigExecTxPayload) error {
	req := payload.Request

	wallet, ok := s.data.MultisigWallets[req.Wallet]
	if !ok {
		return errors.New("replay multisig exec wallet not found")
	}

	// Re-verify wallet nonce — do NOT trust the block payload.
	if req.Nonce != wallet.Nonce {
		return errors.New("replay multisig exec nonce mismatch")
	}

	// Re-verify M-of-N signatures — a malicious block producer could
	// fabricate a multisig_exec without the required co-signers.
	if err := wire.VerifyMultisigExecSignatures(req, *wallet); err != nil {
		return errors.New("replay multisig exec signature verification failed: " + err.Error())
	}

	// Fee check.
	if req.Fee < s.data.FeeMarket.BaseFee {
		return errors.New("replay multisig exec fee below base fee")
	}

	// Re-execute the inner operation from scratch — do NOT trust payload.Response.
	switch req.Operation {
	case "transfer":
		var inner wire.MultisigTransferPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return err
		}
		inner.To = wire.NormalizeAddress(inner.To)
		if inner.To == "" {
			return errors.New("replay multisig exec transfer missing recipient")
		}
		if inner.Amount == 0 {
			return errors.New("replay multisig exec transfer amount must be positive")
		}

		fromAccount := s.accountLocked(wallet.Address)
		total := inner.Amount + req.Fee
		if total < inner.Amount {
			return errors.New("replay multisig exec transfer total overflows")
		}
		if fromAccount.Balance < total {
			return errors.New("replay multisig exec insufficient balance")
		}

		toAccount := s.accountLocked(inner.To)
		fromAccount.Balance -= inner.Amount + req.Fee
		toAccount.Balance += inner.Amount
		s.data.Accounts[wallet.Address] = fromAccount
		s.data.Accounts[inner.To] = toAccount

	case "update_signers":
		var inner wire.MultisigUpdateSignersPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return errors.New("replay multisig exec invalid update_signers payload")
		}
		for i, signer := range inner.NewSigners {
			inner.NewSigners[i] = wire.NormalizeAddress(signer)
		}
		if err := wire.ValidateMultisigSigners(inner.NewSigners); err != nil {
			return errors.New("replay multisig exec update_signers validation failed: " + err.Error())
		}
		if inner.NewThreshold < 1 || int(inner.NewThreshold) > len(inner.NewSigners) {
			return errors.New("replay multisig exec update_signers invalid threshold")
		}
		wallet.Signers = inner.NewSigners
		wallet.Threshold = inner.NewThreshold

	case "update_threshold":
		var inner wire.MultisigUpdateThresholdPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return errors.New("replay multisig exec invalid update_threshold payload")
		}
		if inner.NewThreshold < 1 || int(inner.NewThreshold) > len(wallet.Signers) {
			return errors.New("replay multisig exec update_threshold invalid threshold")
		}
		wallet.Threshold = inner.NewThreshold

	case "create_intent":
		var inner wire.MultisigCreateIntentPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return errors.New("replay multisig exec invalid create_intent payload")
		}
		if inner.FileName == "" {
			return errors.New("replay multisig exec create_intent missing file_name")
		}
		if inner.LockedFee == 0 {
			return errors.New("replay multisig exec create_intent locked_fee must be positive")
		}
		fromAccount := s.accountLocked(wallet.Address)
		total := inner.LockedFee + req.Fee
		if total < inner.LockedFee {
			return errors.New("replay multisig exec create_intent total overflows")
		}
		if fromAccount.Balance < total {
			return errors.New("replay multisig exec create_intent insufficient balance")
		}
		fromAccount.Balance -= total
		s.data.Accounts[wallet.Address] = fromAccount

		intentID := fmt.Sprintf("ms-%s-%d", wallet.Address, wallet.Nonce)
		s.data.Intents[intentID] = &Intent{
			IntentView: wire.IntentView{
				IntentID:  intentID,
				User:      wallet.Address,
				FileName:  inner.FileName,
				FileSize:  inner.FileSize,
				LockedFee: inner.LockedFee,
				Status:    wire.StatusUploading,
			},
		}

	case "batch_transfer":
		var inner wire.MultisigBatchTransferPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return errors.New("replay multisig exec invalid batch_transfer payload")
		}
		if len(inner.Transfers) == 0 {
			return errors.New("replay multisig exec batch_transfer empty transfers")
		}
		var totalAmount uint64
		for i, t := range inner.Transfers {
			inner.Transfers[i].To = wire.NormalizeAddress(t.To)
			if inner.Transfers[i].To == "" {
				return fmt.Errorf("replay multisig exec batch_transfer: transfer %d missing recipient", i)
			}
			if t.Amount == 0 {
				return fmt.Errorf("replay multisig exec batch_transfer: transfer %d amount must be positive", i)
			}
			newTotal := totalAmount + t.Amount
			if newTotal < totalAmount {
				return fmt.Errorf("replay multisig exec batch_transfer: total overflows at transfer %d", i)
			}
			totalAmount = newTotal
		}
		fromAccount := s.accountLocked(wallet.Address)
		totalWithFee := totalAmount + req.Fee
		if totalWithFee < totalAmount {
			return errors.New("replay multisig exec batch_transfer total overflows")
		}
		if fromAccount.Balance < totalWithFee {
			return errors.New("replay multisig exec batch_transfer insufficient balance")
		}
		fromAccount.Balance -= totalWithFee
		for _, t := range inner.Transfers {
			toAccount := s.accountLocked(t.To)
			toAccount.Balance += t.Amount
			s.data.Accounts[t.To] = toAccount
		}
		s.data.Accounts[wallet.Address] = fromAccount

	default:
		return errors.New("replay multisig exec unsupported operation: " + req.Operation)
	}

	wallet.Nonce++
	return nil
}
