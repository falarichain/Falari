package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// ──────────────────────────────────────────────────────────────────────────────
// bridge_out signing
// ──────────────────────────────────────────────────────────────────────────────

type bridgeOutSigningPayload struct {
	ChainID       string `json:"chain_id"`
	Sender        string `json:"sender"`
	Recipient     string `json:"recipient"`
	TargetChainID string `json:"target_chain_id"`
	Amount        uint64 `json:"amount"`
	Fee           uint64 `json:"fee"`
	Nonce         uint64 `json:"nonce"`
}

func bridgeOutHash(req BridgeOutRequest, chainID string) ([]byte, error) {
	payload := bridgeOutSigningPayload{
		ChainID:       chainID,
		Sender:        req.Sender,
		Recipient:     req.Recipient,
		TargetChainID: req.TargetChainID,
		Amount:        req.Amount,
		Fee:           req.Fee,
		Nonce:         req.Nonce,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(raw), nil
}

// SignBridgeOut signs a bridge_out request with the sender's private key.
func SignBridgeOut(req *BridgeOutRequest, privateKey *ecdsa.PrivateKey, chainID string) error {
	if req.PublicKey == "" {
		req.PublicKey = encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey))
	}
	if req.Sender == "" {
		req.Sender = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := bridgeOutHash(*req, chainID)
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

// VerifyBridgeOutSignature verifies the sender's signature on a bridge_out request.
func VerifyBridgeOutSignature(req BridgeOutRequest, chainID string) error {
	sig, err := decodeHex(req.Signature)
	if err != nil {
		return err
	}
	if len(sig) != 65 {
		return errors.New("invalid bridge_out signature size")
	}
	hash, err := bridgeOutHash(req, chainID)
	if err != nil {
		return err
	}
	pub, err := ethcrypto.SigToPub(hash, sig)
	if err != nil {
		return err
	}
	addr := AccountAddress(pub)
	if !strings.EqualFold(addr, req.Sender) {
		return errors.New("bridge_out signature does not match sender")
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// bridge_in_claim signing (relayer)
// ──────────────────────────────────────────────────────────────────────────────

type bridgeInClaimSigningPayload struct {
	ChainID           string `json:"chain_id"`
	SourceTxHash      string `json:"source_tx_hash"`
	SourceBlockNumber uint64 `json:"source_block_number"`
	Recipient         string `json:"recipient"`
	Amount            uint64 `json:"amount"`
	Nonce             uint64 `json:"nonce"`
	Direction         string `json:"direction"`
}

func bridgeInClaimHash(req BridgeInClaimRequest, chainID string) ([]byte, error) {
	payload := bridgeInClaimSigningPayload{
		ChainID:           chainID,
		SourceTxHash:      req.SourceTxHash,
		SourceBlockNumber: req.SourceBlockNumber,
		Recipient:         req.Recipient,
		Amount:            req.Amount,
		Nonce:             req.Nonce,
		Direction:         req.Direction,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(raw), nil
}

// SignBridgeInClaim signs a bridge_in_claim request with the relayer's private key.
func SignBridgeInClaim(req *BridgeInClaimRequest, privateKey *ecdsa.PrivateKey, chainID string) error {
	if req.PublicKey == "" {
		req.PublicKey = encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey))
	}
	hash, err := bridgeInClaimHash(*req, chainID)
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

// VerifyBridgeInClaimSignature verifies the signature on a bridge_in_claim request.
func VerifyBridgeInClaimSignature(req BridgeInClaimRequest, chainID string) error {
	sig, err := decodeHex(req.Signature)
	if err != nil {
		return err
	}
	if len(sig) != 65 {
		return errors.New("invalid bridge_in_claim signature size")
	}
	hash, err := bridgeInClaimHash(req, chainID)
	if err != nil {
		return err
	}
	_, err = ethcrypto.SigToPub(hash, sig)
	return err
}

// RecoverBridgeInClaimSigner recovers the signer address from a bridge_in_claim signature.
func RecoverBridgeInClaimSigner(req BridgeInClaimRequest, chainID string) (string, error) {
	sig, err := decodeHex(req.Signature)
	if err != nil {
		return "", err
	}
	if len(sig) != 65 {
		return "", errors.New("invalid bridge_in_claim signature size")
	}
	hash, err := bridgeInClaimHash(req, chainID)
	if err != nil {
		return "", err
	}
	pub, err := ethcrypto.SigToPub(hash, sig)
	if err != nil {
		return "", err
	}
	return AccountAddress(pub), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// bridge_set_config signing (governance operator)
// ──────────────────────────────────────────────────────────────────────────────

type bridgeSetConfigSigningPayload struct {
	ChainID         string  `json:"chain_id"`
	Action          string  `json:"action"`
	RelayerAddress  string  `json:"relayer_address,omitempty"`
	MinBridgeAmount *uint64 `json:"min_bridge_amount,omitempty"`
	MaxAmountPerDay *uint64 `json:"max_amount_per_day,omitempty"`
	DelaySeconds    *int64  `json:"delay_seconds,omitempty"`
	Timestamp       int64   `json:"timestamp"`
}

func bridgeSetConfigHash(req BridgeSetConfigRequest, chainID string) ([]byte, error) {
	payload := bridgeSetConfigSigningPayload{
		ChainID:         chainID,
		Action:          req.Action,
		RelayerAddress:  req.RelayerAddress,
		MinBridgeAmount: req.MinBridgeAmount,
		MaxAmountPerDay: req.MaxAmountPerDay,
		DelaySeconds:    req.DelaySeconds,
		Timestamp:       req.Timestamp,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(raw), nil
}

// SignBridgeSetConfig signs a bridge_set_config request.
func SignBridgeSetConfig(req *BridgeSetConfigRequest, privateKey *ecdsa.PrivateKey, chainID string) error {
	if req.PublicKey == "" {
		req.PublicKey = encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey))
	}
	hash, err := bridgeSetConfigHash(*req, chainID)
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

// VerifyBridgeSetConfigSignature verifies the signature on a bridge_set_config request.
func VerifyBridgeSetConfigSignature(req BridgeSetConfigRequest, chainID string) error {
	sig, err := decodeHex(req.Signature)
	if err != nil {
		return err
	}
	if len(sig) != 65 {
		return errors.New("invalid bridge_set_config signature size")
	}
	hash, err := bridgeSetConfigHash(req, chainID)
	if err != nil {
		return err
	}
	_, err = ethcrypto.SigToPub(hash, sig)
	return err
}

// RecoverBridgeSetConfigSigner recovers the signer address from a bridge_set_config signature.
func RecoverBridgeSetConfigSigner(req BridgeSetConfigRequest, chainID string) (string, error) {
	sig, err := decodeHex(req.Signature)
	if err != nil {
		return "", err
	}
	if len(sig) != 65 {
		return "", errors.New("invalid bridge_set_config signature size")
	}
	hash, err := bridgeSetConfigHash(req, chainID)
	if err != nil {
		return "", err
	}
	pub, err := ethcrypto.SigToPub(hash, sig)
	if err != nil {
		return "", err
	}
	return AccountAddress(pub), nil
}
