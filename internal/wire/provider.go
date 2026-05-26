package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"sort"
)

type storageProviderSigningPayload struct {
	AccessServiceRequired  bool            `json:"access_service_required,omitempty"`
	CapacityBytes          uint64          `json:"capacity_bytes,omitempty"`
	DownloadServiceEnabled bool            `json:"download_service_enabled,omitempty"`
	Endpoint               string          `json:"endpoint,omitempty"`
	ExpiresAtUnix          int64           `json:"expires_at_unix"`
	LastSeenUnix           int64           `json:"last_seen_unix"`
	MinerAddress           string          `json:"miner_address"`
	PeerAddrs              []string        `json:"peer_addrs,omitempty"`
	PeerID                 string          `json:"peer_id,omitempty"`
	PublicKey              string          `json:"public_key"`
	ShardCount             int             `json:"shard_count,omitempty"`
	ShardHashes            []string        `json:"shard_hashes,omitempty"`
	Shards                 []ProviderShard `json:"shards,omitempty"`
	StoredBytes            uint64          `json:"stored_bytes,omitempty"`
	UploadServiceEnabled   bool            `json:"upload_service_enabled,omitempty"`
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

func SignStorageProvider(record *StorageProviderRecord, privateKey *ecdsa.PrivateKey) error {
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
	sig, pub, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	record.Signature = sig
	if record.PublicKey == "" {
		record.PublicKey = pub
	}
	return nil
}

func VerifyStorageProvider(record StorageProviderRecord) error {
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
	return verifyInfraSignature(record.MinerAddress, record.Signature, payload)
}
