package wire

import (
	"crypto/ecdsa"
	"strconv"
)

// DHTProviderRecord is a per-shard provider record published to the DHT.
// Key in DHT: ShardHash → Value: serialized DHTProviderRecord.
type DHTProviderRecord struct {
	MinerAddress   string   `json:"miner_address"`
	PublicKey      string   `json:"public_key"`
	Endpoint       string   `json:"endpoint"`
	PeerID         string   `json:"peer_id"`
	PeerAddrs      []string `json:"peer_addrs,omitempty"`
	ShardHash      string   `json:"shard_hash"`
	HealthScoreBPS uint64   `json:"health_score_bps"`
	ExpiresAtUnix  int64    `json:"expires_at_unix"`
	Signature      string   `json:"signature"`
}

type dhtProviderSigningPayload struct {
	ExpiresAtUnix string `json:"expires_at_unix"`
	MinerAddress  string `json:"miner_address"`
	ShardHash     string `json:"shard_hash"`
}

func dhtProviderPayload(record DHTProviderRecord) dhtProviderSigningPayload {
	return dhtProviderSigningPayload{
		MinerAddress:  record.MinerAddress,
		ShardHash:     record.ShardHash,
		ExpiresAtUnix: strconv.FormatInt(record.ExpiresAtUnix, 10),
	}
}

func SignDHTProvider(record *DHTProviderRecord, privateKey *ecdsa.PrivateKey) error {
	payload := dhtProviderPayload(*record)
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

func VerifyDHTProvider(record DHTProviderRecord) error {
	payload := dhtProviderPayload(record)
	return verifyInfraSignature(record.MinerAddress, record.Signature, payload)
}
