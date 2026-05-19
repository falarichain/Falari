package chain

import (
	"errors"
	"sort"
	"time"

	"chain/internal/wire"
)

func (s *Store) AcceptStorageProviderAnnouncement(announcement wire.StorageProviderAnnouncement) error {
	record := announcement.Provider
	if record.MinerAddress == "" || record.PublicKey == "" {
		return errors.New("provider miner address and public key are required")
	}
	if err := wire.VerifyStorageProvider(record); err != nil {
		return err
	}
	now := time.Now().Unix()
	if record.ExpiresAtUnix > 0 && record.ExpiresAtUnix < now {
		return errors.New("provider record expired")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if miner, ok := s.data.Miners[record.MinerAddress]; ok {
		if miner.PublicKey != "" && miner.PublicKey != record.PublicKey {
			return errors.New("provider public key mismatch with registered miner")
		}
		if record.Endpoint == "" {
			record.Endpoint = miner.Endpoint
		}
		if record.CapacityBytes == 0 {
			record.CapacityBytes = miner.CapacityBytes
		}
	}
	if record.LastSeenUnix == 0 {
		record.LastSeenUnix = now
	}
	s.data.ProviderRecords[record.MinerAddress] = record
	return s.saveLocked()
}

func (s *Store) StorageProviders(shardHash string, shardCID string, intentID string) (wire.StorageProvidersResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	providersByMiner := map[string]wire.StorageProviderRecord{}
	for minerAddress, record := range s.data.ProviderRecords {
		if record.ExpiresAtUnix > 0 && record.ExpiresAtUnix < now {
			continue
		}
		if !providerRecordMatches(record, shardHash, shardCID) {
			continue
		}
		providersByMiner[minerAddress] = s.enrichProviderRecordLocked(record, true, "provider_record")
	}

	for _, intent := range s.data.Intents {
		if intentID != "" && intent.IntentID != intentID {
			continue
		}
		if !intentAllowsProviderDiscovery(intent) {
			continue
		}
		for _, receipts := range intent.Receipts {
			for _, receipt := range receipts {
				if !receiptMatchesShard(receipt, shardHash, shardCID) {
					continue
				}
				record := providersByMiner[receipt.MinerAddress]
				miner := s.minerStatsLocked(receipt.MinerAddress)
				source := "provider_record"
				live := record.MinerAddress != ""
				if record.MinerAddress == "" {
					record = wire.StorageProviderRecord{
						MinerAddress:  receipt.MinerAddress,
						PublicKey:     receipt.MinerPublicKey,
						Endpoint:      receipt.MinerEndpoint,
						CapacityBytes: miner.CapacityBytes,
						LastSeenUnix:  miner.RegisteredAtUnix,
					}
					source = "receipt"
				}
				if record.Endpoint == "" {
					if receipt.MinerEndpoint != "" {
						record.Endpoint = receipt.MinerEndpoint
					} else {
						record.Endpoint = miner.Endpoint
					}
				}
				if record.PublicKey == "" {
					record.PublicKey = receipt.MinerPublicKey
				}
				if record.CapacityBytes == 0 {
					record.CapacityBytes = miner.CapacityBytes
				}
				record.ShardHashes = appendUnique(record.ShardHashes, receipt.ShardHash)
				record.Shards = appendProviderShard(record.Shards, wire.ProviderShard{
					ShardHash: receipt.ShardHash,
					ShardCID:  receipt.ShardCID,
					Size:      receipt.ShardSize,
				})
				providersByMiner[record.MinerAddress] = s.enrichProviderRecordLocked(record, live, source)
			}
		}
	}

	providers := make([]wire.StorageProviderRecord, 0, len(providersByMiner))
	for _, provider := range providersByMiner {
		sort.Strings(provider.ShardHashes)
		sort.Slice(provider.Shards, func(i, j int) bool {
			return provider.Shards[i].ShardHash < provider.Shards[j].ShardHash
		})
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool {
		if providers[i].HealthScoreBPS != providers[j].HealthScoreBPS {
			return providers[i].HealthScoreBPS > providers[j].HealthScoreBPS
		}
		if providers[i].ProviderRecordLive != providers[j].ProviderRecordLive {
			return providers[i].ProviderRecordLive
		}
		if providers[i].Endpoint != "" && providers[j].Endpoint == "" {
			return true
		}
		if providers[i].Endpoint == "" && providers[j].Endpoint != "" {
			return false
		}
		return providers[i].MinerAddress < providers[j].MinerAddress
	})
	return wire.StorageProvidersResponse{
		ShardHash: shardHash,
		ShardCID:  shardCID,
		IntentID:  intentID,
		Providers: providers,
	}, nil
}

func (s *Store) StorageRoutes(shardHash string, shardCID string, intentID string) (wire.StorageRoutesResponse, error) {
	providersResp, err := s.StorageProviders(shardHash, shardCID, intentID)
	if err != nil {
		return wire.StorageRoutesResponse{}, err
	}
	routes := make([]wire.StorageRoute, 0, len(providersResp.Providers)*3)
	seen := map[string]bool{}
	for _, provider := range providersResp.Providers {
		providerShardHash, providerShardCID := providerPreferredShard(provider, shardHash, shardCID)
		if provider.PeerID != "" && len(provider.PeerAddrs) > 0 && providerShardCID != "" {
			route := storageRouteFromProvider(provider, providerShardHash, providerShardCID, "libp2p", 0)
			key := routeKey(route)
			if !seen[key] {
				seen[key] = true
				routes = append(routes, route)
			}
		}
		if provider.Endpoint != "" && providerShardCID != "" {
			route := storageRouteFromProvider(provider, providerShardHash, providerShardCID, "http-block", 1)
			key := routeKey(route)
			if !seen[key] {
				seen[key] = true
				routes = append(routes, route)
			}
		}
		if provider.Endpoint != "" && providerShardHash != "" {
			route := storageRouteFromProvider(provider, providerShardHash, providerShardCID, "http-shard", 2)
			key := routeKey(route)
			if !seen[key] {
				seen[key] = true
				routes = append(routes, route)
			}
		}
	}
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].HealthScoreBPS != routes[j].HealthScoreBPS {
			return routes[i].HealthScoreBPS > routes[j].HealthScoreBPS
		}
		if routes[i].ProviderRecordLive != routes[j].ProviderRecordLive {
			return routes[i].ProviderRecordLive
		}
		if routes[i].Priority != routes[j].Priority {
			return routes[i].Priority < routes[j].Priority
		}
		return routes[i].MinerAddress < routes[j].MinerAddress
	})
	return wire.StorageRoutesResponse{
		ShardHash: providersResp.ShardHash,
		ShardCID:  providersResp.ShardCID,
		IntentID:  providersResp.IntentID,
		Routes:    routes,
	}, nil
}

func (s *Store) enrichProviderRecordLocked(record wire.StorageProviderRecord, live bool, source string) wire.StorageProviderRecord {
	miner := s.minerStatsLocked(record.MinerAddress)
	if record.Endpoint == "" {
		record.Endpoint = miner.Endpoint
	}
	if record.CapacityBytes == 0 {
		record.CapacityBytes = miner.CapacityBytes
	}
	if record.StoredBytes == 0 {
		record.StoredBytes = miner.UsedBytes
	}
	if record.PublicKey == "" {
		record.PublicKey = miner.PublicKey
	}
	record.ProofSuccess = miner.ProofSuccess
	record.ProofFailure = miner.ProofFailure
	record.ProviderRecordLive = live
	record.ProviderSource = source
	record.HealthScoreBPS = providerHealthScore(record, miner)
	return record
}

func storageRouteFromProvider(provider wire.StorageProviderRecord, shardHash string, shardCID string, transport string, priority int) wire.StorageRoute {
	return wire.StorageRoute{
		MinerAddress:       provider.MinerAddress,
		ShardHash:          shardHash,
		ShardCID:           shardCID,
		Transport:          transport,
		Endpoint:           provider.Endpoint,
		PeerID:             provider.PeerID,
		PeerAddrs:          append([]string(nil), provider.PeerAddrs...),
		HealthScoreBPS:     provider.HealthScoreBPS,
		ProviderRecordLive: provider.ProviderRecordLive,
		ProviderSource:     provider.ProviderSource,
		Priority:           priority,
	}
}

func providerPreferredShard(provider wire.StorageProviderRecord, shardHash string, shardCID string) (string, string) {
	hash := shardHash
	cid := shardCID
	for _, shard := range provider.Shards {
		if shardHash != "" && shard.ShardHash != shardHash {
			continue
		}
		if shardCID != "" && shard.ShardCID != shardCID {
			continue
		}
		if hash == "" {
			hash = shard.ShardHash
		}
		if cid == "" {
			cid = shard.ShardCID
		}
		if hash != "" || cid != "" {
			return hash, cid
		}
	}
	if hash == "" && len(provider.ShardHashes) > 0 {
		hash = provider.ShardHashes[0]
	}
	return hash, cid
}

func routeKey(route wire.StorageRoute) string {
	return route.MinerAddress + ":" + route.Transport + ":" + route.Endpoint + ":" + route.PeerID + ":" + route.ShardHash + ":" + route.ShardCID
}

func providerHealthScore(record wire.StorageProviderRecord, miner wire.MinerStats) uint64 {
	score := uint64(5000)
	totalProofs := miner.ProofSuccess + miner.ProofFailure
	if totalProofs > 0 {
		score = miner.ProofSuccess * 8000 / totalProofs
	} else if miner.Status == wire.MinerStatusActive {
		score = 6500
	}
	if record.Endpoint != "" {
		score += 1000
	}
	if record.ProviderRecordLive {
		score += 1000
	}
	if record.PeerID != "" || len(record.PeerAddrs) > 0 {
		score += 500
	}
	if miner.EffectiveWeight > 0 {
		if miner.EffectiveWeight >= miner.CapacityBytes/2 {
			score += 1000
		} else if miner.EffectiveWeight > 0 {
			score += 500
		}
	}
	if miner.AntiSpamScore > 0 {
		antiSpamBonus := miner.AntiSpamScore / 20
		if antiSpamBonus > 1000 {
			antiSpamBonus = 1000
		}
		score += antiSpamBonus
	}
	if miner.SpeedScore > 0 {
		speedBonus := miner.SpeedScore / 20
		if speedBonus > 1000 {
			speedBonus = 1000
		}
		score += speedBonus
	}
	switch miner.Status {
	case wire.MinerStatusDegraded:
		score /= 2
	case wire.MinerStatusJailed, wire.MinerStatusExiting, wire.MinerStatusExited:
		score = score / 10
	}
	if score > 10000 {
		score = 10000
	}
	return score
}

func providerRecordMatches(record wire.StorageProviderRecord, shardHash string, shardCID string) bool {
	if shardHash == "" && shardCID == "" {
		return true
	}
	for _, hash := range record.ShardHashes {
		if shardHash != "" && hash == shardHash {
			return true
		}
	}
	for _, shard := range record.Shards {
		if shardHash != "" && shard.ShardHash == shardHash {
			return true
		}
		if shardCID != "" && shard.ShardCID == shardCID {
			return true
		}
	}
	return false
}

func receiptMatchesShard(receipt wire.MinerReceipt, shardHash string, shardCID string) bool {
	if shardHash == "" && shardCID == "" {
		return true
	}
	if shardHash != "" && receipt.ShardHash == shardHash {
		return true
	}
	if shardCID != "" && receipt.ShardCID == shardCID {
		return true
	}
	return false
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendProviderShard(values []wire.ProviderShard, value wire.ProviderShard) []wire.ProviderShard {
	if value.ShardHash == "" {
		return values
	}
	for i, existing := range values {
		if existing.ShardHash == value.ShardHash {
			if values[i].Size == 0 {
				values[i].Size = value.Size
			}
			if values[i].ShardCID == "" {
				values[i].ShardCID = value.ShardCID
			}
			return values
		}
	}
	return append(values, value)
}
