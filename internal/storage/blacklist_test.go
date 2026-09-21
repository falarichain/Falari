package storage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	chaincrypto "chain/internal/crypto"
	dhtx "chain/internal/dht"
	"chain/internal/wire"
)

func blacklistUploadRequest(data []byte) wire.UploadRequest {
	return wire.UploadRequest{
		IntentID:    "intent_blacklist_test",
		User:        "alice",
		FileRoot:    "file_root",
		SegmentID:   0,
		SegmentRoot: "segment_root",
		ShardIndex:  0,
		ShardID:     "shard_blacklist_test",
		ShardHash:   chaincrypto.HashBytes(data),
		ShardSize:   int64(len(data)),
		DataBase64:  base64.StdEncoding.EncodeToString(data),
	}
}

func blacklistedCache(hash string) *dhtx.BlacklistCache {
	cache := dhtx.NewBlacklistCache()
	cache.Add(wire.BlacklistEntry{ShardHash: hash, Reason: "legal_hold"})
	return cache
}

// TestNodeRejectsBlacklistedShardOnUpload covers the admission half of the governance
// blacklist. The cache was only ever consulted where shards leave the node, so a miner
// accepted content it was then refusing to hand back out, and the blocked shard stayed on
// its disk until the deal lapsed.
func TestNodeRejectsBlacklistedShardOnUpload(t *testing.T) {
	node, err := OpenNode(t.TempDir(), testNodeKey(t))
	if err != nil {
		t.Fatal(err)
	}
	blocked := []byte("blocked-shard-content")
	clean := []byte("clean-shard-content")
	node.SetBlacklistCache(blacklistedCache(chaincrypto.HashBytes(blocked)))

	if _, err := node.Store(blacklistUploadRequest(blocked)); !errors.Is(err, errShardBlacklisted) {
		t.Fatalf("blacklisted upload: got %v, want the shard to be refused", err)
	}
	if _, err := node.ReadShard(chaincrypto.HashBytes(blocked)); err == nil {
		t.Fatal("blacklisted shard reached the backend")
	}
	if _, err := node.Store(blacklistUploadRequest(clean)); err != nil {
		t.Fatalf("clean upload refused: %v", err)
	}
}

// TestNodeWithoutBlacklistCacheStillAdmits pins today's availability stance: a node whose
// cache is missing or unsynced serves and stores normally rather than shutting the miner
// down, because the cache is filled by polling a chain node. Flipping this to fail-closed is
// an availability decision, not a bug fix.
func TestNodeWithoutBlacklistCacheStillAdmits(t *testing.T) {
	node, err := OpenNode(t.TempDir(), testNodeKey(t))
	if err != nil {
		t.Fatal(err)
	}
	blocked := []byte("blocked-shard-content")
	if _, err := node.Store(blacklistUploadRequest(blocked)); err != nil {
		t.Fatalf("upload without a wired blacklist cache should stay admitted: %v", err)
	}
}

// TestBlacklistAppliesToBothUploadAndServeRoutes checks that the two directions read the same
// cache, and that the upload endpoint answers the block the way the download side already
// does.
func TestBlacklistAppliesToBothUploadAndServeRoutes(t *testing.T) {
	node, err := OpenNode(t.TempDir(), testNodeKey(t))
	if err != nil {
		t.Fatal(err)
	}
	blocked := []byte("blocked-shard-content")
	blockedHash := chaincrypto.HashBytes(blocked)
	node.SetBlacklistCache(blacklistedCache(blockedHash))
	handler := NewServer(node).Routes()

	body, err := json.Marshal(blacklistUploadRequest(blocked))
	if err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRecorder()
	handler.ServeHTTP(upload, httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(body)))
	if upload.Code != http.StatusForbidden {
		t.Fatalf("upload of a blacklisted shard: status %d, body %s", upload.Code, upload.Body.String())
	}

	serve := httptest.NewRecorder()
	handler.ServeHTTP(serve, httptest.NewRequest(http.MethodGet, "/shards/"+blockedHash, nil))
	if serve.Code != http.StatusForbidden {
		t.Fatalf("serving a blacklisted shard: status %d, body %s", serve.Code, serve.Body.String())
	}
}
