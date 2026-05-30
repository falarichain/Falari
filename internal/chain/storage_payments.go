package chain

import (
	"time"

	"chain/internal/wire"
)

// payableStorageRewardLocked calculates the maximum reward payable for a
// storage proof challenge, taking into account user escrow and permanent-fund
// pool subsidy (for permanent intents past the takeover time).
// Finite intents are limited to user escrow only — the global storage mining
// pool is NOT used to cover user payment shortfalls.
func (s *Store) payableStorageRewardLocked(challenge wire.StorageChallenge) uint64 {
	if challenge.Reward == 0 {
		return 0
	}
	intent, ok := s.data.Intents[challenge.IntentID]
	if !ok {
		return 0
	}
	remaining := remainingIntentEscrow(intent)
	if isPermanentIntent(intent) {
		fund := s.ensurePermanentFundLocked(intent, 0)
		remaining = fund.Balance
	}
	user := s.accountLocked(intent.User)
	reward := challenge.Reward
	feeAvailable := remaining
	if !isPermanentIntent(intent) {
		feeAvailable = finiteDealEscrowAvailable(s.dealEscrowLocked(intent), intent, time.Now().Unix())
		if feeAvailable > remaining {
			feeAvailable = remaining
		}
	}
	if user.LockedStorage < feeAvailable {
		feeAvailable = user.LockedStorage
	}
	if feeAvailable >= reward {
		return reward
	}
	// For permanent intents, add platform-level permanent fund pool subsidy (100%)
	// if the takeover time has elapsed.
	if isPermanentIntent(intent) {
		shortfall := reward - feeAvailable
		subsidy := s.permanentFundSubsidyCapLocked(intent, shortfall)
		covered := saturatingAdd(feeAvailable, subsidy)
		if covered < reward {
			return covered
		}
		return reward
	}
	// Finite intents: only user escrow, no pool fallback.
	return feeAvailable
}

func (s *Store) payStorageRewardLocked(challenge wire.StorageChallenge, minerAddress string, reward uint64) {
	if reward == 0 {
		return
	}
	intent, ok := s.data.Intents[challenge.IntentID]
	now := time.Now().Unix()
	remainingReward := reward
	if ok {
		paidFromFees := uint64(0)
		if isPermanentIntent(intent) {
			paidFromFees = s.spendPermanentFundLocked(intent, remainingReward, now)
		} else {
			paidFromFees = s.spendFiniteStorageFeeLocked(intent, remainingReward, now)
		}
		if paidFromFees > 0 {
			s.vestMiningRewardLocked(minerAddress, paidFromFees, miningRewardSourceStorageProof, now)
			if paidFromFees >= remainingReward {
				return
			}
			remainingReward -= paidFromFees
		}
		// Permanent fund pool subsidy: when the takeover time has elapsed,
		// the platform-level permanent fund pool covers 100% of the shortfall.
		if isPermanentIntent(intent) && remainingReward > 0 {
			subsidy := s.payPermanentFundSubsidyLocked(intent, minerAddress, remainingReward, now)
			if subsidy > 0 {
				s.vestMiningRewardLocked(minerAddress, subsidy, miningRewardSourceRepairPoolSubsidy, now)
				if subsidy >= remainingReward {
					return
				}
				remainingReward -= subsidy
			}
		}
		// Finite intents do not fall back to any pool.
	}
}

func (s *Store) payRepairRewardLocked(intent *Intent, minerAddress string, reward uint64) {
	if reward == 0 {
		return
	}
	now := time.Now().Unix()
	remainingReward := reward
	paidFromFees := uint64(0)
	if isPermanentIntent(intent) {
		paidFromFees = s.spendPermanentFundLocked(intent, remainingReward, now)
	} else {
		paidFromFees = s.spendFiniteStorageFeeLocked(intent, remainingReward, now)
	}
	if paidFromFees > 0 {
		s.vestMiningRewardLocked(minerAddress, paidFromFees, miningRewardSourceRepair, now)
		if paidFromFees >= remainingReward {
			return
		}
		remainingReward -= paidFromFees
	}
	// Permanent fund pool subsidy for depleted permanent funds (100% coverage).
	if isPermanentIntent(intent) && remainingReward > 0 {
		subsidy := s.payPermanentFundSubsidyLocked(intent, minerAddress, remainingReward, now)
		if subsidy > 0 {
			s.vestMiningRewardLocked(minerAddress, subsidy, miningRewardSourceRepairPoolSubsidy, now)
			if subsidy >= remainingReward {
				return
			}
			remainingReward -= subsidy
		}
		// Permanent intents do not fall back to any other pool.
		return
	}
	// Finite intents: only user escrow, no pool fallback.
}

// permanentFundSubsidyCapLocked calculates the maximum permanent fund pool
// subsidy for a given shortfall without actually paying. The pool covers 100%
// of the shortfall when the fund's age exceeds the takeover time (default 50
// years). Used by payableStorageRewardLocked.
func (s *Store) permanentFundSubsidyCapLocked(intent *Intent, shortfall uint64) uint64 {
	if intent == nil || shortfall == 0 || !isPermanentIntent(intent) {
		return 0
	}
	params := s.miningParamsLocked()
	if params.PermanentFundTakeoverSeconds <= 0 {
		return 0
	}
	fund := s.ensurePermanentFundLocked(intent, 0)
	if fund.Closed {
		return 0
	}
	// Time-based takeover: trigger when fund age exceeds PermanentFundTakeoverSeconds.
	fundAge := time.Now().Unix() - fund.CreatedAtUnix
	if fundAge < params.PermanentFundTakeoverSeconds {
		return 0
	}
	s.initRewardPoolsLocked()
	// 100% subsidy: cover the entire shortfall.
	subsidy := shortfall
	if subsidy > s.data.RewardPools.PermanentFundRemaining {
		subsidy = s.data.RewardPools.PermanentFundRemaining
	}
	return subsidy
}

// payPermanentFundSubsidyLocked deducts from the platform-level permanent fund
// pool to subsidize miner payments when the fund's age exceeds the takeover
// time (default 50 years). The pool covers 100% of the shortfall.
// Returns the amount actually paid.
func (s *Store) payPermanentFundSubsidyLocked(intent *Intent, minerAddress string, shortfall uint64, now int64) uint64 {
	if intent == nil || shortfall == 0 || !isPermanentIntent(intent) {
		return 0
	}
	params := s.miningParamsLocked()
	if params.PermanentFundTakeoverSeconds <= 0 {
		return 0
	}
	fund := s.ensurePermanentFundLocked(intent, now)
	if fund.Closed {
		return 0
	}
	// Time-based takeover: trigger when fund age exceeds PermanentFundTakeoverSeconds.
	fundAge := now - fund.CreatedAtUnix
	if fundAge < params.PermanentFundTakeoverSeconds {
		return 0
	}
	s.initRewardPoolsLocked()
	// 100% subsidy: cover the entire shortfall.
	subsidy := shortfall
	if subsidy > s.data.RewardPools.PermanentFundRemaining {
		subsidy = s.data.RewardPools.PermanentFundRemaining
	}
	if subsidy == 0 {
		return 0
	}
	s.data.RewardPools.PayFromPermanentFund(subsidy)
	s.data.StorageFeePool.RepairPoolTransferred = saturatingAdd(s.data.StorageFeePool.RepairPoolTransferred, subsidy)
	return subsidy
}

func remainingIntentEscrow(intent *Intent) uint64 {
	used := intent.PaidFee + intent.RefundedFee + intent.BurnedFee
	if used >= intent.LockedFee {
		return 0
	}
	return intent.LockedFee - used
}
