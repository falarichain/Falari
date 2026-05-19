package chain

import (
	"errors"
	"sort"
	"strconv"

	"chain/internal/wire"
)

func (s *Store) buildStorageAssignmentsLocked(req wire.CreateIntentRequest) ([]wire.StorageAssignment, error) {
	miners := s.assignableMinersLocked()
	if len(miners) == 0 {
		return nil, nil
	}
	totalShards := req.Erasure.DataShards + req.Erasure.ParityShards
	if totalShards <= 0 {
		return nil, errors.New("invalid erasure policy")
	}
	available := make(map[string]uint64, len(miners))
	for _, miner := range miners {
		available[miner.MinerAddress] = minerAvailableBytes(miner)
	}
	assignments := make([]wire.StorageAssignment, 0, len(req.Segments)*totalShards)
	for segmentID, segment := range req.Segments {
		if len(segment.ShardHashes) != totalShards {
			return nil, errors.New("segment shard count does not match erasure policy")
		}
		shardSize, err := plannedShardSize(req, segmentID)
		if err != nil {
			return nil, err
		}
		usedInSegment := map[string]bool{}
		for shardIndex, shardHash := range segment.ShardHashes {
			miner, ok := chooseAssignmentMiner(miners, available, usedInSegment, req.FileRoot, segmentID, shardIndex, uint64(shardSize))
			if !ok {
				return nil, errors.New("insufficient active miner capacity for storage assignments")
			}
			available[miner.MinerAddress] -= uint64(shardSize)
			usedInSegment[miner.MinerAddress] = true
			assignments = append(assignments, wire.StorageAssignment{
				SegmentID:    segmentID,
				ShardIndex:   shardIndex,
				MinerAddress: miner.MinerAddress,
				Endpoint:     miner.Endpoint,
				ShardHash:    shardHash,
				ShardCID:     shardCIDForSegment(segment, shardIndex),
				ShardSize:    shardSize,
			})
		}
	}
	return assignments, nil
}

func shardCIDForSegment(segment wire.SegmentPlan, shardIndex int) string {
	if shardIndex >= 0 && shardIndex < len(segment.ShardCIDs) {
		return segment.ShardCIDs[shardIndex]
	}
	cid, err := wire.RawCIDForHash(segment.ShardHashes[shardIndex])
	if err != nil {
		return ""
	}
	return cid
}

func (s *Store) reserveStorageAssignmentsLocked(assignments []wire.StorageAssignment) {
	for _, assignment := range assignments {
		miner := s.minerStatsLocked(assignment.MinerAddress)
		if assignment.ShardSize > 0 {
			miner.ReservedBytes = saturatingAdd(miner.ReservedBytes, uint64(assignment.ShardSize))
		}
		s.data.Miners[assignment.MinerAddress] = miner
	}
}

func (s *Store) releaseStorageReservationLocked(assignment wire.StorageAssignment) {
	if assignment.MinerAddress == "" || assignment.ShardSize <= 0 {
		return
	}
	miner := s.minerStatsLocked(assignment.MinerAddress)
	size := uint64(assignment.ShardSize)
	if miner.ReservedBytes < size {
		miner.ReservedBytes = 0
	} else {
		miner.ReservedBytes -= size
	}
	s.data.Miners[assignment.MinerAddress] = miner
}

func (s *Store) releaseUncommittedStorageReservationsLocked(intent *Intent) {
	for _, assignment := range intent.Assignments {
		if _, ok := intent.Receipts[assignment.SegmentID][assignment.ShardIndex]; ok {
			continue
		}
		s.releaseStorageReservationLocked(assignment)
	}
}

func (s *Store) validateReceiptAssignmentLocked(intent *Intent, receipt wire.MinerReceipt) error {
	assignment, ok := assignmentForShard(intent.Assignments, receipt.SegmentID, receipt.ShardIndex)
	if ok && assignment.MinerAddress == receipt.MinerAddress {
		if assignment.ShardHash != "" && assignment.ShardHash != receipt.ShardHash {
			return errors.New("receipt shard hash does not match storage assignment")
		}
		if assignment.ShardCID != "" && receipt.ShardCID != "" && assignment.ShardCID != receipt.ShardCID {
			return errors.New("receipt shard cid does not match storage assignment")
		}
		if assignment.ShardSize > 0 && assignment.ShardSize != receipt.ShardSize {
			return errors.New("receipt shard size does not match storage assignment")
		}
		return nil
	}
	task, hasRepair := s.pendingRepairTaskForShardLocked(intent.IntentID, receipt.SegmentID, receipt.ShardIndex, receipt.MinerAddress)
	if hasRepair {
		if task.Assignment.ShardHash != "" && task.Assignment.ShardHash != receipt.ShardHash {
			return errors.New("receipt shard hash does not match repair assignment")
		}
		if task.Assignment.ShardCID != "" && receipt.ShardCID != "" && task.Assignment.ShardCID != receipt.ShardCID {
			return errors.New("receipt shard cid does not match repair assignment")
		}
		if task.Assignment.ShardSize > 0 && task.Assignment.ShardSize != receipt.ShardSize {
			return errors.New("receipt shard size does not match repair assignment")
		}
		return nil
	}
	if ok {
		return errors.New("receipt miner does not match storage assignment")
	}
	return errors.New("receipt has no storage assignment")
}

func assignmentForShard(assignments []wire.StorageAssignment, segmentID int, shardIndex int) (wire.StorageAssignment, bool) {
	for _, assignment := range assignments {
		if assignment.SegmentID == segmentID && assignment.ShardIndex == shardIndex {
			return assignment, true
		}
	}
	return wire.StorageAssignment{}, false
}

func setAssignmentForShard(assignments []wire.StorageAssignment, replacement wire.StorageAssignment) []wire.StorageAssignment {
	for i, assignment := range assignments {
		if assignment.SegmentID == replacement.SegmentID && assignment.ShardIndex == replacement.ShardIndex {
			assignments[i] = replacement
			return assignments
		}
	}
	return append(assignments, replacement)
}

func (s *Store) assignableMinersLocked() []wire.MinerStats {
	miners := make([]wire.MinerStats, 0, len(s.data.Miners))
	for _, miner := range s.data.Miners {
		if miner.Status != wire.MinerStatusActive {
			continue
		}
		if minerAvailableBytes(miner) == 0 {
			continue
		}
		miners = append(miners, miner)
	}
	sort.SliceStable(miners, func(i, j int) bool {
		if minerAvailableBytes(miners[i]) != minerAvailableBytes(miners[j]) {
			return minerAvailableBytes(miners[i]) > minerAvailableBytes(miners[j])
		}
		return miners[i].MinerAddress < miners[j].MinerAddress
	})
	return miners
}

func chooseAssignmentMiner(miners []wire.MinerStats, available map[string]uint64, usedInSegment map[string]bool, fileRoot string, segmentID int, shardIndex int, shardSize uint64) (wire.MinerStats, bool) {
	start := assignmentStart(fileRoot, segmentID, shardIndex, len(miners))
	for pass := 0; pass < 2; pass++ {
		for offset := 0; offset < len(miners); offset++ {
			miner := miners[(start+offset)%len(miners)]
			if pass == 0 && usedInSegment[miner.MinerAddress] {
				continue
			}
			if available[miner.MinerAddress] >= shardSize {
				return miner, true
			}
		}
	}
	return wire.MinerStats{}, false
}

func assignmentStart(fileRoot string, segmentID int, shardIndex int, minerCount int) int {
	if minerCount <= 1 {
		return 0
	}
	seed := hashString(fileRoot + ":" + strconv.Itoa(segmentID) + ":" + strconv.Itoa(shardIndex))
	value, err := strconv.ParseUint(seed[:16], 16, 64)
	if err != nil {
		return 0
	}
	return int(value % uint64(minerCount))
}

func plannedShardSize(req wire.CreateIntentRequest, segmentID int) (int64, error) {
	if req.Erasure.ShardSize > 0 {
		return int64(req.Erasure.ShardSize), nil
	}
	if req.Erasure.DataShards <= 0 {
		return 0, errors.New("invalid erasure policy")
	}
	segmentSize := req.SegmentSize
	offset := int64(segmentID) * req.SegmentSize
	if remaining := req.FileSize - offset; remaining > 0 && remaining < segmentSize {
		segmentSize = remaining
	}
	if segmentSize <= 0 {
		return 0, errors.New("invalid segment size")
	}
	return int64((segmentSize + int64(req.Erasure.DataShards) - 1) / int64(req.Erasure.DataShards)), nil
}

func minerAvailableBytes(miner wire.MinerStats) uint64 {
	used := saturatingAdd(miner.UsedBytes, miner.ReservedBytes)
	if used >= miner.CapacityBytes {
		return 0
	}
	return miner.CapacityBytes - used
}
