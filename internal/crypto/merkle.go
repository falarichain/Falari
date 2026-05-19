package crypto

import "errors"

const DefaultLeafSize = 32 * 1024

type MerkleProof struct {
	Root      string
	LeafHash  string
	LeafIndex int
	LeafSize  int
	Path      []string
}

func DataMerkleRoot(data []byte, leafSize int) string {
	proof, err := BuildMerkleProof(data, leafSize, 0)
	if err != nil {
		return HashBytes(nil)
	}
	return proof.Root
}

func BuildMerkleProof(data []byte, leafSize int, leafIndex int) (MerkleProof, error) {
	if leafSize <= 0 {
		return MerkleProof{}, errors.New("leaf size must be positive")
	}
	leaves := leafHashes(data, leafSize)
	if len(leaves) == 0 {
		leaves = []string{HashBytes(nil)}
	}
	if leafIndex < 0 || leafIndex >= len(leaves) {
		return MerkleProof{}, errors.New("leaf index out of range")
	}

	path := make([]string, 0)
	index := leafIndex
	level := append([]string(nil), leaves...)
	for len(level) > 1 {
		sibling := index ^ 1
		if sibling >= len(level) {
			sibling = index
		}
		path = append(path, level[sibling])

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
		index /= 2
	}

	return MerkleProof{
		Root:      level[0],
		LeafHash:  leaves[leafIndex],
		LeafIndex: leafIndex,
		LeafSize:  leafSize,
		Path:      path,
	}, nil
}

func VerifyMerkleProof(root string, leafHash string, leafIndex int, path []string) bool {
	if root == "" || leafHash == "" || leafIndex < 0 {
		return false
	}
	computed := leafHash
	index := leafIndex
	for _, sibling := range path {
		if index%2 == 0 {
			computed = HashPair(computed, sibling)
		} else {
			computed = HashPair(sibling, computed)
		}
		index /= 2
	}
	return computed == root
}

func LeafCount(size int64, leafSize int) int {
	if leafSize <= 0 || size <= 0 {
		return 1
	}
	return int((size + int64(leafSize) - 1) / int64(leafSize))
}

func leafHashes(data []byte, leafSize int) []string {
	if len(data) == 0 {
		return []string{HashBytes(nil)}
	}
	leaves := make([]string, 0, (len(data)+leafSize-1)/leafSize)
	for start := 0; start < len(data); start += leafSize {
		end := start + leafSize
		if end > len(data) {
			end = len(data)
		}
		leaves = append(leaves, HashBytes(data[start:end]))
	}
	return leaves
}
