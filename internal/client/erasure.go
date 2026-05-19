package client

import (
	"bytes"
	"errors"

	"github.com/klauspost/reedsolomon"
)

func EncodeShards(data []byte, dataShards, parityShards int) ([][]byte, error) {
	if dataShards <= 0 || parityShards < 0 {
		return nil, errors.New("invalid erasure policy")
	}
	enc, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return nil, err
	}
	shards, err := enc.Split(data)
	if err != nil {
		return nil, err
	}
	if err := enc.Encode(shards); err != nil {
		return nil, err
	}
	return shards, nil
}

func DecodeShards(shards [][]byte, dataShards, parityShards int, size int) ([]byte, error) {
	if dataShards <= 0 || parityShards < 0 {
		return nil, errors.New("invalid erasure policy")
	}
	enc, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return nil, err
	}
	if hasMissingShard(shards) {
		if err := enc.Reconstruct(shards); err != nil {
			return nil, err
		}
	}
	ok, err := enc.Verify(shards)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("erasure shards failed verification")
	}
	var out bytes.Buffer
	if err := enc.Join(&out, shards, size); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func hasMissingShard(shards [][]byte) bool {
	for _, shard := range shards {
		if shard == nil {
			return true
		}
	}
	return false
}
