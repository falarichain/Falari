package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type retrievalReceiptClientPayload struct {
	BytesServed    uint64 `json:"bytes_served"`
	ClientAddress  string `json:"client_address"`
	IntentID       string `json:"intent_id"`
	MinerAddress   string `json:"miner_address"`
	MinerPublicKey string `json:"miner_public_key"`
	ReceiptID      string `json:"receipt_id"`
	RequestID      string `json:"request_id"`
	ServedAtUnix   int64  `json:"served_at_unix"`
	ShardHash      string `json:"shard_hash"`
	User           string `json:"user"`
}

type retrievalReceiptMinerPayload struct {
	retrievalReceiptClientPayload
	ClientSignature string `json:"client_signature"`
}

func RetrievalClientPayload(r RetrievalReceipt) ([]byte, error) {
	payload := retrievalReceiptClientPayload{
		ReceiptID:      r.ReceiptID,
		RequestID:      r.RequestID,
		IntentID:       r.IntentID,
		ShardHash:      r.ShardHash,
		User:           NormalizeAddress(r.User),
		ClientAddress:  NormalizeAddress(r.ClientAddress),
		MinerAddress:   r.MinerAddress,
		MinerPublicKey: r.MinerPublicKey,
		BytesServed:    r.BytesServed,
		ServedAtUnix:   r.ServedAtUnix,
	}
	return json.Marshal(payload)
}

func RetrievalMinerPayload(r RetrievalReceipt) ([]byte, error) {
	clientPayload := retrievalReceiptClientPayload{
		ReceiptID:      r.ReceiptID,
		RequestID:      r.RequestID,
		IntentID:       r.IntentID,
		ShardHash:      r.ShardHash,
		User:           NormalizeAddress(r.User),
		ClientAddress:  NormalizeAddress(r.ClientAddress),
		MinerAddress:   r.MinerAddress,
		MinerPublicKey: r.MinerPublicKey,
		BytesServed:    r.BytesServed,
		ServedAtUnix:   r.ServedAtUnix,
	}
	payload := retrievalReceiptMinerPayload{
		retrievalReceiptClientPayload: clientPayload,
		ClientSignature:               r.ClientSignature,
	}
	return json.Marshal(payload)
}

func SignRetrievalClientReceipt(r *RetrievalReceipt, privateKey *ecdsa.PrivateKey) error {
	if r.ClientAddress == "" {
		r.ClientAddress = AccountAddress(&privateKey.PublicKey)
	}
	if r.User == "" {
		r.User = r.ClientAddress
	}
	payload, err := RetrievalClientPayload(*r)
	if err != nil {
		return err
	}
	signature, err := ethcrypto.Sign(ethcrypto.Keccak256(payload), privateKey)
	if err != nil {
		return err
	}
	r.ClientSignature = encodeHex(signature)
	return nil
}

func SignRetrievalReceiptMiner(r *RetrievalReceipt, privateKey *ecdsa.PrivateKey) error {
	clientPayload := retrievalReceiptClientPayload{
		ReceiptID:      r.ReceiptID,
		RequestID:      r.RequestID,
		IntentID:       r.IntentID,
		ShardHash:      r.ShardHash,
		User:           NormalizeAddress(r.User),
		ClientAddress:  NormalizeAddress(r.ClientAddress),
		MinerAddress:   r.MinerAddress,
		MinerPublicKey: r.MinerPublicKey,
		BytesServed:    r.BytesServed,
		ServedAtUnix:   r.ServedAtUnix,
	}
	payload := retrievalReceiptMinerPayload{
		retrievalReceiptClientPayload: clientPayload,
		ClientSignature:               r.ClientSignature,
	}
	sig, pub, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	r.MinerSignature = sig
	if r.MinerPublicKey == "" {
		r.MinerPublicKey = pub
	}
	return nil
}

func VerifyRetrievalReceipt(r RetrievalReceipt) error {
	if r.ReceiptID == "" {
		return errors.New("retrieval receipt id is required")
	}
	if r.RequestID == "" {
		return errors.New("retrieval request id is required")
	}
	if r.IntentID == "" || r.ShardHash == "" {
		return errors.New("retrieval intent and shard hash are required")
	}
	if r.BytesServed == 0 {
		return errors.New("retrieval bytes served must be positive")
	}
	if r.ServedAtUnix <= 0 {
		return errors.New("retrieval served_at_unix is required")
	}
	if r.User == "" || r.ClientAddress == "" {
		return errors.New("retrieval user and client address are required")
	}
	if !strings.EqualFold(NormalizeAddress(r.User), NormalizeAddress(r.ClientAddress)) {
		return errors.New("retrieval client address must match user")
	}
	if err := VerifyRetrievalClientReceipt(r); err != nil {
		return err
	}
	return verifyRetrievalMinerSignature(r)
}

func VerifyRetrievalClientReceipt(r RetrievalReceipt) error {
	if r.ReceiptID == "" {
		return errors.New("retrieval receipt id is required")
	}
	if r.RequestID == "" {
		return errors.New("retrieval request id is required")
	}
	if r.IntentID == "" || r.ShardHash == "" {
		return errors.New("retrieval intent and shard hash are required")
	}
	if r.BytesServed == 0 {
		return errors.New("retrieval bytes served must be positive")
	}
	if r.ServedAtUnix <= 0 {
		return errors.New("retrieval served_at_unix is required")
	}
	if r.User == "" || r.ClientAddress == "" {
		return errors.New("retrieval user and client address are required")
	}
	if !strings.EqualFold(NormalizeAddress(r.User), NormalizeAddress(r.ClientAddress)) {
		return errors.New("retrieval client address must match user")
	}
	signature, err := decodeHex(r.ClientSignature)
	if err != nil {
		return err
	}
	payload, err := RetrievalClientPayload(r)
	if err != nil {
		return err
	}
	publicKey, err := recoverSigner(ethcrypto.Keccak256(payload), signature)
	if err != nil {
		return err
	}
	if !strings.EqualFold(AccountAddress(publicKey), NormalizeAddress(r.ClientAddress)) {
		return errors.New("retrieval client signature does not match client address")
	}
	return nil
}

func verifyRetrievalMinerSignature(r RetrievalReceipt) error {
	clientPayload := retrievalReceiptClientPayload{
		ReceiptID:      r.ReceiptID,
		RequestID:      r.RequestID,
		IntentID:       r.IntentID,
		ShardHash:      r.ShardHash,
		User:           NormalizeAddress(r.User),
		ClientAddress:  NormalizeAddress(r.ClientAddress),
		MinerAddress:   r.MinerAddress,
		MinerPublicKey: r.MinerPublicKey,
		BytesServed:    r.BytesServed,
		ServedAtUnix:   r.ServedAtUnix,
	}
	payload := retrievalReceiptMinerPayload{
		retrievalReceiptClientPayload: clientPayload,
		ClientSignature:               r.ClientSignature,
	}
	return verifyInfraSignature(r.MinerAddress, r.MinerSignature, payload)
}
