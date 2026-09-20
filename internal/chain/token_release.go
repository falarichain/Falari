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

// permanentFundInjectionAmountLocked returns how much of a gross storage or
// validator release still fits under the fund's lifetime cap. The cap changes
// how fast the fund fills, never how many tokens exist: once it is full the
// carve-out returns 0 and the recipients get the whole release. Pure, so a
// release that turns out to pay nobody cannot leave the fund inflated; the
// caller credits the fund once the release is committed. Caller must hold s.mu.
func (s *Store) permanentFundInjectionAmountLocked(gross uint64) uint64 {
	if gross == 0 {
		return 0
	}
	s.initRewardPoolsLocked()
	bps := s.miningParamsLocked().PermanentFundInjectionBPS
	if bps == 0 {
		return 0
	}
	if bps > 10000 {
		bps = 10000
	}
	filled := s.data.RewardPools.PermanentFundRemaining
	if filled >= reward.PermanentFundCap {
		return 0
	}
	want := gross * bps / 10000
	if headroom := reward.PermanentFundCap - filled; want > headroom {
		want = headroom
	}
	return want
}

// consensusValidatorPowerLocked is the total voting power available to receive
// the staking share of the validator release. Caller must hold s.mu.
func (s *Store) consensusValidatorPowerLocked() uint64 {
	var total uint64
	for _, address := range s.consensusValidatorAddressesLocked() {
		total = saturatingAdd(total, s.validatorPowerLocked(address))
	}
	return total
}

// releaseFoundationPerBlockLocked releases a fixed FoundationRewardPerBlock from
// the FoundationPool on every block. The reward is sent directly to the
// FoundationAddress (no vesting). Without a recipient the pool stays untouched:
// the emission is deferred, never minted and refunded.
func (s *Store) releaseFoundationPerBlockLocked(now int64) {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()

	perBlock := params.FoundationRewardPerBlock
	if perBlock == 0 {
		perBlock = 16 * reward.TokenUnit
	}
	if s.data.FoundationAddress == "" || s.data.RewardPools.FoundationRemaining == 0 {
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
// RetrievalAddress (no vesting). Without a recipient the pool stays untouched,
// so a node that never provisions the gateway address keeps the reservation
// intact instead of dropping it out of the supply schedule.
func (s *Store) releaseRetrievalPerBlockLocked(now int64) {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()

	perBlock := params.RetrievalRewardPerBlock
	if perBlock == 0 {
		perBlock = 10 * reward.TokenUnit
	}
	if s.data.RetrievalAddress == "" || s.data.RewardPools.RetrievalRemaining == 0 {
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
// the ValidatorPool on every block. PermanentFundInjectionBPS of the gross
// release is parked in the permanent-storage fund; the remainder is split:
// BlockProductionRewardBPS (default 30%) goes directly to the block producer
// without vesting, the rest to all consensus validators with 90-day vesting.
func (s *Store) releaseValidatorPerBlockLocked(now int64, producerAddress string) {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()

	perBlock := params.ValidatorRewardPerBlock
	if perBlock == 0 {
		perBlock = 22 * reward.TokenUnit
	}
	// No one to pay means no release: hold the pool instead of burning it, so the
	// validator stream still adds up to its lifetime total.
	if s.data.RewardPools.ValidatorRemaining == 0 || s.consensusValidatorPowerLocked() == 0 {
		return
	}
	validatorRelease := perBlock
	if validatorRelease > s.data.RewardPools.ValidatorRemaining {
		validatorRelease = s.data.RewardPools.ValidatorRemaining
	}
	fundShare := s.permanentFundInjectionAmountLocked(validatorRelease)
	payout := validatorRelease - fundShare
	s.data.RewardPools.ValidatorRemaining -= validatorRelease
	s.data.RewardPools.PermanentFundRemaining = reward.SaturatingAdd(s.data.RewardPools.PermanentFundRemaining, fundShare)
	s.data.RewardPools.TokensReleased = saturatingAdd(s.data.RewardPools.TokensReleased, validatorRelease)
	s.data.LastValidatorReleaseAtUnix = now

	// Split: block production reward (direct to producer) + staking reward (distributed).
	productionBPS := params.BlockProductionRewardBPS
	if productionBPS == 0 {
		productionBPS = 3000
	}
	if productionBPS > 10000 {
		productionBPS = 10000
	}
	blockReward := payout * productionBPS / 10000
	stakingReward := payout - blockReward

	// Credit block production reward directly to producer (no vesting).
	if blockReward > 0 && producerAddress != "" {
		account := s.accountLocked(producerAddress)
		account.Balance += blockReward
		s.data.Accounts[account.Address] = account
		validator := s.validatorLocked(producerAddress)
		validator.Rewards = saturatingAdd(validator.Rewards, blockReward)
		s.data.Validators[producerAddress] = validator
	} else if blockReward > 0 {
		stakingReward = saturatingAdd(stakingReward, blockReward)
	}

	// Distribute staking reward to all consensus validators (with vesting).
	s.distributeValidatorPoolRewardsLocked(stakingReward, now)
	if validatorRelease > 0 {
		log.Printf("validator per-block release total=%d fund=%d producer=%d staking=%d",
			validatorRelease, fundShare, blockReward, stakingReward)
	}
}

// releaseStoragePerBlockLocked releases a fixed StorageRewardPerBlock from
// the StoragePool on every block. PermanentFundInjectionBPS of the gross release
// is parked in the permanent-storage fund; the remainder feeds a global reward
// index that miners settle when they submit a valid storage proof.
func (s *Store) releaseStoragePerBlockLocked(now int64) {
	s.initRewardPoolsLocked()
	params := s.miningParamsLocked()

	perBlock := params.StorageRewardPerBlock
	if perBlock == 0 {
		perBlock = 70 * reward.TokenUnit
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
	fundShare := s.permanentFundInjectionAmountLocked(storageRelease)
	minerShare := storageRelease - fundShare
	numerator := new(big.Int).Mul(new(big.Int).SetUint64(minerShare), storageRewardIndexScale())
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
	s.data.RewardPools.PermanentFundRemaining = reward.SaturatingAdd(s.data.RewardPools.PermanentFundRemaining, fundShare)
	s.data.RewardPools.TokensReleased = saturatingAdd(s.data.RewardPools.TokensReleased, storageRelease)

	if storageRelease > 0 {
		log.Printf("storage per-block release total=%d fund=%d index_increment=%s time=%d",
			storageRelease, fundShare, indexIncrement.String(), now)
	}
}

// distributeRetrievalPoolRewardsLocked credits the retrieval gateway share. The
// caller only releases once a recipient exists, so an unset address can never
// mint tokens that then have to be clawed back.
func (s *Store) distributeRetrievalPoolRewardsLocked(amount uint64) {
	addr := s.data.RetrievalAddress
	if amount == 0 || addr == "" {
		return
	}
	account := s.accountLocked(addr)
	account.Balance += amount
	s.data.Accounts[account.Address] = account
}

func (s *Store) distributeFoundationPoolRewardsLocked(amount uint64) {
	addr := s.data.FoundationAddress
	if amount == 0 || addr == "" {
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
	for _, address := range validators {
		power := s.validatorPowerLocked(address)
		if power == 0 {
			continue
		}
		reward := amount * power / totalPower
		if reward == 0 {
			continue
		}
		distributed = saturatingAdd(distributed, reward)
		s.distributeValidatorRewardLocked(address, reward, now)
	}
	// Carry rounding remainder to next distribution cycle for fairness.
	if distributed < amount {
		s.data.RewardPools.ValidatorRemaining = saturatingAdd(
			s.data.RewardPools.ValidatorRemaining, amount-distributed)
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
