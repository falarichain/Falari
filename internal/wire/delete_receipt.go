package wire

import (
	"crypto/ecdsa"
	"encoding/json"
)

type deleteReceiptSigningPayload struct {
	DeletedAtUnix int64  `json:"deleted_at_unix"`
	IntentID      string `json:"intent_id"`
	MinerAddress  string `json:"miner_address"`
	ShardHash     string `json:"shard_hash"`
}

func DeleteReceiptPayload(receipt DeleteReceipt) ([]byte, error) {
	payload := deleteReceiptSigningPayload{
		IntentID:      receipt.IntentID,
		ShardHash:     receipt.ShardHash,
		MinerAddress:  receipt.MinerAddress,
		DeletedAtUnix: receipt.DeletedAtUnix,
	}
	return json.Marshal(payload)
}

func SignDeleteReceipt(receipt *DeleteReceipt, privateKey *ecdsa.PrivateKey) error {
	payload := deleteReceiptSigningPayload{
		IntentID:      receipt.IntentID,
		ShardHash:     receipt.ShardHash,
		MinerAddress:  receipt.MinerAddress,
		DeletedAtUnix: receipt.DeletedAtUnix,
	}
	sig, pub, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	receipt.Signature = sig
	if receipt.MinerPublicKey == "" {
		receipt.MinerPublicKey = pub
	}
	return nil
}

func VerifyDeleteReceipt(receipt DeleteReceipt) error {
	payload := deleteReceiptSigningPayload{
		IntentID:      receipt.IntentID,
		ShardHash:     receipt.ShardHash,
		MinerAddress:  receipt.MinerAddress,
		DeletedAtUnix: receipt.DeletedAtUnix,
	}
	return verifyInfraSignature(receipt.MinerAddress, receipt.Signature, payload)
}
