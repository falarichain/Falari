package dht

import (
	"sync"
	"time"

	"chain/internal/wire"
)

// ProviderCache is a thread-safe TTL cache for DHT provider lookup results.
type ProviderCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	records   []wire.DHTProviderRecord
	expiresAt time.Time
}

// NewProviderCache creates a new provider cache with the given TTL.
func NewProviderCache(ttl time.Duration) *ProviderCache {
	return &ProviderCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
}

// Get returns cached provider records if they exist and haven't expired.
func (c *ProviderCache) Get(shardHash string) ([]wire.DHTProviderRecord, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[shardHash]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.records, true
}

// Set caches provider records for a shard hash.
func (c *ProviderCache) Set(shardHash string, records []wire.DHTProviderRecord) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[shardHash] = cacheEntry{
		records:   records,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Cleanup removes expired entries from the cache.
func (c *ProviderCache) Cleanup() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}
