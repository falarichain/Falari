package wire

import (
	"testing"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestSignAndVerifyDHTProvider(t *testing.T) {
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := AccountAddress(&key.PublicKey)

	record := DHTProviderRecord{
		MinerAddress:   addr,
		PublicKey:      EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey)),
		Endpoint:       "http://localhost:9090",
		PeerID:         "QmTest123",
		PeerAddrs:      []string{"/ip4/127.0.0.1/tcp/4001"},
		ShardHash:      "abc123def456",
		HealthScoreBPS: 9500,
		ExpiresAtUnix:  time.Now().Add(5 * time.Minute).Unix(),
	}

	if err := SignDHTProvider(&record, key); err != nil {
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
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	record := DHTProviderRecord{
		MinerAddress:   AccountAddress(&key.PublicKey),
		PublicKey:      EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey)),
		Endpoint:       "http://legitimate:9090",
		PeerID:         "QmTest123",
		HealthScoreBPS: 9500,
		ShardHash:      "original_hash",
		ExpiresAtUnix:  time.Now().Add(5 * time.Minute).Unix(),
	}
	if err := SignDHTProvider(&record, key); err != nil {
		t.Fatal(err)
	}

	// Tamper with the shard hash.
	tampered := record
	tampered.ShardHash = "tampered_hash"
	if err := VerifyDHTProvider(tampered); err == nil {
		t.Fatal("expected verification to fail on tampered shard hash")
	}

	// Tamper with the endpoint.
	tampered = record
	tampered.Endpoint = "http://malicious:9090"
	if err := VerifyDHTProvider(tampered); err == nil {
		t.Fatal("expected verification to fail on tampered endpoint")
	}

	// Tamper with the health score.
	tampered = record
	tampered.HealthScoreBPS = 10000
	if err := VerifyDHTProvider(tampered); err == nil {
		t.Fatal("expected verification to fail on tampered health score")
	}

	// Tamper with the peer ID.
	tampered = record
	tampered.PeerID = "QmEvil456"
	if err := VerifyDHTProvider(tampered); err == nil {
		t.Fatal("expected verification to fail on tampered peer ID")
	}
}

func TestVerifyDHTProviderMissingKey(t *testing.T) {
	record := DHTProviderRecord{
		MinerAddress: "0x0000000000000000000000000000000000000000",
		ShardHash:    "hash123",
		Signature:    EncodeHex([]byte("fake")),
	}
	if err := VerifyDHTProvider(record); err == nil {
		t.Fatal("expected error for missing public key")
	}
}

func TestVerifyDHTProviderWrongKey(t *testing.T) {
	key1, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	key2, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	record := DHTProviderRecord{
		MinerAddress:  AccountAddress(&key2.PublicKey),
		PublicKey:     EncodeHex(ethcrypto.CompressPubkey(&key2.PublicKey)),
		ShardHash:     "hash456",
		ExpiresAtUnix: time.Now().Add(5 * time.Minute).Unix(),
	}
	if err := SignDHTProvider(&record, key1); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDHTProvider(record); err == nil {
		t.Fatal("expected verification to fail with wrong key")
	}
}
