package chain

import (
	"chain/internal/wire"
)

func (s *Store) computeMinerEffectiveWeightLocked(stats wire.MinerStats) uint64 {
	if stats.Status == wire.MinerStatusExiting || stats.Status == wire.MinerStatusExited {
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

	weight := storedBytes * proofScore / 10000 * defaultMinerStoredBytesWeightBPS / 10000
	weight += storedBytes * availabilityScore / 10000 * defaultMinerAvailabilityWeightBPS / 10000
	weight += storedBytes * decentralizationScore / 10000 * defaultMinerDecentralizationWeightBPS / 10000
	weight += storedBytes * defaultMinerProofScoreWeightBPS / 10000

	return weight
}

func (s *Store) RecomputeAllMinerWeightsLocked() {
	for addr, stats := range s.data.Miners {
		stats.EffectiveWeight = s.computeMinerEffectiveWeightLocked(stats)
		s.data.Miners[addr] = stats
	}
}
