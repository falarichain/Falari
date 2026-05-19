package wire

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
)

type deleteReceiptSigningPayload struct {
	IntentID      string `json:"intent_id"`
	ShardHash     string `json:"shard_hash"`
	MinerAddress  string `json:"miner_address"`
	DeletedAtUnix int64  `json:"deleted_at_unix"`
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

func SignDeleteReceipt(receipt *DeleteReceipt, privateKey ed25519.PrivateKey) error {
	payload, err := DeleteReceiptPayload(*receipt)
	if err != nil {
		return err
	}
	receipt.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}

func VerifyDeleteReceipt(receipt DeleteReceipt) error {
	publicKey, err := base64.StdEncoding.DecodeString(receipt.MinerPublicKey)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid miner public key size")
	}
	signature, err := base64.StdEncoding.DecodeString(receipt.Signature)
	if err != nil {
		return err
	}
	payload, err := DeleteReceiptPayload(receipt)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("invalid delete receipt signature")
	}
	return nil
}
