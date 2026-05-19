package wire

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
)

type proofSigningPayload struct {
	ChallengeID        string     `json:"challenge_id"`
	ProofType          string     `json:"proof_type,omitempty"`
	ChallengeHash      string     `json:"challenge_hash,omitempty"`
	MinerAddress       string     `json:"miner_address"`
	ShardHash          string     `json:"shard_hash"`
	ShardSize          int64      `json:"shard_size"`
	SectorCommitment   string     `json:"sector_commitment"`
	LeafSize           int        `json:"leaf_size"`
	LeafIndex          int        `json:"leaf_index"`
	LeafIndices        []int      `json:"leaf_indices,omitempty"`
	LeafHash           string     `json:"leaf_hash"`
	LeafHashes         []string   `json:"leaf_hashes,omitempty"`
	LeafDataBase64     string     `json:"leaf_data_base64,omitempty"`
	LeafPayloadsBase64 []string   `json:"leaf_payloads_base64,omitempty"`
	MerklePath         []string   `json:"merkle_path"`
	MerklePaths        [][]string `json:"merkle_paths,omitempty"`
	ProofHash          string     `json:"proof_hash"`
}

func ProofPayload(p StorageProof) ([]byte, error) {
	payload := proofSigningPayload{
		ChallengeID:        p.ChallengeID,
		ProofType:          p.ProofType,
		ChallengeHash:      p.ChallengeHash,
		MinerAddress:       p.MinerAddress,
		ShardHash:          p.ShardHash,
		ShardSize:          p.ShardSize,
		SectorCommitment:   p.SectorCommitment,
		LeafSize:           p.LeafSize,
		LeafIndex:          p.LeafIndex,
		LeafIndices:        p.LeafIndices,
		LeafHash:           p.LeafHash,
		LeafHashes:         p.LeafHashes,
		LeafDataBase64:     p.LeafDataBase64,
		LeafPayloadsBase64: p.LeafPayloadsBase64,
		MerklePath:         p.MerklePath,
		MerklePaths:        p.MerklePaths,
		ProofHash:          p.ProofHash,
	}
	return json.Marshal(payload)
}

func SignProof(p *StorageProof, privateKey ed25519.PrivateKey) error {
	payload, err := ProofPayload(*p)
	if err != nil {
		return err
	}
	p.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}

func VerifyProof(p StorageProof) error {
	publicKey, err := base64.StdEncoding.DecodeString(p.MinerPublicKey)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid miner public key size")
	}
	signature, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil {
		return err
	}
	payload, err := ProofPayload(p)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("invalid proof signature")
	}
	return nil
}
