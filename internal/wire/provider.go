package wire

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
)

type storageProviderSigningPayload struct {
	MinerAddress           string          `json:"miner_address"`
	PublicKey              string          `json:"public_key"`
	Endpoint               string          `json:"endpoint,omitempty"`
	PeerID                 string          `json:"peer_id,omitempty"`
	PeerAddrs              []string        `json:"peer_addrs,omitempty"`
	CapacityBytes          uint64          `json:"capacity_bytes,omitempty"`
	StoredBytes            uint64          `json:"stored_bytes,omitempty"`
	ShardCount             int             `json:"shard_count,omitempty"`
	AccessServiceRequired  bool            `json:"access_service_required,omitempty"`
	UploadServiceEnabled   bool            `json:"upload_service_enabled,omitempty"`
	DownloadServiceEnabled bool            `json:"download_service_enabled,omitempty"`
	ShardHashes            []string        `json:"shard_hashes,omitempty"`
	Shards                 []ProviderShard `json:"shards,omitempty"`
	LastSeenUnix           int64           `json:"last_seen_unix"`
	ExpiresAtUnix          int64           `json:"expires_at_unix"`
}

func StorageProviderPayload(record StorageProviderRecord) ([]byte, error) {
	record.PeerAddrs = append([]string(nil), record.PeerAddrs...)
	record.ShardHashes = append([]string(nil), record.ShardHashes...)
	record.Shards = append([]ProviderShard(nil), record.Shards...)
	sort.Strings(record.PeerAddrs)
	sort.Strings(record.ShardHashes)
	sort.Slice(record.Shards, func(i, j int) bool {
		return record.Shards[i].ShardHash < record.Shards[j].ShardHash
	})
	payload := storageProviderSigningPayload{
		MinerAddress:           record.MinerAddress,
		PublicKey:              record.PublicKey,
		Endpoint:               record.Endpoint,
		PeerID:                 record.PeerID,
		PeerAddrs:              record.PeerAddrs,
		CapacityBytes:          record.CapacityBytes,
		StoredBytes:            record.StoredBytes,
		ShardCount:             record.ShardCount,
		AccessServiceRequired:  record.AccessServiceRequired,
		UploadServiceEnabled:   record.UploadServiceEnabled,
		DownloadServiceEnabled: record.DownloadServiceEnabled,
		ShardHashes:            record.ShardHashes,
		Shards:                 record.Shards,
		LastSeenUnix:           record.LastSeenUnix,
		ExpiresAtUnix:          record.ExpiresAtUnix,
	}
	return json.Marshal(payload)
}

func SignStorageProvider(record *StorageProviderRecord, privateKey ed25519.PrivateKey) error {
	payload, err := StorageProviderPayload(*record)
	if err != nil {
		return err
	}
	record.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}

func VerifyStorageProvider(record StorageProviderRecord) error {
	publicKey, err := base64.StdEncoding.DecodeString(record.PublicKey)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid provider public key size")
	}
	signature, err := base64.StdEncoding.DecodeString(record.Signature)
	if err != nil {
		return err
	}
	payload, err := StorageProviderPayload(record)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("invalid provider signature")
	}
	return nil
}
