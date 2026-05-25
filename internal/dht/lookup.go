package dht

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"time"

	"chain/internal/wire"
)

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
	// Query DHT for provider records stored via PutValue.
	records, err := s.lookupViaValue(ctx, shardHash)
	if err != nil {
		log.Printf("dht: value lookup for %s failed: %v", shardHash, err)
	}
	// Also query DHT provider mechanism for additional peers.
	providerRecords, err := s.lookupViaProviders(ctx, shardHash)
	if err != nil {
		log.Printf("dht: provider lookup for %s failed: %v", shardHash, err)
	}
	// Merge results, deduplicating by miner address.
	records = mergeRecords(records, providerRecords)
	// Filter expired records.
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
	// Cache the result.
	if len(filtered) > 0 {
		s.cache.Set(shardHash, filtered)
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

// mergeRecords deduplicates provider records by miner address, preferring
// records with richer information (non-empty signature > with endpoint > peer only).
func mergeRecords(a, b []wire.DHTProviderRecord) []wire.DHTProviderRecord {
	seen := make(map[string]int)
	result := make([]wire.DHTProviderRecord, 0, len(a)+len(b))

	for _, r := range a {
		key := r.MinerAddress
		if key == "" {
			key = r.PeerID
		}
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
		key := r.MinerAddress
		if key == "" {
			key = r.PeerID
		}
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
