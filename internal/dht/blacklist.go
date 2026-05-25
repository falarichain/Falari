package dht

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"chain/internal/client"
	"chain/internal/wire"
)

// BlacklistCache is a thread-safe in-memory cache of governance blacklisted shards.
type BlacklistCache struct {
	mu      sync.RWMutex
	entries map[string]wire.BlacklistEntry
	height  uint64
}

// NewBlacklistCache creates a new empty blacklist cache.
func NewBlacklistCache() *BlacklistCache {
	return &BlacklistCache{
		entries: make(map[string]wire.BlacklistEntry),
	}
}

// IsBlocked returns true if the shard hash is currently blacklisted.
func (b *BlacklistCache) IsBlocked(shardHash string) bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry, ok := b.entries[shardHash]
	if !ok {
		return false
	}
	// Check if the entry has expired.
	if entry.ExpiresAtUnix > 0 && entry.ExpiresAtUnix < time.Now().Unix() {
		return false
	}
	return true
}

// Add adds or updates a blacklist entry.
func (b *BlacklistCache) Add(entry wire.BlacklistEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[entry.ShardHash] = entry
}

// Remove removes a blacklist entry (e.g., on successful appeal).
func (b *BlacklistCache) Remove(shardHash string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, shardHash)
}

// Set replaces the entire blacklist with the given entries.
func (b *BlacklistCache) Set(entries []wire.BlacklistEntry, height uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = make(map[string]wire.BlacklistEntry, len(entries))
	for _, e := range entries {
		b.entries[e.ShardHash] = e
	}
	b.height = height
}

// Count returns the number of active blacklist entries.
func (b *BlacklistCache) Count() int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.entries)
}

// SyncFromChain fetches blacklist updates from the chain node.
func (b *BlacklistCache) SyncFromChain(chainURL string) {
	if chainURL == "" {
		return
	}
	b.mu.RLock()
	currentHeight := b.height
	b.mu.RUnlock()

	httpClient := client.NewHTTP(chainURL)
	var resp wire.BlacklistResponse
	path := "/governance/blacklist"
	if currentHeight > 0 {
		path += "?since_height=" + u64toa(currentHeight)
	}
	if err := httpClient.Get(path, &resp); err != nil {
		// Silently ignore - chain may not have this endpoint yet.
		return
	}
	if len(resp.Entries) == 0 && currentHeight == 0 {
		return
	}
	// Apply entries: those with non-zero ExpiresAtUnix in the past are removals,
	// others are additions.
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, entry := range resp.Entries {
		if entry.ExpiresAtUnix > 0 && entry.ExpiresAtUnix < time.Now().Unix() {
			delete(b.entries, entry.ShardHash)
		} else {
			b.entries[entry.ShardHash] = entry
		}
	}
	if resp.CurrentHeight > b.height {
		b.height = resp.CurrentHeight
	}
	// Clean up expired entries.
	now := time.Now().Unix()
	for hash, entry := range b.entries {
		if entry.ExpiresAtUnix > 0 && entry.ExpiresAtUnix < now {
			delete(b.entries, hash)
		}
	}
}

// SyncLoop periodically syncs the blacklist from the chain.
func (b *BlacklistCache) SyncLoop(ctx context.Context, chainURL string, interval time.Duration) {
	if chainURL == "" {
		return
	}
	// Initial sync.
	b.SyncFromChain(chainURL)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.SyncFromChain(chainURL)
		}
	}
}

// Entries returns a copy of all current blacklist entries.
func (b *BlacklistCache) Entries() []wire.BlacklistEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]wire.BlacklistEntry, 0, len(b.entries))
	for _, e := range b.entries {
		out = append(out, e)
	}
	return out
}

// ServeBlacklistHandler returns an HTTP handler for the chain's blacklist API.
// This is used by the chain node to expose GET /governance/blacklist.
func ServeBlacklistHandler(getBlacklist func() wire.BlacklistResponse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := getBlacklist()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func u64toa(v uint64) string {
	if v == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	return string(buf)
}
