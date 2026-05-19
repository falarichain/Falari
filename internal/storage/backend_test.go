package storage

import (
	"os"
	"path/filepath"
	"testing"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

func TestFileBackendStoresAndLoadsByHashAndCID(t *testing.T) {
	backend, err := NewFileBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("blockstore-data")
	hash := chaincrypto.HashBytes(data)
	cid, err := wire.RawCIDForBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.PutBlock(StoredBlock{Hash: hash, CID: cid, Size: int64(len(data))}, data); err != nil {
		t.Fatal(err)
	}

	byHash, getHashErr := backend.GetByHash(hash)
	if getHashErr != nil {
		t.Fatal(getHashErr)
	}
	if string(byHash) != string(data) {
		t.Fatalf("unexpected hash lookup payload: %q", byHash)
	}

	byCID, getCIDErr := backend.GetByCID(cid)
	if getCIDErr != nil {
		t.Fatal(getCIDErr)
	}
	if string(byCID) != string(data) {
		t.Fatalf("unexpected cid lookup payload: %q", byCID)
	}

	blocks, listErr := backend.ListBlocks()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(blocks) != 1 || blocks[0].Hash != hash || blocks[0].CID != cid {
		t.Fatalf("unexpected listed blocks: %+v", blocks)
	}

	if err := backend.DeleteByCID(cid); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.GetByHash(hash); err == nil {
		t.Fatal("expected deleted block to be unavailable by hash")
	}
}

func TestFileBackendReadsLegacyShardLayout(t *testing.T) {
	dataDir := t.TempDir()
	backend, err := NewFileBackend(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("legacy-shard-data")
	hash := chaincrypto.HashBytes(data)
	cid, err := wire.RawCIDForHash(hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "shards", hash+".bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	byHash, getHashErr := backend.GetByHash(hash)
	if getHashErr != nil {
		t.Fatal(getHashErr)
	}
	if string(byHash) != string(data) {
		t.Fatalf("unexpected legacy hash lookup payload: %q", byHash)
	}

	byCID, getCIDErr := backend.GetByCID(cid)
	if getCIDErr != nil {
		t.Fatal(getCIDErr)
	}
	if string(byCID) != string(data) {
		t.Fatalf("unexpected legacy cid lookup payload: %q", byCID)
	}

	blocks, listErr := backend.ListBlocks()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(blocks) != 1 || blocks[0].Hash != hash || blocks[0].CID != cid {
		t.Fatalf("unexpected legacy listed blocks: %+v", blocks)
	}
}
