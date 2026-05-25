package chain

import (
	"strconv"
	"time"

	"chain/internal/wire"
)

const (
	miningRewardSourceStorageProof      = "storage_proof"
	miningRewardSourceStoragePool       = "storage_pool"
	miningRewardSourceRetrievalReceipt  = "retrieval_receipt"
	miningRewardSourceRetrievalPool     = "retrieval_pool"
	miningRewardSourceRepair            = "repair"
	miningRewardSourceRepairPoolSubsidy = "repair_pool_subsidy"
	miningRewardSourceValidatorPool     = "validator_pool"
	miningRewardSourceDelegation        = "validator_delegation"
)

func (s *Store) vestMiningRewardLocked(address string, amount uint64, source string, now int64) uint64 {
	if amount == 0 {
		return 0
	}
	address = wire.NormalizeAddress(address)
	if address == "" {
		return 0
	}
	if now == 0 {
		now = time.Now().Unix()
	}
	if source == "" {
		source = "mining"
	}
	if s.data.MiningRewardVestings == nil {
		s.data.MiningRewardVestings = map[string]wire.MiningRewardVestingBucket{}
	}
	dayUnix := miningRewardVestingDayStart(now)
	bucketID := miningRewardVestingBucketID(address, dayUnix)
	bucket := s.data.MiningRewardVestings[bucketID]
	if bucket.BucketID == "" {
		bucket = wire.MiningRewardVestingBucket{
			BucketID:      bucketID,
			Address:       address,
			DayUnix:       dayUnix,
			CreatedAtUnix: now,
			Sources:       map[string]uint64{},
		}
	}
	if bucket.Sources == nil {
		bucket.Sources = map[string]uint64{}
	}
	bucket.Total = saturatingAdd(bucket.Total, amount)
	bucket.Sources[source] = saturatingAdd(bucket.Sources[source], amount)
	s.data.MiningRewardVestings[bucketID] = bucket

	account := s.accountLocked(address)
	account.PendingMiningRewards = saturatingAdd(account.PendingMiningRewards, amount)
	s.data.Accounts[address] = account
	return amount
}

func (s *Store) releaseVestedMiningRewardsLocked(now int64) (releasedBuckets int, totalReleased uint64) {
	if len(s.data.MiningRewardVestings) == 0 {
		return 0, 0
	}
	if now == 0 {
		now = time.Now().Unix()
	}
	for bucketID, bucket := range s.data.MiningRewardVestings {
		release := miningRewardBucketReleasable(bucket, now)
		if release == 0 {
			continue
		}
		account := s.accountLocked(bucket.Address)
		if account.PendingMiningRewards < release {
			release = account.PendingMiningRewards
		}
		if release == 0 {
			continue
		}
		account.PendingMiningRewards -= release
		account.Balance = saturatingAdd(account.Balance, release)
		s.data.Accounts[account.Address] = account

		bucket.Released = saturatingAdd(bucket.Released, release)
		bucket.LastReleasedAtUnix = now
		if bucket.Released >= bucket.Total {
			delete(s.data.MiningRewardVestings, bucketID)
		} else {
			s.data.MiningRewardVestings[bucketID] = bucket
		}
		releasedBuckets++
		totalReleased = saturatingAdd(totalReleased, release)
	}
	return releasedBuckets, totalReleased
}

func (s *Store) ReleaseVestedMiningRewards() (int, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	releasedBuckets, totalReleased := s.releaseVestedMiningRewardsLocked(time.Now().Unix())
	if releasedBuckets > 0 {
		if err := s.saveLocked(); err != nil {
			return 0, 0, err
		}
	}
	return releasedBuckets, totalReleased, nil
}

func miningRewardBucketReleasable(bucket wire.MiningRewardVestingBucket, now int64) uint64 {
	if bucket.Total <= bucket.Released || bucket.DayUnix == 0 || now <= bucket.DayUnix {
		return 0
	}
	elapsedDays := (now - bucket.DayUnix) / miningRewardVestingDaySeconds
	if elapsedDays <= 0 {
		return 0
	}
	if elapsedDays > miningRewardVestingDays {
		elapsedDays = miningRewardVestingDays
	}
	targetReleased := bucket.Total * uint64(elapsedDays) / uint64(miningRewardVestingDays)
	if elapsedDays == miningRewardVestingDays {
		targetReleased = bucket.Total
	}
	if targetReleased <= bucket.Released {
		return 0
	}
	return targetReleased - bucket.Released
}

func miningRewardVestingDayStart(unix int64) int64 {
	if unix <= 0 {
		return 0
	}
	return unix - unix%miningRewardVestingDaySeconds
}

func miningRewardVestingBucketID(address string, dayUnix int64) string {
	return address + ":" + strconv.FormatInt(dayUnix, 10)
}
