package client

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeShardsWithOneMissingShard(t *testing.T) {
	data := bytes.Repeat([]byte("storage-chain"), 1024)
	shards, err := EncodeShards(data, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	shards[1] = nil
	restored, err := DecodeShards(shards, 2, 1, len(data))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, data) {
		t.Fatal("restored data mismatch")
	}
}

// A segment sliced out of a pooled buffer carries capacity that Reed-Solomon would happily
// use as parity scratch space, overwriting whatever the caller keeps after the segment.
func TestEncodeShardsDoesNotWritePastTheInput(t *testing.T) {
	const (
		dataShards      = 4
		parityShards    = 2
		segmentLength   = 40_000
		survivingLength = 20_000
	)
	backing := make([]byte, 3*segmentLength)
	for i := range backing {
		backing[i] = byte(i * 31)
	}
	segment := backing[:segmentLength]
	beyond := append([]byte(nil), backing[segmentLength:segmentLength+survivingLength]...)

	shards, err := EncodeShards(segment, dataShards, parityShards)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != dataShards+parityShards {
		t.Fatalf("got %d shards, want %d", len(shards), dataShards+parityShards)
	}
	if !bytes.Equal(backing[segmentLength:segmentLength+survivingLength], beyond) {
		t.Fatal("EncodeShards overwrote the caller's bytes past the input slice")
	}
	for i := 0; i < dataShards; i++ {
		if want := segment[i*len(shards[0]) : (i+1)*len(shards[0])]; !bytes.Equal(shards[i], want) {
			t.Fatalf("data shard %d does not hold its slice of the input", i)
		}
	}

	restored, err := DecodeShards(shards, dataShards, parityShards, segmentLength)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, segment) {
		t.Fatal("restored data mismatch")
	}
}

func TestStreamingSegmentEncodeMatchesDecode(t *testing.T) {
	data := bytes.Repeat([]byte("streaming-storage-chain"), 8192)
	path := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	shardFiles, cleanup, err := EncodeSegmentToTempFiles(file, 0, int64(len(data)), 4, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	shards := make([][]byte, len(shardFiles))
	for _, shard := range shardFiles {
		raw, err := os.ReadFile(shard.Path)
		if err != nil {
			t.Fatal(err)
		}
		shards[shard.Index] = raw
	}
	shards[1] = nil
	shards[4] = nil
	restored, err := DecodeShards(shards, 4, 2, len(data))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, data) {
		t.Fatal("restored streaming data mismatch")
	}
}
