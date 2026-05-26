package chain

import (
	"testing"
	"time"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestAcceptStorageProviderAnnouncementAndQueryByShard(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	minerAddress := wire.AccountAddress(&key.PublicKey)
	minerPublicKey := wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey))
	store.data.Miners[minerAddress] = wire.MinerStats{
		MinerAddress:  minerAddress,
		PublicKey:     minerPublicKey,
		Endpoint:      "http://registered-miner",
		CapacityBytes: 1 << 40,
		Stake:         100,
		Status:        "active",
	}

	record := wire.StorageProviderRecord{
		MinerAddress:  minerAddress,
		PublicKey:     minerPublicKey,
		Endpoint:      "http://provider-miner",
		PeerID:        "12D3KooWprovider",
		PeerAddrs:     []string{"/ip4/127.0.0.1/tcp/7000/p2p/12D3KooWprovider"},
		CapacityBytes: 1 << 40,
		StoredBytes:   2048,
		ShardCount:    2,
		ShardHashes:   []string{"shard_b", "shard_a"},
		Shards: []wire.ProviderShard{
			{ShardHash: "shard_a", ShardCID: "bafkreicidsharda", Size: 1024},
			{ShardHash: "shard_b", ShardCID: "bafkreicidshardb", Size: 1024},
		},
		LastSeenUnix:  time.Now().Unix(),
		ExpiresAtUnix: time.Now().Add(time.Minute).Unix(),
	}
	if signErr := wire.SignStorageProvider(&record, key); signErr != nil {
		t.Fatal(signErr)
	}
	if acceptErr := store.AcceptStorageProviderAnnouncement(wire.StorageProviderAnnouncement{Provider: record}); acceptErr != nil {
		t.Fatal(acceptErr)
	}

	resp, err := store.StorageProviders("shard_a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Providers) != 1 {
		t.Fatalf("expected one provider, got %+v", resp.Providers)
	}
	provider := resp.Providers[0]
	if provider.MinerAddress != minerAddress || provider.Endpoint != "http://provider-miner" || provider.PeerID == "" {
		t.Fatalf("unexpected provider %+v", provider)
	}

	resp, err = store.StorageProviders("missing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Providers) != 0 {
		t.Fatalf("expected no provider for missing shard, got %+v", resp.Providers)
	}

	resp, err = store.StorageProviders("", "bafkreicidsharda", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Providers) != 1 || resp.ShardCID != "bafkreicidsharda" {
		t.Fatalf("expected cid query to return provider, got %+v", resp)
	}
}

func TestStorageProvidersIncludesCommittedReceiptEndpoint(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Miners["miner_receipt"] = wire.MinerStats{
		MinerAddress:  "miner_receipt",
		PublicKey:     "miner_pub",
		Endpoint:      "http://registered-receipt-miner",
		CapacityBytes: 100,
		Stake:         100,
		Status:        "active",
	}
	store.data.Intents["intent_provider"] = &Intent{
		IntentView: wire.IntentView{
			IntentID: "intent_provider",
			Status:   wire.StatusFinalized,
		},
		Receipts: map[int]map[int]wire.MinerReceipt{
			0: {
				0: {
					MinerAddress:   "miner_receipt",
					MinerPublicKey: "miner_pub",
					MinerEndpoint:  "http://receipt-miner",
					ShardHash:      "receipt_shard",
					ShardSize:      32,
				},
			},
		},
	}

	resp, err := store.StorageProviders("receipt_shard", "", "intent_provider")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Providers) != 1 {
		t.Fatalf("expected one receipt provider, got %+v", resp.Providers)
	}
	if resp.Providers[0].Endpoint != "http://receipt-miner" {
		t.Fatalf("expected receipt endpoint, got %+v", resp.Providers[0])
	}
}

func TestStorageProvidersOrdersByHealthScore(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Miners["miner_good"] = wire.MinerStats{
		MinerAddress:  "miner_good",
		PublicKey:     "pub_good",
		Endpoint:      "http://good",
		CapacityBytes: 100,
		Stake:         100,
		Status:        "active",
		ProofSuccess:  9,
		ProofFailure:  1,
	}
	store.data.Miners["miner_weak"] = wire.MinerStats{
		MinerAddress:  "miner_weak",
		PublicKey:     "pub_weak",
		Endpoint:      "http://weak",
		CapacityBytes: 100,
		Stake:         100,
		Status:        "active",
		ProofSuccess:  1,
		ProofFailure:  9,
	}
	store.data.Intents["intent_health"] = &Intent{
		IntentView: wire.IntentView{IntentID: "intent_health", Status: wire.StatusFinalized},
		Receipts: map[int]map[int]wire.MinerReceipt{
			0: {
				0: {MinerAddress: "miner_weak", MinerPublicKey: "pub_weak", ShardHash: "shared_shard", ShardSize: 32},
				1: {MinerAddress: "miner_good", MinerPublicKey: "pub_good", ShardHash: "shared_shard", ShardSize: 32},
			},
		},
	}

	resp, err := store.StorageProviders("shared_shard", "", "intent_health")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Providers) != 2 {
		t.Fatalf("expected two providers, got %+v", resp.Providers)
	}
	if resp.Providers[0].MinerAddress != "miner_good" {
		t.Fatalf("expected healthier provider first, got %+v", resp.Providers)
	}
	if resp.Providers[0].HealthScoreBPS <= resp.Providers[1].HealthScoreBPS {
		t.Fatalf("expected descending health score, got %+v", resp.Providers)
	}
}

func TestStorageRoutesReturnsTransportOrderedCIDRoutes(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.Miners["miner_route"] = wire.MinerStats{
		MinerAddress:  "miner_route",
		PublicKey:     "pub_route",
		Endpoint:      "http://route",
		CapacityBytes: 100,
		Stake:         100,
		Status:        "active",
		ProofSuccess:  10,
	}
	store.data.ProviderRecords["miner_route"] = wire.StorageProviderRecord{
		MinerAddress:       "miner_route",
		PublicKey:          "pub_route",
		Endpoint:           "http://route",
		PeerID:             "peer_route",
		PeerAddrs:          []string{"/ip4/127.0.0.1/tcp/7000/p2p/peer_route"},
		ShardHashes:        []string{"route_hash"},
		Shards:             []wire.ProviderShard{{ShardHash: "route_hash", ShardCID: "bafkroute"}},
		LastSeenUnix:       time.Now().Unix(),
		ExpiresAtUnix:      time.Now().Add(time.Minute).Unix(),
		ProviderRecordLive: true,
	}

	resp, err := store.StorageRoutes("", "bafkroute", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Routes) != 3 {
		t.Fatalf("expected libp2p, http-block, and http-shard routes, got %+v", resp.Routes)
	}
	if resp.Routes[0].Transport != "libp2p" || resp.Routes[1].Transport != "http-block" || resp.Routes[2].Transport != "http-shard" {
		t.Fatalf("unexpected route order: %+v", resp.Routes)
	}
	if resp.Routes[0].ShardCID != "bafkroute" || resp.Routes[2].ShardHash != "route_hash" {
		t.Fatalf("routes should carry cid/hash hints: %+v", resp.Routes)
	}
}
