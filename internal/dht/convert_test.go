package dht

import (
	"testing"

	"chain/internal/wire"
)

func TestToStorageProviderRecords(t *testing.T) {
	records := []wire.DHTProviderRecord{
		{
			MinerAddress:   "0xABC",
			PublicKey:      "pk1",
			Endpoint:       "http://miner1:9090",
			PeerID:         "12D3KooWA",
			PeerAddrs:      []string{"/ip4/1.2.3.4/tcp/4001"},
			ShardHash:      "hash1",
			HealthScoreBPS: 9500,
			ExpiresAtUnix:  1000,
			Signature:      "sig1",
		},
		{
			MinerAddress:   "0xDEF",
			Endpoint:       "http://miner2:9090",
			PeerID:         "12D3KooWB",
			ShardHash:      "hash2",
			HealthScoreBPS: 8000,
			ExpiresAtUnix:  2000,
		},
	}

	result := ToStorageProviderRecords(records)
	if len(result) != 2 {
		t.Fatalf("expected 2 records, got %d", len(result))
	}
	r := result[0]
	if r.MinerAddress != "0xABC" {
		t.Errorf("MinerAddress = %q, want %q", r.MinerAddress, "0xABC")
	}
	if r.Endpoint != "http://miner1:9090" {
		t.Errorf("Endpoint = %q, want %q", r.Endpoint, "http://miner1:9090")
	}
	if r.PeerID != "12D3KooWA" {
		t.Errorf("PeerID = %q, want %q", r.PeerID, "12D3KooWA")
	}
	if r.HealthScoreBPS != 9500 {
		t.Errorf("HealthScoreBPS = %d, want %d", r.HealthScoreBPS, 9500)
	}
	if r.ProviderSource != "dht" {
		t.Errorf("ProviderSource = %q, want %q", r.ProviderSource, "dht")
	}
	if len(r.PeerAddrs) != 1 || r.PeerAddrs[0] != "/ip4/1.2.3.4/tcp/4001" {
		t.Errorf("PeerAddrs = %v, want [/ip4/1.2.3.4/tcp/4001]", r.PeerAddrs)
	}

	// Empty input.
	empty := ToStorageProviderRecords(nil)
	if len(empty) != 0 {
		t.Errorf("expected empty result for nil input, got %d", len(empty))
	}
}
