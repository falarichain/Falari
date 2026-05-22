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
	if remaining == 0 {
		return 0
	}
	user := s.accountLocked(intent.User)
	reward := challenge.Reward
	if reward > remaining {
		reward = remaining
	}
	if user.LockedStorage < reward {
		return user.LockedStorage
	}
	return reward
}

func (s *Store) payStorageRewardLocked(challenge wire.StorageChallenge, minerAddress string, reward uint64) {
	if reward == 0 {
		return
	}
	intent, ok := s.data.Intents[challenge.IntentID]
	if ok {
		if isPermanentIntent(intent) {
			reward = s.spendPermanentFundLocked(intent, reward, time.Now().Unix())
		}
		user := s.accountLocked(intent.User)
		if user.LockedStorage < reward {
			reward = user.LockedStorage
		}
		if reward > 0 {
			user.LockedStorage -= reward
			s.data.Accounts[intent.User] = user
			intent.PaidFee += reward
			s.payToMinerLocked(minerAddress, reward)
			return
		}
	}
	if s.payStorageRewardFromPoolLocked(minerAddress, reward) {
		s.payToMinerLocked(minerAddress, reward)
	}
}

func (s *Store) payToMinerLocked(minerAddress string, reward uint64) {
	if reward == 0 {
		return
	}
	miner := s.accountLocked(minerAddress)
	miner.Balance += reward
	s.data.Accounts[minerAddress] = miner
}

func (s *Store) payRepairRewardLocked(intent *Intent, minerAddress string, reward uint64) {
	if reward == 0 {
		return
	}
	remaining := remainingIntentEscrow(intent)
	if reward > remaining {
		reward = remaining
	}
	if reward > 0 {
		user := s.accountLocked(intent.User)
		if user.LockedStorage < reward {
			reward = user.LockedStorage
		}
		user.LockedStorage -= reward
		s.data.Accounts[intent.User] = user
		intent.PaidFee += reward
		s.payToMinerLocked(minerAddress, reward)
		return
	}
	if s.payRepairRewardFromPoolLocked(minerAddress, reward) {
		s.payToMinerLocked(minerAddress, reward)
	}
}

func remainingIntentEscrow(intent *Intent) uint64 {
	used := intent.PaidFee + intent.RefundedFee
	if used >= intent.LockedFee {
		return 0
	}
	return intent.LockedFee - used
}
