package client

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSegmentEncryptionRoundTripAndWrongKeyFails(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	wrongKey := bytes.Repeat([]byte{8}, 32)
	nonce := bytes.Repeat([]byte{9}, 32)
	plain := []byte("private storage memory")
	meta, err := EncryptionMetadata(key, nonce, int64(len(plain)), int64(len(plain)))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := EncryptSegment(plain, key, *meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecryptSegment(ciphertext, key, *meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, plain) {
		t.Fatal("decrypted data mismatch")
	}
	if _, err := DecryptSegment(ciphertext, wrongKey, *meta, 0); err == nil {
		t.Fatal("expected wrong key to fail")
	}
}

func TestComputeEncryptedErasurePlanMatchesEncryptedUploadSegment(t *testing.T) {
	data := bytes.Repeat([]byte("encrypted-storage-chain"), 256)
	path := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{1}, 32)
	nonce := bytes.Repeat([]byte{2}, 32)
	size, segmentSize, meta, segments, roots, fileRoot, err := ComputeEncryptedErasurePlan(path, 512, 2, 1, key, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if size <= int64(len(data)) || segmentSize != 528 || meta.PlaintextSize != int64(len(data)) {
		t.Fatalf("unexpected encrypted plan sizes size=%d segment=%d meta=%+v", size, segmentSize, meta)
	}
	if len(segments) == 0 || len(segments) != len(roots) || fileRoot == "" {
		t.Fatalf("unexpected encrypted plan roots: segments=%d roots=%d fileRoot=%s", len(segments), len(roots), fileRoot)
	}
}
