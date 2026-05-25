package wire

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	MinerAddress  string `json:"miner_address"`
	ShardHash     string `json:"shard_hash"`
	ExpiresAtUnix string `json:"expires_at_unix"`
}

func dhtProviderPayload(record DHTProviderRecord) ([]byte, error) {
	return json.Marshal(dhtProviderSigningPayload{
		MinerAddress:  record.MinerAddress,
		ShardHash:     record.ShardHash,
		ExpiresAtUnix: strconv.FormatInt(record.ExpiresAtUnix, 10),
	})
}

func SignDHTProvider(record *DHTProviderRecord, privateKey ed25519.PrivateKey) error {
	payload, err := dhtProviderPayload(*record)
	if err != nil {
		return err
	}
	record.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}

func VerifyDHTProvider(record DHTProviderRecord) error {
	if record.PublicKey == "" {
		return errors.New("missing public key")
	}
	publicKey, err := base64.StdEncoding.DecodeString(record.PublicKey)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid dht provider public key size")
	}
	signature, err := base64.StdEncoding.DecodeString(record.Signature)
	if err != nil {
		return err
	}
	payload, err := dhtProviderPayload(record)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("invalid dht provider signature")
	}
	return nil
}
