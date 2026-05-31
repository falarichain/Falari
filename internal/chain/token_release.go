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

// releaseFoundationPerBlockLocked releases a fixed FoundationRewardPerBlock from
// the FoundationPool on every block. The reward is sent directly to the
// FoundationAddress (no vesting).
func (s *Store) releaseFoundationPerBlockLocked(now int64) {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()

	perBlock := params.FoundationRewardPerBlock
	if perBlock == 0 {
		perBlock = 16 * reward.TokenUnit
	}
	if s.data.RewardPools.FoundationRemaining == 0 {
		return
	}
	amount := perBlock
	if amount > s.data.RewardPools.FoundationRemaining {
		amount = s.data.RewardPools.FoundationRemaining
	}
	s.data.RewardPools.FoundationRemaining -= amount
	s.data.RewardPools.TokensReleased = saturatingAdd(s.data.RewardPools.TokensReleased, amount)

	s.distributeFoundationPoolRewardsLocked(amount)
	if amount > 0 {
		log.Printf("foundation per-block release total=%d time=%d", amount, now)
	}
}

// releaseRetrievalPerBlockLocked releases a fixed RetrievalRewardPerBlock from
// the RetrievalPool on every block. The reward is sent directly to the
// RetrievalAddress (no vesting).
func (s *Store) releaseRetrievalPerBlockLocked(now int64) {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()

	perBlock := params.RetrievalRewardPerBlock
	if perBlock == 0 {
		perBlock = 10 * reward.TokenUnit
	}
	if s.data.RewardPools.RetrievalRemaining == 0 {
		return
	}
	amount := perBlock
	if amount > s.data.RewardPools.RetrievalRemaining {
		amount = s.data.RewardPools.RetrievalRemaining
	}
	s.data.RewardPools.RetrievalRemaining -= amount
	s.data.RewardPools.TokensReleased = saturatingAdd(s.data.RewardPools.TokensReleased, amount)

	s.distributeRetrievalPoolRewardsLocked(amount)
	if amount > 0 {
		log.Printf("retrieval per-block release total=%d time=%d", amount, now)
	}
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
	var distributed uint64
	for i, entry := range entries {
		reward := amount * entry.weight / totalWeight
		// Give integer-division remainder to the last entry to avoid token dust loss.
		if i == len(entries)-1 && distributed+reward < amount {
			reward = amount - distributed
		}
		if reward == 0 {
			continue
		}
		s.vestMiningRewardLocked(entry.address, reward, miningRewardSourceStoragePool, now)
		stats := s.minerStatsLocked(entry.address)
		stats.StorageRewards = saturatingAdd(stats.StorageRewards, reward)
		stats.Rewards = saturatingAdd(stats.Rewards, reward)
		s.data.Miners[entry.address] = stats
		distributed += reward
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
	commission := mulDivUint64(amount, commissionBPS, 1, 10000)
	selfReward := mulDivUint64(amount, selfStake, 1, totalPower)
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
			share = mulDivUint64(delegatorPool, delegation.Amount, 1, validator.DelegatedStake)
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

func (s *Store) addSlashedToPermanentFundLocked(amount uint64) {
	if amount == 0 {
		return
	}
	s.initRewardPoolsLocked()
	s.data.RewardPools.PermanentFundRemaining = saturatingAdd(s.data.RewardPools.PermanentFundRemaining, amount)
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
