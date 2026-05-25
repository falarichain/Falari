package wire

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func TestSignAndVerifyDHTProvider(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	record := DHTProviderRecord{
		MinerAddress:   "miner_test123",
		PublicKey:      base64.StdEncoding.EncodeToString(pub),
		Endpoint:       "http://localhost:9090",
		PeerID:         "QmTest123",
		PeerAddrs:      []string{"/ip4/127.0.0.1/tcp/4001"},
		ShardHash:      "abc123def456",
		HealthScoreBPS: 9500,
		ExpiresAtUnix:  time.Now().Add(5 * time.Minute).Unix(),
	}

	if err := SignDHTProvider(&record, priv); err != nil {
		t.Fatalf("SignDHTProvider failed: %v", err)
	}
	if record.Signature == "" {
		t.Fatal("signature is empty after signing")
	}
	if err := VerifyDHTProvider(record); err != nil {
		t.Fatalf("VerifyDHTProvider failed: %v", err)
	}
}

func TestVerifyDHTProviderTampered(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	record := DHTProviderRecord{
		MinerAddress:  "miner_tamper",
		PublicKey:     base64.StdEncoding.EncodeToString(pub),
		ShardHash:     "original_hash",
		ExpiresAtUnix: time.Now().Add(5 * time.Minute).Unix(),
	}
	if err := SignDHTProvider(&record, priv); err != nil {
		t.Fatal(err)
	}

	// Tamper with the shard hash.
	record.ShardHash = "tampered_hash"
	if err := VerifyDHTProvider(record); err == nil {
		t.Fatal("expected verification to fail on tampered record")
	}
}

func TestVerifyDHTProviderMissingKey(t *testing.T) {
	record := DHTProviderRecord{
		MinerAddress: "miner_nokey",
		ShardHash:    "hash123",
		Signature:    base64.StdEncoding.EncodeToString([]byte("fake")),
	}
	if err := VerifyDHTProvider(record); err == nil {
		t.Fatal("expected error for missing public key")
	}
}

func TestVerifyDHTProviderWrongKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	record := DHTProviderRecord{
		MinerAddress:  "miner_wrongkey",
		PublicKey:     base64.StdEncoding.EncodeToString(pub2),
		ShardHash:     "hash456",
		ExpiresAtUnix: time.Now().Add(5 * time.Minute).Unix(),
	}
	if err := SignDHTProvider(&record, priv); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDHTProvider(record); err == nil {
		t.Fatal("expected verification to fail with wrong key")
	}
}
