package dht

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestProviderCacheSetAndGet(t *testing.T) {
	cache := NewProviderCache(1 * time.Minute)

	records := []wire.DHTProviderRecord{
		{MinerAddress: "miner1", ShardHash: "hash1"},
		{MinerAddress: "miner2", ShardHash: "hash1"},
	}
	cache.Set("hash1", records)

	got, ok := cache.Get("hash1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d", len(got))
	}
}

func TestProviderCacheMiss(t *testing.T) {
	cache := NewProviderCache(1 * time.Minute)
	_, ok := cache.Get("nonexistent")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestProviderCacheExpiration(t *testing.T) {
	cache := NewProviderCache(1 * time.Millisecond)
	cache.Set("hash1", []wire.DHTProviderRecord{{MinerAddress: "m1"}})

	// Wait for expiration.
	time.Sleep(5 * time.Millisecond)

	_, ok := cache.Get("hash1")
	if ok {
		t.Fatal("expected cache miss after TTL")
	}
}

func TestProviderCacheCleanup(t *testing.T) {
	cache := NewProviderCache(1 * time.Millisecond)
	cache.Set("h1", []wire.DHTProviderRecord{{MinerAddress: "m1"}})
	cache.Set("h2", []wire.DHTProviderRecord{{MinerAddress: "m2"}})

	time.Sleep(5 * time.Millisecond)
	cache.Cleanup()

	// After cleanup, the internal map should be empty.
	cache.mu.RLock()
	count := len(cache.entries)
	cache.mu.RUnlock()
	if count != 0 {
		t.Fatalf("expected 0 entries after cleanup, got %d", count)
	}
}

func TestProviderCacheNilSafe(t *testing.T) {
	var cache *ProviderCache
	_, ok := cache.Get("anything")
	if ok {
		t.Fatal("nil cache should miss")
	}
	cache.Set("anything", nil) // should not panic
	cache.Cleanup()            // should not panic
}
