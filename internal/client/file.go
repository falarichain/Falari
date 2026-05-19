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
