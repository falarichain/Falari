package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// startEpochSigningPayload is the canonical payload for start_epoch operator signatures.
// The Signature field is excluded — it is what we are computing.
type startEpochSigningPayload struct {
	OperatorAddress     string `json:"operator_address"`
	ChainID             string `json:"chain_id"`
	IntentID            string `json:"intent_id,omitempty"`
	ChallengesPerDeal   int    `json:"challenges_per_deal"`
	DurationSeconds     int64  `json:"duration_seconds"`
	RewardPerProof      uint64 `json:"reward_per_proof"`
	SlashPerMissedProof uint64 `json:"slash_per_missed_proof"`
	Nonce               uint64 `json:"nonce"`
	CreatedAtUnix       int64  `json:"created_at_unix"`
}

// finalizeEpochSigningPayload is the canonical payload for finalize_epoch operator signatures.
type finalizeEpochSigningPayload struct {
	OperatorAddress string `json:"operator_address"`
	ChainID         string `json:"chain_id"`
	EpochID         string `json:"epoch_id"`
	Nonce           uint64 `json:"nonce"`
	CreatedAtUnix   int64  `json:"created_at_unix"`
}

// StartEpochPayload returns the JSON-encoded signing payload for a start epoch request.
func StartEpochPayload(req StartEpochRequest) ([]byte, error) {
	payload := startEpochSigningPayload{
		OperatorAddress:     req.OperatorAddress,
		ChainID:             req.ChainID,
		IntentID:            req.IntentID,
		ChallengesPerDeal:   req.ChallengesPerDeal,
		DurationSeconds:     req.DurationSeconds,
		RewardPerProof:      req.RewardPerProof,
		SlashPerMissedProof: req.SlashPerMissedProof,
		Nonce:               req.Nonce,
		CreatedAtUnix:       req.CreatedAtUnix,
	}
	return json.Marshal(payload)
}

// StartEpochHash returns the Keccak256 hash of the start epoch signing payload.
func StartEpochHash(req StartEpochRequest) ([]byte, error) {
	payload, err := StartEpochPayload(req)
	if err != nil {
		return nil, err
	}
	hash := ethcrypto.Keccak256(payload)
	return hash, nil
}

// SignStartEpochRequest signs the start epoch request with the operator's private key.
func SignStartEpochRequest(req *StartEpochRequest, privateKey *ecdsa.PrivateKey) error {
	hash, err := StartEpochHash(*req)
	if err != nil {
		return err
	}
	signature, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return err
	}
	req.Signature = encodeHex(signature)
	return nil
}

// VerifyStartEpochRequest verifies the operator signature on a start epoch request.
func VerifyStartEpochRequest(req StartEpochRequest, expectedAddress string) error {
	_, recoveredAddress, err := recoverStartEpochSigner(req)
	if err != nil {
		return err
	}
	if !strings.EqualFold(recoveredAddress, expectedAddress) {
		return errors.New("start epoch signature does not match operator address")
	}
	return nil
}

// recoverStartEpochSigner recovers the public key and address from a start epoch signature.
func recoverStartEpochSigner(req StartEpochRequest) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	hash, err := StartEpochHash(req)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := recoverSigner(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}

// FinalizeEpochPayload returns the JSON-encoded signing payload for a finalize epoch request.
func FinalizeEpochPayload(req FinalizeEpochRequest) ([]byte, error) {
	payload := finalizeEpochSigningPayload{
		OperatorAddress: req.OperatorAddress,
		ChainID:         req.ChainID,
		EpochID:         req.EpochID,
		Nonce:           req.Nonce,
		CreatedAtUnix:   req.CreatedAtUnix,
	}
	return json.Marshal(payload)
}

// FinalizeEpochHash returns the Keccak256 hash of the finalize epoch signing payload.
func FinalizeEpochHash(req FinalizeEpochRequest) ([]byte, error) {
	payload, err := FinalizeEpochPayload(req)
	if err != nil {
		return nil, err
	}
	hash := ethcrypto.Keccak256(payload)
	return hash, nil
}

// SignFinalizeEpochRequest signs the finalize epoch request with the operator's private key.
func SignFinalizeEpochRequest(req *FinalizeEpochRequest, privateKey *ecdsa.PrivateKey) error {
	hash, err := FinalizeEpochHash(*req)
	if err != nil {
		return err
	}
	signature, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return err
	}
	req.Signature = encodeHex(signature)
	return nil
}

// VerifyFinalizeEpochRequest verifies the operator signature on a finalize epoch request.
func VerifyFinalizeEpochRequest(req FinalizeEpochRequest, expectedAddress string) error {
	_, recoveredAddress, err := recoverFinalizeEpochSigner(req)
	if err != nil {
		return err
	}
	if !strings.EqualFold(recoveredAddress, expectedAddress) {
		return errors.New("finalize epoch signature does not match operator address")
	}
	return nil
}

// recoverFinalizeEpochSigner recovers the public key and address from a finalize epoch signature.
func recoverFinalizeEpochSigner(req FinalizeEpochRequest) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	hash, err := FinalizeEpochHash(req)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := recoverSigner(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}
