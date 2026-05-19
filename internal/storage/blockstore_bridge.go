package storage

import (
	"context"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

type BackendBlockstore struct {
	backend StorageBackend
}

func NewBackendBlockstore(backend StorageBackend) *BackendBlockstore {
	return &BackendBlockstore{backend: backend}
}

func (b *BackendBlockstore) Put(ctx context.Context, block Block) error {
	hash := chaincrypto.HashBytes(block.Data)
	cid := block.CID
	if cid == "" {
		var err error
		cid, err = wire.RawCIDForHash(hash)
		if err != nil {
			return err
		}
	}
	return b.backend.PutBlock(StoredBlock{CID: cid, Hash: hash, Size: int64(len(block.Data))}, block.Data)
}

func (b *BackendBlockstore) Get(ctx context.Context, cid string) (Block, error) {
	data, err := b.backend.GetByCID(cid)
	if err != nil {
		return Block{}, err
	}
	return Block{CID: cid, Data: data}, nil
}

func (b *BackendBlockstore) Has(ctx context.Context, cid string) (bool, error) {
	_, err := b.backend.GetByCID(cid)
	return err == nil, nil
}

func (b *BackendBlockstore) GetSize(ctx context.Context, cid string) (int, error) {
	data, err := b.backend.GetByCID(cid)
	if err != nil {
		return -1, err
	}
	return len(data), nil
}

func (b *BackendBlockstore) DeleteBlock(ctx context.Context, cid string) error {
	return b.backend.DeleteByCID(cid)
}

func (b *BackendBlockstore) AllKeysChan(ctx context.Context) (<-chan string, error) {
	blocks, err := b.backend.ListBlocks()
	if err != nil {
		return nil, err
	}
	ch := make(chan string, len(blocks))
	go func() {
		defer close(ch)
		for _, block := range blocks {
			select {
			case ch <- block.CID:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (b *BackendBlockstore) PutMany(ctx context.Context, blocks []Block) error {
	for _, block := range blocks {
		if err := b.Put(ctx, block); err != nil {
			return err
		}
	}
	return nil
}

func (b *BackendBlockstore) HashOnRead(enabled bool) {
}
