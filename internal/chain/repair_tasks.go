package chain

import (
	"errors"
	"sort"
	"strconv"
	"time"

	"chain/internal/wire"
)

const repairStatusPending = "pending"
const repairStatusProofPending = "proof_pending"
const repairStatusCompleted = "completed"
const defaultRepairProofChallengeDuration = 10 * time.Minute

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

// findPoolForSegment returns the RepairPool containing the given segmentID
// and the index (0 or 1) of segmentID within that pool. Returns nil, -1 when
// the segment is not part of any pool.
func findPoolForSegment(pools []wire.RepairPool, segmentID int) (*wire.RepairPool, int) {
	for i := range pools {
		if pools[i].SegmentIDs[0] == segmentID {
			return &pools[i], 0
		}
		if pools[i].SegmentIDs[1] == segmentID {
			return &pools[i], 1
		}
	}
	return nil, -1
}

// crossParityReceiptsAvailable checks whether both the peer segment's shard
// and the cross-parity shard are available (have receipts from online miners).
func crossParityReceiptsAvailable(intent *Intent, peerSegID, paritySegID, shardIndex int, unavailable map[string]bool) bool {
	peerReceipts := intent.Receipts[peerSegID]
	peerReceipt, hasPeer := peerReceipts[shardIndex]
	if !hasPeer || unavailable[peerReceipt.MinerAddress] {
		return false
	}
	parityReceipts := intent.Receipts[paritySegID]
	parityReceipt, hasParity := parityReceipts[shardIndex]
	if !hasParity || unavailable[parityReceipt.MinerAddress] {
		return false
	}
	return true
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

	// Phase A: regular segment shards — prefer cross-parity when available.
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

			// Try cross-parity repair first (only needs 2 downloads).
			if pool, posInPool := findPoolForSegment(intent.RepairPools, segmentID); pool != nil {
				peerSegID := pool.SegmentIDs[1-posInPool]
				paritySegID := -(pool.PoolID + 1)
				if crossParityReceiptsAvailable(intent, peerSegID, paritySegID, shardIndex, unavailable) {
					task, err := s.buildCrossParityRepairTaskLocked(intent, pool, segmentID, peerSegID, paritySegID, shardIndex, oldMiner, reason)
					if err == nil {
						tasks = append(tasks, task)
						continue
					}
					// Fall through to RS repair on error.
				}
			}

			task, err := s.buildRepairTaskForShardLocked(intent, segmentID, shardIndex, oldMiner, reason, unavailable)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, task)
		}
	}

	// Phase B: cross-parity shard repair (negative segmentIDs).
	for _, pool := range intent.RepairPools {
		paritySegID := -(pool.PoolID + 1)
		parityReceipts := intent.Receipts[paritySegID]
		for shardIndex := 0; shardIndex < totalShards; shardIndex++ {
			receipt, hasReceipt := parityReceipts[shardIndex]
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
			if _, exists := s.pendingRepairTaskForShardLocked(intent.IntentID, paritySegID, shardIndex, ""); exists {
				continue
			}
			task, err := s.buildCrossParityRebuildTaskLocked(intent, &pool, paritySegID, shardIndex, oldMiner, reason, unavailable)
			if err != nil {
				continue // skip if insufficient sources; may recover later
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

// buildCrossParityRepairTaskLocked creates a repair task that recovers a lost
// segment shard using the cross-parity path: download the peer segment's shard
// and the cross-parity shard, then XOR them. This requires only 2 downloads
// instead of k (DataShards).
func (s *Store) buildCrossParityRepairTaskLocked(intent *Intent, pool *wire.RepairPool, segmentID, peerSegID, paritySegID, shardIndex int, oldMiner string, reason string) (wire.RepairTask, error) {
	totalShards := intent.Erasure.DataShards + intent.Erasure.ParityShards
	assignment, err := s.buildCrossParityAssignmentLocked(intent, pool, segmentID, shardIndex, oldMiner)
	if err != nil {
		return wire.RepairTask{}, err
	}
	// The rebuilt shard must match the original segment shard hash, not the
	// cross-parity hash. Override with the segment plan's hash.
	if segmentID >= 0 && segmentID < len(intent.Segments) && shardIndex >= 0 && shardIndex < len(intent.Segments[segmentID].ShardHashes) {
		assignment.ShardHash = intent.Segments[segmentID].ShardHashes[shardIndex]
	}
	sourceReceipts := crossParityRepairSources(intent, peerSegID, paritySegID, shardIndex, oldMiner)
	return wire.RepairTask{
		RepairID:            repairTaskID(intent.IntentID, segmentID, shardIndex, assignment.MinerAddress),
		IntentID:            intent.IntentID,
		SegmentID:           segmentID,
		ShardIndex:          shardIndex,
		OldMinerAddress:     oldMiner,
		Reason:              reason,
		Status:              repairStatusPending,
		AvailableShards:     availableShardCount(intent.Receipts[segmentID], nil),
		RequiredShards:      2,
		TargetShards:        totalShards,
		MissingShardIndexes: []int{shardIndex},
		Assignment:          assignment,
		SourceReceipts:      sourceReceipts,
		RepairMode:          "cross_parity",
		PoolID:              pool.PoolID,
		PeerSegmentID:       peerSegID,
	}, nil
}

// buildCrossParityRebuildTaskLocked creates a repair task that rebuilds a lost
// cross-parity shard from the two source segments' shards. The prover will
// download segA.shard[j] and segB.shard[j], then XOR them.
func (s *Store) buildCrossParityRebuildTaskLocked(intent *Intent, pool *wire.RepairPool, paritySegID, shardIndex int, oldMiner string, reason string, unavailable map[string]bool) (wire.RepairTask, error) {
	totalShards := intent.Erasure.DataShards + intent.Erasure.ParityShards
	assignment, err := s.buildCrossParityAssignmentLocked(intent, pool, paritySegID, shardIndex, oldMiner)
	if err != nil {
		return wire.RepairTask{}, err
	}
	// Collect one source receipt from each segment in the pool.
	var sourceReceipts []wire.MinerReceipt
	for _, segID := range pool.SegmentIDs {
		segReceipts := intent.Receipts[segID]
		if segReceipts == nil {
			continue
		}
		receipt, ok := segReceipts[shardIndex]
		if !ok || unavailable[receipt.MinerAddress] {
			continue
		}
		sourceReceipts = append(sourceReceipts, receipt)
	}
	if len(sourceReceipts) < 2 {
		return wire.RepairTask{}, errors.New("insufficient source receipts for cross-parity rebuild")
	}
	return wire.RepairTask{
		RepairID:            repairTaskID(intent.IntentID, paritySegID, shardIndex, assignment.MinerAddress),
		IntentID:            intent.IntentID,
		SegmentID:           paritySegID,
		ShardIndex:          shardIndex,
		OldMinerAddress:     oldMiner,
		Reason:              reason,
		Status:              repairStatusPending,
		AvailableShards:     availableShardCount(intent.Receipts[paritySegID], unavailable),
		RequiredShards:      2,
		TargetShards:        totalShards,
		MissingShardIndexes: []int{shardIndex},
		Assignment:          assignment,
		SourceReceipts:      sourceReceipts,
		RepairMode:          "cross_parity_rebuild",
		PoolID:              pool.PoolID,
	}, nil
}

// buildCrossParityAssignmentLocked assigns a new miner for a cross-parity
// repair or cross-parity rebuild task, using the pool's cross-parity shard
// size and hash.
func (s *Store) buildCrossParityAssignmentLocked(intent *Intent, pool *wire.RepairPool, segmentID, shardIndex int, oldMiner string) (wire.StorageAssignment, error) {
	miners := s.assignableMinersLocked()
	if len(miners) == 0 {
		return wire.StorageAssignment{}, errors.New("no active miner capacity for repair")
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
	miner, ok := chooseAssignmentMiner(miners, available, used, "repair:"+intent.FileRoot, segmentID, shardIndex, uint64(pool.CrossParity.ShardSize))
	if !ok {
		return wire.StorageAssignment{}, errors.New("insufficient active miner capacity for repair")
	}
	shardHash := ""
	if shardIndex >= 0 && shardIndex < len(pool.CrossParity.ShardHashes) {
		shardHash = pool.CrossParity.ShardHashes[shardIndex]
	}
	return wire.StorageAssignment{
		SegmentID:    segmentID,
		ShardIndex:   shardIndex,
		MinerAddress: miner.MinerAddress,
		Endpoint:     miner.Endpoint,
		ShardHash:    shardHash,
		ShardSize:    pool.CrossParity.ShardSize,
	}, nil
}

// crossParityRepairSources returns the two source receipts needed for a
// cross-parity repair: the peer segment's shard and the cross-parity shard.
func crossParityRepairSources(intent *Intent, peerSegID, paritySegID, shardIndex int, oldMiner string) []wire.MinerReceipt {
	var receipts []wire.MinerReceipt
	if peerReceipts := intent.Receipts[peerSegID]; peerReceipts != nil {
		if r, ok := peerReceipts[shardIndex]; ok {
			if oldMiner == "" || r.MinerAddress != oldMiner {
				receipts = append(receipts, r)
			}
		}
	}
	if parityReceipts := intent.Receipts[paritySegID]; parityReceipts != nil {
		if r, ok := parityReceipts[shardIndex]; ok {
			if oldMiner == "" || r.MinerAddress != oldMiner {
				receipts = append(receipts, r)
			}
		}
	}
	return receipts
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

func (s *Store) requireRepairProofLocked(task wire.RepairTask, receipt wire.MinerReceipt, now int64) (wire.StorageChallenge, error) {
	if task.RepairID == "" {
		return wire.StorageChallenge{}, errors.New("repair task id is required")
	}
	challengeID, err := randomID("repair_challenge")
	if err != nil {
		return wire.StorageChallenge{}, err
	}
	nonce, err := randomID("repair_nonce")
	if err != nil {
		return wire.StorageChallenge{}, err
	}
	challenge := s.repairProofChallengeFromReceiptLocked(task, receipt, challengeID, nonce, now)
	return s.applyRepairProofChallengeLocked(task, receipt, challenge)
}

func (s *Store) applyRepairProofChallengeLocked(task wire.RepairTask, receipt wire.MinerReceipt, challenge wire.StorageChallenge) (wire.StorageChallenge, error) {
	if task.RepairID == "" {
		return wire.StorageChallenge{}, errors.New("repair task id is required")
	}
	if challenge.RepairID != task.RepairID {
		return wire.StorageChallenge{}, errors.New("repair proof challenge id mismatch")
	}
	if challenge.ChallengeID == "" || challenge.Nonce == "" {
		return wire.StorageChallenge{}, errors.New("repair proof challenge missing id or nonce")
	}
	if challenge.MinerAddress != receipt.MinerAddress || challenge.MinerPublicKey != receipt.MinerPublicKey {
		return wire.StorageChallenge{}, errors.New("repair proof challenge miner mismatch")
	}
	if challenge.IntentID != task.IntentID || challenge.SegmentID != task.SegmentID || challenge.ShardIndex != task.ShardIndex {
		return wire.StorageChallenge{}, errors.New("repair proof challenge shard mismatch")
	}
	if challenge.Reward != 0 {
		return wire.StorageChallenge{}, errors.New("repair proof challenge must not pay storage proof reward")
	}
	if challenge.ChallengeHash != storageChallengeHash(challenge) {
		return wire.StorageChallenge{}, errors.New("repair proof challenge hash mismatch")
	}
	existing, ok := s.data.RepairTasks[task.RepairID]
	if !ok {
		existing = task
	}
	existing.Status = repairStatusProofPending
	existing.ProofChallengeID = challenge.ChallengeID
	existing.ProofVerified = false
	s.data.RepairTasks[task.RepairID] = existing
	s.data.Challenges[challenge.ChallengeID] = challenge
	return challenge, nil
}

func (s *Store) repairProofChallengeFromReceiptLocked(task wire.RepairTask, receipt wire.MinerReceipt, challengeID string, nonce string, now int64) wire.StorageChallenge {
	intent := s.data.Intents[task.IntentID]
	deadline := now + int64(defaultRepairProofChallengeDuration/time.Second)
	return s.storageChallengeForReceiptLocked(intent, receipt, "", task.RepairID, challengeID, nonce, deadline, 0)
}

func (s *Store) completeRepairTaskAfterProofLocked(repairID string, challengeID string) uint64 {
	if repairID == "" || challengeID == "" {
		return 0
	}
	existing, ok := s.data.RepairTasks[repairID]
	if !ok || existing.Status == repairStatusCompleted {
		return 0
	}
	if existing.ProofChallengeID != challengeID {
		return 0
	}
	intent, ok := s.data.Intents[existing.IntentID]
	if !ok {
		return 0
	}
	existing.Status = repairStatusCompleted
	existing.ProofVerified = true
	s.data.RepairTasks[repairID] = existing

	reward := s.computeRepairRewardLocked(existing.Assignment.ShardSize)
	s.payRepairRewardLocked(intent, existing.Assignment.MinerAddress, reward)
	stats := s.minerStatsLocked(existing.Assignment.MinerAddress)
	stats.RepairRewards = saturatingAdd(stats.RepairRewards, reward)
	stats.Rewards = saturatingAdd(stats.Rewards, reward)
	s.data.Miners[existing.Assignment.MinerAddress] = stats
	return reward
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

// computeRepairRewardLocked calculates the repair reward as one-tenth of the
// storage base price for the given shard size: shardSizeMiB × basePrice / 10.
func (s *Store) computeRepairRewardLocked(shardSize int64) uint64 {
	basePrice := s.data.StoragePricing.BasePrice
	if basePrice == 0 {
		basePrice = defaultStorageBasePrice
	}
	if shardSize <= 0 {
		return 0
	}
	const bytesPerMiB = 1024 * 1024
	shardSizeMiB := uint64(shardSize) / bytesPerMiB
	if shardSizeMiB == 0 {
		shardSizeMiB = 1 // minimum 1 MiB
	}
	return (shardSizeMiB*basePrice + 9) / 10
}

// pendingShardRepairKey builds the composite key for the PendingShardRepairs map.
func pendingShardRepairKey(intentID string, segmentID int, shardIndex int, minerAddress string) string {
	return intentID + ":" + strconv.Itoa(segmentID) + ":" + strconv.Itoa(shardIndex) + ":" + minerAddress
}

// trackMissedProofForRepairLocked records a missed proof for delayed repair
// tracking. It increments the consecutive miss counter for the shard and, once
// the counter reaches RepairDelayEpochs, promotes the pending entry to a full
// RepairTask. Returns the created task (if any) and whether one was created.
func (s *Store) trackMissedProofForRepairLocked(challenge wire.StorageChallenge, epochRound uint64) (wire.RepairTask, bool) {
	if challenge.IntentID == "" || challenge.MinerAddress == "" {
		return wire.RepairTask{}, false
	}

	key := pendingShardRepairKey(challenge.IntentID, challenge.SegmentID, challenge.ShardIndex, challenge.MinerAddress)
	delay := s.miningParamsLocked().RepairDelayEpochs
	if delay == 0 {
		delay = 1 // safety: 0 means no delay, create immediately
	}

	pending, exists := s.data.PendingShardRepairs[key]
	if !exists {
		pending = wire.PendingShardRepair{
			IntentID:              challenge.IntentID,
			SegmentID:             challenge.SegmentID,
			ShardIndex:            challenge.ShardIndex,
			MinerAddress:          challenge.MinerAddress,
			FirstMissedEpochRound: epochRound,
			ConsecutiveMisses:     1,
		}
	} else {
		pending.ConsecutiveMisses++
	}

	if pending.ConsecutiveMisses < delay {
		s.data.PendingShardRepairs[key] = pending
		return wire.RepairTask{}, false
	}

	// Threshold reached — promote to a real repair task.
	delete(s.data.PendingShardRepairs, key)

	task, ok := s.repairTaskForMissedChallengeLocked(challenge)
	if !ok {
		return wire.RepairTask{}, false
	}
	return task, true
}

// clearPendingShardRepairsForMinerLocked removes all pending shard repair
// tracking entries for the given miner and cancels any pending (not yet
// committed) repair tasks where the miner is the old miner. This is called
// when a miner successfully submits a proof, indicating they are back online.
func (s *Store) clearPendingShardRepairsForMinerLocked(minerAddress string) {
	// Remove pending tracking entries.
	for key, pending := range s.data.PendingShardRepairs {
		if pending.MinerAddress == minerAddress {
			delete(s.data.PendingShardRepairs, key)
		}
	}

	// Cancel pending repair tasks where this miner is the old (offline) miner.
	for repairID, task := range s.data.RepairTasks {
		if task.OldMinerAddress == minerAddress && task.Status == repairStatusPending {
			s.releaseStorageReservationLocked(task.Assignment)
			delete(s.data.RepairTasks, repairID)
		}
	}
}
