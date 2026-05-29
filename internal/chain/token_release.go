package chain

import (
	"log"
	"math/big"
	"time"

	"chain/internal/reward"
	"chain/internal/wire"
)

func (s *Store) initRewardPoolsLocked() {
	if s.data.RewardPools == nil {
		s.data.RewardPools = reward.NewPools()
	}
}

func (s *Store) releaseEpochRewardsLocked(now int64) {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()

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

	// Storage: released per-block, not per-epoch (see releaseStoragePerBlockLocked).

	// Foundation: linear release (initialAmount × rate).
	foundationRelease := releaseProportionalLocked(&s.data.RewardPools.FoundationRemaining, reward.FoundationPoolInitial, params.FoundationAnnualRateBPS, elapsed, secondsPerYear)

	// Retrieval: linear release (initialAmount × rate).
	retrievalRelease := releaseProportionalLocked(&s.data.RewardPools.RetrievalRemaining, reward.RetrievalPoolInitial, params.RetrievalAnnualRateBPS, elapsed, secondsPerYear)

	// Validator: released per-block, not per-epoch (see releaseValidatorPerBlockLocked).

	totalRelease := saturatingAdd(retrievalRelease, foundationRelease)
	s.data.RewardPools.TokensReleased = saturatingAdd(s.data.RewardPools.TokensReleased, totalRelease)
	s.distributeRetrievalPoolRewardsLocked(retrievalRelease)
	s.distributeFoundationPoolRewardsLocked(foundationRelease)
	if retrievalRelease > 0 || foundationRelease > 0 {
		log.Printf("token release epoch=%d elapsed=%ds retrieval=%d foundation=%d total=%d",
			s.data.EpochRound, elapsed, retrievalRelease, foundationRelease,
			retrievalRelease+foundationRelease)
	}
}

func releaseProportionalLocked(pool *uint64, baseAmount uint64, annualRateBPS uint64, elapsed int64, secondsPerYear int64) uint64 {
	if pool == nil || *pool == 0 || baseAmount == 0 || annualRateBPS == 0 || elapsed <= 0 || secondsPerYear <= 0 {
		return 0
	}
	amount := mulDivUint64(baseAmount, annualRateBPS, uint64(elapsed), uint64(secondsPerYear)*10000)
	if amount > *pool {
		amount = *pool
	}
	if amount == 0 {
		return 0
	}
	*pool -= amount
	return amount
}

func mulDivUint64(a, b, c, denominator uint64) uint64 {
	if denominator == 0 {
		return 0
	}
	n := new(big.Int).SetUint64(a)
	n.Mul(n, new(big.Int).SetUint64(b))
	n.Mul(n, new(big.Int).SetUint64(c))
	n.Div(n, new(big.Int).SetUint64(denominator))
	if !n.IsUint64() {
		return ^uint64(0)
	}
	return n.Uint64()
}

// releaseValidatorPerBlockLocked releases a fixed ValidatorRewardPerBlock from
// the ValidatorPool on every block. The reward is split: BlockProductionRewardBPS
// (default 30%) goes directly to the block producer without vesting; the remainder
// (70%) is distributed to all consensus validators with 90-day vesting.
func (s *Store) releaseValidatorPerBlockLocked(now int64, producerAddress string) {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()

	perBlock := params.ValidatorRewardPerBlock
	if perBlock == 0 {
		perBlock = 16 * reward.TokenUnit
	}
	if s.data.RewardPools.ValidatorRemaining == 0 {
		return
	}
	validatorRelease := perBlock
	if validatorRelease > s.data.RewardPools.ValidatorRemaining {
		validatorRelease = s.data.RewardPools.ValidatorRemaining
	}
	s.data.RewardPools.ValidatorRemaining -= validatorRelease
	s.data.RewardPools.TokensReleased = saturatingAdd(s.data.RewardPools.TokensReleased, validatorRelease)
	s.data.LastValidatorReleaseAtUnix = now

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
	s.distributeValidatorPoolRewardsLocked(stakingReward, now)
	if validatorRelease > 0 {
		log.Printf("validator per-block release total=%d producer=%d staking=%d",
			validatorRelease, blockReward, stakingReward)
	}
}

// releaseStoragePerBlockLocked releases a fixed StorageRewardPerBlock from
// the StoragePool on every block into a global reward index. Miners settle
// their accrued share when they submit a valid storage proof.
func (s *Store) releaseStoragePerBlockLocked(now int64) {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()

	perBlock := params.StorageRewardPerBlock
	if perBlock == 0 {
		perBlock = 50 * reward.TokenUnit
	}
	if s.data.RewardPools.StorageRemaining == 0 {
		return
	}
	_, totalWeight := s.storageRewardEligibleEntriesLocked()
	if totalWeight.Sign() == 0 {
		return
	}
	storageRelease := perBlock
	if storageRelease > s.data.RewardPools.StorageRemaining {
		storageRelease = s.data.RewardPools.StorageRemaining
	}
	numerator := new(big.Int).Mul(new(big.Int).SetUint64(storageRelease), storageRewardIndexScale())
	numerator.Add(numerator, parseStorageRewardIndex(s.data.StorageRewardRemainder))
	indexIncrement := new(big.Int).Div(numerator, totalWeight)
	remainder := new(big.Int).Mod(numerator, totalWeight)
	if indexIncrement.Sign() == 0 {
		s.data.StorageRewardRemainder = remainder.String()
		return
	}
	index := parseStorageRewardIndex(s.data.StorageRewardIndex)
	index.Add(index, indexIncrement)
	s.data.StorageRewardIndex = index.String()
	s.data.StorageRewardRemainder = remainder.String()
	s.data.RewardPools.StorageRemaining -= storageRelease
	s.data.RewardPools.TokensReleased = saturatingAdd(s.data.RewardPools.TokensReleased, storageRelease)

	if storageRelease > 0 {
		log.Printf("storage per-block release total=%d index_increment=%s time=%d", storageRelease, indexIncrement.String(), now)
	}
}

func (s *Store) distributeStoragePoolRewardsLocked(amount uint64, now int64) {
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
		if stats.Status == wire.MinerStatusExiting || stats.Status == wire.MinerStatusExited || stats.Status == wire.MinerStatusJailed {
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
		s.vestMiningRewardLocked(entry.address, reward, miningRewardSourceStoragePool, now)
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

func (s *Store) distributeValidatorPoolRewardsLocked(amount uint64, now int64) {
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
		s.distributeValidatorRewardLocked(address, reward, now)
	}
}

func (s *Store) distributeValidatorRewardLocked(validatorAddress string, amount uint64, now int64) {
	validator := s.validatorLocked(validatorAddress)
	selfStake := validator.SelfStake
	if selfStake == 0 {
		selfStake = validator.Stake
	}
	totalPower := selfStake + validator.DelegatedStake
	if totalPower == 0 {
		s.vestMiningRewardLocked(validatorAddress, amount, miningRewardSourceValidatorPool, now)
		validator.Rewards = saturatingAdd(validator.Rewards, amount)
		s.data.Validators[validatorAddress] = validator
		return
	}
	// Use per-validator commission rate if set, otherwise fall back to global default
	commissionBPS := validator.CommissionRateBPS
	if commissionBPS == 0 {
		commissionBPS = s.miningParamsLocked().ValidatorCommissionBPS
	}
	commission := amount * commissionBPS / 10000
	selfReward := amount * selfStake / totalPower
	validatorReward := saturatingAdd(commission, selfReward)
	if validatorReward > amount {
		validatorReward = amount
	}
	delegatorPool := amount - validatorReward
	s.vestMiningRewardLocked(validatorAddress, validatorReward, miningRewardSourceValidatorPool, now)
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
		s.vestMiningRewardLocked(delegation.Delegator, share, miningRewardSourceDelegation, now)
		validator.DelegationRewards = saturatingAdd(validator.DelegationRewards, share)
	}
	if delegatedPaid < delegatorPool {
		remainder := delegatorPool - delegatedPaid
		s.vestMiningRewardLocked(validatorAddress, remainder, miningRewardSourceValidatorPool, now)
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

func (s *Store) payRepairRewardFromPoolLocked(minerAddress string, reward uint64) bool {
	s.initRewardPoolsLocked()
	return s.data.RewardPools.PayFromRepairPool(reward)
}

// StartTokenReleaseScheduler is deprecated. Token release is now handled
// deterministically in the block production path using block time.
// Kept as a no-op for backward compatibility with existing callers.
func (s *Store) StartTokenReleaseScheduler(interval time.Duration) {
	if interval <= 0 {
		return
	}
	log.Printf("StartTokenReleaseScheduler: deprecated — release now handled by block production")
}
