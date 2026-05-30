package chain

import (
	"time"

	"chain/internal/reward"
)

// Epoch timing defaults — single source of truth for all epoch-derived values.
// Change these to adjust the epoch cycle across the entire system.
const (
	EpochIntervalDefault = 30 * time.Minute // epoch trigger interval
	EpochDurationDefault = 10 * time.Minute // proof submission window per epoch
)

// MiningParams holds all mining-related parameters that can be hot-reloaded
// via the admin API without restarting the chain.
type MiningParams struct {
	// ── Pool release rates (BPS: basis points per epoch, 1 BPS = 0.01%) ──
	// Legacy per-epoch rates (deprecated, kept for backward compatibility).
	StorageReleaseRateBPS    uint64 `json:"storage_release_rate_bps"`
	RetrievalReleaseRateBPS  uint64 `json:"retrieval_release_rate_bps"`
	FoundationReleaseRateBPS uint64 `json:"foundation_release_rate_bps"`

	// ── Annual release rates (BPS per year, time-proportional) ──
	// effectiveBPS = annualRateBPS * elapsedSeconds / 31_536_000
	StorageAnnualRateBPS    uint64 `json:"storage_annual_rate_bps"`
	RetrievalAnnualRateBPS  uint64 `json:"retrieval_annual_rate_bps"`
	FoundationAnnualRateBPS uint64 `json:"foundation_annual_rate_bps"`

	// ── Storage per-block reward ──
	// StorageRewardPerBlock: fixed number of smallest-unit tokens released from
	// the StoragePool on every block. Default 50 * TokenUnit (50 tokens).
	// When the pool is depleted, no more storage rewards are emitted.
	StorageRewardPerBlock uint64 `json:"storage_reward_per_block,omitempty"`

	// ── Miner effective weight factors (BPS) ──
	StoredBytesWeightBPS    uint64 `json:"stored_bytes_weight_bps"`
	ProofScoreWeightBPS     uint64 `json:"proof_score_weight_bps"`
	AvailabilityWeightBPS   uint64 `json:"availability_weight_bps"`
	RetrievalSpeedWeightBPS uint64 `json:"retrieval_speed_weight_bps"`
	IPDispersionWeightBPS   uint64 `json:"ip_dispersion_weight_bps"`

	// ── Legacy retrieval receipts (kept for telemetry / audits) ──
	RetrievalRewardPerMiB       uint64 `json:"retrieval_reward_per_mib"`
	MaxRetrievalRewardPerWindow uint64 `json:"max_retrieval_reward_per_window"`

	// ── Permanent fund takeover (subsidizes payments when permanent fund is depleted) ──
	// PermanentFundTakeoverSeconds: the platform-level permanent fund pool begins
	// subsidizing miner payments only after this duration has elapsed since the
	// fund was created. Default: 50 years (matching the permanent storage billing period).
	PermanentFundTakeoverSeconds int64 `json:"repair_pool_takeover_seconds"`

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

	// ── Validator per-block reward ──
	// ValidatorRewardPerBlock: fixed number of smallest-unit tokens released from
	// the ValidatorPool on every block. Default 16 * TokenUnit (16 tokens).
	// When the pool is depleted, no more validator rewards are emitted.
	ValidatorRewardPerBlock uint64 `json:"validator_reward_per_block,omitempty"`

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

	// ── Registration bonus ──
	// RegistrationBonusAmount: one-time locked bonus granted to each new miner.
	// Default 5000 * TokenUnit (5000 tokens).
	RegistrationBonusAmount uint64 `json:"registration_bonus_amount,omitempty"`
	// MinBonusProofCount: minimum successful proofs required to release the bonus.
	// Default 5000 (~57 days at 4 proofs/epoch, 1h epoch, 95% success rate).
	MinBonusProofCount uint64 `json:"min_bonus_proof_count,omitempty"`
	// MinBonusSuccessRateBPS: minimum proof success rate (BPS) to release the bonus.
	// Default 9500 = 95%.
	MinBonusSuccessRateBPS uint64 `json:"min_bonus_success_rate_bps,omitempty"`
	// MinBonusRetrievalCount: minimum successful retrievals required to release the bonus.
	// Default 100. Set to 0 to disable retrieval count check.
	MinBonusRetrievalCount uint64 `json:"min_bonus_retrieval_count,omitempty"`
	// MaxBonusAddresses: maximum number of miners who can receive the registration bonus.
	// Default 200_000. Set to 0 for unlimited.
	MaxBonusAddresses uint64 `json:"max_bonus_addresses,omitempty"`
	// BonusDeadlineSeconds: maximum time after registration to meet release conditions.
	// Default 7_776_000 (90 days). Set to 0 to disable deadline.
	BonusDeadlineSeconds uint64 `json:"bonus_deadline_seconds,omitempty"`
	// ActivationWindowSeconds: maximum time after registration to submit the
	// first valid storage proof. Miners who fail to do so are considered
	// inactive: their bonus is cancelled and they enter the exit flow.
	// Default 604_800 (7 days). Set to 0 to disable.
	ActivationWindowSeconds uint64 `json:"activation_window_seconds,omitempty"`

	// ── Repair delay ──
	// RepairDelayEpochs: number of consecutive missed epochs before a repair task
	// is created for a missing shard. Default 3.
	RepairDelayEpochs uint64 `json:"repair_delay_epochs,omitempty"`

	// ── Miner registration requirements ──
	// MinCapacityBytes: minimum disk capacity a miner must declare to register.
	// Default 200 GiB (200 * 1024^3). Set to 0 to disable.
	MinCapacityBytes uint64 `json:"min_capacity_bytes,omitempty"`
	// StakePerTiB: required locked stake (bonus + stake) per TiB of declared
	// capacity. Default 1000 * TokenUnit (1000 tokens/TiB). Set to 0 to disable.
	StakePerTiB uint64 `json:"stake_per_tib,omitempty"`
	// CapacityAdjustCooldownSeconds: minimum time between capacity adjustments.
	// Default 604_800 (7 days). Set to 0 to disable cooldown.
	CapacityAdjustCooldownSeconds uint64 `json:"capacity_adjust_cooldown_seconds,omitempty"`
}

// DefaultMiningParams returns the factory-default mining parameters.
func DefaultMiningParams() MiningParams {
	return MiningParams{
		StorageReleaseRateBPS:       3,
		RetrievalReleaseRateBPS:     0,
		FoundationReleaseRateBPS:    1,
		RetrievalAnnualRateBPS:      1000,
		FoundationAnnualRateBPS:     1000,
		StorageRewardPerBlock:       50 * reward.TokenUnit,
		StoredBytesWeightBPS:      4000,
		ProofScoreWeightBPS:       3000,
		AvailabilityWeightBPS:     1000,
		RetrievalSpeedWeightBPS:   1000,
		IPDispersionWeightBPS:     1000,
		RetrievalRewardPerMiB:       0,
		MaxRetrievalRewardPerWindow: 0,
		PermanentFundTakeoverSeconds: 50 * 365 * 24 * 60 * 60, // 50 years
		MinerDegradeThreshold:    24,
		StorageProofSamples:         16,
		ValidatorCommissionBPS:      1000,
		AvailabilityWindowSize:      7200,
		AvailabilityThresholdBPS:    6000,
		BlockProductionRewardBPS:    3000,
		ValidatorRewardPerBlock:     16 * reward.TokenUnit,
		MaxConsensusValidators:      21,
		MinConsensusValidators:      2,
		TargetBlockBytes:            defaultTargetBlockBytes,
		MaxBlockBytes:               defaultMaxBlockBytes,
		MaxBlockTxs:                 defaultMaxBlockTxs,
		MaxTxBytes:                  defaultMaxTxBytes,
		MaxStorageTxBytes:           defaultMaxStorageTxBytes,
		RetrievalWeightBPS:          3000,
		RegistrationBonusAmount:     5000 * reward.TokenUnit,
		MinBonusProofCount:          5000,
		MinBonusSuccessRateBPS:      9500,
		MinBonusRetrievalCount:      100,
		MaxBonusAddresses:           200_000,
		BonusDeadlineSeconds:        90 * 24 * 60 * 60,
		ActivationWindowSeconds:     7 * 24 * 60 * 60,
		RepairDelayEpochs:           3,
		MinCapacityBytes:            200 * 1024 * 1024 * 1024,
		StakePerTiB:                 1000 * reward.TokenUnit,
		CapacityAdjustCooldownSeconds: 7 * 24 * 60 * 60,
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

// RequiredStakeForCapacity calculates the minimum locked stake (LockedBonus +
// LockedStake) required for a miner to register with the given capacity.
// Uses ceiling division: sub-TiB fractions are charged as 1 TiB.
func RequiredStakeForCapacity(capacityBytes uint64, stakePerTiB uint64) uint64 {
	const TiB = uint64(1) << 40
	if capacityBytes == 0 || stakePerTiB == 0 {
		return 0
	}
	tibCount := (capacityBytes + TiB - 1) / TiB
	return tibCount * stakePerTiB
}

// Governance parameter bounds — hard safety limits that cannot be exceeded
// even through governance proposals.
const (
	maxAnnualReleaseRateBPS     = 5000                    // 50%/year
	maxValidatorRewardPerBlock  = 1000 * reward.TokenUnit // safety cap: 1000 tokens/block
	maxStorageRewardPerBlock    = 1000 * reward.TokenUnit // safety cap: 1000 tokens/block
	maxWeightBPSSum             = 10000                   // sum of 4 weight BPS must not exceed 100%
	minStorageProofSamples      = 1
	maxStorageProofSamples      = 64
	minMinerDegradeThreshold    = 1
	maxMinerDegradeThreshold    = 100
	maxConsensusValidatorsLimit = 100
)
