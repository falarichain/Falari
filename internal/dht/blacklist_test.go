package dht

import (
	"testing"
	"time"

	"chain/internal/wire"
)

func TestBlacklistCacheAddAndIsBlocked(t *testing.T) {
	bl := NewBlacklistCache()

	entry := wire.BlacklistEntry{
		ShardHash: "hash_blocked",
		Reason:    "test",
	}
	bl.Add(entry)

	if !bl.IsBlocked("hash_blocked") {
		t.Fatal("expected hash to be blocked")
	}
	if bl.IsBlocked("hash_not_blocked") {
		t.Fatal("expected hash to not be blocked")
	}
}

func TestBlacklistCacheRemove(t *testing.T) {
	bl := NewBlacklistCache()
	bl.Add(wire.BlacklistEntry{ShardHash: "hash1", Reason: "block"})
	bl.Add(wire.BlacklistEntry{ShardHash: "hash2", Reason: "freeze"})

	bl.Remove("hash1")
	if bl.IsBlocked("hash1") {
		t.Fatal("expected hash1 to be unblocked after remove")
	}
	if !bl.IsBlocked("hash2") {
		t.Fatal("expected hash2 to still be blocked")
	}
}

func TestBlacklistCacheExpiration(t *testing.T) {
	bl := NewBlacklistCache()

	// Add expired entry.
	bl.Add(wire.BlacklistEntry{
		ShardHash:     "hash_expired",
		Reason:        "freeze",
		ExpiresAtUnix: time.Now().Add(-1 * time.Hour).Unix(),
	})
	if bl.IsBlocked("hash_expired") {
		t.Fatal("expired entry should not be blocked")
	}

	// Add permanent entry.
	bl.Add(wire.BlacklistEntry{
		ShardHash:     "hash_permanent",
		Reason:        "block",
		ExpiresAtUnix: 0,
	})
	if !bl.IsBlocked("hash_permanent") {
		t.Fatal("permanent entry should be blocked")
	}
}

func TestBlacklistCacheCount(t *testing.T) {
	bl := NewBlacklistCache()
	if bl.Count() != 0 {
		t.Fatalf("expected count 0, got %d", bl.Count())
	}

	bl.Add(wire.BlacklistEntry{ShardHash: "h1", Reason: "block"})
	bl.Add(wire.BlacklistEntry{ShardHash: "h2", Reason: "block"})
	if bl.Count() != 2 {
		t.Fatalf("expected count 2, got %d", bl.Count())
	}
}

func TestBlacklistCacheSet(t *testing.T) {
	bl := NewBlacklistCache()
	bl.Add(wire.BlacklistEntry{ShardHash: "old", Reason: "block"})

	entries := []wire.BlacklistEntry{
		{ShardHash: "new1", Reason: "block"},
		{ShardHash: "new2", Reason: "freeze"},
	}
	bl.Set(entries, 100)

	if bl.IsBlocked("old") {
		t.Fatal("old entry should be gone after Set")
	}
	if !bl.IsBlocked("new1") || !bl.IsBlocked("new2") {
		t.Fatal("new entries should be blocked after Set")
	}
	if bl.Count() != 2 {
		t.Fatalf("expected count 2, got %d", bl.Count())
	}
}

func TestBlacklistCacheNilSafe(t *testing.T) {
	var bl *BlacklistCache
	if bl.IsBlocked("anything") {
		t.Fatal("nil blacklist should not block")
	}
	if bl.Count() != 0 {
		t.Fatal("nil blacklist count should be 0")
	}
}

func TestBlacklistCacheEntries(t *testing.T) {
	bl := NewBlacklistCache()
	bl.Add(wire.BlacklistEntry{ShardHash: "a"})
	bl.Add(wire.BlacklistEntry{ShardHash: "b"})

	entries := bl.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}
