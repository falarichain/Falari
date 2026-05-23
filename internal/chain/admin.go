package chain

import (
	"errors"
)

// GetMiningParams returns the current mining parameters.
func (s *Store) GetMiningParams() MiningParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.MiningParams == nil {
		return DefaultMiningParams()
	}
	return *s.data.MiningParams
}

// UpdateMiningParams applies a partial update to mining parameters.
// Zero values in the request are ignored (fields left unchanged).
func (s *Store) UpdateMiningParams(req MiningParams) (*MiningParams, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data.MiningParams == nil {
		return nil, errors.New("mining params not initialized")
	}

	p := s.data.MiningParams

	applyIfNonZero(&p.StorageReleaseRateBPS, req.StorageReleaseRateBPS)
	applyIfNonZero(&p.RetrievalReleaseRateBPS, req.RetrievalReleaseRateBPS)
	applyIfNonZero(&p.ValidatorReleaseRateBPS, req.ValidatorReleaseRateBPS)
	applyIfNonZero(&p.StoredBytesWeightBPS, req.StoredBytesWeightBPS)
	applyIfNonZero(&p.ProofScoreWeightBPS, req.ProofScoreWeightBPS)
	applyIfNonZero(&p.AvailabilityWeightBPS, req.AvailabilityWeightBPS)
	applyIfNonZero(&p.DecentralizationWeightBPS, req.DecentralizationWeightBPS)
	applyIfNonZero(&p.RetrievalRewardPerMiB, req.RetrievalRewardPerMiB)
	applyIfNonZero(&p.MaxRetrievalRewardPerWindow, req.MaxRetrievalRewardPerWindow)
	applyIfNonZero(&p.RepairRewardPerShard, req.RepairRewardPerShard)
	applyIfNonZero(&p.MinerDegradeThreshold, req.MinerDegradeThreshold)
	if req.StorageProofSamples > 0 {
		p.StorageProofSamples = req.StorageProofSamples
	}
	applyIfNonZero(&p.ValidatorCommissionBPS, req.ValidatorCommissionBPS)

	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return p, nil
}

func applyIfNonZero(target *uint64, source uint64) {
	if source != 0 {
		*target = source
	}
}
