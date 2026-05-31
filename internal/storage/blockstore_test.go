package storage

import (
	"context"
	"os"
	"testing"
)

func TestLevelDBBlkStorePutGet(t *testing.T) {
	dir, err := os.MkdirTemp("", "blockstore-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewLevelDBBlkStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	block := Block{CID: "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi", Data: []byte("hello falari")}
	if err := store.Put(ctx, block); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, block.CID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != "hello falari" {
		t.Fatalf("unexpected data: %s", got.Data)
	}
	if got.CID != block.CID {
		t.Fatalf("unexpected cid: %s", got.CID)
	}
}

func TestLevelDBBlkStoreHasDelete(t *testing.T) {
	dir, err := os.MkdirTemp("", "blockstore-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, _ := NewLevelDBBlkStore(dir)
	defer store.Close()

	ctx := context.Background()
	cid := "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"

	has, err := store.Has(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("should not have block before put")
	}

	if err := store.Put(ctx, Block{CID: cid, Data: []byte("data")}); err != nil {
		t.Fatal(err)
	}

	has, err = store.Has(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("should have block after put")
	}

	size, err := store.GetSize(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if size != 4 {
		t.Fatalf("unexpected size: %d", size)
	}

	if err := store.DeleteBlock(ctx, cid); err != nil {
		t.Fatal(err)
	}

	has, err = store.Has(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("should not have block after delete")
	}
}

func TestLevelDBBlkStorePutMany(t *testing.T) {
	dir, err := os.MkdirTemp("", "blockstore-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, _ := NewLevelDBBlkStore(dir)
	defer store.Close()

	ctx := context.Background()
	blocks := []Block{
		{CID: "cid-1", Data: []byte("a")},
		{CID: "cid-2", Data: []byte("bb")},
		{CID: "cid-3", Data: []byte("ccc")},
	}
	if err := store.PutMany(ctx, blocks); err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		has, _ := store.Has(ctx, b.CID)
		if !has {
			t.Fatalf("missing block %s", b.CID)
		}
	}
}

func TestLevelDBBlkStoreAllKeys(t *testing.T) {
	dir, err := os.MkdirTemp("", "blockstore-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, _ := NewLevelDBBlkStore(dir)
	defer store.Close()

	ctx := context.Background()
	_ = store.Put(ctx, Block{CID: "cid-a", Data: []byte("1")})
	_ = store.Put(ctx, Block{CID: "cid-b", Data: []byte("2")})

	ch, err := store.AllKeysChan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for cid := range ch {
		seen[cid] = true
	}
	if !seen["cid-a"] || !seen["cid-b"] {
		t.Fatalf("missing keys: %v", seen)
	}
}

func TestMemoryBlkStoreSmoke(t *testing.T) {
	store, err := NewMemoryBlkStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Put(ctx, Block{CID: "mem-cid", Data: []byte("mem")}); err != nil {
		t.Fatal(err)
	}
	block, err := store.Get(ctx, "mem-cid")
	if err != nil {
		t.Fatal(err)
	}
	if string(block.Data) != "mem" {
		t.Fatalf("unexpected memory block: %s", block.Data)
	}
}

func TestBackendBlockstoreRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("", "backend-bs-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	backend, err := NewFileBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	bs := NewBackendBlockstore(backend)

	ctx := context.Background()
	data := []byte("bridged content")
	if err := bs.Put(ctx, Block{CID: "bridge-cid", Data: data}); err != nil {
		t.Fatal(err)
	}

	block, err := bs.Get(ctx, "bridge-cid")
	if err != nil {
		t.Fatal(err)
	}
	if string(block.Data) != "bridged content" {
		t.Fatalf("unexpected: %s", block.Data)
	}

	has, _ := bs.Has(ctx, "bridge-cid")
	if !has {
		t.Fatal("should have block")
	}

	ch, _ := bs.AllKeysChan(ctx)
	found := false
	for cid := range ch {
		if cid == "bridge-cid" {
			found = true
		}
	}
	if !found {
		t.Fatal("bridge-cid not in AllKeysChan")
	}

	if err := bs.DeleteBlock(ctx, "bridge-cid"); err != nil {
		t.Fatal(err)
	}
	has, _ = bs.Has(ctx, "bridge-cid")
	if has {
		t.Fatal("should be deleted")
	}
}
