package wire

import (
	"crypto/ecdsa"
	"encoding/json"
)

type proofSigningPayload struct {
	ChallengeHash      string     `json:"challenge_hash,omitempty"`
	ChallengeID        string     `json:"challenge_id"`
	LeafDataBase64     string     `json:"leaf_data_base64,omitempty"`
	LeafHash           string     `json:"leaf_hash"`
	LeafHashes         []string   `json:"leaf_hashes,omitempty"`
	LeafIndex          int        `json:"leaf_index"`
	LeafIndices        []int      `json:"leaf_indices,omitempty"`
	LeafPayloadsBase64 []string   `json:"leaf_payloads_base64,omitempty"`
	LeafSize           int        `json:"leaf_size"`
	MerklePath         []string   `json:"merkle_path"`
	MerklePaths        [][]string `json:"merkle_paths,omitempty"`
	MinerAddress       string     `json:"miner_address"`
	ProofHash          string     `json:"proof_hash"`
	ProofType          string     `json:"proof_type,omitempty"`
	SectorCommitment   string     `json:"sector_commitment"`
	ShardHash          string     `json:"shard_hash"`
	ShardSize          int64      `json:"shard_size"`
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

func SignProof(p *StorageProof, privateKey *ecdsa.PrivateKey) error {
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
	sig, pub, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	p.Signature = sig
	if p.MinerPublicKey == "" {
		p.MinerPublicKey = pub
	}
	return nil
}

func VerifyProof(p StorageProof) error {
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
	return verifyInfraSignature(p.MinerAddress, p.Signature, payload)
}
