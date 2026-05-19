package crypto

import (
	"crypto/sha256"
	"encoding/hex"
)

func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func HashPair(left, right string) string {
	return HashBytes([]byte(left + right))
}

func MerkleRoot(leaves []string) string {
	if len(leaves) == 0 {
		return HashBytes(nil)
	}

	level := append([]string(nil), leaves...)
	for len(level) > 1 {
		next := make([]string, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			left := level[i]
			right := left
			if i+1 < len(level) {
				right = level[i+1]
			}
			next = append(next, HashPair(left, right))
		}
		level = next
	}
	return level[0]
}
