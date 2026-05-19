package storage

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

type StoredBlock struct {
	CID  string
	Hash string
	Size int64
}

type StorageBackend interface {
	PutBlock(block StoredBlock, data []byte) error
	GetByHash(hash string) ([]byte, error)
	GetByCID(cid string) ([]byte, error)
	DeleteByHash(hash string) error
	DeleteByCID(cid string) error
	ListBlocks() ([]StoredBlock, error)
}

type FileBackend struct {
	blockDir  string
	hashDir   string
	cidDir    string
	legacyDir string
}

func NewFileBackend(dataDir string) (*FileBackend, error) {
	backend := &FileBackend{
		blockDir:  filepath.Join(dataDir, "blocks"),
		hashDir:   filepath.Join(dataDir, "indexes", "hash"),
		cidDir:    filepath.Join(dataDir, "indexes", "cid"),
		legacyDir: filepath.Join(dataDir, "shards"),
	}
	for _, dir := range []string{backend.blockDir, backend.hashDir, backend.cidDir, backend.legacyDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return backend, nil
}

func (b *FileBackend) PutBlock(block StoredBlock, data []byte) error {
	if block.Hash == "" {
		block.Hash = chaincrypto.HashBytes(data)
	}
	if block.CID == "" {
		cid, err := wire.RawCIDForHash(block.Hash)
		if err != nil {
			return err
		}
		block.CID = cid
	}
	if err := os.WriteFile(b.blockPath(block.CID), data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(b.hashIndexPath(block.Hash), []byte(block.CID), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(b.cidIndexPath(block.CID), []byte(block.Hash), 0o644); err != nil {
		return err
	}
	return nil
}

func (b *FileBackend) GetByHash(hash string) ([]byte, error) {
	if cid, err := b.cidForHash(hash); err == nil && cid != "" {
		return os.ReadFile(b.blockPath(cid))
	}
	return os.ReadFile(b.legacyPath(hash))
}

func (b *FileBackend) GetByCID(cid string) ([]byte, error) {
	data, err := os.ReadFile(b.blockPath(cid))
	if err == nil {
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	hash, indexErr := b.hashForCID(cid)
	if indexErr != nil || hash == "" {
		hash, indexErr = wire.HashFromRawCID(cid)
		if indexErr != nil {
			return nil, err
		}
	}
	return os.ReadFile(b.legacyPath(hash))
}

func (b *FileBackend) DeleteByHash(hash string) error {
	cid, err := b.cidForHash(hash)
	if err == nil && cid != "" {
		if removeErr := os.Remove(b.blockPath(cid)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return removeErr
		}
		if removeErr := os.Remove(b.cidIndexPath(cid)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return removeErr
		}
	}
	if removeErr := os.Remove(b.hashIndexPath(hash)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	if removeErr := os.Remove(b.legacyPath(hash)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

func (b *FileBackend) DeleteByCID(cid string) error {
	hash, err := b.hashForCID(cid)
	if err == nil && hash != "" {
		return b.DeleteByHash(hash)
	}
	hash, err = wire.HashFromRawCID(cid)
	if err != nil {
		return os.Remove(b.blockPath(cid))
	}
	return b.DeleteByHash(hash)
}

func (b *FileBackend) ListBlocks() ([]StoredBlock, error) {
	blocksByHash := map[string]StoredBlock{}

	hashEntries, err := os.ReadDir(b.hashDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range hashEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".cid" {
			continue
		}
		hash := strings.TrimSuffix(entry.Name(), ".cid")
		cidValue, cidErr := b.cidForHash(hash)
		if cidErr != nil || cidValue == "" {
			continue
		}
		info, statErr := os.Stat(b.blockPath(cidValue))
		if statErr != nil {
			continue
		}
		blocksByHash[hash] = StoredBlock{Hash: hash, CID: cidValue, Size: info.Size()}
	}

	legacyEntries, err := os.ReadDir(b.legacyDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range legacyEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".bin" {
			continue
		}
		hash := strings.TrimSuffix(entry.Name(), ".bin")
		if _, exists := blocksByHash[hash]; exists {
			continue
		}
		cid, err := wire.RawCIDForHash(hash)
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		blocksByHash[hash] = StoredBlock{Hash: hash, CID: cid, Size: info.Size()}
	}

	blocks := make([]StoredBlock, 0, len(blocksByHash))
	for _, block := range blocksByHash {
		blocks = append(blocks, block)
	}
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].Hash < blocks[j].Hash
	})
	return blocks, nil
}

func (b *FileBackend) cidForHash(hash string) (string, error) {
	raw, err := os.ReadFile(b.hashIndexPath(hash))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func (b *FileBackend) hashForCID(cid string) (string, error) {
	raw, err := os.ReadFile(b.cidIndexPath(cid))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func (b *FileBackend) blockPath(cid string) string {
	return filepath.Join(b.blockDir, cid+".block")
}

func (b *FileBackend) hashIndexPath(hash string) string {
	return filepath.Join(b.hashDir, hash+".cid")
}

func (b *FileBackend) cidIndexPath(cid string) string {
	return filepath.Join(b.cidDir, cid+".hash")
}

func (b *FileBackend) legacyPath(hash string) string {
	return filepath.Join(b.legacyDir, hash+".bin")
}
