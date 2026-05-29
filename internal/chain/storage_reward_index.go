package chain

import (
	"math/big"
	"time"

	"chain/internal/wire"
)

const storageRewardIndexScaleUint64 = 1_000_000_000_000_000_000

type storageRewardWeightEntry struct {
	address string
	weight  uint64
}

func storageRewardIndexScale() *big.Int {
	return new(big.Int).SetUint64(storageRewardIndexScaleUint64)
}

func parseStorageRewardIndex(value string) *big.Int {
	if value == "" {
		return new(big.Int)
	}
	n, ok := new(big.Int).SetString(value, 10)
	if !ok || n.Sign() < 0 {
		return new(big.Int)
	}
	return n
}

func storageRewardBigToUint64(n *big.Int) uint64 {
	if n == nil || n.Sign() <= 0 {
		return 0
	}
	if !n.IsUint64() {
		return ^uint64(0)
	}
	return n.Uint64()
}

func (s *Store) storageRewardWeightLocked(stats wire.MinerStats) uint64 {
	if stats.Status == wire.MinerStatusExiting || stats.Status == wire.MinerStatusExited || stats.Status == wire.MinerStatusJailed {
		return 0
	}
	w := stats.EffectiveWeight
	if w == 0 && stats.UsedBytes > 0 && stats.Status == wire.MinerStatusActive {
		w = stats.UsedBytes
	}
	return w
}

func (s *Store) storageRewardEligibleEntriesLocked() ([]storageRewardWeightEntry, *big.Int) {
	entries := make([]storageRewardWeightEntry, 0, len(s.data.Miners))
	total := new(big.Int)
	for addr, stats := range s.data.Miners {
		weight := s.storageRewardWeightLocked(stats)
		if weight == 0 {
			continue
		}
		entries = append(entries, storageRewardWeightEntry{address: addr, weight: weight})
		total.Add(total, new(big.Int).SetUint64(weight))
	}
	return entries, total
}

func (s *Store) unsettledStorageRewardLocked(stats wire.MinerStats) uint64 {
	if stats.MinerAddress == "" || stats.StorageRewardIndex == "" || s.data.StorageRewardIndex == "" {
		return 0
	}
	weight := s.storageRewardWeightLocked(stats)
	if weight == 0 {
		return 0
	}
	globalIndex := parseStorageRewardIndex(s.data.StorageRewardIndex)
	minerIndex := parseStorageRewardIndex(stats.StorageRewardIndex)
	if globalIndex.Cmp(minerIndex) <= 0 {
		return 0
	}
	delta := new(big.Int).Sub(globalIndex, minerIndex)
	amount := new(big.Int).Mul(delta, new(big.Int).SetUint64(weight))
	amount.Div(amount, storageRewardIndexScale())
	return storageRewardBigToUint64(amount)
}

func (s *Store) accrueStorageRewardForMinerLocked(address string) uint64 {
	address = wire.NormalizeAddress(address)
	if address == "" {
		return 0
	}
	stats, ok := s.data.Miners[address]
	if !ok {
		return 0
	}
	if stats.StorageRewardIndex == "" {
		stats.StorageRewardIndex = s.data.StorageRewardIndex
		s.data.Miners[address] = stats
		return 0
	}
	amount := s.unsettledStorageRewardLocked(stats)
	stats.StorageRewardIndex = s.data.StorageRewardIndex
	if amount > 0 {
		stats.StorageRewardAccrued = saturatingAdd(stats.StorageRewardAccrued, amount)
	}
	s.data.Miners[address] = stats
	return amount
}

func (s *Store) settleStorageRewardForMinerLocked(address string, now int64) uint64 {
	address = wire.NormalizeAddress(address)
	if address == "" {
		return 0
	}
	if now == 0 {
		now = time.Now().Unix()
	}
	s.accrueStorageRewardForMinerLocked(address)
	stats, ok := s.data.Miners[address]
	if !ok {
		return 0
	}
	amount := stats.StorageRewardAccrued
	if amount > 0 {
		stats.StorageRewardAccrued = 0
		s.vestMiningRewardLocked(address, amount, miningRewardSourceStoragePool, now)
		stats.StorageRewards = saturatingAdd(stats.StorageRewards, amount)
		stats.Rewards = saturatingAdd(stats.Rewards, amount)
	}
	s.data.Miners[address] = stats
	return amount
}

func (s *Store) attachEstimatedStorageRewardsLocked(stats wire.MinerStats) wire.MinerStats {
	stats.UnsettledStorageRewards = saturatingAdd(stats.StorageRewardAccrued, s.unsettledStorageRewardLocked(stats))
	stats.EstimatedStorageRewards = saturatingAdd(stats.StorageRewards, stats.UnsettledStorageRewards)
	return stats
}
