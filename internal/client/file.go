package client

import (
	"io"
	"os"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)
func ComputeSegmentRoots(path string, segmentSize int64) (int64, []string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, nil, "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return 0, nil, "", err
	}

	buf := make([]byte, segmentSize)
	var roots []string
	for {
		n, err := io.ReadFull(file, buf)
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			roots = append(roots, chaincrypto.HashBytes(buf[:n]))
			break
		}
		if err != nil {
			return 0, nil, "", err
		}
		roots = append(roots, chaincrypto.HashBytes(buf[:n]))
	}

	return info.Size(), roots, chaincrypto.MerkleRoot(roots), nil
}

func ComputeErasurePlan(path string, segmentSize int64, dataShards, parityShards int) (int64, []wire.SegmentPlan, []string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, nil, nil, "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return 0, nil, nil, "", err
	}

	var segments []wire.SegmentPlan
	var segmentRoots []string
	for segmentID, offset := 0, int64(0); offset < info.Size(); segmentID, offset = segmentID+1, offset+segmentSize {
		size := segmentSize
		if remaining := info.Size() - offset; remaining < size {
			size = remaining
		}
		shards, cleanup, err := EncodeSegmentToTempFiles(file, offset, size, dataShards, parityShards, "")
		if err != nil {
			return 0, nil, nil, "", err
		}
		shardHashes := make([]string, len(shards))
		shardCIDs := make([]string, len(shards))
		for i, shard := range shards {
			shardHashes[i] = shard.Hash
			shardCIDs[i], err = wire.RawCIDForHash(shard.Hash)
			if err != nil {
				cleanup()
				return 0, nil, nil, "", err
			}
		}
		cleanup()
		segmentRoot := chaincrypto.MerkleRoot(shardHashes)
		segments = append(segments, wire.SegmentPlan{
			SegmentID:   segmentID,
			SegmentRoot: segmentRoot,
			ShardHashes: shardHashes,
			ShardCIDs:   shardCIDs,
		})
		segmentRoots = append(segmentRoots, segmentRoot)
	}

	return info.Size(), segments, segmentRoots, chaincrypto.MerkleRoot(segmentRoots), nil
}

func ComputeEncryptedErasurePlan(path string, plaintextSegmentSize int64, dataShards, parityShards int, key []byte, nonce []byte) (int64, int64, *wire.EncryptionMetadata, []wire.SegmentPlan, []string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, nil, nil, nil, "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return 0, 0, nil, nil, nil, "", err
	}
	meta, err := EncryptionMetadata(key, nonce, info.Size(), plaintextSegmentSize)
	if err != nil {
		return 0, 0, nil, nil, nil, "", err
	}
	storedSegmentSize := plaintextSegmentSize + 16
	storedSize := int64(0)
	var segments []wire.SegmentPlan
	var segmentRoots []string
	for segmentID, offset := 0, int64(0); offset < info.Size(); segmentID, offset = segmentID+1, offset+plaintextSegmentSize {
		size := plaintextSegmentSize
		if remaining := info.Size() - offset; remaining < size {
			size = remaining
		}
		plain := make([]byte, size)
		if _, err := file.ReadAt(plain, offset); err != nil && err != io.EOF {
			return 0, 0, nil, nil, nil, "", err
		}
		ciphertext, err := EncryptSegment(plain, key, *meta, segmentID)
		if err != nil {
			return 0, 0, nil, nil, nil, "", err
		}
		shards, err := EncodeShards(ciphertext, dataShards, parityShards)
		if err != nil {
			return 0, 0, nil, nil, nil, "", err
		}
		shardHashes := make([]string, len(shards))
		shardCIDs := make([]string, len(shards))
		for i, shard := range shards {
			shardHashes[i] = chaincrypto.HashBytes(shard)
			shardCIDs[i], err = wire.RawCIDForBytes(shard)
			if err != nil {
				return 0, 0, nil, nil, nil, "", err
			}
		}
		segmentRoot := chaincrypto.MerkleRoot(shardHashes)
		segments = append(segments, wire.SegmentPlan{
			SegmentID:   segmentID,
			SegmentRoot: segmentRoot,
			ShardHashes: shardHashes,
			ShardCIDs:   shardCIDs,
		})
		segmentRoots = append(segmentRoots, segmentRoot)
		storedSize += int64(len(ciphertext))
	}
	return storedSize, storedSegmentSize, meta, segments, segmentRoots, chaincrypto.MerkleRoot(segmentRoots), nil
}

// ComputeRepairPools computes cross-parity repair pools for consecutive segment
// pairs. Each pool pairs two segments and computes XOR cross-parity shards,
// enabling single-shard repair with only 2 downloads instead of k.
// Segments that don't have a pair (odd count) are skipped.
func ComputeRepairPools(path string, segmentSize int64, segments []wire.SegmentPlan, dataShards, parityShards int) ([]wire.RepairPool, error) {
	if len(segments) < 2 {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var pools []wire.RepairPool
	for poolID := 0; poolID+1 < len(segments); poolID += 2 {
		segA := segments[poolID]
		segB := segments[poolID+1]

		shardsA, err := encodeSegmentFromFile(file, int64(segA.SegmentID)*segmentSize, segmentSize, dataShards, parityShards)
		if err != nil {
			return nil, err
		}
		shardsB, err := encodeSegmentFromFile(file, int64(segB.SegmentID)*segmentSize, segmentSize, dataShards, parityShards)
		if err != nil {
			return nil, err
		}

		crossShards, err := ComputeCrossParityShards(shardsA, shardsB)
		if err != nil {
			return nil, err
		}

		crossHashes := make([]string, len(crossShards))
		crossCIDs := make([]string, len(crossShards))
		var shardSize int64
		for i, shard := range crossShards {
			crossHashes[i] = chaincrypto.HashBytes(shard)
			crossCIDs[i], err = wire.RawCIDForHash(crossHashes[i])
			if err != nil {
				return nil, err
			}
			shardSize = int64(len(shard))
		}

		pools = append(pools, wire.RepairPool{
			PoolID:     poolID / 2,
			SegmentIDs: [2]int{segA.SegmentID, segB.SegmentID},
			CrossParity: wire.CrossParityPlan{
				ShardHashes: crossHashes,
				ShardCIDs:   crossCIDs,
				ShardSize:   shardSize,
			},
		})
	}
	return pools, nil
}

// ComputeRepairPoolsEncrypted computes cross-parity repair pools for encrypted
// uploads. Each segment is encrypted then RS-encoded before XOR cross-parity
// is computed, so the cross-parity shards operate on ciphertext shards.
func ComputeRepairPoolsEncrypted(path string, plaintextSegmentSize int64, key []byte, meta wire.EncryptionMetadata, segments []wire.SegmentPlan, dataShards, parityShards int) ([]wire.RepairPool, error) {
	if len(segments) < 2 {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var pools []wire.RepairPool
	for poolID := 0; poolID+1 < len(segments); poolID += 2 {
		segA := segments[poolID]
		segB := segments[poolID+1]

		shardsA, err := encryptAndEncodeSegment(file, int64(segA.SegmentID)*plaintextSegmentSize, plaintextSegmentSize, key, meta, segA.SegmentID, dataShards, parityShards)
		if err != nil {
			return nil, err
		}
		shardsB, err := encryptAndEncodeSegment(file, int64(segB.SegmentID)*plaintextSegmentSize, plaintextSegmentSize, key, meta, segB.SegmentID, dataShards, parityShards)
		if err != nil {
			return nil, err
		}

		crossShards, err := ComputeCrossParityShards(shardsA, shardsB)
		if err != nil {
			return nil, err
		}

		crossHashes := make([]string, len(crossShards))
		crossCIDs := make([]string, len(crossShards))
		var shardSize int64
		for i, shard := range crossShards {
			crossHashes[i] = chaincrypto.HashBytes(shard)
			crossCIDs[i], err = wire.RawCIDForHash(crossHashes[i])
			if err != nil {
				return nil, err
			}
			shardSize = int64(len(shard))
		}

		pools = append(pools, wire.RepairPool{
			PoolID:     poolID / 2,
			SegmentIDs: [2]int{segA.SegmentID, segB.SegmentID},
			CrossParity: wire.CrossParityPlan{
				ShardHashes: crossHashes,
				ShardCIDs:   crossCIDs,
				ShardSize:   shardSize,
			},
		})
	}
	return pools, nil
}

// encryptAndEncodeSegment reads plaintext from the file, encrypts it, and
// RS-encodes the ciphertext into shards.
func encryptAndEncodeSegment(file *os.File, offset, plaintextSegmentSize int64, key []byte, meta wire.EncryptionMetadata, segmentID, dataShards, parityShards int) ([][]byte, error) {
	plain := make([]byte, plaintextSegmentSize)
	if _, err := file.ReadAt(plain, offset); err != nil && err != io.EOF {
		return nil, err
	}
	// Trim to actual remaining bytes (last segment may be shorter).
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if remaining := info.Size() - offset; remaining < plaintextSegmentSize {
		plain = plain[:remaining]
	}
	ciphertext, err := EncryptSegment(plain, key, meta, segmentID)
	if err != nil {
		return nil, err
	}
	return EncodeShards(ciphertext, dataShards, parityShards)
}

// encodeSegmentFromFile reads a segment from the file and encodes it into shards.
// The data buffer is trimmed to the actual remaining bytes for the last segment,
// ensuring consistency with ComputeErasurePlan.
func encodeSegmentFromFile(file *os.File, offset, size int64, dataShards, parityShards int) ([][]byte, error) {
	data := make([]byte, size)
	if _, err := file.ReadAt(data, offset); err != nil && err != io.EOF {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if remaining := info.Size() - offset; remaining < size {
		data = data[:remaining]
	}
	return EncodeShards(data, dataShards, parityShards)
}
