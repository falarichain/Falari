package dht

import "chain/internal/wire"

// ToStorageProviderRecords converts DHT provider records to the
// StorageProviderRecord format used by download and repair code paths.
func ToStorageProviderRecords(records []wire.DHTProviderRecord) []wire.StorageProviderRecord {
	out := make([]wire.StorageProviderRecord, 0, len(records))
	for _, r := range records {
		out = append(out, wire.StorageProviderRecord{
			MinerAddress:   r.MinerAddress,
			PublicKey:      r.PublicKey,
			Endpoint:       r.Endpoint,
			PeerID:         r.PeerID,
			PeerAddrs:      r.PeerAddrs,
			HealthScoreBPS: r.HealthScoreBPS,
			ExpiresAtUnix:  r.ExpiresAtUnix,
			ProviderSource: "dht",
		})
	}
	return out
}
