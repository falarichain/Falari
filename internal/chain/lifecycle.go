package chain

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"chain/internal/wire"
)

type terminateDealTxPayload struct {
	Request  wire.TerminateDealRequest  `json:"request"`
	Response wire.TerminateDealResponse `json:"response"`
}

type setAccessPolicyTxPayload struct {
	Request  wire.SetAccessPolicyRequest  `json:"request"`
	Response wire.SetAccessPolicyResponse `json:"response"`
}

type governanceDealActionTxPayload struct {
	Request  wire.GovernanceDealActionRequest  `json:"request"`
	Response wire.GovernanceDealActionResponse `json:"response"`
}

type committeeFreezeDealTxPayload struct {
	Request  wire.CommitteeFreezeDealRequest   `json:"request"`
	Response wire.GovernanceDealActionResponse `json:"response"`
}

type governanceBlockDealTxPayload struct {
	Request  wire.GovernanceBlockDealRequest   `json:"request"`
	Response wire.GovernanceDealActionResponse `json:"response"`
}

type submitDeleteReceiptTxPayload struct {
	Request  wire.SubmitDeleteReceiptRequest  `json:"request"`
	Response wire.SubmitDeleteReceiptResponse `json:"response"`
}

type directGovernanceActionTxPayload struct {
	Request  wire.DirectGovernanceActionRequest  `json:"request"`
	Response wire.DirectGovernanceActionResponse `json:"response"`
}

type directActionReviewVoteTxPayload struct {
	Request  wire.DirectActionReviewVoteRequest  `json:"request"`
	Response wire.DirectActionReviewVoteResponse `json:"response"`
}

func defaultAccessStatus(intent wire.IntentView) string {
	if intent.AccessStatus != "" {
		return intent.AccessStatus
	}
	if intent.Encryption != nil {
		return wire.AccessStatusPrivate
	}
	return wire.AccessStatusPublic
}

func normalizeIntentLifecycle(intent *Intent) {
	normalizeIntentLifecycleAt(intent, time.Now().Unix())
}

func normalizeIntentLifecycleAt(intent *Intent, now int64) {
	if intent == nil {
		return
	}
	if intent.StorageStatus == "" {
		switch intent.Status {
		case wire.StatusFinalized:
			intent.StorageStatus = wire.StorageStatusActive
		case wire.StatusExpired:
			intent.StorageStatus = wire.StorageStatusExpired
		case wire.StatusDeleted:
			intent.StorageStatus = wire.StorageStatusDeleted
		default:
			intent.StorageStatus = wire.StorageStatusPending
		}
	}
	if intent.AccessStatus == "" {
		intent.AccessStatus = defaultAccessStatus(intent.IntentView)
	}
	if intent.ModerationStatus == "" {
		intent.ModerationStatus = wire.ModerationStatusNone
	}
	if intent.ExpiresAtUnix == 0 && intent.Status == wire.StatusFinalized && intent.Policy.Duration > 0 && intent.UpdatedAt > 0 {
		intent.ExpiresAtUnix = intent.UpdatedAt + intent.Policy.Duration
	}
	expireGovernanceAction(intent, now)
}

func expireGovernanceAction(intent *Intent, now int64) {
	if intent == nil {
		return
	}
	if intent.ModerationStatus != wire.ModerationStatusFrozen || intent.ModerationExpiresAtUnix == 0 {
		return
	}
	if now < intent.ModerationExpiresAtUnix {
		return
	}
	intent.ModerationStatus = wire.ModerationStatusNone
	intent.ModerationExpiresAtUnix = 0
	intent.BlockedReasonHash = ""
	if intent.StorageStatus == wire.StorageStatusActive {
		defaultView := intent.IntentView
		defaultView.AccessStatus = ""
		intent.AccessStatus = defaultAccessStatus(defaultView)
	}
}

func (s *Store) TerminateDeal(req wire.TerminateDealRequest) (wire.TerminateDealResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if requestUsesAgent(req.AgentKeyID) {
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, req.User, "terminate_deal", 0, func(agentPub string) error {
			return wire.VerifyTerminateDealAgent(req, agentPub)
		}); err != nil {
			return wire.TerminateDealResponse{}, err
		}
	} else {
		if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
			return wire.VerifyTerminateDeal(req)
		}); err != nil {
			return wire.TerminateDealResponse{}, err
		}
	}

	resp, err := s.terminateDealLocked(req, time.Now().Unix())
	if err != nil {
		return wire.TerminateDealResponse{}, err
	}

	if requestUsesAgent(req.AgentKeyID) {
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, 0); err != nil {
			return wire.TerminateDealResponse{}, err
		}
	} else {
		s.consumeAccountNonceLocked(req.User)
	}

	s.recordTxLocked("terminate_deal", req.User, terminateDealTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.TerminateDealResponse{}, err
	}
	return resp, nil
}

func (s *Store) terminateDealLocked(req wire.TerminateDealRequest, now int64) (wire.TerminateDealResponse, error) {
	if req.IntentID == "" {
		return wire.TerminateDealResponse{}, errors.New("intent is required")
	}
	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.TerminateDealResponse{}, errors.New("intent not found")
	}
	normalizeIntentLifecycleAt(intent, now)
	if req.User != "" && req.User != intent.User {
		return wire.TerminateDealResponse{}, errors.New("intent user mismatch")
	}
	if intent.StorageStatus == wire.StorageStatusDeleted {
		return wire.TerminateDealResponse{}, errors.New("intent already deleted")
	}
	intent.StorageStatus = wire.StorageStatusTerminating
	intent.AccessStatus = wire.AccessStatusBlocked
	intent.TerminatedAtUnix = now
	intent.UpdatedAt = now
	s.releaseUncommittedStorageReservationsLocked(intent)
	// Early termination: remaining locked tokens are burned (sent to black-hole
	// address) rather than refunded to the user.
	var burned uint64
	if isPermanentIntent(intent) {
		burned = s.closePermanentFundLocked(intent, "terminate_deal", now)
	} else {
		burn := remainingIntentEscrow(intent)
		if burn > 0 {
			user := s.accountLocked(intent.User)
			if user.LockedStorage < burn {
				burn = user.LockedStorage
			}
			if burn > 0 {
				user.LockedStorage -= burn
				s.data.Accounts[user.Address] = user
				s.recordStorageFeeBurnLocked(intent, burn)
				burned = burn
			}
		}
	}
	var tasks []wire.DeleteTask
	if intent.Policy.DeletionPolicy != wire.DeletionPolicyRetain {
		tasks = s.ensureDeleteTasksLocked(intent, firstNonEmpty(req.Reason, "terminate_deal"), now)
	}
	return wire.TerminateDealResponse{
		IntentID:         intent.IntentID,
		Status:           intent.Status,
		StorageStatus:    intent.StorageStatus,
		AccessStatus:     intent.AccessStatus,
		BurnedFee:        burned,
		DeleteTasks:      tasks,
		TerminatedAtUnix: now,
	}, nil
}

func (s *Store) SetAccessPolicy(req wire.SetAccessPolicyRequest) (wire.SetAccessPolicyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
		return wire.VerifySetAccessPolicy(req)
	}); err != nil {
		return wire.SetAccessPolicyResponse{}, err
	}
	resp, err := s.setAccessPolicyLocked(req, time.Now().Unix())
	if err != nil {
		return wire.SetAccessPolicyResponse{}, err
	}
	s.consumeAccountNonceLocked(req.User)
	s.recordTxLocked("set_access_policy", req.User, setAccessPolicyTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.SetAccessPolicyResponse{}, err
	}
	return resp, nil
}

func (s *Store) setAccessPolicyLocked(req wire.SetAccessPolicyRequest, now int64) (wire.SetAccessPolicyResponse, error) {
	if req.IntentID == "" {
		return wire.SetAccessPolicyResponse{}, errors.New("intent is required")
	}
	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.SetAccessPolicyResponse{}, errors.New("intent not found")
	}
	normalizeIntentLifecycleAt(intent, now)
	if req.User != "" && req.User != intent.User {
		return wire.SetAccessPolicyResponse{}, errors.New("intent user mismatch")
	}
	if !validAccessStatus(req.AccessStatus) {
		return wire.SetAccessPolicyResponse{}, errors.New("invalid access status")
	}
	intent.AccessStatus = req.AccessStatus
	if req.ReasonHash != "" {
		intent.BlockedReasonHash = req.ReasonHash
	}
	intent.AccessUpdatedAtUnix = now
	intent.UpdatedAt = now
	return wire.SetAccessPolicyResponse{
		IntentID:         intent.IntentID,
		AccessStatus:     intent.AccessStatus,
		ModerationStatus: intent.ModerationStatus,
		UpdatedAtUnix:    now,
	}, nil
}

func (s *Store) governanceDealActionLocked(req wire.GovernanceDealActionRequest, now int64) (wire.GovernanceDealActionResponse, error) {
	if req.IntentID == "" {
		return wire.GovernanceDealActionResponse{}, errors.New("intent is required")
	}
	if req.ReasonHash == "" {
		return wire.GovernanceDealActionResponse{}, errors.New("reason hash is required")
	}
	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.GovernanceDealActionResponse{}, errors.New("intent not found")
	}
	normalizeIntentLifecycleAt(intent, now)
	switch req.Action {
	case "freeze":
		if req.ExpiresAtUnix <= now {
			return wire.GovernanceDealActionResponse{}, errors.New("freeze action requires a future expires_at_unix")
		}
		intent.AccessStatus = wire.AccessStatusSuspended
		intent.ModerationStatus = wire.ModerationStatusFrozen
		intent.ModerationExpiresAtUnix = req.ExpiresAtUnix
		intent.AppealDeadlineUnix = 0
		s.addBlacklistForIntent(intent, req.ReasonHash, req.Operator, "freeze", req.ExpiresAtUnix, now)
	case "block":
		if req.AppealDeadlineUnix > 0 && req.AppealDeadlineUnix <= now {
			return wire.GovernanceDealActionResponse{}, errors.New("block action requires appeal_deadline_unix to be in the future")
		}
		intent.AccessStatus = wire.AccessStatusBlocked
		intent.ModerationStatus = wire.ModerationStatusBlocked
		intent.ModerationExpiresAtUnix = 0
		intent.AppealDeadlineUnix = req.AppealDeadlineUnix
		s.addBlacklistForIntent(intent, req.ReasonHash, req.Operator, "block", 0, now)
		if !req.PreserveStorage {
			intent.StorageStatus = wire.StorageStatusTerminating
			intent.TerminatedAtUnix = now
			s.closePermanentFundLocked(intent, "governance_block", now)
			s.ensureDeleteTasksLocked(intent, "governance_block", now)
		}
	case "legal_hold":
		intent.AccessStatus = wire.AccessStatusBlocked
		intent.ModerationStatus = wire.ModerationStatusLegalHold
		intent.StorageStatus = wire.StorageStatusActive
		intent.ModerationExpiresAtUnix = 0
		intent.AppealDeadlineUnix = 0
		s.addBlacklistForIntent(intent, req.ReasonHash, req.Operator, "legal_hold", 0, now)
	case "appeal":
		intent.ModerationStatus = wire.ModerationStatusAppealed
		intent.ModerationExpiresAtUnix = 0
		s.removeBlacklistForIntent(intent)
	default:
		return wire.GovernanceDealActionResponse{}, errors.New("invalid governance action")
	}
	intent.BlockedReasonHash = req.ReasonHash
	intent.AccessUpdatedAtUnix = now
	intent.UpdatedAt = now
	resp := wire.GovernanceDealActionResponse{
		IntentID:                intent.IntentID,
		GovernanceType:          governanceTypeForAction(req.Action),
		AccessStatus:            intent.AccessStatus,
		ModerationStatus:        intent.ModerationStatus,
		StorageStatus:           intent.StorageStatus,
		BlockedReasonHash:       intent.BlockedReasonHash,
		ModerationExpiresAtUnix: intent.ModerationExpiresAtUnix,
		AppealDeadlineUnix:      intent.AppealDeadlineUnix,
		UpdatedAtUnix:           now,
	}
	s.appendGovernanceAuditLocked(req, resp, now)
	return resp, nil
}

// addBlacklistForIntent adds blacklist entries for all shard hashes of an intent.
func (s *Store) addBlacklistForIntent(intent *Intent, reasonHash, operator, reason string, expiresAtUnix, now int64) {
	if s.data.BlacklistedShards == nil {
		s.data.BlacklistedShards = make(map[string]wire.BlacklistEntry)
	}
	for _, hash := range intentShardHashes(intent) {
		s.data.BlacklistedShards[hash] = wire.BlacklistEntry{
			ShardHash:     hash,
			IntentID:      intent.IntentID,
			Reason:        reason,
			ReasonHash:    reasonHash,
			Operator:      operator,
			BlockedAtUnix: now,
			ExpiresAtUnix: expiresAtUnix,
		}
	}
}

// removeBlacklistForIntent removes all blacklist entries for an intent's shards.
func (s *Store) removeBlacklistForIntent(intent *Intent) {
	for _, hash := range intentShardHashes(intent) {
		delete(s.data.BlacklistedShards, hash)
	}
}

// intentShardHashes extracts all shard hashes from an intent's segments.
func intentShardHashes(intent *Intent) []string {
	var hashes []string
	for _, seg := range intent.Segments {
		hashes = append(hashes, seg.ShardHashes...)
	}
	return hashes
}

// Blacklist returns all current blacklist entries, automatically cleaning up
// expired entries.
func (s *Store) Blacklist() wire.BlacklistResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	entries := make([]wire.BlacklistEntry, 0, len(s.data.BlacklistedShards))
	for hash, e := range s.data.BlacklistedShards {
		// Skip and clean up expired entries.
		if e.ExpiresAtUnix > 0 && e.ExpiresAtUnix < now {
			delete(s.data.BlacklistedShards, hash)
			continue
		}
		entries = append(entries, e)
	}
	var height uint64
	if len(s.data.Blocks) > 0 {
		height = s.data.Blocks[len(s.data.Blocks)-1].Height
	}
	return wire.BlacklistResponse{
		Entries:       entries,
		CurrentHeight: height,
	}
}

// IsShardBlacklisted checks whether a shard hash is currently blacklisted.
// Returns false for entries that have passed their expiration time.
func (s *Store) IsShardBlacklisted(shardHash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.data.BlacklistedShards[shardHash]
	if !ok {
		return false
	}
	// Permanent entries (ExpiresAtUnix == 0) are always blocked.
	// Temporary entries are blocked only until ExpiresAtUnix.
	if entry.ExpiresAtUnix > 0 && entry.ExpiresAtUnix < time.Now().Unix() {
		return false
	}
	return true
}

func (s *Store) SubmitDeleteReceipt(req wire.SubmitDeleteReceiptRequest) (wire.SubmitDeleteReceiptResponse, error) {
	if err := wire.VerifyDeleteReceipt(req.Receipt); err != nil {
		return wire.SubmitDeleteReceiptResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resp, err := s.submitDeleteReceiptLocked(req)
	if err != nil {
		return wire.SubmitDeleteReceiptResponse{}, err
	}
	s.recordTxLocked("submit_delete_receipt", req.Receipt.MinerAddress, submitDeleteReceiptTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.SubmitDeleteReceiptResponse{}, err
	}
	return resp, nil
}

func (s *Store) DeleteTasks(intentID string, minerAddress string, status string) wire.DeleteTaskResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	tasks := make([]wire.DeleteTask, 0, len(s.data.DeleteTasks))
	for _, task := range s.data.DeleteTasks {
		if intentID != "" && task.IntentID != intentID {
			continue
		}
		if minerAddress != "" && task.MinerAddress != minerAddress {
			continue
		}
		if status != "" && task.Status != status {
			continue
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].CreatedAtUnix != tasks[j].CreatedAtUnix {
			return tasks[i].CreatedAtUnix > tasks[j].CreatedAtUnix
		}
		return tasks[i].TaskID < tasks[j].TaskID
	})
	return wire.DeleteTaskResponse{Tasks: tasks}
}

func (s *Store) GovernanceAudit(intentID string, operator string, action string) wire.GovernanceAuditResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	records := make([]wire.GovernanceAuditRecord, 0, len(s.data.GovernanceAudits))
	for _, record := range s.data.GovernanceAudits {
		if intentID != "" && record.IntentID != intentID {
			continue
		}
		if operator != "" && record.Operator != operator {
			continue
		}
		if action != "" && record.Action != action {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].RecordedAtUnix != records[j].RecordedAtUnix {
			return records[i].RecordedAtUnix > records[j].RecordedAtUnix
		}
		return records[i].AuditID < records[j].AuditID
	})
	return wire.GovernanceAuditResponse{Records: records}
}

func (s *Store) submitDeleteReceiptLocked(req wire.SubmitDeleteReceiptRequest) (wire.SubmitDeleteReceiptResponse, error) {
	receipt := req.Receipt
	receiptTime := receipt.DeletedAtUnix
	if receiptTime <= 0 {
		return wire.SubmitDeleteReceiptResponse{}, errors.New("delete receipt timestamp is required")
	}
	intent, ok := s.data.Intents[receipt.IntentID]
	if !ok {
		return wire.SubmitDeleteReceiptResponse{}, errors.New("intent not found")
	}
	normalizeIntentLifecycleAt(intent, receiptTime)
	if intent.StorageStatus != wire.StorageStatusTerminating && intent.StorageStatus != wire.StorageStatusDeleted {
		return wire.SubmitDeleteReceiptResponse{}, errors.New("intent is not terminating")
	}
	if _, err := s.registeredMinerLocked(receipt.MinerAddress, receipt.MinerPublicKey); err != nil {
		return wire.SubmitDeleteReceiptResponse{}, err
	}
	original, ok := intentReceiptForShard(intent, receipt.ShardHash, receipt.MinerAddress)
	if !ok {
		return wire.SubmitDeleteReceiptResponse{}, errors.New("receipt shard is not assigned to miner")
	}
	taskID, task, ok := s.pendingDeleteTaskLocked(receipt.IntentID, receipt.ShardHash, receipt.MinerAddress)
	if !ok {
		return wire.SubmitDeleteReceiptResponse{}, errors.New("no pending delete task for shard")
	}
	key := deleteReceiptKey(receipt.IntentID, receipt.ShardHash, receipt.MinerAddress)
	if _, exists := s.data.DeleteReceipts[key]; !exists {
		s.data.DeleteReceipts[key] = receipt
		miner := s.minerStatsLocked(receipt.MinerAddress)
		size := uint64(original.ShardSize)
		if miner.UsedBytes < size {
			miner.UsedBytes = 0
		} else {
			miner.UsedBytes -= size
		}
		s.data.Miners[receipt.MinerAddress] = miner
		intent.DeleteReceiptCount++
		task.Status = deleteTaskStatusCompleted
		task.CompletedAtUnix = receipt.DeletedAtUnix
		s.data.DeleteTasks[taskID] = task
	}
	if intent.DeleteTaskCount > 0 && intent.DeleteReceiptCount >= intent.DeleteTaskCount {
		intent.StorageStatus = wire.StorageStatusDeleted
		intent.Status = wire.StatusDeleted
	}
	intent.UpdatedAt = receiptTime
	return wire.SubmitDeleteReceiptResponse{
		IntentID:           receipt.IntentID,
		ShardHash:          receipt.ShardHash,
		MinerAddress:       receipt.MinerAddress,
		DeleteReceiptCount: intent.DeleteReceiptCount,
		Status:             intent.StorageStatus,
	}, nil
}

const (
	deleteTaskStatusPending   = "pending"
	deleteTaskStatusCompleted = "completed"
)

func (s *Store) ensureDeleteTasksLocked(intent *Intent, reason string, now int64) []wire.DeleteTask {
	if intent == nil {
		return nil
	}
	tasks := make([]wire.DeleteTask, 0)
	for _, receipt := range flattenReceipts(intent) {
		taskID := deleteTaskID(intent.IntentID, receipt.ShardHash, receipt.MinerAddress)
		task, exists := s.data.DeleteTasks[taskID]
		if !exists {
			activeReferences := s.activeShardReferencesLocked(intent.IntentID, receipt.ShardHash, receipt.MinerAddress, now)
			task = wire.DeleteTask{
				TaskID:           taskID,
				IntentID:         intent.IntentID,
				ShardHash:        receipt.ShardHash,
				MinerAddress:     receipt.MinerAddress,
				MinerPublicKey:   receipt.MinerPublicKey,
				Status:           deleteTaskStatusPending,
				Reason:           reason,
				RetainPhysical:   activeReferences > 0,
				ActiveReferences: activeReferences,
				CreatedAtUnix:    now,
			}
		} else {
			activeReferences := s.activeShardReferencesLocked(intent.IntentID, receipt.ShardHash, receipt.MinerAddress, now)
			task.RetainPhysical = activeReferences > 0
			task.ActiveReferences = activeReferences
			if task.Reason == "" {
				task.Reason = reason
			}
			if task.CreatedAtUnix == 0 {
				task.CreatedAtUnix = now
			}
		}
		s.data.DeleteTasks[taskID] = task
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].MinerAddress != tasks[j].MinerAddress {
			return tasks[i].MinerAddress < tasks[j].MinerAddress
		}
		return tasks[i].ShardHash < tasks[j].ShardHash
	})
	intent.DeleteTaskCount = len(tasks)
	if intent.DeleteTaskCount == 0 {
		intent.StorageStatus = wire.StorageStatusDeleted
		intent.Status = wire.StatusDeleted
	}
	return tasks
}

func (s *Store) activeShardReferencesLocked(excludingIntentID string, shardHash string, minerAddress string, now int64) int {
	if shardHash == "" || minerAddress == "" {
		return 0
	}
	count := 0
	for _, candidate := range s.data.Intents {
		if candidate == nil || candidate.IntentID == excludingIntentID {
			continue
		}
		normalizeIntentLifecycleAt(candidate, now)
		if candidate.Status != wire.StatusFinalized {
			continue
		}
		if candidate.StorageStatus != wire.StorageStatusActive {
			continue
		}
		if candidate.AccessStatus == wire.AccessStatusBlocked {
			continue
		}
		for _, byShard := range candidate.Receipts {
			for _, receipt := range byShard {
				if receipt.ShardHash == shardHash && receipt.MinerAddress == minerAddress {
					count++
				}
			}
		}
	}
	return count
}

func (s *Store) pendingDeleteTaskLocked(intentID string, shardHash string, minerAddress string) (string, wire.DeleteTask, bool) {
	taskID := deleteTaskID(intentID, shardHash, minerAddress)
	task, ok := s.data.DeleteTasks[taskID]
	if !ok || task.Status != deleteTaskStatusPending {
		return "", wire.DeleteTask{}, false
	}
	return taskID, task, true
}

func (s *Store) appendGovernanceAuditLocked(req wire.GovernanceDealActionRequest, resp wire.GovernanceDealActionResponse, now int64) {
	record := wire.GovernanceAuditRecord{
		AuditID:                 governanceAuditID(req, now),
		IntentID:                req.IntentID,
		Operator:                req.Operator,
		Action:                  req.Action,
		GovernanceType:          resp.GovernanceType,
		ReasonHash:              req.ReasonHash,
		PreserveStorage:         req.PreserveStorage,
		AccessStatus:            resp.AccessStatus,
		ModerationStatus:        resp.ModerationStatus,
		StorageStatus:           resp.StorageStatus,
		ModerationExpiresAtUnix: resp.ModerationExpiresAtUnix,
		AppealDeadlineUnix:      resp.AppealDeadlineUnix,
		RecordedAtUnix:          now,
	}
	s.data.GovernanceAudits = append(s.data.GovernanceAudits, record)
}

func validAccessStatus(status string) bool {
	switch status {
	case wire.AccessStatusPublic, wire.AccessStatusPrivate, wire.AccessStatusSuspended, wire.AccessStatusBlocked:
		return true
	default:
		return false
	}
}

func (s *Store) validateGovernanceOperatorLocked(operator string, action string) error {
	operator = normalizeGovernanceOperator(operator)
	if operator == "" {
		return errors.New("governance operator is required")
	}
	if len(s.data.GovernanceOperators) == 0 {
		return nil
	}
	record, ok := s.data.GovernanceOperators[operator]
	if !ok || !record.Enabled {
		return errors.New("governance operator is not authorized")
	}
	// Operator management and config actions are allowed by any enabled operator (BFT voting ensures consensus).
	if isOperatorManagementAction(action) || isConfigAction(action) {
		return nil
	}
	if len(record.Permissions) == 0 {
		return nil
	}
	for _, permission := range record.Permissions {
		if permission == action || permission == governanceTypeForAction(action) || permission == "all" {
			return nil
		}
	}
	return errors.New("governance operator lacks permission: " + action)
}

func normalizeGovernanceOperator(operator string) string {
	trimmed := strings.TrimSpace(operator)
	if trimmed == "" {
		return ""
	}
	normalized := wire.NormalizeAddress(trimmed)
	if normalized != "" {
		return normalized
	}
	return trimmed
}

func intentReceiptForShard(intent *Intent, shardHash string, minerAddress string) (wire.MinerReceipt, bool) {
	for _, receipts := range intent.Receipts {
		for _, receipt := range receipts {
			if receipt.ShardHash == shardHash && (minerAddress == "" || receipt.MinerAddress == minerAddress) {
				return receipt, true
			}
		}
	}
	return wire.MinerReceipt{}, false
}

func deleteReceiptKey(intentID string, shardHash string, minerAddress string) string {
	return hashString("delete:" + intentID + ":" + shardHash + ":" + minerAddress)
}

func deleteTaskID(intentID string, shardHash string, minerAddress string) string {
	return hashString("delete-task:" + intentID + ":" + shardHash + ":" + minerAddress)
}

func governanceAuditID(req wire.GovernanceDealActionRequest, now int64) string {
	return hashString("governance-audit:" + req.IntentID + ":" + req.Operator + ":" + req.Action + ":" + req.ReasonHash + ":" + strconv.FormatInt(now, 10))
}

func governanceTypeForAction(action string) string {
	switch action {
	case "freeze":
		return "committee_freeze_deal"
	case "block":
		return "governance_block_deal"
	case "legal_hold":
		return "legal_hold"
	case "appeal":
		return "appeal"
	default:
		return "governance_deal_action"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func intentAllowsProviderDiscovery(intent *Intent) bool {
	if intent == nil {
		return false
	}
	normalizeIntentLifecycle(intent)
	if intent.StorageStatus == wire.StorageStatusDeleted || intent.StorageStatus == wire.StorageStatusTerminating || intent.StorageStatus == wire.StorageStatusExpired {
		return false
	}
	if intent.AccessStatus == wire.AccessStatusBlocked || intent.AccessStatus == wire.AccessStatusSuspended {
		return false
	}
	return true
}

func intentAllowsStorageProof(intent *Intent) bool {
	if intent == nil {
		return false
	}
	normalizeIntentLifecycle(intent)
	if intent.Status != wire.StatusFinalized {
		return false
	}
	return intent.StorageStatus == wire.StorageStatusActive
}

// ── Direct Governance Actions (Execute First, Review Later) ──

// DirectGovernanceAction allows an enabled operator to directly execute a data moderation
// action (freeze/block/legal_hold) without going through the full proposal→vote→execute cycle.
// The action takes effect immediately but is subject to a post-execution committee review window.
// During the review window, data is NOT deleted and permanent fund is NOT closed —
// these irreversible actions only happen upon ratification.
func (s *Store) DirectGovernanceAction(req wire.DirectGovernanceActionRequest) (wire.DirectGovernanceActionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operator := normalizeGovernanceOperator(req.Operator)
	if operator == "" {
		return wire.DirectGovernanceActionResponse{}, errors.New("operator is required")
	}
	if req.ChainID != s.data.ChainID {
		return wire.DirectGovernanceActionResponse{}, errors.New("direct action chain_id mismatch")
	}

	// Verify operator identity and permissions.
	op, ok := s.data.GovernanceOperators[operator]
	if !ok || !op.Enabled {
		return wire.DirectGovernanceActionResponse{}, errors.New("operator is not an enabled governance operator")
	}
	if op.PublicKey == "" {
		return wire.DirectGovernanceActionResponse{}, errors.New("operator has no public key registered")
	}
	if err := s.validateGovernanceOperatorLocked(operator, req.Action); err != nil {
		return wire.DirectGovernanceActionResponse{}, err
	}

	usesAgent := requestUsesAgent(req.AgentKeyID)
	if usesAgent {
		// Agent key authentication path.
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, operator, "direct_governance_action", 0, func(agentPub string) error {
			return wire.VerifyDirectGovernanceActionAgent(req, agentPub)
		}); err != nil {
			return wire.DirectGovernanceActionResponse{}, err
		}
	} else {
		// Operator direct signature verification.
		if err := wire.VerifyDirectGovernanceAction(req, operator); err != nil {
			return wire.DirectGovernanceActionResponse{}, err
		}
		// Verify operator nonce.
		expectedNonce := s.data.OperatorNonces[operator]
		if req.Nonce != expectedNonce {
			return wire.DirectGovernanceActionResponse{}, errors.New("invalid operator nonce")
		}
	}

	// Validate clock skew.
	now := time.Now().Unix()
	if abs64(req.CreatedAtUnix-now) > governanceClockSkewSeconds {
		return wire.DirectGovernanceActionResponse{}, errors.New("direct action timestamp outside acceptable clock skew")
	}

	// Only allow data moderation actions for direct execution.
	switch req.Action {
	case "freeze", "block", "legal_hold":
		// allowed
	default:
		return wire.DirectGovernanceActionResponse{}, errors.New("direct action only supports freeze, block, legal_hold")
	}

	// Validate action-specific fields.
	if req.Action == "freeze" && req.ExpiresAtUnix <= now {
		return wire.DirectGovernanceActionResponse{}, errors.New("freeze action requires a future expires_at_unix")
	}
	if req.Action == "block" && req.AppealDeadlineUnix > 0 && req.AppealDeadlineUnix <= now {
		return wire.DirectGovernanceActionResponse{}, errors.New("block action requires appeal_deadline_unix to be in the future")
	}
	if req.IntentID == "" {
		return wire.DirectGovernanceActionResponse{}, errors.New("intent_id is required")
	}
	if req.ReasonHash == "" {
		return wire.DirectGovernanceActionResponse{}, errors.New("reason_hash is required")
	}

	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.DirectGovernanceActionResponse{}, errors.New("intent not found")
	}
	normalizeIntentLifecycleAt(intent, now)

	// Snapshot pre-action state for rollback on rejection.
	preAccess := intent.AccessStatus
	preModeration := intent.ModerationStatus
	preStorage := intent.StorageStatus
	preExpires := intent.ModerationExpiresAtUnix
	preAppealDeadline := intent.AppealDeadlineUnix

	// Apply the governance action (this modifies intent status and adds blacklist entries).
	govReq := wire.GovernanceDealActionRequest{
		IntentID:           req.IntentID,
		Operator:           operator,
		Action:             req.Action,
		ReasonHash:         req.ReasonHash,
		ExpiresAtUnix:      req.ExpiresAtUnix,
		PreserveStorage:    true, // always preserve during review — deletion happens on ratify
		AppealDeadlineUnix: req.AppealDeadlineUnix,
	}
	govResp, err := s.governanceDealActionLocked(govReq, now)
	if err != nil {
		return wire.DirectGovernanceActionResponse{}, err
	}

	// Generate action ID.
	actionID, err := randomID("direct_action")
	if err != nil {
		return wire.DirectGovernanceActionResponse{}, errors.New("failed to generate direct action id")
	}

	reviewDeadline := now + s.data.DirectActionReviewWindowSeconds

	// Tag blacklist entries with review status.
	for _, hash := range intentShardHashes(intent) {
		entry := s.data.BlacklistedShards[hash]
		entry.ReviewStatus = wire.DirectActionPendingReview
		entry.ActionID = actionID
		s.data.BlacklistedShards[hash] = entry
	}

	// Create the direct action record.
	record := wire.DirectActionRecord{
		ActionID:              actionID,
		IntentID:              req.IntentID,
		Operator:              operator,
		Action:                req.Action,
		ReasonHash:            req.ReasonHash,
		ExpiresAtUnix:         req.ExpiresAtUnix,
		PreserveStorage:       req.PreserveStorage,
		AppealDeadlineUnix:    req.AppealDeadlineUnix,
		ReviewStatus:          wire.DirectActionPendingReview,
		ReviewDeadlineUnix:    reviewDeadline,
		CreatedAtUnix:         now,
		PreAccessStatus:       preAccess,
		PreModerationStatus:   preModeration,
		PreStorageStatus:      preStorage,
		PreExpiresAtUnix:      preExpires,
		PreAppealDeadlineUnix: preAppealDeadline,
	}
	s.data.DirectActionRecords[actionID] = record
	if s.data.DirectActionReviewVotes[actionID] == nil {
		s.data.DirectActionReviewVotes[actionID] = []wire.DirectActionReviewVote{}
	}

	// Advance nonce.
	if usesAgent {
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, 0); err != nil {
			return wire.DirectGovernanceActionResponse{}, err
		}
	} else {
		s.data.OperatorNonces[operator] = s.data.OperatorNonces[operator] + 1
	}

	resp := wire.DirectGovernanceActionResponse{
		Record:             record,
		GovernanceResult:   govResp,
		ReviewDeadlineUnix: reviewDeadline,
	}
	s.recordTxLocked("direct_governance_action", operator, directGovernanceActionTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.DirectGovernanceActionResponse{}, err
	}
	return resp, nil
}

// CastDirectActionReviewVote allows an enabled operator to vote to reject a pending direct action.
// If rejection votes reach the threshold (same as data moderation threshold), the action is rolled back.
func (s *Store) CastDirectActionReviewVote(req wire.DirectActionReviewVoteRequest) (wire.DirectActionReviewVoteResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	voter := normalizeGovernanceOperator(req.Voter)
	if voter == "" {
		return wire.DirectActionReviewVoteResponse{}, errors.New("voter is required")
	}
	if req.ChainID != s.data.ChainID {
		return wire.DirectActionReviewVoteResponse{}, errors.New("review vote chain_id mismatch")
	}

	// Verify voter is an enabled governance operator.
	op, ok := s.data.GovernanceOperators[voter]
	if !ok || !op.Enabled {
		return wire.DirectActionReviewVoteResponse{}, errors.New("voter is not an enabled governance operator")
	}
	if op.PublicKey == "" {
		return wire.DirectActionReviewVoteResponse{}, errors.New("voter has no public key registered")
	}

	usesAgent := requestUsesAgent(req.AgentKeyID)
	if usesAgent {
		// Agent key authentication path.
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, voter, "direct_action_review_vote", 0, func(agentPub string) error {
			return wire.VerifyDirectActionReviewVoteAgent(req, agentPub)
		}); err != nil {
			return wire.DirectActionReviewVoteResponse{}, err
		}
	} else {
		// Operator direct signature verification.
		if err := wire.VerifyDirectActionReviewVote(req, voter); err != nil {
			return wire.DirectActionReviewVoteResponse{}, err
		}
		// Verify voter nonce.
		expectedNonce := s.data.OperatorNonces[voter]
		if req.Nonce != expectedNonce {
			return wire.DirectActionReviewVoteResponse{}, errors.New("invalid voter nonce")
		}
	}

	// Validate clock skew.
	now := time.Now().Unix()
	if abs64(req.CreatedAtUnix-now) > governanceClockSkewSeconds {
		return wire.DirectActionReviewVoteResponse{}, errors.New("review vote timestamp outside acceptable clock skew")
	}

	// Look up the direct action record.
	record, ok := s.data.DirectActionRecords[req.ActionID]
	if !ok {
		return wire.DirectActionReviewVoteResponse{}, errors.New("direct action record not found")
	}
	if record.ReviewStatus != wire.DirectActionPendingReview {
		return wire.DirectActionReviewVoteResponse{}, errors.New("direct action is not pending review")
	}

	// Check review window has not expired.
	if now > record.ReviewDeadlineUnix {
		// Auto-ratify since window expired.
		s.autoRatifyDirectActionLocked(record, now)
		return wire.DirectActionReviewVoteResponse{}, errors.New("review window expired; action auto-ratified")
	}

	// Only reject votes are meaningful (approve is the default via inaction).
	if !req.Reject {
		return wire.DirectActionReviewVoteResponse{}, errors.New("only reject votes are accepted for direct action review")
	}

	// Record the vote.
	vote := wire.DirectActionReviewVote{
		ActionID:       req.ActionID,
		Voter:          voter,
		VoterSignature: req.Signature,
		Reject:         true,
		CreatedAtUnix:  req.CreatedAtUnix,
	}
	s.data.DirectActionReviewVotes[req.ActionID] = append(s.data.DirectActionReviewVotes[req.ActionID], vote)

	// Count reject votes from currently enabled operators.
	rejectCount := 0
	for _, v := range s.data.DirectActionReviewVotes[req.ActionID] {
		if !v.Reject {
			continue
		}
		voterAddr := normalizeGovernanceOperator(v.Voter)
		voterOp, ok := s.data.GovernanceOperators[voterAddr]
		if !ok || !voterOp.Enabled {
			continue
		}
		rejectCount++
	}

	threshold := s.governanceThresholdLocked(record.Action)
	rejected := rejectCount >= threshold

	if rejected {
		s.rejectDirectActionLocked(record, now)
	}

	// Advance nonce.
	if usesAgent {
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, 0); err != nil {
			return wire.DirectActionReviewVoteResponse{}, err
		}
	} else {
		s.data.OperatorNonces[voter] = s.data.OperatorNonces[voter] + 1
	}

	resp := wire.DirectActionReviewVoteResponse{
		Vote:        vote,
		RejectCount: rejectCount,
		Threshold:   threshold,
		Rejected:    rejected,
	}
	s.recordTxLocked("direct_action_review_vote", voter, directActionReviewVoteTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.DirectActionReviewVoteResponse{}, err
	}
	return resp, nil
}

// RatifyDirectAction explicitly ratifies a pending direct action.
// Can be called after the review window or by a governance proposal.
func (s *Store) RatifyDirectAction(actionID string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.data.DirectActionRecords[actionID]
	if !ok {
		return errors.New("direct action record not found")
	}
	if record.ReviewStatus != wire.DirectActionPendingReview {
		return errors.New("direct action is not pending review")
	}
	s.ratifyDirectActionLocked(record, now)
	if err := s.saveLocked(); err != nil {
		return err
	}
	return nil
}

// ExpireDirectActionReviews checks all pending direct actions and auto-ratifies
// those whose review window has expired. Should be called periodically.
func (s *Store) ExpireDirectActionReviews() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	count := 0
	for id, record := range s.data.DirectActionRecords {
		if record.ReviewStatus != wire.DirectActionPendingReview {
			continue
		}
		if now <= record.ReviewDeadlineUnix {
			continue
		}
		s.autoRatifyDirectActionLocked(record, now)
		_ = id
		count++
	}
	if count > 0 {
		_ = s.saveLocked()
	}
	return count
}

// ListDirectActions returns all direct action records and their review votes.
func (s *Store) ListDirectActions(intentID string, status string) wire.DirectActionListResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	records := make([]wire.DirectActionRecord, 0)
	votes := make(map[string][]wire.DirectActionReviewVote)
	for _, record := range s.data.DirectActionRecords {
		if intentID != "" && record.IntentID != intentID {
			continue
		}
		if status != "" && record.ReviewStatus != status {
			continue
		}
		records = append(records, record)
		votes[record.ActionID] = s.data.DirectActionReviewVotes[record.ActionID]
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAtUnix > records[j].CreatedAtUnix
	})
	return wire.DirectActionListResponse{Records: records, Votes: votes}
}

// ── Internal helpers for direct action ratification and rejection ──

func (s *Store) ratifyDirectActionLocked(record wire.DirectActionRecord, now int64) {
	record.ReviewStatus = wire.DirectActionRatified
	record.RatifiedAtUnix = now
	s.data.DirectActionRecords[record.ActionID] = record

	// Clear review status from blacklist entries.
	s.clearBlacklistReviewStatusLocked(record.ActionID)

	// Now perform irreversible actions that were deferred during direct execution.
	intent, ok := s.data.Intents[record.IntentID]
	if !ok {
		return
	}

	if record.Action == "block" && !record.PreserveStorage {
		intent.StorageStatus = wire.StorageStatusTerminating
		intent.TerminatedAtUnix = now
		s.closePermanentFundLocked(intent, "governance_block_ratified", now)
		s.ensureDeleteTasksLocked(intent, "governance_block", now)
	}
}

func (s *Store) autoRatifyDirectActionLocked(record wire.DirectActionRecord, now int64) {
	record.ReviewStatus = wire.DirectActionAutoRatified
	record.RatifiedAtUnix = now
	s.data.DirectActionRecords[record.ActionID] = record

	// Clear review status from blacklist entries.
	s.clearBlacklistReviewStatusLocked(record.ActionID)

	// Perform deferred irreversible actions.
	intent, ok := s.data.Intents[record.IntentID]
	if !ok {
		return
	}

	if record.Action == "block" && !record.PreserveStorage {
		intent.StorageStatus = wire.StorageStatusTerminating
		intent.TerminatedAtUnix = now
		s.closePermanentFundLocked(intent, "governance_block_auto_ratified", now)
		s.ensureDeleteTasksLocked(intent, "governance_block", now)
	}
}

func (s *Store) rejectDirectActionLocked(record wire.DirectActionRecord, now int64) {
	record.ReviewStatus = wire.DirectActionRejected
	record.RejectedAtUnix = now
	s.data.DirectActionRecords[record.ActionID] = record

	// Remove blacklist entries that were added by this direct action.
	s.removeBlacklistByActionIDLocked(record.ActionID)

	// Cancel any pending_review delete tasks for this action.
	s.cancelDeleteTasksByActionIDLocked(record.ActionID)

	// Rollback intent state.
	intent, ok := s.data.Intents[record.IntentID]
	if !ok {
		return
	}
	intent.AccessStatus = record.PreAccessStatus
	intent.ModerationStatus = record.PreModerationStatus
	intent.StorageStatus = record.PreStorageStatus
	intent.ModerationExpiresAtUnix = record.PreExpiresAtUnix
	intent.AppealDeadlineUnix = record.PreAppealDeadlineUnix
	intent.BlockedReasonHash = ""
	intent.AccessUpdatedAtUnix = now
	intent.UpdatedAt = now
}

func (s *Store) clearBlacklistReviewStatusLocked(actionID string) {
	for hash, entry := range s.data.BlacklistedShards {
		if entry.ActionID == actionID {
			entry.ReviewStatus = ""
			entry.ActionID = ""
			s.data.BlacklistedShards[hash] = entry
		}
	}
}

func (s *Store) removeBlacklistByActionIDLocked(actionID string) {
	for hash, entry := range s.data.BlacklistedShards {
		if entry.ActionID == actionID {
			delete(s.data.BlacklistedShards, hash)
		}
	}
}

func (s *Store) cancelDeleteTasksByActionIDLocked(actionID string) {
	for id, task := range s.data.DeleteTasks {
		if task.ActionID == actionID && task.ReviewStatus == wire.DirectActionPendingReview {
			delete(s.data.DeleteTasks, id)
		}
	}
}
