package chain

import (
	"log"
	"time"

	"chain/internal/reward"
	"chain/internal/wire"
)

func (s *Store) initRewardPoolsLocked() {
	if s.data.RewardPools == nil {
		s.data.RewardPools = reward.NewPools()
	}
}

func (s *Store) releaseEpochRewardsLocked() {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()
	storageRelease, retrievalRelease, validatorRelease := s.data.RewardPools.ReleaseEpochRewards(
		params.StorageReleaseRateBPS,
		params.RetrievalReleaseRateBPS,
		params.ValidatorReleaseRateBPS,
	)

	s.distributeStoragePoolRewardsLocked(storageRelease)
	s.distributeRetrievalPoolRewardsLocked(retrievalRelease)
	s.distributeValidatorPoolRewardsLocked(validatorRelease)
	if storageRelease > 0 || retrievalRelease > 0 || validatorRelease > 0 {
		log.Printf("token release epoch=%d storage=%d retrieval=%d validator=%d total=%d",
			s.data.EpochRound, storageRelease, retrievalRelease, validatorRelease,
			storageRelease+retrievalRelease+validatorRelease)
	}
}

func (s *Store) distributeStoragePoolRewardsLocked(amount uint64) {
	if amount == 0 {
		return
	}
	var totalWeight uint64
	type weightEntry struct {
		address string
		weight  uint64
	}
	entries := make([]weightEntry, 0, len(s.data.Miners))
	for addr, stats := range s.data.Miners {
		if stats.Status == wire.MinerStatusExiting || stats.Status == wire.MinerStatusExited {
			continue
		}
		w := stats.EffectiveWeight
		if w == 0 && stats.UsedBytes > 0 && stats.Status == wire.MinerStatusActive {
			w = stats.UsedBytes
		}
		if w == 0 {
			continue
		}
		entries = append(entries, weightEntry{address: addr, weight: w})
		totalWeight = saturatingAdd(totalWeight, w)
	}
	if totalWeight == 0 || len(entries) == 0 {
		return
	}
	for _, entry := range entries {
		reward := amount * entry.weight / totalWeight
		if reward == 0 {
			continue
		}
		s.vestMiningRewardLocked(entry.address, reward, miningRewardSourceStoragePool, time.Now().Unix())
		stats := s.minerStatsLocked(entry.address)
		stats.StorageRewards = saturatingAdd(stats.StorageRewards, reward)
		stats.Rewards = saturatingAdd(stats.Rewards, reward)
		s.data.Miners[entry.address] = stats
	}
}

func (s *Store) distributeRetrievalPoolRewardsLocked(amount uint64) {
	if amount == 0 {
		return
	}
	type weightEntry struct {
		address string
		weight  uint64
	}
	entries := make([]weightEntry, 0, len(s.data.Miners))
	var totalWeight uint64
	for addr, stats := range s.data.Miners {
		if stats.Status == wire.MinerStatusExiting || stats.Status == wire.MinerStatusExited || stats.RetrievalBytes == 0 {
			continue
		}
		score := stats.AntiSpamScore
		if score == 0 {
			score = 10000
		}
		speed := stats.SpeedScore
		if speed == 0 {
			speed = 5000
		}
		weight := stats.RetrievalBytes * score / 10000
		weight = weight * speed / 10000
		if weight == 0 {
			weight = 1
		}
		entries = append(entries, weightEntry{address: addr, weight: weight})
		totalWeight = saturatingAdd(totalWeight, weight)
	}
	if totalWeight == 0 || len(entries) == 0 {
		return
	}
	var distributed uint64
	for i, entry := range entries {
		reward := amount * entry.weight / totalWeight
		if i == len(entries)-1 && distributed < amount {
			reward = amount - distributed
		}
		if reward == 0 {
			continue
		}
		distributed = saturatingAdd(distributed, reward)
		s.vestMiningRewardLocked(entry.address, reward, miningRewardSourceRetrievalPool, time.Now().Unix())
		stats := s.minerStatsLocked(entry.address)
		stats.RetrievalRewards = saturatingAdd(stats.RetrievalRewards, reward)
		stats.Rewards = saturatingAdd(stats.Rewards, reward)
		s.data.Miners[entry.address] = stats
	}
}

func (s *Store) distributeValidatorPoolRewardsLocked(amount uint64) {
	if amount == 0 {
		return
	}
	validators := s.consensusValidatorAddressesLocked()
	if len(validators) == 0 {
		return
	}
	var totalPower uint64
	for _, address := range validators {
		totalPower = saturatingAdd(totalPower, s.validatorPowerLocked(address))
	}
	if totalPower == 0 {
		return
	}
	var distributed uint64
	for i, address := range validators {
		power := s.validatorPowerLocked(address)
		if power == 0 {
			continue
		}
		reward := amount * power / totalPower
		if i == len(validators)-1 && distributed < amount {
			reward = amount - distributed
		}
		if reward == 0 {
			continue
		}
		distributed = saturatingAdd(distributed, reward)
		s.distributeValidatorRewardLocked(address, reward)
	}
}

func (s *Store) distributeValidatorRewardLocked(validatorAddress string, amount uint64) {
	validator := s.validatorLocked(validatorAddress)
	selfStake := validator.SelfStake
	if selfStake == 0 {
		selfStake = validator.Stake
	}
	totalPower := selfStake + validator.DelegatedStake
	if totalPower == 0 {
		s.vestMiningRewardLocked(validatorAddress, amount, miningRewardSourceValidatorPool, time.Now().Unix())
		validator.Rewards = saturatingAdd(validator.Rewards, amount)
		s.data.Validators[validatorAddress] = validator
		return
	}
	commission := amount * s.miningParamsLocked().ValidatorCommissionBPS / 10000
	selfReward := amount * selfStake / totalPower
	validatorReward := saturatingAdd(commission, selfReward)
	if validatorReward > amount {
		validatorReward = amount
	}
	delegatorPool := amount - validatorReward
	s.vestMiningRewardLocked(validatorAddress, validatorReward, miningRewardSourceValidatorPool, time.Now().Unix())
	validator.Rewards = saturatingAdd(validator.Rewards, validatorReward)

	delegations := s.validatorDelegationsLocked(validatorAddress)
	var delegatedPaid uint64
	for i, delegation := range delegations {
		share := uint64(0)
		if validator.DelegatedStake > 0 {
			share = delegatorPool * delegation.Amount / validator.DelegatedStake
		}
		if i == len(delegations)-1 && delegatedPaid < delegatorPool {
			share = delegatorPool - delegatedPaid
		}
		if share == 0 {
			continue
		}
		delegatedPaid = saturatingAdd(delegatedPaid, share)
		s.vestMiningRewardLocked(delegation.Delegator, share, miningRewardSourceDelegation, time.Now().Unix())
		validator.DelegationRewards = saturatingAdd(validator.DelegationRewards, share)
	}
	if delegatedPaid < delegatorPool {
		remainder := delegatorPool - delegatedPaid
		s.vestMiningRewardLocked(validatorAddress, remainder, miningRewardSourceValidatorPool, time.Now().Unix())
		validator.Rewards = saturatingAdd(validator.Rewards, remainder)
	}
	s.data.Validators[validatorAddress] = validator
}

func (s *Store) validatorDelegationsLocked(validatorAddress string) []wire.StakeDelegation {
	delegations := make([]wire.StakeDelegation, 0)
	for _, delegation := range s.data.StakeDelegations {
		if delegation.Validator == validatorAddress && delegation.Amount > 0 {
			delegations = append(delegations, delegation)
		}
	}
	return delegations
}

func (s *Store) addSlashedToRepairPoolLocked(amount uint64) {
	if amount == 0 {
		return
	}
	s.initRewardPoolsLocked()
	s.data.RewardPools.RepairRemaining = saturatingAdd(s.data.RewardPools.RepairRemaining, amount)
}

func (s *Store) payStorageRewardFromPoolLocked(minerAddress string, reward uint64) bool {
	s.initRewardPoolsLocked()
	return s.data.RewardPools.PayFromStoragePool(reward)
}

func (s *Store) payRetrievalRewardFromPoolLocked(minerAddress string, reward uint64) bool {
	s.initRewardPoolsLocked()
	return s.data.RewardPools.PayFromRetrievalPool(reward)
}

func (s *Store) payRepairRewardFromPoolLocked(minerAddress string, reward uint64) bool {
	s.initRewardPoolsLocked()
	return s.data.RewardPools.PayFromRepairPool(reward)
}

func (s *Store) StartTokenReleaseScheduler(interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			s.mu.Lock()
			releasedBuckets, releasedTotal := s.releaseVestedMiningRewardsLocked(time.Now().Unix())
			if releasedBuckets > 0 {
				log.Printf("released %d mining reward vesting buckets total=%d", releasedBuckets, releasedTotal)
			}
			s.releaseEpochRewardsLocked()
			if err := s.saveLocked(); err != nil {
				log.Printf("save token release state failed: %v", err)
			}
			s.mu.Unlock()
		}
	}()
}
