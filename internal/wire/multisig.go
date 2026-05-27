package wire

import (
	"crypto/ecdsa"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

const (
	MultisigVersionByte    = 0x01
	MultisigMinSigners     = 2
	MultisigMaxSigners     = 16
	MultisigProposalPrefix = "fms_"
)

// multisigExecSigningPayload is the canonical payload for multisig execution signatures.
type multisigExecSigningPayload struct {
	Wallet      string `json:"wallet"`
	Operation   string `json:"operation"`
	PayloadHash string `json:"payload_hash"`
	ChainID     string `json:"chain_id"`
	Nonce       uint64 `json:"nonce"`
	Fee         uint64 `json:"fee"`
}

// multisigCreateSigningPayload is the canonical payload for multisig creation signatures.
type multisigCreateSigningPayload struct {
	ChainID   string   `json:"chain_id"`
	Signers   []string `json:"signers"`
	Threshold uint8    `json:"threshold"`
	Salt      uint64   `json:"salt"`
}

// MultisigAddress computes the deterministic address for a multisig wallet.
// Signers are sorted, then concatenated with the version byte, threshold, and salt.
func MultisigAddress(signers []string, threshold uint8, salt uint64) string {
	sorted := make([]string, len(signers))
	for i, s := range signers {
		sorted[i] = NormalizeAddress(s)
	}
	slices.SortFunc(sorted, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})

	buf := []byte{MultisigVersionByte}
	for _, addr := range sorted {
		a := common.HexToAddress(addr)
		buf = append(buf, a.Bytes()...)
	}
	buf = append(buf, threshold)
	saltBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(saltBytes, salt)
	buf = append(buf, saltBytes...)

	hash := ethcrypto.Keccak256(buf)
	return common.BytesToAddress(hash[12:]).Hex()
}

// ValidateMultisigSigners checks that the signer list is valid:
// 2–16 signers, all valid hex addresses, no duplicates, sorted.
func ValidateMultisigSigners(signers []string) error {
	n := len(signers)
	if n < MultisigMinSigners {
		return fmt.Errorf("multisig requires at least %d signers, got %d", MultisigMinSigners, n)
	}
	if n > MultisigMaxSigners {
		return fmt.Errorf("multisig supports at most %d signers, got %d", MultisigMaxSigners, n)
	}
	seen := make(map[string]bool, n)
	var prev string
	for i, s := range signers {
		if !common.IsHexAddress(s) {
			return fmt.Errorf("signer %d is not a valid hex address: %s", i, s)
		}
		norm := NormalizeAddress(s)
		lower := strings.ToLower(norm)
		if seen[lower] {
			return fmt.Errorf("duplicate signer: %s", s)
		}
		seen[lower] = true
		if i > 0 && strings.Compare(strings.ToLower(prev), lower) >= 0 {
			return errors.New("signers must be sorted by address (ascending, case-insensitive)")
		}
		prev = norm
	}
	return nil
}

// MultisigCreateHash computes the signing hash for a multisig creation request.
func MultisigCreateHash(req MultisigCreateRequest) ([]byte, error) {
	sorted := make([]string, len(req.Signers))
	for i, s := range req.Signers {
		sorted[i] = NormalizeAddress(s)
	}
	payload, err := json.Marshal(multisigCreateSigningPayload{
		ChainID:   req.ChainID,
		Signers:   sorted,
		Threshold: req.Threshold,
		Salt:      req.Salt,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

// SignMultisigCreate signs a multisig creation request with the given private key.
func SignMultisigCreate(req *MultisigCreateRequest, privateKey *ecdsa.PrivateKey) error {
	hash, err := MultisigCreateHash(*req)
	if err != nil {
		return err
	}
	sig, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return err
	}
	req.Signature = encodeHex(sig)
	return nil
}

// VerifyMultisigCreateSignature verifies that the creation signature is from one of the signers.
func VerifyMultisigCreateSignature(req MultisigCreateRequest) error {
	if req.Signature == "" {
		return errors.New("multisig create requires a signature from one of the signers")
	}
	sigBytes, err := decodeHex(req.Signature)
	if err != nil {
		return fmt.Errorf("invalid multisig create signature hex: %w", err)
	}
	if len(sigBytes) != 65 {
		return errors.New("invalid multisig create signature size")
	}
	hash, err := MultisigCreateHash(req)
	if err != nil {
		return err
	}
	pubKey, err := ethcrypto.SigToPub(hash, sigBytes)
	if err != nil {
		return fmt.Errorf("multisig create signature recovery failed: %w", err)
	}
	signerAddr := AccountAddress(pubKey)
	for _, s := range req.Signers {
		if strings.EqualFold(NormalizeAddress(s), signerAddr) {
			return nil
		}
	}
	return errors.New("multisig create signature is not from any of the signers")
}

// MultisigExecHash computes the signing hash for a multisig execution request.
func MultisigExecHash(req MultisigExecRequest) ([]byte, error) {
	innerHash := ethcrypto.Keccak256(req.Payload)
	payload, err := json.Marshal(multisigExecSigningPayload{
		Wallet:      NormalizeAddress(req.Wallet),
		Operation:   req.Operation,
		PayloadHash: encodeHex(innerHash),
		ChainID:     req.ChainID,
		Nonce:       req.Nonce,
		Fee:         req.Fee,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

// SignMultisigExec signs a multisig execution request and appends the signature.
// The caller must provide the signer's address and private key.
func SignMultisigExec(req *MultisigExecRequest, signer string, privateKey *ecdsa.PrivateKey) error {
	hash, err := MultisigExecHash(*req)
	if err != nil {
		return err
	}
	sig, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return err
	}
	req.Signatures = append(req.Signatures, MultisigSignature{
		Signer:    NormalizeAddress(signer),
		Signature: encodeHex(sig),
	})
	return nil
}

// VerifyMultisigExecSignatures verifies that the execution has at least threshold
// valid signatures from distinct signers in the wallet's signer set.
func VerifyMultisigExecSignatures(req MultisigExecRequest, wallet MultisigWallet) error {
	if len(req.Signatures) < int(wallet.Threshold) {
		return fmt.Errorf("insufficient signatures: need %d, got %d", wallet.Threshold, len(req.Signatures))
	}

	hash, err := MultisigExecHash(req)
	if err != nil {
		return err
	}

	// Build a set of allowed signers (lowercase for comparison).
	allowed := make(map[string]bool, len(wallet.Signers))
	for _, s := range wallet.Signers {
		allowed[strings.ToLower(NormalizeAddress(s))] = true
	}

	seen := make(map[string]bool, len(req.Signatures))
	var prevLower string

	for i, ms := range req.Signatures {
		sigBytes, err := decodeHex(ms.Signature)
		if err != nil {
			return fmt.Errorf("signature %d: invalid hex: %w", i, err)
		}
		if len(sigBytes) != 65 {
			return fmt.Errorf("signature %d: invalid size %d", i, len(sigBytes))
		}
		if sigBytes[64] > 1 {
			return fmt.Errorf("signature %d: invalid recovery id %d", i, sigBytes[64])
		}

		pubKey, err := ethcrypto.SigToPub(hash, sigBytes)
		if err != nil {
			return fmt.Errorf("signature %d: recovery failed: %w", i, err)
		}
		recovered := AccountAddress(pubKey)

		if !strings.EqualFold(recovered, ms.Signer) {
			return fmt.Errorf("signature %d: recovered address %s does not match claimed signer %s", i, recovered, ms.Signer)
		}

		recoveredLower := strings.ToLower(NormalizeAddress(recovered))
		if !allowed[recoveredLower] {
			return fmt.Errorf("signature %d: signer %s is not in the wallet's signer set", i, recovered)
		}
		if seen[recoveredLower] {
			return fmt.Errorf("signature %d: duplicate signer %s", i, recovered)
		}
		seen[recoveredLower] = true

		// Enforce sorted order to prevent reordering attacks.
		if i > 0 && strings.Compare(prevLower, recoveredLower) >= 0 {
			return fmt.Errorf("signature %d: signatures must be sorted by signer address (ascending)", i)
		}
		prevLower = recoveredLower
	}

	return nil
}
