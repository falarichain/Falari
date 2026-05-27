package chain

import (
	"encoding/json"
	"errors"
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

	// Currently only "transfer" is supported.
	if req.Operation != "transfer" {
		return wire.MultisigExecResponse{}, errors.New("unsupported multisig operation: " + req.Operation)
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
	default:
		return errors.New("replay multisig exec unsupported operation: " + req.Operation)
	}

	wallet.Nonce++
	return nil
}
