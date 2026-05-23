package chain

import (
	"errors"
	"sort"
	"strconv"
	"time"

	"chain/internal/wire"
)

const repairStatusPending = "pending"
const repairStatusCompleted = "completed"

type createRepairTasksTxPayload struct {
	Request       wire.CreateRepairRequest `json:"request"`
	Tasks         []wire.RepairTask        `json:"tasks"`
	CreatedAtUnix int64                    `json:"created_at_unix"`
}

func (s *Store) CreateRepairTasks(req wire.CreateRepairRequest) (wire.CreateRepairResponse, error) {
	if req.IntentID == "" {
		return wire.CreateRepairResponse{}, errors.New("intent is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tasks, err := s.buildRepairTasksLocked(req)
	if err != nil {
		return wire.CreateRepairResponse{}, err
	}
	if len(tasks) > 0 {
		if err := s.applyRepairTasksLocked(tasks); err != nil {
			return wire.CreateRepairResponse{}, err
		}
		s.recordTxLocked("create_repair_tasks", "", createRepairTasksTxPayload{
			Request:       req,
			Tasks:         tasks,
			CreatedAtUnix: time.Now().Unix(),
		})
	}
	if err := s.saveLocked(); err != nil {
		return wire.CreateRepairResponse{}, err
	}
	return wire.CreateRepairResponse{IntentID: req.IntentID, Tasks: tasks}, nil
}

func (s *Store) buildRepairTasksLocked(req wire.CreateRepairRequest) ([]wire.RepairTask, error) {
	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return nil, errors.New("intent not found")
	}
	if intent.Status == wire.StatusExpired {
		return nil, errors.New("intent is expired")
	}
	unavailable := map[string]bool{}
	for _, miner := range req.UnavailableMiners {
		if miner != "" {
			unavailable[miner] = true
		}
	}
	totalShards := intent.Erasure.DataShards + intent.Erasure.ParityShards
	if totalShards <= 0 {
		return nil, errors.New("invalid erasure policy")
	}
	tasks := make([]wire.RepairTask, 0)
	for segmentID := range intent.SegmentRoots {
		receipts := intent.Receipts[segmentID]
		for shardIndex := 0; shardIndex < totalShards; shardIndex++ {
			receipt, hasReceipt := receipts[shardIndex]
			reason := ""
			oldMiner := ""
			switch {
			case hasReceipt && unavailable[receipt.MinerAddress]:
				reason = "unavailable_miner"
				oldMiner = receipt.MinerAddress
			case !hasReceipt && req.IncludeMissing:
				reason = "missing_shard"
			default:
				continue
			}
			if _, exists := s.pendingRepairTaskForShardLocked(intent.IntentID, segmentID, shardIndex, ""); exists {
				continue
			}
			task, err := s.buildRepairTaskForShardLocked(intent, segmentID, shardIndex, oldMiner, reason, unavailable)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, task)
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].SegmentID != tasks[j].SegmentID {
			return tasks[i].SegmentID < tasks[j].SegmentID
		}
		return tasks[i].ShardIndex < tasks[j].ShardIndex
	})
	return tasks, nil
}

func (s *Store) buildRepairTaskForShardLocked(intent *Intent, segmentID int, shardIndex int, oldMiner string, reason string, unavailable map[string]bool) (wire.RepairTask, error) {
	totalShards := intent.Erasure.DataShards + intent.Erasure.ParityShards
	assignment, err := s.buildRepairAssignmentLocked(intent, segmentID, shardIndex, oldMiner)
	if err != nil {
		return wire.RepairTask{}, err
	}
	sourceReceipts := repairSourceReceipts(intent.Receipts[segmentID], shardIndex, oldMiner, unavailable)
	if len(sourceReceipts) < intent.Erasure.DataShards {
		return wire.RepairTask{}, errors.New("insufficient source receipts for repair")
	}
	return wire.RepairTask{
		RepairID:            repairTaskID(intent.IntentID, segmentID, shardIndex, assignment.MinerAddress),
		IntentID:            intent.IntentID,
		SegmentID:           segmentID,
		ShardIndex:          shardIndex,
		OldMinerAddress:     oldMiner,
		Reason:              reason,
		Status:              repairStatusPending,
		AvailableShards:     availableShardCount(intent.Receipts[segmentID], unavailable),
		RequiredShards:      intent.Erasure.DataShards,
		TargetShards:        totalShards,
		MissingShardIndexes: []int{shardIndex},
		Assignment:          assignment,
		SourceReceipts:      sourceReceipts,
	}, nil
}

func (s *Store) buildRepairAssignmentLocked(intent *Intent, segmentID int, shardIndex int, oldMiner string) (wire.StorageAssignment, error) {
	miners := s.assignableMinersLocked()
	if len(miners) == 0 {
		return wire.StorageAssignment{}, errors.New("no active miner capacity for repair")
	}
	shardSize, err := plannedShardSize(wire.CreateIntentRequest{
		FileSize:    intent.FileSize,
		SegmentSize: intent.SegmentSize,
		Erasure:     intent.Erasure,
	}, segmentID)
	if err != nil {
		return wire.StorageAssignment{}, err
	}
	used := map[string]bool{}
	if oldMiner != "" {
		used[oldMiner] = true
	}
	if receipts := intent.Receipts[segmentID]; receipts != nil {
		for index, receipt := range receipts {
			if index != shardIndex {
				used[receipt.MinerAddress] = true
			}
		}
	}
	available := make(map[string]uint64, len(miners))
	for _, miner := range miners {
		available[miner.MinerAddress] = minerAvailableBytes(miner)
	}
	if oldMiner != "" {
		available[oldMiner] = 0
	}
	miner, ok := chooseAssignmentMiner(miners, available, used, "repair:"+intent.FileRoot, segmentID, shardIndex, uint64(shardSize))
	if !ok {
		return wire.StorageAssignment{}, errors.New("insufficient active miner capacity for repair")
	}
	shardHash := ""
	if segmentID >= 0 && segmentID < len(intent.Segments) && shardIndex >= 0 && shardIndex < len(intent.Segments[segmentID].ShardHashes) {
		shardHash = intent.Segments[segmentID].ShardHashes[shardIndex]
	}
	return wire.StorageAssignment{
		SegmentID:    segmentID,
		ShardIndex:   shardIndex,
		MinerAddress: miner.MinerAddress,
		Endpoint:     miner.Endpoint,
		ShardHash:    shardHash,
		ShardSize:    shardSize,
	}, nil
}

func (s *Store) applyRepairTasksLocked(tasks []wire.RepairTask) error {
	for _, task := range tasks {
		if task.RepairID == "" {
			return errors.New("repair task id is required")
		}
		if existing, ok := s.data.RepairTasks[task.RepairID]; ok {
			if existing.Status != repairStatusPending {
				continue
			}
			continue
		}
		if task.Status == "" {
			task.Status = repairStatusPending
		}
		if task.Status != repairStatusPending {
			return errors.New("new repair task must be pending")
		}
		if _, ok := s.data.Intents[task.IntentID]; !ok {
			return errors.New("repair task intent not found")
		}
		if task.Assignment.MinerAddress == "" || task.Assignment.ShardSize <= 0 {
			return errors.New("repair task assignment is invalid")
		}
		s.reserveStorageAssignmentsLocked([]wire.StorageAssignment{task.Assignment})
		s.data.RepairTasks[task.RepairID] = task
	}
	return nil
}

func (s *Store) pendingRepairTaskForShardLocked(intentID string, segmentID int, shardIndex int, minerAddress string) (wire.RepairTask, bool) {
	for _, task := range s.data.RepairTasks {
		if task.IntentID != intentID || task.Status != repairStatusPending {
			continue
		}
		if task.SegmentID != segmentID || task.ShardIndex != shardIndex {
			continue
		}
		if minerAddress != "" && task.Assignment.MinerAddress != minerAddress {
			continue
		}
		return task, true
	}
	return wire.RepairTask{}, false
}

func (s *Store) completeRepairTaskLocked(task wire.RepairTask) {
	if task.RepairID == "" {
		return
	}
	existing, ok := s.data.RepairTasks[task.RepairID]
	if !ok || existing.Status == repairStatusCompleted {
		return
	}
	existing.Status = repairStatusCompleted
	s.data.RepairTasks[task.RepairID] = existing

	intent, ok := s.data.Intents[existing.IntentID]
	if !ok {
		return
	}
	reward := s.miningParamsLocked().RepairRewardPerShard
	s.payRepairRewardLocked(intent, existing.Assignment.MinerAddress, reward)
	stats := s.minerStatsLocked(existing.Assignment.MinerAddress)
	stats.RepairRewards = saturatingAdd(stats.RepairRewards, reward)
	stats.Rewards = saturatingAdd(stats.Rewards, reward)
	s.data.Miners[existing.Assignment.MinerAddress] = stats
}

func repairTaskID(intentID string, segmentID int, shardIndex int, minerAddress string) string {
	return hashString("repair:" + intentID + ":" + strconv.Itoa(segmentID) + ":" + strconv.Itoa(shardIndex) + ":" + minerAddress)
}

func repairShardKey(segmentID int, shardIndex int) string {
	return strconv.Itoa(segmentID) + ":" + strconv.Itoa(shardIndex)
}

func availableShardCount(receipts map[int]wire.MinerReceipt, unavailable map[string]bool) int {
	count := 0
	for _, receipt := range receipts {
		if unavailable[receipt.MinerAddress] {
			continue
		}
		count++
	}
	return count
}

func repairSourceReceipts(receipts map[int]wire.MinerReceipt, shardIndex int, oldMiner string, unavailable map[string]bool) []wire.MinerReceipt {
	indexes := make([]int, 0, len(receipts))
	for index := range receipts {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	out := make([]wire.MinerReceipt, 0, len(indexes))
	for _, index := range indexes {
		receipt := receipts[index]
		if index == shardIndex {
			continue
		}
		if oldMiner != "" && receipt.MinerAddress == oldMiner {
			continue
		}
		if unavailable[receipt.MinerAddress] {
			continue
		}
		out = append(out, receipt)
	}
	return out
}
