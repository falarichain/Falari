package chain

import (
	"encoding/base64"
	"errors"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

const proofTypeMerklePORV1 = "merkle-por-v1"

func validateStorageProof(challenge wire.StorageChallenge, proof wire.StorageProof) error {
	if proof.ShardHash != challenge.ShardHash {
		return errors.New("proof shard mismatch")
	}
	if proof.ShardSize != challenge.ShardSize {
		return errors.New("proof shard size mismatch")
	}
	if proof.SectorCommitment != challenge.SectorCommitment {
		return errors.New("proof sector commitment mismatch")
	}
	if proof.LeafSize != challenge.LeafSize {
		return errors.New("proof leaf size mismatch")
	}
	if challenge.ProofType != "" && proof.ProofType != challenge.ProofType {
		return errors.New("proof type mismatch")
	}
	if challenge.ChallengeHash != "" && proof.ChallengeHash != challenge.ChallengeHash {
		return errors.New("proof challenge hash mismatch")
	}

	indices := challengeLeafIndicesForValidation(challenge)
	proofIndices := proofLeafIndices(proof)
	leafHashes := proofLeafHashes(proof)
	paths := proofMerklePaths(proof)
	payloads := proofLeafPayloads(proof)
	if len(proofIndices) != len(indices) || len(leafHashes) != len(indices) || len(paths) != len(indices) || len(payloads) != len(indices) {
		return errors.New("proof sample count mismatch")
	}
	ranges := challengeLeafRangesForValidation(challenge, indices)
	for i, expectedIndex := range indices {
		if proofIndices[i] != expectedIndex {
			return errors.New("proof leaf challenge mismatch")
		}
		payload, err := base64.StdEncoding.DecodeString(payloads[i])
		if err != nil {
			return errors.New("invalid proof leaf payload encoding")
		}
		if len(payload) != ranges[i].Length {
			return errors.New("proof leaf payload length mismatch")
		}
		if chaincrypto.HashBytes(payload) != leafHashes[i] {
			return errors.New("proof leaf payload hash mismatch")
		}
		if !chaincrypto.VerifyMerkleProof(proof.SectorCommitment, leafHashes[i], expectedIndex, paths[i]) {
			return errors.New("invalid merkle proof")
		}
	}
	if proof.ProofHash != expectedProofHash(challenge, proof) {
		return errors.New("proof hash mismatch")
	}
	return nil
}

func challengeLeafIndicesForValidation(challenge wire.StorageChallenge) []int {
	if len(challenge.LeafIndices) > 0 {
		return append([]int(nil), challenge.LeafIndices...)
	}
	return []int{challenge.LeafIndex}
}

func proofLeafIndices(proof wire.StorageProof) []int {
	if len(proof.LeafIndices) > 0 {
		return append([]int(nil), proof.LeafIndices...)
	}
	return []int{proof.LeafIndex}
}

func challengeLeafRangesForValidation(challenge wire.StorageChallenge, indices []int) []wire.LeafRange {
	if len(challenge.LeafRanges) == len(indices) {
		return append([]wire.LeafRange(nil), challenge.LeafRanges...)
	}
	ranges := make([]wire.LeafRange, 0, len(indices))
	for _, index := range indices {
		ranges = append(ranges, challengeLeafRange(challenge.ShardSize, challenge.LeafSize, index))
	}
	return ranges
}

func proofLeafHashes(proof wire.StorageProof) []string {
	if len(proof.LeafHashes) > 0 {
		return append([]string(nil), proof.LeafHashes...)
	}
	return []string{proof.LeafHash}
}

func proofMerklePaths(proof wire.StorageProof) [][]string {
	if len(proof.MerklePaths) > 0 {
		paths := make([][]string, 0, len(proof.MerklePaths))
		for _, path := range proof.MerklePaths {
			paths = append(paths, append([]string(nil), path...))
		}
		return paths
	}
	return [][]string{append([]string(nil), proof.MerklePath...)}
}

func proofLeafPayloads(proof wire.StorageProof) []string {
	if len(proof.LeafPayloadsBase64) > 0 {
		return append([]string(nil), proof.LeafPayloadsBase64...)
	}
	if proof.LeafDataBase64 != "" {
		return []string{proof.LeafDataBase64}
	}
	return nil
}
