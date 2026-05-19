package chain

import (
	"errors"
	"sort"
	"strconv"
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
	expireGovernanceAction(intent, time.Now().Unix())
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
	resp, err := s.terminateDealLocked(req, time.Now().Unix())
	if err != nil {
		return wire.TerminateDealResponse{}, err
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
	normalizeIntentLifecycle(intent)
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
	refund := remainingIntentEscrow(intent)
	if refund > 0 {
		user := s.accountLocked(intent.User)
		if user.LockedStorage < refund {
			refund = user.LockedStorage
		}
		user.LockedStorage -= refund
		user.Balance += refund
		intent.RefundedFee += refund
		s.data.Accounts[user.Address] = user
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
		RefundedFee:      refund,
		DeleteTasks:      tasks,
		TerminatedAtUnix: now,
	}, nil
}

func (s *Store) SetAccessPolicy(req wire.SetAccessPolicyRequest) (wire.SetAccessPolicyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp, err := s.setAccessPolicyLocked(req, time.Now().Unix())
	if err != nil {
		return wire.SetAccessPolicyResponse{}, err
	}
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
	normalizeIntentLifecycle(intent)
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

func (s *Store) GovernanceDealAction(req wire.GovernanceDealActionRequest) (wire.GovernanceDealActionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp, err := s.governanceDealActionLocked(req, time.Now().Unix())
	if err != nil {
		return wire.GovernanceDealActionResponse{}, err
	}
	s.recordTxLocked("governance_deal_action", req.Operator, governanceDealActionTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.GovernanceDealActionResponse{}, err
	}
	return resp, nil
}

func (s *Store) CommitteeFreezeDeal(req wire.CommitteeFreezeDealRequest) (wire.GovernanceDealActionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	resp, err := s.governanceDealActionLocked(wire.GovernanceDealActionRequest{
		IntentID:      req.IntentID,
		Operator:      req.Operator,
		Action:        "freeze",
		ReasonHash:    req.ReasonHash,
		ExpiresAtUnix: req.ExpiresAtUnix,
	}, now)
	if err != nil {
		return wire.GovernanceDealActionResponse{}, err
	}
	s.recordTxLocked("committee_freeze_deal", req.Operator, committeeFreezeDealTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.GovernanceDealActionResponse{}, err
	}
	return resp, nil
}

func (s *Store) GovernanceBlockDeal(req wire.GovernanceBlockDealRequest) (wire.GovernanceDealActionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	resp, err := s.governanceDealActionLocked(wire.GovernanceDealActionRequest{
		IntentID:           req.IntentID,
		Operator:           req.Operator,
		Action:             "block",
		ReasonHash:         req.ReasonHash,
		AppealDeadlineUnix: req.AppealDeadlineUnix,
		PreserveStorage:    req.PreserveStorage,
	}, now)
	if err != nil {
		return wire.GovernanceDealActionResponse{}, err
	}
	s.recordTxLocked("governance_block_deal", req.Operator, governanceBlockDealTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.GovernanceDealActionResponse{}, err
	}
	return resp, nil
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
	normalizeIntentLifecycle(intent)
	switch req.Action {
	case "freeze":
		if req.ExpiresAtUnix <= now {
			return wire.GovernanceDealActionResponse{}, errors.New("freeze action requires a future expires_at_unix")
		}
		intent.AccessStatus = wire.AccessStatusSuspended
		intent.ModerationStatus = wire.ModerationStatusFrozen
		intent.ModerationExpiresAtUnix = req.ExpiresAtUnix
		intent.AppealDeadlineUnix = 0
	case "block":
		if req.AppealDeadlineUnix > 0 && req.AppealDeadlineUnix <= now {
			return wire.GovernanceDealActionResponse{}, errors.New("block action requires appeal_deadline_unix to be in the future")
		}
		intent.AccessStatus = wire.AccessStatusBlocked
		intent.ModerationStatus = wire.ModerationStatusBlocked
		intent.ModerationExpiresAtUnix = 0
		intent.AppealDeadlineUnix = req.AppealDeadlineUnix
		if !req.PreserveStorage {
			intent.StorageStatus = wire.StorageStatusTerminating
			intent.TerminatedAtUnix = now
			s.ensureDeleteTasksLocked(intent, "governance_block", now)
		}
	case "legal_hold":
		intent.AccessStatus = wire.AccessStatusBlocked
		intent.ModerationStatus = wire.ModerationStatusLegalHold
		intent.StorageStatus = wire.StorageStatusActive
		intent.ModerationExpiresAtUnix = 0
		intent.AppealDeadlineUnix = 0
	case "appeal":
		intent.ModerationStatus = wire.ModerationStatusAppealed
		intent.ModerationExpiresAtUnix = 0
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
	intent, ok := s.data.Intents[receipt.IntentID]
	if !ok {
		return wire.SubmitDeleteReceiptResponse{}, errors.New("intent not found")
	}
	normalizeIntentLifecycle(intent)
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
	intent.UpdatedAt = time.Now().Unix()
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
			task = wire.DeleteTask{
				TaskID:         taskID,
				IntentID:       intent.IntentID,
				ShardHash:      receipt.ShardHash,
				MinerAddress:   receipt.MinerAddress,
				MinerPublicKey: receipt.MinerPublicKey,
				Status:         deleteTaskStatusPending,
				Reason:         reason,
				CreatedAtUnix:  now,
			}
		} else {
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
