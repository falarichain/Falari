package chain

import (
	"time"

	"chain/internal/wire"
)

// DHTStalenessSeconds is the maximum age (in seconds) of a miner's last DHT
// publish before it is considered stale. Miners with stale DHT records have
// their RetrievalObligMet flag cleared at epoch finalization.
const DHTStalenessSeconds = 900 // 15 minutes (~3 epochs at 5 min)

func (s *Store) computeMinerEffectiveWeightLocked(stats wire.MinerStats) uint64 {
	if stats.Status == wire.MinerStatusExiting || stats.Status == wire.MinerStatusExited || stats.Status == wire.MinerStatusJailed {
		return 0
	}

	storedBytes := stats.UsedBytes
	if storedBytes == 0 {
		return 0
	}

	proofScore := uint64(5000)
	totalProofs := stats.ProofSuccess + stats.ProofFailure
	if totalProofs > 0 {
		proofScore = stats.ProofSuccess * 10000 / totalProofs
	} else if stats.Status == wire.MinerStatusActive {
		proofScore = 6500
	}

	availabilityScore := uint64(10000)
	if stats.ConsecutiveFailures > 0 {
		divisor := uint64(1) << stats.ConsecutiveFailures
		if divisor == 0 {
			divisor = 1
		}
		if divisor <= 64 {
			availabilityScore = 10000 / divisor
		} else {
			availabilityScore = 0
		}
	}

	decentralizationScore := uint64(5000)
	if stats.DelegatorCount > 1 {
		decentralizationScore = 10000
	}

	switch stats.Status {
	case wire.MinerStatusDegraded:
		proofScore /= 2
		availabilityScore /= 2
	case wire.MinerStatusJailed:
		proofScore /= 10
		availabilityScore = 0
	}

	params := s.miningParamsLocked()
	weight := storedBytes * proofScore / 10000 * params.StoredBytesWeightBPS / 10000
	weight += storedBytes * availabilityScore / 10000 * params.AvailabilityWeightBPS / 10000
	weight += storedBytes * decentralizationScore / 10000 * params.DecentralizationWeightBPS / 10000
	weight += storedBytes * proofScore / 10000 * params.ProofScoreWeightBPS / 10000

	// Retrieval obligation penalty: miners who don't participate in
	// retrieval+DHT lose a portion of their weight.
	if !stats.RetrievalObligMet && stats.DHTLastPublishUnix > 0 && params.RetrievalWeightBPS > 0 {
		weight = weight * (10000 - params.RetrievalWeightBPS) / 10000
	}

	return weight
}

func (s *Store) RecomputeAllMinerWeightsLocked() {
	for addr, stats := range s.data.Miners {
		stats.EffectiveWeight = s.computeMinerEffectiveWeightLocked(stats)
		s.data.Miners[addr] = stats
	}
}

// checkDHTObligationsLocked clears RetrievalObligMet for miners whose DHT
// publish records have gone stale. Called during epoch finalization.
func (s *Store) checkDHTObligationsLocked() {
	cutoff := time.Now().Unix() - DHTStalenessSeconds
	for addr, stats := range s.data.Miners {
		if stats.Status == wire.MinerStatusExiting || stats.Status == wire.MinerStatusExited {
			continue
		}
		if stats.DHTLastPublishUnix > 0 && stats.DHTLastPublishUnix < cutoff {
			stats.RetrievalObligMet = false
			s.data.Miners[addr] = stats
		}
	}
}
