package client

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ComputeCrossParityShards XORs corresponding shards from two segments to
// produce cross-parity shards. Both inputs must have the same shard count.
// If shards differ in length the shorter one is zero-padded.
func ComputeCrossParityShards(shardsA, shardsB [][]byte) ([][]byte, error) {
	if len(shardsA) != len(shardsB) {
		return nil, errors.New("cross-parity: shard count mismatch")
	}
	if len(shardsA) == 0 {
		return nil, errors.New("cross-parity: empty shard slices")
	}
	cross := make([][]byte, len(shardsA))
	for i := range shardsA {
		a, b := shardsA[i], shardsB[i]
		size := len(a)
		if len(b) > size {
			size = len(b)
		}
		out := make([]byte, size)
		for j := 0; j < size; j++ {
			var va, vb byte
			if j < len(a) {
				va = a[j]
			}
			if j < len(b) {
				vb = b[j]
			}
			out[j] = va ^ vb
		}
		cross[i] = out
	}
	return cross, nil
}

// RepairFromCrossParity recovers a lost shard by XORing the surviving peer
// shard with the cross-parity shard: lost = peer ⊕ cross_parity.
func RepairFromCrossParity(peerShard, crossParityShard []byte) ([]byte, error) {
	if len(peerShard) != len(crossParityShard) {
		return nil, errors.New("cross-parity repair: shard size mismatch")
	}
	out := make([]byte, len(peerShard))
	for i := range out {
		out[i] = peerShard[i] ^ crossParityShard[i]
	}
	return out, nil
}

// HashCrossParityShards computes the SHA-256 hex hash for each cross-parity
// shard, returning them in the same order.
func HashCrossParityShards(shards [][]byte) ([]string, error) {
	hashes := make([]string, len(shards))
	for i, shard := range shards {
		if shard == nil {
			return nil, errors.New("cross-parity hash: nil shard")
		}
		h := sha256.Sum256(shard)
		hashes[i] = hex.EncodeToString(h[:])
	}
	return hashes, nil
}
