package client

import (
	"bytes"
	"os"
	"testing"

	"chain/internal/wire"
)

func TestComputeCrossParityShards(t *testing.T) {
	a := [][]byte{{1, 2, 3}, {4, 5, 6}}
	b := [][]byte{{7, 8, 9}, {10, 11, 12}}

	cross, err := ComputeCrossParityShards(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(cross) != 2 {
		t.Fatalf("expected 2 cross shards, got %d", len(cross))
	}
	// cross[0] = a[0] XOR b[0] = {1^7, 2^8, 3^9} = {6, 10, 10}
	expect := []byte{1 ^ 7, 2 ^ 8, 3 ^ 9}
	if !bytes.Equal(cross[0], expect) {
		t.Errorf("cross[0] = %v, want %v", cross[0], expect)
	}
}

func TestComputeCrossParityShards_UnevenLengths(t *testing.T) {
	a := [][]byte{{1, 2}}
	b := [][]byte{{3, 4, 5, 6}}

	cross, err := ComputeCrossParityShards(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(cross[0]) != 4 {
		t.Fatalf("expected length 4, got %d", len(cross[0]))
	}
	// a[0] zero-padded to {1,2,0,0}, XOR with {3,4,5,6} = {2,6,5,6}
	expect := []byte{1 ^ 3, 2 ^ 4, 0 ^ 5, 0 ^ 6}
	if !bytes.Equal(cross[0], expect) {
		t.Errorf("cross[0] = %v, want %v", cross[0], expect)
	}
}

func TestComputeCrossParityShards_CountMismatch(t *testing.T) {
	_, err := ComputeCrossParityShards([][]byte{{1}}, [][]byte{{1}, {2}})
	if err == nil {
		t.Fatal("expected error for mismatched shard count")
	}
}

func TestRepairFromCrossParity(t *testing.T) {
	original := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	peer := []byte{0x11, 0x22, 0x33, 0x44}
	// cross = original XOR peer
	cross := make([]byte, len(original))
	for i := range original {
		cross[i] = original[i] ^ peer[i]
	}

	recovered, err := RepairFromCrossParity(peer, cross)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recovered, original) {
		t.Errorf("recovered = %x, want %x", recovered, original)
	}
}

func TestRepairFromCrossParity_SizeMismatch(t *testing.T) {
	_, err := RepairFromCrossParity([]byte{1, 2}, []byte{1})
	if err == nil {
		t.Fatal("expected error for size mismatch")
	}
}

func TestCrossParityRoundTrip_WithRS(t *testing.T) {
	data := bytes.Repeat([]byte("repair-pool-test"), 1024)

	shardsA, err := EncodeShards(data, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	shardsB, err := EncodeShards(data, 4, 2)
	if err != nil {
		t.Fatal(err)
	}

	cross, err := ComputeCrossParityShards(shardsA, shardsB)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate losing shardA[2] — repair using shardB[2] + cross[2].
	recovered, err := RepairFromCrossParity(shardsB[2], cross[2])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recovered, shardsA[2]) {
		t.Error("cross-parity round-trip failed: recovered shard does not match original")
	}
}

func TestHashCrossParityShards(t *testing.T) {
	shards := [][]byte{{1, 2, 3}, {4, 5, 6}}
	hashes, err := HashCrossParityShards(shards)
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 2 {
		t.Fatalf("expected 2 hashes, got %d", len(hashes))
	}
	if hashes[0] == "" || hashes[1] == "" {
		t.Error("empty hash returned")
	}
	if hashes[0] == hashes[1] {
		t.Error("different shards produced same hash")
	}
}

func TestHashCrossParityShards_NilShard(t *testing.T) {
	_, err := HashCrossParityShards([][]byte{{1}, nil})
	if err == nil {
		t.Fatal("expected error for nil shard")
	}
}

func TestComputeRepairPoolsEncrypted_RoundTrip(t *testing.T) {
	// Create a temp file with enough data for 2 segments.
	plaintextSegmentSize := int64(1024)
	data := bytes.Repeat([]byte("encrypted-cross-parity-test!"), 128) // 3072 bytes = 3 segments
	tmpFile, err := writeTempFile(data)
	if err != nil {
		t.Fatal(err)
	}
	defer removeTempFile(tmpFile)

	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := GenerateEncryptionNonce()
	if err != nil {
		t.Fatal(err)
	}

	_, _, meta, segments, _, _, err := ComputeEncryptedErasurePlan(tmpFile, plaintextSegmentSize, 4, 2, key, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) < 2 {
		t.Fatalf("need at least 2 segments, got %d", len(segments))
	}

	pools, err := ComputeRepairPoolsEncrypted(tmpFile, plaintextSegmentSize, key, *meta, segments, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) == 0 {
		t.Fatal("expected at least 1 repair pool")
	}

	pool := pools[0]
	if pool.SegmentIDs[0] != 0 || pool.SegmentIDs[1] != 1 {
		t.Errorf("unexpected segment IDs: %v", pool.SegmentIDs)
	}
	if len(pool.CrossParity.ShardHashes) != 6 { // 4 data + 2 parity
		t.Errorf("expected 6 cross-parity shards, got %d", len(pool.CrossParity.ShardHashes))
	}

	// Verify cross-parity can recover a lost shard.
	// Re-encrypt and re-encode segment 0 to get its shards.
	shardsA, err := encryptAndEncodeSegmentForTest(tmpFile, 0, plaintextSegmentSize, key, *meta, 0, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	shardsB, err := encryptAndEncodeSegmentForTest(tmpFile, plaintextSegmentSize, plaintextSegmentSize, key, *meta, 1, 4, 2)
	if err != nil {
		t.Fatal(err)
	}

	// Re-compute cross-parity and verify repair.
	crossShards, err := ComputeCrossParityShards(shardsA, shardsB)
	if err != nil {
		t.Fatal(err)
	}

	// Lose shardA[3], recover from shardB[3] + cross[3].
	recovered, err := RepairFromCrossParity(shardsB[3], crossShards[3])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recovered, shardsA[3]) {
		t.Error("encrypted cross-parity round-trip: recovered shard does not match original")
	}
}

func writeTempFile(data []byte) (string, error) {
	f, err := os.CreateTemp("", "cross-parity-enc-test-*")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

func removeTempFile(path string) {
	os.Remove(path)
}

func encryptAndEncodeSegmentForTest(path string, offset, plaintextSegmentSize int64, key []byte, meta wire.EncryptionMetadata, segmentID, dataShards, parityShards int) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return encryptAndEncodeSegment(f, offset, plaintextSegmentSize, key, meta, segmentID, dataShards, parityShards)
}
