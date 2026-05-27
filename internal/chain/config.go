package chain

// MiningParams holds all mining-related parameters that can be hot-reloaded
// via the admin API without restarting the chain.
type MiningParams struct {
	// ── Pool release rates (BPS: basis points per epoch, 1 BPS = 0.01%) ──
	// Legacy per-epoch rates (deprecated, kept for backward compatibility).
	StorageReleaseRateBPS    uint64 `json:"storage_release_rate_bps"`
	RetrievalReleaseRateBPS  uint64 `json:"retrieval_release_rate_bps"`
	ValidatorReleaseRateBPS  uint64 `json:"validator_release_rate_bps"`
	FoundationReleaseRateBPS uint64 `json:"foundation_release_rate_bps"`

	// ── Annual release rates (BPS per year, time-proportional) ──
	// effectiveBPS = annualRateBPS * elapsedSeconds / 31_536_000
	// StoragePool: 500 BPS = 5%/year → 6B total, ~300M/year.
	StorageAnnualRateBPS    uint64 `json:"storage_annual_rate_bps"`
	RetrievalAnnualRateBPS  uint64 `json:"retrieval_annual_rate_bps"`
	ValidatorAnnualRateBPS  uint64 `json:"validator_annual_rate_bps"`
	FoundationAnnualRateBPS uint64 `json:"foundation_annual_rate_bps"`

	// ── Release coefficient (governance-adjustable multiplier) ──
	// Applied to all pools' annual release rates.
	// effectiveAnnualBPS = annualRateBPS * ReleaseCoefficientBPS / 10000
	// Default 10000 = 1.0x. Governance committee votes to increase or decrease.
	ReleaseCoefficientBPS uint64 `json:"release_coefficient_bps"`

	// ── Miner effective weight factors (BPS) ──
	StoredBytesWeightBPS      uint64 `json:"stored_bytes_weight_bps"`
	ProofScoreWeightBPS       uint64 `json:"proof_score_weight_bps"`
	AvailabilityWeightBPS     uint64 `json:"availability_weight_bps"`
	DecentralizationWeightBPS uint64 `json:"decentralization_weight_bps"`

	// ── Legacy retrieval receipts (kept for telemetry / audits) ──
	RetrievalRewardPerMiB       uint64 `json:"retrieval_reward_per_mib"`
	MaxRetrievalRewardPerWindow uint64 `json:"max_retrieval_reward_per_window"`

	// ── Repair ──
	RepairRewardPerShard uint64 `json:"repair_reward_per_shard"`

	// ── Repair pool takeover (subsidizes payments when permanent fund is depleted) ──
	// RepairPoolTakeoverBPS: when a permanent fund's SustainableDailyRate drops
	// below InitialDailyRate * RepairPoolTakeoverBPS / 10000, the repair pool
	// takes over. Default 1000 = 10% of initial rate.
	RepairPoolTakeoverBPS uint64 `json:"repair_pool_takeover_bps"`
	// RepairPoolSubsidyBPS: fraction of shortfall paid by repair pool (basis points).
	// Default 8000 = 80%.
	RepairPoolSubsidyBPS uint64 `json:"repair_pool_subsidy_bps"`

	// ── Penalties / thresholds ──
	MinerDegradeThreshold  uint64 `json:"miner_degrade_threshold"`
	StorageProofSamples    int    `json:"storage_proof_samples"`
	ValidatorCommissionBPS uint64 `json:"validator_commission_bps"`

	// ── Validator availability scoring ──
	// AvailabilityWindowSize: number of proposer turns in the sliding window.
	// Default 7200 (~10 hours at 5s block interval).
	AvailabilityWindowSize uint64 `json:"availability_window_size,omitempty"`
	// AvailabilityThresholdBPS: minimum availability score (BPS) to remain in
	// the consensus set. Default 6000 = 60%.
	AvailabilityThresholdBPS uint64 `json:"availability_threshold_bps,omitempty"`

	// ── Validator reward split ──
	// BlockProductionRewardBPS: fraction of per-block validator reward that goes
	// directly to the block producer (no vesting). Default 3000 = 30%.
	// The remainder (70%) is distributed to all consensus validators with vesting.
	BlockProductionRewardBPS uint64 `json:"block_production_reward_bps,omitempty"`

	// ── Consensus validator set limits ──
	MaxConsensusValidators uint64 `json:"max_consensus_validators,omitempty"`
	MinConsensusValidators uint64 `json:"min_consensus_validators,omitempty"`

	// ── Block and transaction size limits ──
	TargetBlockBytes  uint64 `json:"target_block_bytes,omitempty"`
	MaxBlockBytes     uint64 `json:"max_block_bytes,omitempty"`
	MaxBlockTxs       uint64 `json:"max_block_txs,omitempty"`
	MaxTxBytes        uint64 `json:"max_tx_bytes,omitempty"`
	MaxStorageTxBytes uint64 `json:"max_storage_tx_bytes,omitempty"`

	// ── DHT / Retrieval obligation ──
	RetrievalWeightBPS uint64 `json:"retrieval_weight_bps"`
}

// DefaultMiningParams returns the factory-default mining parameters.
func DefaultMiningParams() MiningParams {
	return MiningParams{
		StorageReleaseRateBPS:       3,
		RetrievalReleaseRateBPS:     0,
		ValidatorReleaseRateBPS:     2,
		FoundationReleaseRateBPS:    1,
		StorageAnnualRateBPS:        500,
		RetrievalAnnualRateBPS:      1000,
		ValidatorAnnualRateBPS:      1000,
		FoundationAnnualRateBPS:     1000,
		ReleaseCoefficientBPS:       10000,
		StoredBytesWeightBPS:        4000,
		ProofScoreWeightBPS:         3500,
		AvailabilityWeightBPS:       1500,
		DecentralizationWeightBPS:   1000,
		RetrievalRewardPerMiB:       0,
		MaxRetrievalRewardPerWindow: 0,
		RepairRewardPerShard:        100_000_000,
		RepairPoolTakeoverBPS:       1000,
		RepairPoolSubsidyBPS:        8000,
		MinerDegradeThreshold:       3,
		StorageProofSamples:         16,
		ValidatorCommissionBPS:      1000,
		AvailabilityWindowSize:      7200,
		AvailabilityThresholdBPS:    6000,
		BlockProductionRewardBPS:    3000,
		MaxConsensusValidators:      21,
		MinConsensusValidators:      2,
		TargetBlockBytes:            defaultTargetBlockBytes,
		MaxBlockBytes:               defaultMaxBlockBytes,
		MaxBlockTxs:                 defaultMaxBlockTxs,
		MaxTxBytes:                  defaultMaxTxBytes,
		MaxStorageTxBytes:           defaultMaxStorageTxBytes,
		RetrievalWeightBPS:          1000,
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

// Governance parameter bounds — hard safety limits that cannot be exceeded
// even through governance proposals.
const (
	maxAnnualReleaseRateBPS    = 5000  // 50%/year
	minReleaseCoefficientBPS   = 1000  // 0.1x
	maxReleaseCoefficientBPS   = 50000 // 5.0x
	maxWeightBPSSum            = 10000 // sum of 4 weight BPS must not exceed 100%
	minStorageProofSamples     = 1
	maxStorageProofSamples     = 64
	minMinerDegradeThreshold   = 1
	maxMinerDegradeThreshold   = 100
	maxConsensusValidatorsLimit = 100
)
