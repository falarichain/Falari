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
