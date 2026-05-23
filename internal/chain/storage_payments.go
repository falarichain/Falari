package chain

import (
	"time"

	"chain/internal/wire"
)

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
	s.initRewardPoolsLocked()
	poolAvailable := s.data.RewardPools.StorageRemaining
	totalAvailable := saturatingAdd(feeAvailable, poolAvailable)
	if totalAvailable < reward {
		return totalAvailable
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
	if s.payRepairRewardFromPoolLocked(minerAddress, remainingReward) {
		s.vestMiningRewardLocked(minerAddress, remainingReward, miningRewardSourceRepair, now)
	}
}

func remainingIntentEscrow(intent *Intent) uint64 {
	used := intent.PaidFee + intent.RefundedFee
	if used >= intent.LockedFee {
		return 0
	}
	return intent.LockedFee - used
}
