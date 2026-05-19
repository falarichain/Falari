package storage

import (
	"sort"
	"strings"
	"sync"
	"time"

	"chain/internal/wire"
)

const (
	providerFailureCooldown       = 30 * time.Second
	providerFailureCooldownThresh = 2
	maxProviderMemoryEntries      = 8
)

var providerTransportMemoryStore = struct {
	mu      sync.RWMutex
	records map[string]wire.ProviderTransportMemory
}{
	records: map[string]wire.ProviderTransportMemory{},
}

func RememberProviderFetchSuccess(provider wire.StorageProviderRecord, transport string) {
	key := providerMemoryKey(provider)
	if key == "" {
		return
	}
	now := time.Now().Unix()
	providerTransportMemoryStore.mu.Lock()
	defer providerTransportMemoryStore.mu.Unlock()
	record := providerTransportMemoryStore.records[key]
	record.ProviderKey = key
	record.MinerAddress = provider.MinerAddress
	record.Endpoint = strings.TrimRight(provider.Endpoint, "/")
	record.PeerID = provider.PeerID
	record.LastTransport = transport
	record.LastOutcome = "success"
	record.LastSuccessUnix = now
	record.UpdatedAtUnix = now
	record.LastError = ""
	record.ConsecutiveFailures = 0
	record.CooldownUntilUnix = 0
	providerTransportMemoryStore.records[key] = record
}

func RememberProviderFetchFailure(provider wire.StorageProviderRecord, transport string, err error) {
	key := providerMemoryKey(provider)
	if key == "" {
		return
	}
	now := time.Now().Unix()
	providerTransportMemoryStore.mu.Lock()
	defer providerTransportMemoryStore.mu.Unlock()
	record := providerTransportMemoryStore.records[key]
	record.ProviderKey = key
	record.MinerAddress = provider.MinerAddress
	record.Endpoint = strings.TrimRight(provider.Endpoint, "/")
	record.PeerID = provider.PeerID
	record.LastTransport = transport
	record.LastOutcome = "failure"
	record.LastFailureUnix = now
	record.UpdatedAtUnix = now
	if err != nil {
		record.LastError = err.Error()
	}
	record.ConsecutiveFailures++
	if record.ConsecutiveFailures >= providerFailureCooldownThresh {
		record.CooldownUntilUnix = now + int64(providerFailureCooldown.Seconds())
	}
	providerTransportMemoryStore.records[key] = record
}

func ProviderTransportMemorySnapshot(limit int) []wire.ProviderTransportMemory {
	providerTransportMemoryStore.mu.RLock()
	defer providerTransportMemoryStore.mu.RUnlock()
	records := make([]wire.ProviderTransportMemory, 0, len(providerTransportMemoryStore.records))
	for _, record := range providerTransportMemoryStore.records {
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].UpdatedAtUnix != records[j].UpdatedAtUnix {
			return records[i].UpdatedAtUnix > records[j].UpdatedAtUnix
		}
		return records[i].ProviderKey < records[j].ProviderKey
	})
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	return records
}

func providerTransportMemory(provider wire.StorageProviderRecord) (wire.ProviderTransportMemory, bool) {
	key := providerMemoryKey(provider)
	if key == "" {
		return wire.ProviderTransportMemory{}, false
	}
	providerTransportMemoryStore.mu.RLock()
	defer providerTransportMemoryStore.mu.RUnlock()
	record, ok := providerTransportMemoryStore.records[key]
	return record, ok
}

func providerMemoryKey(provider wire.StorageProviderRecord) string {
	switch {
	case provider.PeerID != "":
		return "peer:" + provider.PeerID
	case strings.TrimSpace(provider.Endpoint) != "":
		return "endpoint:" + strings.TrimRight(provider.Endpoint, "/")
	case strings.TrimSpace(provider.MinerAddress) != "":
		return "miner:" + provider.MinerAddress
	default:
		return ""
	}
}

func resetProviderTransportMemoryForTests() {
	providerTransportMemoryStore.mu.Lock()
	defer providerTransportMemoryStore.mu.Unlock()
	providerTransportMemoryStore.records = map[string]wire.ProviderTransportMemory{}
}
