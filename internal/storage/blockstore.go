package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/util"
)

type Block struct {
	CID  string
	Data []byte
}

type BlockstoreReader interface {
	Get(ctx context.Context, cid string) (Block, error)
	Has(ctx context.Context, cid string) (bool, error)
	GetSize(ctx context.Context, cid string) (int, error)
	AllKeysChan(ctx context.Context) (<-chan string, error)
	HashOnRead(enabled bool)
}

type Blockstore interface {
	BlockstoreReader
	Put(ctx context.Context, block Block) error
	PutMany(ctx context.Context, blocks []Block) error
	DeleteBlock(ctx context.Context, cid string) error
}

type CIDBlkStore struct {
	mu         sync.RWMutex
	db         *leveldb.DB
	verifyHash bool
	tempPath   string
}

const leveldbBloomsize = 10

func NewLevelDBBlkStore(path string) (*CIDBlkStore, error) {
	if path == "" {
		path = filepath.Join(os.TempDir(), "falari-blockstore")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	db, err := leveldb.OpenFile(filepath.Join(path, "blocks"), nil)
	if err != nil {
		return nil, err
	}
	return &CIDBlkStore{db: db}, nil
}

func NewMemoryBlkStore() *CIDBlkStore {
	dir, err := os.MkdirTemp("", "falari-blockstore-*")
	if err != nil {
		panic(err)
	}
	store, err := NewLevelDBBlkStore(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		panic(err)
	}
	store.tempPath = dir
	return store
}

func (s *CIDBlkStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	tempPath := s.tempPath
	s.tempPath = ""
	defer func() {
		s.mu.Unlock()
		if tempPath != "" {
			_ = os.RemoveAll(tempPath)
		}
	}()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *CIDBlkStore) HashOnRead(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verifyHash = enabled
}

func (s *CIDBlkStore) Put(ctx context.Context, block Block) error {
	if block.CID == "" {
		return errors.New("blockstore: cid is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Put(cidKey(block.CID), block.Data, nil)
}

func (s *CIDBlkStore) PutMany(ctx context.Context, blocks []Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := new(leveldb.Batch)
	for _, block := range blocks {
		if block.CID == "" {
			return errors.New("blockstore: cid is required")
		}
		batch.Put(cidKey(block.CID), block.Data)
	}
	return s.db.Write(batch, nil)
}

func (s *CIDBlkStore) Get(ctx context.Context, cid string) (Block, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := s.db.Get(cidKey(cid), nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return Block{}, ErrNotFound
		}
		return Block{}, err
	}
	if s.verifyHash {
		hash, _ := wire.HashFromRawCID(cid)
		if hash != "" && chaincrypto.HashBytes(data) != hash {
			return Block{}, ErrHashMismatch
		}
	}
	return Block{CID: cid, Data: data}, nil
}

func (s *CIDBlkStore) Has(ctx context.Context, cid string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	exists, err := s.db.Has(cidKey(cid), nil)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (s *CIDBlkStore) GetSize(ctx context.Context, cid string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := s.db.Get(cidKey(cid), nil)
	if err != nil {
		return -1, ErrNotFound
	}
	return len(data), nil
}

func (s *CIDBlkStore) DeleteBlock(ctx context.Context, cid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Delete(cidKey(cid), nil)
}

func (s *CIDBlkStore) AllKeysChan(ctx context.Context) (<-chan string, error) {
	s.mu.RLock()
	iter := s.db.NewIterator(util.BytesPrefix([]byte("b:")), nil)
	ch := make(chan string, 128)

	go func() {
		defer close(ch)
		defer iter.Release()
		defer s.mu.RUnlock()
		for iter.Next() {
			select {
			case ch <- cidFromKey(iter.Key()):
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (s *CIDBlkStore) HashFromCID(ctx context.Context, cid string) (string, error) {
	block, err := s.Get(ctx, cid)
	if err != nil {
		return "", err
	}
	return chaincrypto.HashBytes(block.Data), nil
}

func (s *CIDBlkStore) Iterator() iterator.Iterator {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db.NewIterator(nil, nil)
}

var (
	ErrNotFound     = errors.New("blockstore: block not found")
	ErrHashMismatch = errors.New("blockstore: hash mismatch")
)

func cidKey(cid string) []byte {
	buf := make([]byte, 2+len(cid))
	copy(buf, "b:")
	copy(buf[2:], cid)
	return buf
}

func cidFromKey(key []byte) string {
	if len(key) < 3 {
		return ""
	}
	return string(key[2:])
}
