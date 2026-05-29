package dht

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"sync"
	"time"

	"chain/internal/wire"
)

const negativeCacheTTL = 10 * time.Second

// FindProviders discovers providers for a given shard hash using the DHT.
// It first checks the local cache, then queries the DHT, and falls back
// to the chain API if needed.
func (s *Service) FindProviders(ctx context.Context, shardHash string) ([]wire.DHTProviderRecord, error) {
	if s == nil {
		return nil, nil
	}
	// Check blacklist first.
	if s.blacklist.IsBlocked(shardHash) {
		return nil, nil
	}
	// Check local cache.
	if cached, ok := s.cache.Get(shardHash); ok {
		return cached, nil
	}
	// Run both lookups in parallel.
	var (
		valueRecords    []wire.DHTProviderRecord
		providerRecords []wire.DHTProviderRecord
		wg              sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		var err error
		valueRecords, err = s.lookupViaValue(ctx, shardHash)
		if err != nil {
			log.Printf("dht: value lookup for %s failed: %v", shardHash, err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		providerRecords, err = s.lookupViaProviders(ctx, shardHash)
		if err != nil {
			log.Printf("dht: provider lookup for %s failed: %v", shardHash, err)
		}
	}()
	wg.Wait()

	// Merge results, deduplicating by composite key (miner address + peer ID).
	records := mergeRecords(valueRecords, providerRecords)
	// Filter expired and unsigned records.
	now := time.Now().Unix()
	filtered := make([]wire.DHTProviderRecord, 0, len(records))
	for _, r := range records {
		if r.ExpiresAtUnix > 0 && r.ExpiresAtUnix < now {
			continue
		}
		// Verify signature.
		if err := wire.VerifyDHTProvider(r); err != nil {
			log.Printf("dht: invalid signature from %s for shard %s: %v", r.MinerAddress, shardHash, err)
			continue
		}
		filtered = append(filtered, r)
	}
	// Sort by health score descending (best providers first).
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].HealthScoreBPS > filtered[j].HealthScoreBPS
	})
	// Cache the result (including empty results with a shorter TTL).
	if len(filtered) > 0 {
		s.cache.Set(shardHash, filtered)
	} else {
		s.cache.SetWithTTL(shardHash, nil, negativeCacheTTL)
	}
	return filtered, nil
}

// lookupViaValue retrieves DHT provider records stored via PutValue.
func (s *Service) lookupViaValue(ctx context.Context, shardHash string) ([]wire.DHTProviderRecord, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, s.cfg.LookupTimeout)
	defer cancel()

	ch, err := s.dht.SearchValue(lookupCtx, "/falari/shard/"+shardHash)
	if err != nil {
		return nil, err
	}

	var records []wire.DHTProviderRecord
	for data := range ch {
		var record wire.DHTProviderRecord
		if err := json.Unmarshal(data, &record); err != nil {
			log.Printf("dht: unmarshal provider record failed: %v", err)
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

// lookupViaProviders uses the standard DHT Provide/FindProviders mechanism.
// Returns peer.AddrInfo which the caller can use to connect directly.
func (s *Service) lookupViaProviders(ctx context.Context, shardHash string) ([]wire.DHTProviderRecord, error) {
	c, err := shardHashToCID(shardHash)
	if err != nil {
		return nil, err
	}
	lookupCtx, cancel := context.WithTimeout(ctx, s.cfg.LookupTimeout)
	defer cancel()

	ch := s.dht.FindProvidersAsync(lookupCtx, c, 20)
	var records []wire.DHTProviderRecord
	for ai := range ch {
		if ai.ID == s.host.ID() {
			continue // skip self
		}
		addrs := make([]string, 0, len(ai.Addrs))
		for _, addr := range ai.Addrs {
			addrs = append(addrs, addr.String())
		}
		records = append(records, wire.DHTProviderRecord{
			PeerID:    ai.ID.String(),
			PeerAddrs: addrs,
			ShardHash: shardHash,
		})
	}
	return records, nil
}

// mergeRecords deduplicates provider records using a composite key of
// MinerAddress and PeerID, preferring records with richer information.
func mergeRecords(a, b []wire.DHTProviderRecord) []wire.DHTProviderRecord {
	seen := make(map[string]int)
	result := make([]wire.DHTProviderRecord, 0, len(a)+len(b))

	for _, r := range a {
		key := recordKey(r)
		if key == "" {
			continue
		}
		if idx, ok := seen[key]; ok {
			if recordScore(r) > recordScore(result[idx]) {
				result[idx] = r
			}
		} else {
			seen[key] = len(result)
			result = append(result, r)
		}
	}
	for _, r := range b {
		key := recordKey(r)
		if key == "" {
			continue
		}
		if idx, ok := seen[key]; ok {
			if recordScore(r) > recordScore(result[idx]) {
				result[idx] = r
			}
		} else {
			seen[key] = len(result)
			result = append(result, r)
		}
	}
	return result
}

// recordKey returns a composite deduplication key for a provider record.
// Uses both MinerAddress and PeerID to avoid collisions between records
// from different sources (value lookup vs provider lookup).
func recordKey(r wire.DHTProviderRecord) string {
	if r.MinerAddress == "" && r.PeerID == "" {
		return ""
	}
	return r.MinerAddress + "|" + r.PeerID
}

func recordScore(r wire.DHTProviderRecord) int {
	score := 0
	if r.Signature != "" {
		score += 4
	}
	if r.Endpoint != "" {
		score += 2
	}
	if r.PeerID != "" {
		score += 1
	}
	return score
}
