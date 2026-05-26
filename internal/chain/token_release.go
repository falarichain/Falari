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

	now := time.Now().Unix()
	lastRelease := s.data.LastReleaseAtUnix
	if lastRelease == 0 {
		lastRelease = now
		s.data.LastReleaseAtUnix = now
		return
	}
	elapsed := now - lastRelease
	if elapsed <= 0 {
		return
	}
	s.data.LastReleaseAtUnix = now

	const secondsPerYear int64 = 365 * 86400

	// Governance-controlled release coefficient.
	coeff := params.ReleaseCoefficientBPS
	if coeff == 0 {
		coeff = 10000
	}

	// Storage: exponential decay (pool_remaining × rate).
	storageBPS := params.StorageAnnualRateBPS * uint64(elapsed) / uint64(secondsPerYear)
	storageBPS = storageBPS * coeff / 10000
	// Pass 0 for retrieval/validator/foundation — handled separately.
	storageRelease, _, _, _ := s.data.RewardPools.ReleaseEpochRewards(
		storageBPS, 0, 0, 0,
	)

	// Foundation: linear release (initialAmount × rate).
	foundationBPS := params.FoundationAnnualRateBPS * uint64(elapsed) / uint64(secondsPerYear)
	foundationBPS = foundationBPS * coeff / 10000
	foundationRelease := s.data.RewardPools.ReleaseLinear(
		&s.data.RewardPools.FoundationRemaining,
		reward.FoundationPoolInitial,
		foundationBPS,
	)

	// Retrieval: linear release (initialAmount × rate).
	retrievalBPS := params.RetrievalAnnualRateBPS * uint64(elapsed) / uint64(secondsPerYear)
	retrievalBPS = retrievalBPS * coeff / 10000
	retrievalRelease := s.data.RewardPools.ReleaseLinear(
		&s.data.RewardPools.RetrievalRemaining,
		reward.RetrievalPoolInitial,
		retrievalBPS,
	)

	// Validator: released per-block, not per-epoch (see releaseValidatorPerBlockLocked).

	s.distributeStoragePoolRewardsLocked(storageRelease)
	s.distributeRetrievalPoolRewardsLocked(retrievalRelease)
	s.distributeFoundationPoolRewardsLocked(foundationRelease)
	if storageRelease > 0 || retrievalRelease > 0 || foundationRelease > 0 {
		log.Printf("token release epoch=%d elapsed=%ds coeff=%d storage=%d retrieval=%d foundation=%d total=%d",
			s.data.EpochRound, elapsed, coeff, storageRelease, retrievalRelease, foundationRelease,
			storageRelease+retrievalRelease+foundationRelease)
	}
}

// releaseValidatorPerBlockLocked releases validator rewards proportional to the
// time elapsed since the last per-block release. Uses linear release (constant
// emission based on ValidatorPoolInitial). Called on every block production / acceptance.
// Rewards are split: BlockProductionRewardBPS (default 30%) goes directly to the block
// producer without vesting; the remainder (70%) is distributed to all consensus
// validators with 90-day vesting.
func (s *Store) releaseValidatorPerBlockLocked(now int64, producerAddress string) {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()

	lastRelease := s.data.LastValidatorReleaseAtUnix
	if lastRelease == 0 {
		s.data.LastValidatorReleaseAtUnix = now
		return
	}
	elapsed := now - lastRelease
	if elapsed <= 0 {
		return
	}
	s.data.LastValidatorReleaseAtUnix = now

	const secondsPerYear int64 = 365 * 86400

	// Governance-controlled release coefficient.
	coeff := params.ReleaseCoefficientBPS
	if coeff == 0 {
		coeff = 10000
	}

	// Validator: linear release (initialAmount × rate).
	validatorBPS := params.ValidatorAnnualRateBPS * uint64(elapsed) / uint64(secondsPerYear)
	validatorBPS = validatorBPS * coeff / 10000
	validatorRelease := s.data.RewardPools.ReleaseLinear(
		&s.data.RewardPools.ValidatorRemaining,
		reward.ValidatorPoolInitial,
		validatorBPS,
	)

	if validatorRelease == 0 {
		return
	}

	// Split: block production reward (direct to producer) + staking reward (distributed).
	productionBPS := params.BlockProductionRewardBPS
	if productionBPS == 0 {
		productionBPS = 3000
	}
	blockReward := validatorRelease * productionBPS / 10000
	stakingReward := validatorRelease - blockReward

	// Credit block production reward directly to producer (no vesting).
	if blockReward > 0 && producerAddress != "" {
		account := s.accountLocked(producerAddress)
		account.Balance += blockReward
		s.data.Accounts[account.Address] = account
		validator := s.validatorLocked(producerAddress)
		validator.Rewards = saturatingAdd(validator.Rewards, blockReward)
		s.data.Validators[producerAddress] = validator
	}

	// Distribute staking reward to all consensus validators (with vesting).
	s.distributeValidatorPoolRewardsLocked(stakingReward)
	if validatorRelease > 0 {
		log.Printf("validator per-block release elapsed=%ds coeff=%d total=%d producer=%d staking=%d",
			elapsed, coeff, validatorRelease, blockReward, stakingReward)
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
	addr := s.data.RetrievalAddress
	if addr == "" {
		// No retrieval address configured — return tokens to pool.
		s.initRewardPoolsLocked()
		if s.data.RewardPools.TokensReleased >= amount {
			s.data.RewardPools.RetrievalRemaining = saturatingAdd(s.data.RewardPools.RetrievalRemaining, amount)
			s.data.RewardPools.TokensReleased -= amount
		}
		return
	}
	account := s.accountLocked(addr)
	account.Balance += amount
	s.data.Accounts[account.Address] = account
}

func (s *Store) distributeFoundationPoolRewardsLocked(amount uint64) {
	if amount == 0 {
		return
	}
	addr := s.data.FoundationAddress
	if addr == "" {
		// No foundation address configured — return tokens to pool.
		s.initRewardPoolsLocked()
		if s.data.RewardPools.TokensReleased >= amount {
			s.data.RewardPools.FoundationRemaining = saturatingAdd(s.data.RewardPools.FoundationRemaining, amount)
			s.data.RewardPools.TokensReleased -= amount
		}
		return
	}
	account := s.accountLocked(addr)
	account.Balance += amount
	s.data.Accounts[account.Address] = account
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
