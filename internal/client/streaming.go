package client

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/reedsolomon"
)

type ShardFile struct {
	Index int
	Path  string
	Hash  string
	Size  int64
}

func EncodeSegmentToTempFiles(file *os.File, offset int64, size int64, dataShards int, parityShards int, tempRoot string) ([]ShardFile, func(), error) {
	if file == nil {
		return nil, nil, errors.New("file is required")
	}
	if size <= 0 {
		return nil, nil, errors.New("segment size must be positive")
	}
	if dataShards <= 0 || parityShards < 0 {
		return nil, nil, errors.New("invalid erasure policy")
	}
	dir, err := os.MkdirTemp(tempRoot, "chain-segment-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	totalShards := dataShards + parityShards
	paths := make([]string, totalShards)
	for i := range paths {
		paths[i] = filepath.Join(dir, "shard-"+itoa(i)+".bin")
	}
	if err := splitSegment(file, offset, size, dataShards, parityShards, paths); err != nil {
		cleanup()
		return nil, nil, err
	}
	shards := make([]ShardFile, totalShards)
	for i, path := range paths {
		hash, shardSize, err := hashFile(path)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		shards[i] = ShardFile{Index: i, Path: path, Hash: hash, Size: shardSize}
	}
	return shards, cleanup, nil
}

func EncodeBytesToTempFiles(data []byte, dataShards int, parityShards int, tempRoot string) ([]ShardFile, func(), error) {
	if len(data) == 0 {
		return nil, nil, errors.New("segment data must be non-empty")
	}
	if dataShards <= 0 || parityShards < 0 {
		return nil, nil, errors.New("invalid erasure policy")
	}
	dir, err := os.MkdirTemp(tempRoot, "chain-segment-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	shardData, err := EncodeShards(data, dataShards, parityShards)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	shards := make([]ShardFile, len(shardData))
	for i, shard := range shardData {
		path := filepath.Join(dir, "shard-"+itoa(i)+".bin")
		if err := os.WriteFile(path, shard, 0o644); err != nil {
			cleanup()
			return nil, nil, err
		}
		hash, shardSize, err := hashFile(path)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		shards[i] = ShardFile{Index: i, Path: path, Hash: hash, Size: shardSize}
	}
	return shards, cleanup, nil
}

func splitSegment(file *os.File, offset int64, size int64, dataShards int, parityShards int, paths []string) error {
	enc, err := reedsolomon.NewStream(dataShards, parityShards)
	if err != nil {
		return err
	}
	dataFiles := make([]*os.File, dataShards)
	dataWriters := make([]io.Writer, dataShards)
	for i := 0; i < dataShards; i++ {
		f, err := os.Create(paths[i])
		if err != nil {
			closeFiles(dataFiles)
			return err
		}
		dataFiles[i] = f
		dataWriters[i] = f
	}
	if err := enc.Split(io.NewSectionReader(file, offset, size), dataWriters, size); err != nil {
		closeFiles(dataFiles)
		return err
	}
	if err := closeFiles(dataFiles); err != nil {
		return err
	}

	readers := make([]io.Reader, dataShards)
	readerFiles := make([]*os.File, dataShards)
	for i := 0; i < dataShards; i++ {
		f, err := os.Open(paths[i])
		if err != nil {
			closeFiles(readerFiles)
			return err
		}
		readerFiles[i] = f
		readers[i] = f
	}
	parityFiles := make([]*os.File, parityShards)
	parityWriters := make([]io.Writer, parityShards)
	for i := 0; i < parityShards; i++ {
		f, err := os.Create(paths[dataShards+i])
		if err != nil {
			closeFiles(readerFiles)
			closeFiles(parityFiles)
			return err
		}
		parityFiles[i] = f
		parityWriters[i] = f
	}
	if err := enc.Encode(readers, parityWriters); err != nil {
		closeFiles(readerFiles)
		closeFiles(parityFiles)
		return err
	}
	if err := closeFiles(readerFiles); err != nil {
		closeFiles(parityFiles)
		return err
	}
	return closeFiles(parityFiles)
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

func closeFiles(files []*os.File) error {
	var firstErr error
	for _, file := range files {
		if file == nil {
			continue
		}
		if err := file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
