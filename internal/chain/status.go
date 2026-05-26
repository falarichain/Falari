package chain

import (
	"time"

	"chain/internal/reward"
	"chain/internal/wire"
)

func (s *Store) Status() wire.ChainStatusResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	resp := wire.ChainStatusResponse{
		ChainID:             s.data.ChainID,
		Status:              "ok",
		Height:              uint64(len(s.data.Blocks)),
		PendingTransactions: len(s.data.PendingTxs),
		Accounts:            len(s.data.Accounts),
		Intents:             len(s.data.Intents),
		Deals:               len(s.data.Deals),
		Collections:         len(s.data.Collections),
		DataRecords:         len(s.data.DataRecords),
		Miners:              len(s.data.Miners),
		Validators:          len(s.data.Validators),
		ConsensusValidators: len(s.data.ConsensusValidators),
		EpochRound:          s.data.EpochRound,
		TotalSupply:         reward.TotalSupply,
		FeeMarket:           s.data.FeeMarket,
		StoragePricing:      s.data.StoragePricing,
		StorageFeePool:      s.data.StorageFeePool,
	}
	if len(s.data.Blocks) > 0 {
		latest := s.data.Blocks[len(s.data.Blocks)-1]
		resp.LatestBlockHash = latest.Hash
		resp.LatestBlockTimeUnix = latest.TimeUnix
	}
	for i := len(s.data.Blocks) - 1; i >= 0; i-- {
		if s.data.Blocks[i].Finality.Finalized {
			resp.LatestFinalizedHeight = s.data.Blocks[i].Height
			break
		}
	}
	for _, intent := range s.data.Intents {
		switch intent.Status {
		case wire.StatusUploading:
			resp.UploadingIntents++
		case wire.StatusPartial:
			resp.PartialIntents++
		case wire.StatusFinalized:
			resp.FinalizedIntents++
		case wire.StatusExpired:
			resp.ExpiredIntents++
		}
	}
	for _, miner := range s.data.Miners {
		if miner.Status != wire.MinerStatusActive {
			continue
		}
		resp.ActiveMiners++
		resp.CapacityBytes = saturatingAdd(resp.CapacityBytes, miner.CapacityBytes)
		resp.UsedBytes = saturatingAdd(resp.UsedBytes, miner.UsedBytes)
		resp.ReservedBytes = saturatingAdd(resp.ReservedBytes, miner.ReservedBytes)
		resp.RetrievalBytes = saturatingAdd(resp.RetrievalBytes, miner.RetrievalBytes)
	}
	resp.RetrievalReceipts = len(s.data.RetrievalReceipts)
	if s.data.RewardPools != nil {
		resp.StoragePoolRemaining = s.data.RewardPools.StorageRemaining
		resp.RetrievalPoolRemaining = s.data.RewardPools.RetrievalRemaining
		resp.ValidatorPoolRemaining = s.data.RewardPools.ValidatorRemaining
		resp.RepairPoolRemaining = s.data.RewardPools.RepairRemaining
		resp.FoundationPoolRemaining = s.data.RewardPools.FoundationRemaining
		resp.TokensReleased = s.data.RewardPools.TokensReleased
	}
	// Compute estimated per-block validator reward.
	if s.blockInterval > 0 {
		params := s.miningParamsLocked()
		blockSecs := uint64(s.blockInterval.Seconds())
		if blockSecs == 0 {
			blockSecs = 5
		}
		coeff := params.ReleaseCoefficientBPS
		if coeff == 0 {
			coeff = 10000
		}
		const secondsPerYear uint64 = 365 * 86400
		resp.ValidatorRewardPerBlock = reward.ValidatorPoolInitial * params.ValidatorAnnualRateBPS * blockSecs / secondsPerYear * coeff / 10000
		// Compute average availability and below-threshold count for consensus validators.
		availThreshold := params.AvailabilityThresholdBPS
		if availThreshold == 0 {
			availThreshold = 6000
		}
		var totalAvail uint64
		cvCount := len(s.data.ConsensusValidators)
		for addr := range s.data.ConsensusValidators {
			score := s.availabilityScoreLocked(addr)
			totalAvail += score
			if score < availThreshold {
				resp.ValidatorsBelowThreshold++
			}
		}
		if cvCount > 0 {
			resp.AverageAvailabilityBPS = totalAvail / uint64(cvCount)
		}
	}
	resp.FoundationAddress = s.data.FoundationAddress
	resp.RetrievalAddress = s.data.RetrievalAddress
	for challengeID := range s.data.Challenges {
		if _, ok := s.data.Proofs[challengeID]; !ok {
			resp.PendingChallenges++
		}
	}
	for _, epoch := range s.data.Epochs {
		if epoch.Status != "finalized" {
			resp.ActiveEpochs++
		} else {
			resp.EpochsFinalized++
		}
	}
	for _, task := range s.data.RepairTasks {
		switch task.Status {
		case "completed":
			resp.CompletedRepairTasks++
		default:
			resp.PendingRepairTasks++
		}
	}
	for _, health := range s.data.DealHealths {
		switch health.Status {
		case wire.DealHealthAtRisk:
			resp.DealsAtRisk++
		case wire.DealHealthCritical:
			resp.DealsCritical++
		}
	}
	for _, miner := range s.data.Miners {
		resp.TotalStorageRewards = saturatingAdd(resp.TotalStorageRewards, miner.StorageRewards)
		resp.TotalRetrievalRewards = saturatingAdd(resp.TotalRetrievalRewards, miner.RetrievalRewards)
		resp.TotalRepairRewards = saturatingAdd(resp.TotalRepairRewards, miner.RepairRewards)
		resp.TotalSlashed = saturatingAdd(resp.TotalSlashed, miner.Slashed)
	}
	for _, bucket := range s.data.MiningRewardVestings {
		if bucket.Total <= bucket.Released {
			continue
		}
		resp.TotalMiningPending = saturatingAdd(resp.TotalMiningPending, bucket.Total-bucket.Released)
		resp.PendingMiningBuckets++
	}
	return resp
}

func (s *Store) Snapshot() wire.StateSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := wire.StateSnapshot{
		Height:               uint64(len(s.data.Blocks)),
		EpochRound:           s.data.EpochRound,
		Accounts:             len(s.data.Accounts),
		Intents:              len(s.data.Intents),
		Deals:                len(s.data.Deals),
		ConsensusValidators:  len(s.data.ConsensusValidators),
		PendingMiningBuckets: len(s.data.MiningRewardVestings),
		TotalTokenSupply:     reward.TotalSupply,
		SnapshotAtUnix:       time.Now().Unix(),
	}

	for _, intent := range s.data.Intents {
		if intent.Status == wire.StatusFinalized {
			snap.FinalizedIntents++
		}
	}

	for _, miner := range s.data.Miners {
		switch miner.Status {
		case wire.MinerStatusActive:
			snap.MinersActive++
		case wire.MinerStatusDegraded:
			snap.MinersDegraded++
		case wire.MinerStatusJailed:
			snap.MinersJailed++
		case wire.MinerStatusExiting:
			snap.MinersExiting++
		}
	}

	for _, validator := range s.data.Validators {
		switch {
		case validator.Status == wire.ValidatorStatusActive:
			snap.ValidatorsActive++
		case validator.Status == wire.ValidatorStatusSlashed:
			snap.ValidatorsSlashed++
		}
	}

	for _, account := range s.data.Accounts {
		snap.TotalStakeLocked = saturatingAdd(snap.TotalStakeLocked, account.LockedStake)
		snap.TotalStorageLocked = saturatingAdd(snap.TotalStorageLocked, account.LockedStorage)
		snap.TotalMiningPending = saturatingAdd(snap.TotalMiningPending, account.PendingMiningRewards)
		snap.TotalTokenSupply = saturatingAdd(snap.TotalTokenSupply, account.Balance)
	}
	snap.TotalTokenSupply = saturatingAdd(snap.TotalTokenSupply, snap.TotalStakeLocked)
	snap.TotalTokenSupply = saturatingAdd(snap.TotalTokenSupply, snap.TotalStorageLocked)
	snap.TotalTokenSupply = saturatingAdd(snap.TotalTokenSupply, snap.TotalMiningPending)

	for challengeID := range s.data.Challenges {
		if _, ok := s.data.Proofs[challengeID]; !ok {
			snap.PendingChallenges++
		}
	}
	for _, epoch := range s.data.Epochs {
		if epoch.Status != "finalized" {
			snap.ActiveEpochs++
		}
	}
	for _, task := range s.data.RepairTasks {
		if task.Status != "completed" {
			snap.PendingRepairTasks++
		}
	}
	for _, health := range s.data.DealHealths {
		switch health.Status {
		case wire.DealHealthAtRisk:
			snap.DealsAtRisk++
		case wire.DealHealthCritical:
			snap.DealsCritical++
		}
	}
	for _, miner := range s.data.Miners {
		snap.TotalStorageRewards = saturatingAdd(snap.TotalStorageRewards, miner.StorageRewards)
		snap.TotalRetrievalRewards = saturatingAdd(snap.TotalRetrievalRewards, miner.RetrievalRewards)
		snap.TotalRepairRewards = saturatingAdd(snap.TotalRepairRewards, miner.RepairRewards)
		snap.TotalSlashed = saturatingAdd(snap.TotalSlashed, miner.Slashed)
	}
	return snap
}
