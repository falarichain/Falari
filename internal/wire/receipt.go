package wire

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
)

type receiptSigningPayload struct {
	Version          int    `json:"version"`
	MinerAddress     string `json:"miner_address"`
	MinerPublicKey   string `json:"miner_public_key"`
	User             string `json:"user"`
	IntentID         string `json:"intent_id"`
	FileRoot         string `json:"file_root"`
	SegmentID        int    `json:"segment_id"`
	SegmentRoot      string `json:"segment_root"`
	ShardIndex       int    `json:"shard_index"`
	ShardID          string `json:"shard_id"`
	ShardHash        string `json:"shard_hash"`
	ShardCID         string `json:"shard_cid,omitempty"`
	ShardSize        int64  `json:"shard_size"`
	SectorCommitment string `json:"sector_commitment"`
	ExpiresAtUnix    int64  `json:"expires_at_unix"`
}

func ReceiptPayload(r MinerReceipt) ([]byte, error) {
	payload := receiptSigningPayload{
		Version:          r.Version,
		MinerAddress:     r.MinerAddress,
		MinerPublicKey:   r.MinerPublicKey,
		User:             r.User,
		IntentID:         r.IntentID,
		FileRoot:         r.FileRoot,
		SegmentID:        r.SegmentID,
		SegmentRoot:      r.SegmentRoot,
		ShardIndex:       r.ShardIndex,
		ShardID:          r.ShardID,
		ShardHash:        r.ShardHash,
		ShardCID:         r.ShardCID,
		ShardSize:        r.ShardSize,
		SectorCommitment: r.SectorCommitment,
		ExpiresAtUnix:    r.ExpiresAtUnix,
	}
	return json.Marshal(payload)
}

func SignReceipt(r *MinerReceipt, privateKey ed25519.PrivateKey) error {
	payload, err := ReceiptPayload(*r)
	if err != nil {
		return err
	}
	r.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}

func VerifyReceipt(r MinerReceipt) error {
	publicKey, err := base64.StdEncoding.DecodeString(r.MinerPublicKey)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid miner public key size")
	}
	signature, err := base64.StdEncoding.DecodeString(r.Signature)
	if err != nil {
		return err
	}
	payload, err := ReceiptPayload(r)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("invalid receipt signature")
	}
	return nil
}
