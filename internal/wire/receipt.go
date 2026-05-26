package wire

import (
	"crypto/ecdsa"
	"encoding/json"
)

type receiptSigningPayload struct {
	ExpiresAtUnix    int64  `json:"expires_at_unix"`
	FileRoot         string `json:"file_root"`
	IntentID         string `json:"intent_id"`
	MinerAddress     string `json:"miner_address"`
	MinerPublicKey   string `json:"miner_public_key"`
	SectorCommitment string `json:"sector_commitment"`
	SegmentID        int    `json:"segment_id"`
	SegmentRoot      string `json:"segment_root"`
	ShardCID         string `json:"shard_cid,omitempty"`
	ShardHash        string `json:"shard_hash"`
	ShardID          string `json:"shard_id"`
	ShardIndex       int    `json:"shard_index"`
	ShardSize        int64  `json:"shard_size"`
	User             string `json:"user"`
	Version          int    `json:"version"`
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

func SignReceipt(r *MinerReceipt, privateKey *ecdsa.PrivateKey) error {
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
	sig, _, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	r.Signature = sig
	return nil
}

func VerifyReceipt(r MinerReceipt) error {
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
	return verifyInfraSignature(r.MinerAddress, r.Signature, payload)
}
