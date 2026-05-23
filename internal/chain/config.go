package chain

// MiningParams holds all mining-related parameters that can be hot-reloaded
// via the admin API without restarting the chain.
type MiningParams struct {
	// ── Pool release rates (BPS: basis points per epoch, 1 BPS = 0.01%) ──
	StorageReleaseRateBPS   uint64 `json:"storage_release_rate_bps"`
	RetrievalReleaseRateBPS uint64 `json:"retrieval_release_rate_bps"`
	ValidatorReleaseRateBPS uint64 `json:"validator_release_rate_bps"`

	// ── Miner effective weight factors (BPS) ──
	StoredBytesWeightBPS      uint64 `json:"stored_bytes_weight_bps"`
	ProofScoreWeightBPS       uint64 `json:"proof_score_weight_bps"`
	AvailabilityWeightBPS     uint64 `json:"availability_weight_bps"`
	DecentralizationWeightBPS uint64 `json:"decentralization_weight_bps"`

	// ── Retrieval mining ──
	RetrievalRewardPerMiB       uint64 `json:"retrieval_reward_per_mib"`
	MaxRetrievalRewardPerWindow uint64 `json:"max_retrieval_reward_per_window"`

	// ── Repair ──
	RepairRewardPerShard uint64 `json:"repair_reward_per_shard"`

	// ── Penalties / thresholds ──
	MinerDegradeThreshold  uint64 `json:"miner_degrade_threshold"`
	StorageProofSamples    int    `json:"storage_proof_samples"`
	ValidatorCommissionBPS uint64 `json:"validator_commission_bps"`
}

// DefaultMiningParams returns the factory-default mining parameters.
func DefaultMiningParams() MiningParams {
	return MiningParams{
		StorageReleaseRateBPS:       3,
		RetrievalReleaseRateBPS:     20,
		ValidatorReleaseRateBPS:     2,
		StoredBytesWeightBPS:        4000,
		ProofScoreWeightBPS:         3500,
		AvailabilityWeightBPS:       1500,
		DecentralizationWeightBPS:   1000,
		RetrievalRewardPerMiB:       1,
		MaxRetrievalRewardPerWindow: 100,
		RepairRewardPerShard:        1,
		MinerDegradeThreshold:       3,
		StorageProofSamples:         8,
		ValidatorCommissionBPS:      1000,
	}
}

// miningParamsLocked returns the current mining parameters.
// Never returns nil — falls back to defaults if not yet initialized.
// Caller must hold s.mu.
func (s *Store) miningParamsLocked() *MiningParams {
	if s.data.MiningParams == nil {
		defaults := DefaultMiningParams()
		s.data.MiningParams = &defaults
	}
	return s.data.MiningParams
}
