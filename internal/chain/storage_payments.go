package chain

import (
	"time"

	"chain/internal/wire"
)

// payableStorageRewardLocked calculates the maximum reward payable for a
// storage proof challenge, taking into account user escrow, repair-pool
// subsidy (for depleted permanent funds), and the storage mining pool.
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
	shortfall := reward - feeAvailable
	// For permanent intents with depleted fund, add repair pool subsidy.
	subsidy := uint64(0)
	if isPermanentIntent(intent) {
		subsidy = s.repairPoolSubsidyCapLocked(intent, shortfall)
		shortfall -= subsidy
		if shortfall == 0 {
			return reward
		}
	}
	s.initRewardPoolsLocked()
	poolAvailable := s.data.RewardPools.StorageRemaining
	covered := saturatingAdd(feeAvailable, saturatingAdd(subsidy, poolAvailable))
	if covered < reward {
		return covered
	}
	return reward
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
		// Repair pool subsidy: when permanent fund is depleted, the repair pool
		// covers a configurable fraction of the shortfall.
		if isPermanentIntent(intent) && remainingReward > 0 {
			subsidy := s.payRepairPoolSubsidyLocked(intent, minerAddress, remainingReward, now)
			if subsidy > 0 {
				s.vestMiningRewardLocked(minerAddress, subsidy, miningRewardSourceRepairPoolSubsidy, now)
				if subsidy >= remainingReward {
					return
				}
				remainingReward -= subsidy
			}
		}
	}
	if s.payStorageRewardFromPoolLocked(minerAddress, remainingReward) {
		s.vestMiningRewardLocked(minerAddress, remainingReward, miningRewardSourceStoragePool, now)
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
	// Repair pool subsidy for depleted permanent funds.
	if isPermanentIntent(intent) && remainingReward > 0 {
		subsidy := s.payRepairPoolSubsidyLocked(intent, minerAddress, remainingReward, now)
		if subsidy > 0 {
			s.vestMiningRewardLocked(minerAddress, subsidy, miningRewardSourceRepairPoolSubsidy, now)
			if subsidy >= remainingReward {
				return
			}
			remainingReward -= subsidy
		}
	}
	if s.payRepairRewardFromPoolLocked(minerAddress, remainingReward) {
		s.vestMiningRewardLocked(minerAddress, remainingReward, miningRewardSourceRepair, now)
	}
}

// repairPoolSubsidyCapLocked calculates the maximum repair pool subsidy for a
// given shortfall without actually paying. Used by payableStorageRewardLocked.
func (s *Store) repairPoolSubsidyCapLocked(intent *Intent, shortfall uint64) uint64 {
	if intent == nil || shortfall == 0 || !isPermanentIntent(intent) {
		return 0
	}
	params := s.miningParamsLocked()
	if params.RepairPoolTakeoverBPS == 0 || params.RepairPoolSubsidyBPS == 0 {
		return 0
	}
	fund := s.ensurePermanentFundLocked(intent, 0)
	if fund.Closed {
		return 0
	}
	// Ratio-based takeover: trigger when current daily rate drops below
	// InitialDailyRate * RepairPoolTakeoverBPS / 10000.
	threshold := fund.InitialDailyRate * params.RepairPoolTakeoverBPS / 10000
	if threshold == 0 {
		threshold = 1
	}
	if fund.SustainableDailyRate >= threshold {
		return 0
	}
	s.initRewardPoolsLocked()
	subsidy := shortfall * params.RepairPoolSubsidyBPS / 10000
	if subsidy > s.data.RewardPools.RepairRemaining {
		subsidy = s.data.RewardPools.RepairRemaining
	}
	return subsidy
}

// payRepairPoolSubsidyLocked deducts from the repair pool to subsidize miner
// payments when a permanent storage fund's daily rate drops below the takeover
// threshold (a configurable fraction of the initial rate). Returns the amount
// actually paid.
func (s *Store) payRepairPoolSubsidyLocked(intent *Intent, minerAddress string, shortfall uint64, now int64) uint64 {
	if intent == nil || shortfall == 0 || !isPermanentIntent(intent) {
		return 0
	}
	params := s.miningParamsLocked()
	if params.RepairPoolTakeoverBPS == 0 || params.RepairPoolSubsidyBPS == 0 {
		return 0
	}
	fund := s.ensurePermanentFundLocked(intent, now)
	if fund.Closed {
		return 0
	}
	// Ratio-based takeover: trigger when current daily rate drops below
	// InitialDailyRate * RepairPoolTakeoverBPS / 10000.
	threshold := fund.InitialDailyRate * params.RepairPoolTakeoverBPS / 10000
	if threshold == 0 {
		threshold = 1
	}
	if fund.SustainableDailyRate >= threshold {
		return 0
	}
	s.initRewardPoolsLocked()
	subsidy := shortfall * params.RepairPoolSubsidyBPS / 10000
	if subsidy > s.data.RewardPools.RepairRemaining {
		subsidy = s.data.RewardPools.RepairRemaining
	}
	if subsidy == 0 {
		return 0
	}
	s.data.RewardPools.PayFromRepairPool(subsidy)
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
