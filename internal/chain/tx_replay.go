package chain

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"chain/internal/wire"
)

type createIntentTxPayload struct {
	IntentID      string                   `json:"intent_id"`
	Request       wire.CreateIntentRequest `json:"request"`
	CreatedAtUnix int64                    `json:"created_at_unix"`
}

type batchCommitTxPayload struct {
	Request           wire.BatchCommitRequest `json:"request"`
	CommittedSegments int                     `json:"committed_segments"`
	UploadedSize      int64                   `json:"uploaded_size"`
}

type finalizeDealTxPayload struct {
	IntentID     string `json:"intent_id"`
	DealID       string `json:"deal_id"`
	User         string `json:"user"`
	ManifestRoot string `json:"manifest_root"`
}

type settleIntentTxPayload struct {
	Request       wire.SettleIntentRequest  `json:"request"`
	Response      wire.SettleIntentResponse `json:"response"`
	SettledAtUnix int64                     `json:"settled_at_unix"`
}

type generateChallengesTxPayload struct {
	Request    wire.GenerateChallengeRequest `json:"request"`
	Challenges []wire.StorageChallenge       `json:"challenges"`
}

type startEpochTxPayload struct {
	Request    wire.StartEpochRequest  `json:"request"`
	Epoch      wire.ProofEpoch         `json:"epoch"`
	Challenges []wire.StorageChallenge `json:"challenges"`
}

type submitProofTxPayload struct {
	Request         wire.SubmitProofRequest `json:"request"`
	Reward          uint64                  `json:"reward"`
	AlreadyRewarded bool                    `json:"already_rewarded"`
	SubmittedAtUnix int64                   `json:"submitted_at_unix"`
}

type finalizeEpochTxPayload struct {
	Response    wire.FinalizeEpochResponse `json:"response"`
	RepairTasks []wire.RepairTask          `json:"repair_tasks,omitempty"`
}

func (s *Store) applyBlockTransactionsLocked(block wire.Block) error {
	for _, tx := range block.Transactions {
		if s.data.ConfirmedTxs[tx.TxID] {
			return errors.New("block contains already confirmed transaction")
		}
		if err := s.validateTransactionFeeLocked(tx); err != nil {
			return err
		}
		if err := s.validateAgentKeyTxLocked(tx); err != nil {
			return err
		}
		if !s.data.AppliedTxs[tx.TxID] {
			if err := s.applyTransactionLocked(tx); err != nil {
				return err
			}
			s.data.AppliedTxs[tx.TxID] = true
		}
		if err := s.chargeTransactionFeeLocked(tx, block.ProducerAddress); err != nil {
			return err
		}
		s.data.ConfirmedTxs[tx.TxID] = true
	}
	return nil
}

func (s *Store) applyPendingTransactionsForBlockLocked(txs []wire.Transaction, producerAddress string) error {
	for _, tx := range txs {
		if s.data.ConfirmedTxs[tx.TxID] {
			return errors.New("pending transaction already confirmed")
		}
		if err := s.validateTransactionFeeLocked(tx); err != nil {
			return err
		}
		if err := s.validateAgentKeyTxLocked(tx); err != nil {
			return err
		}
		if !s.data.AppliedTxs[tx.TxID] {
			if err := s.applyTransactionLocked(tx); err != nil {
				return err
			}
			s.data.AppliedTxs[tx.TxID] = true
		}
		if err := s.chargeTransactionFeeLocked(tx, producerAddress); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyTransactionLocked(tx wire.Transaction) error {
	switch tx.Type {
	case "faucet":
		var req wire.FaucetRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			return err
		}
		account := s.accountLocked(req.Address)
		account.Balance += req.Amount
		s.data.Accounts[req.Address] = account
	case "transfer":
		var req wire.TransferRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			return err
		}
		_, _, err := s.applyTransferLocked(req)
		return err
	case "register_validator":
		var req wire.RegisterValidatorRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			return err
		}
		return s.applyValidatorRegistrationLocked(req)
	case "register_miner":
		var req wire.RegisterMinerRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			return err
		}
		return s.applyMinerRegistrationLocked(req)
	case "create_intent":
		var payload createIntentTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyCreateIntentLocked(payload)
	case "create_collection":
		var payload createCollectionTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyDataCollectionPayloadLocked(payload)
	case "append_record":
		var payload appendRecordTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyDataRecordPayloadLocked(payload)
	case "create_key_envelope":
		var payload createKeyEnvelopeTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyCreateKeyEnvelopeLocked(payload)
	case "create_share":
		var payload createShareTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyCreateShareLocked(payload)
	case "revoke_share":
		var payload revokeShareTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyRevokeShareLocked(payload)
	case "batch_commit":
		var payload batchCommitTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyBatchCommitLocked(payload)
	case "finalize_deal":
		var payload finalizeDealTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyFinalizeDealLocked(payload)
	case "settle_intent":
		var payload settleIntentTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applySettleIntentLocked(payload)
	case "renew_deal":
		var payload renewDealTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyRenewDealLocked(payload)
	case "permanent_fund_topup":
		var payload permanentFundTopUpTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyPermanentFundTopUpLocked(payload)
	case "terminate_deal":
		var payload terminateDealTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyTerminateDealLocked(payload)
	case "set_access_policy":
		var payload setAccessPolicyTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applySetAccessPolicyLocked(payload)
	case "governance_deal_action":
		var payload governanceDealActionTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyGovernanceDealActionLocked(payload)
	case "committee_freeze_deal":
		var payload committeeFreezeDealTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyCommitteeFreezeDealLocked(payload)
	case "governance_block_deal":
		var payload governanceBlockDealTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyGovernanceBlockDealLocked(payload)
	case "submit_delete_receipt":
		var payload submitDeleteReceiptTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applySubmitDeleteReceiptLocked(payload)
	case "submit_retrieval_receipt":
		var payload submitRetrievalReceiptTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applySubmitRetrievalReceiptLocked(payload)
	case "generate_challenges":
		var payload generateChallengesTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyGenerateChallengesLocked(payload)
	case "create_repair_tasks":
		var payload createRepairTasksTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyRepairTasksLocked(payload.Tasks)
	case "start_epoch":
		var payload startEpochTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyStartEpochLocked(payload)
	case "submit_proof":
		var payload submitProofTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applySubmitProofLocked(payload)
	case "finalize_epoch":
		var payload finalizeEpochTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyFinalizeEpochLocked(payload)
	case "validator_evidence":
		var payload validatorEvidenceTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		_, err := s.applyValidatorEvidenceLocked(payload.Evidence)
		return err
	default:
		return nil
	}
	return nil
}

func (s *Store) applyTransferLocked(req wire.TransferRequest) (wire.Account, wire.Account, error) {
	fromAddress := wire.NormalizeAddress(req.From)
	toAddress := wire.NormalizeAddress(req.To)
	from := s.accountLocked(fromAddress)
	if wire.IsSignedTransfer(req) {
		if req.Signature == "" {
			return wire.Account{}, wire.Account{}, errors.New("signed transfer requires signature")
		}
		recoveredPublicKey, err := wire.RecoverTransferPublicKey(req)
		if err != nil {
			return wire.Account{}, wire.Account{}, err
		}
		if req.PublicKey != "" && !strings.EqualFold(req.PublicKey, recoveredPublicKey) {
			return wire.Account{}, wire.Account{}, errors.New("transfer public key does not match signature")
		}
		req.PublicKey = recoveredPublicKey
		if err := wire.VerifyTransferSignature(req); err != nil {
			return wire.Account{}, wire.Account{}, err
		}
		if from.PublicKey != "" && !strings.EqualFold(from.PublicKey, req.PublicKey) {
			return wire.Account{}, wire.Account{}, errors.New("transfer public key mismatch with account")
		}
		if req.Fee < s.data.FeeMarket.BaseFee {
			return wire.Account{}, wire.Account{}, errors.New("transfer fee below current base fee")
		}
		expectedNonce := from.Nonce
		if req.Nonce != expectedNonce {
			return wire.Account{}, wire.Account{}, errors.New("invalid transfer nonce")
		}
		return s.applyNonceProtectedTransferLocked(req, req.PublicKey)
	}
	total, err := transferTotalCost(req.Amount, req.Fee)
	if err != nil {
		return wire.Account{}, wire.Account{}, err
	}
	if from.Balance < total {
		return wire.Account{}, wire.Account{}, errors.New("insufficient balance")
	}
	to := s.accountLocked(toAddress)
	from.Balance -= req.Amount
	to.Balance += req.Amount
	s.data.Accounts[fromAddress] = from
	s.data.Accounts[toAddress] = to
	return from, to, nil
}

func (s *Store) applyNonceProtectedTransferLocked(req wire.TransferRequest, publicKey string) (wire.Account, wire.Account, error) {
	fromAddress := wire.NormalizeAddress(req.From)
	toAddress := wire.NormalizeAddress(req.To)
	from := s.accountLocked(fromAddress)
	expectedNonce := from.Nonce
	if req.Nonce != expectedNonce {
		return wire.Account{}, wire.Account{}, errors.New("invalid transfer nonce")
	}
	total, err := transferTotalCost(req.Amount, req.Fee)
	if err != nil {
		return wire.Account{}, wire.Account{}, err
	}
	if from.Balance < total {
		return wire.Account{}, wire.Account{}, errors.New("insufficient balance")
	}
	to := s.accountLocked(toAddress)
	from.Balance -= req.Amount
	from.Nonce++
	if publicKey != "" {
		from.PublicKey = publicKey
	}
	to.Balance += req.Amount
	s.data.Accounts[fromAddress] = from
	s.data.Accounts[toAddress] = to
	return from, to, nil
}

func (s *Store) applyValidatorRegistrationLocked(req wire.RegisterValidatorRequest) error {
	if err := wire.VerifyValidatorRegistration(req); err != nil {
		return err
	}
	existing := s.validatorLocked(req.Address)
	account := s.accountLocked(req.Address)
	if req.Stake > existing.Stake {
		additionalStake := req.Stake - existing.Stake
		if account.Balance < additionalStake {
			return errors.New("replay validator registration has insufficient stake balance")
		}
		account.Balance -= additionalStake
		account.LockedStake += additionalStake
	}
	existing.Address = req.Address
	existing.PublicKey = req.PublicKey
	existing.Endpoint = req.Endpoint
	existing.Stake = req.Stake
	existing.SelfStake = req.Stake
	existing.Status = wire.ValidatorStatusActive
	if existing.RegisteredAtUnix == 0 {
		existing.RegisteredAtUnix = time.Now().Unix()
	}
	s.data.Accounts[account.Address] = account
	s.data.Validators[req.Address] = existing
	s.data.ConsensusValidators[req.Address] = true
	return nil
}

func (s *Store) applyMinerRegistrationLocked(req wire.RegisterMinerRequest) error {
	if err := wire.VerifyMinerRegistration(req); err != nil {
		return err
	}
	existing := s.minerStatsLocked(req.MinerAddress)
	account := s.accountLocked(req.MinerAddress)
	if req.Stake > existing.Stake {
		additionalStake := req.Stake - existing.Stake
		if account.Balance < additionalStake {
			return errors.New("replay miner registration has insufficient stake balance")
		}
		account.Balance -= additionalStake
		account.LockedStake += additionalStake
	}
	existing.MinerAddress = req.MinerAddress
	existing.PublicKey = req.PublicKey
	existing.Endpoint = req.Endpoint
	existing.CapacityBytes = req.CapacityBytes
	existing.Stake = req.Stake
	existing.Status = "active"
	if existing.RegisteredAtUnix == 0 {
		existing.RegisteredAtUnix = time.Now().Unix()
	}
	s.data.Accounts[req.MinerAddress] = account
	s.data.Miners[req.MinerAddress] = existing
	return nil
}

func (s *Store) applyCreateIntentLocked(payload createIntentTxPayload) error {
	req := payload.Request
	if payload.IntentID == "" {
		return errors.New("replay create intent missing intent id")
	}
	if _, exists := s.data.Intents[payload.IntentID]; exists {
		return nil
	}
	quote, err := s.storageQuoteForIntentLocked(req)
	if err != nil {
		return err
	}
	if req.LockedFee == 0 {
		req.LockedFee = quote.RequiredFee
	}
	if req.LockedFee < quote.RequiredFee {
		return errors.New("replay create intent storage fee below current quote")
	}
	assignments, err := s.buildStorageAssignmentsLocked(req)
	if err != nil {
		return err
	}
	account := s.accountLocked(req.User)
	if account.Balance < req.LockedFee {
		return errors.New("replay create intent has insufficient storage fee balance")
	}
	account.Balance -= req.LockedFee
	account.LockedStorage += req.LockedFee
	s.data.Accounts[account.Address] = account

	createdAt := payload.CreatedAtUnix
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}
	s.data.Intents[payload.IntentID] = &Intent{
		IntentView: wire.IntentView{
			IntentID:         payload.IntentID,
			User:             req.User,
			FileName:         req.FileName,
			FileSize:         req.FileSize,
			SegmentSize:      req.SegmentSize,
			FileRoot:         req.FileRoot,
			SegmentRoots:     req.SegmentRoots,
			Segments:         req.Segments,
			Assignments:      assignments,
			Erasure:          req.Erasure,
			Encryption:       req.Encryption,
			Policy:           req.Policy,
			LockedFee:        req.LockedFee,
			Status:           wire.StatusUploading,
			StorageStatus:    wire.StorageStatusPending,
			AccessStatus:     defaultAccessStatus(wire.IntentView{Encryption: req.Encryption}),
			ModerationStatus: wire.ModerationStatusNone,
		},
		DeadlineUnix: req.DeadlineUnix,
		Receipts:     map[int]map[int]wire.MinerReceipt{},
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	s.createPermanentFundLocked(s.data.Intents[payload.IntentID], createdAt)
	s.reserveStorageAssignmentsLocked(assignments)
	return nil
}

func (s *Store) applyBatchCommitLocked(payload batchCommitTxPayload) error {
	req := payload.Request
	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return errors.New("replay batch commit intent not found")
	}
	for _, receipt := range req.Receipts {
		if err := validateReceiptForReplay(intent, receipt); err != nil {
			return err
		}
		if err := s.validateReceiptAssignmentLocked(intent, receipt); err != nil {
			return err
		}
		miner, ok := s.data.Miners[receipt.MinerAddress]
		if !ok || miner.Status != wire.MinerStatusActive {
			return errors.New("replay batch commit miner not found")
		}
		if intent.Receipts[receipt.SegmentID] == nil {
			intent.Receipts[receipt.SegmentID] = map[int]wire.MinerReceipt{}
		}
		oldReceipt, exists := intent.Receipts[receipt.SegmentID][receipt.ShardIndex]
		repairTask, hasRepairTask := s.pendingRepairTaskForShardLocked(intent.IntentID, receipt.SegmentID, receipt.ShardIndex, receipt.MinerAddress)
		if exists && oldReceipt.MinerAddress != receipt.MinerAddress {
			oldMiner := s.minerStatsLocked(oldReceipt.MinerAddress)
			oldSize := uint64(oldReceipt.ShardSize)
			if oldMiner.UsedBytes < oldSize {
				oldMiner.UsedBytes = 0
			} else {
				oldMiner.UsedBytes -= oldSize
			}
			s.data.Miners[oldReceipt.MinerAddress] = oldMiner
			if hasRepairTask {
				s.releaseStorageReservationLocked(repairTask.Assignment)
				intent.Assignments = setAssignmentForShard(intent.Assignments, repairTask.Assignment)
				s.completeRepairTaskLocked(repairTask)
				miner = s.minerStatsLocked(receipt.MinerAddress)
			}
			miner.UsedBytes += uint64(receipt.ShardSize)
		} else if !exists {
			if assignment, ok := assignmentForShard(intent.Assignments, receipt.SegmentID, receipt.ShardIndex); ok {
				s.releaseStorageReservationLocked(assignment)
				miner = s.minerStatsLocked(receipt.MinerAddress)
			}
			if hasRepairTask {
				s.releaseStorageReservationLocked(repairTask.Assignment)
				intent.Assignments = setAssignmentForShard(intent.Assignments, repairTask.Assignment)
				s.completeRepairTaskLocked(repairTask)
				miner = s.minerStatsLocked(receipt.MinerAddress)
			}
			miner.UsedBytes += uint64(receipt.ShardSize)
		}
		intent.Receipts[receipt.SegmentID][receipt.ShardIndex] = receipt
		s.data.Miners[receipt.MinerAddress] = miner
	}
	intent.CommittedSegments = payload.CommittedSegments
	intent.UploadedSize = payload.UploadedSize
	intent.Status = wire.StatusPartial
	intent.UpdatedAt = time.Now().Unix()
	return nil
}

func (s *Store) applyFinalizeDealLocked(payload finalizeDealTxPayload) error {
	intent, ok := s.data.Intents[payload.IntentID]
	if !ok {
		return errors.New("replay finalize deal intent not found")
	}
	if payload.User != "" && intent.User != payload.User {
		return errors.New("replay finalize deal user mismatch")
	}
	intent.DealID = payload.DealID
	intent.Status = wire.StatusFinalized
	now := time.Now().Unix()
	intent.StorageStatus = wire.StorageStatusActive
	intent.AccessStatus = defaultAccessStatus(intent.IntentView)
	intent.ModerationStatus = wire.ModerationStatusNone
	if intent.Policy.Duration > 0 {
		intent.ExpiresAtUnix = now + intent.Policy.Duration
	}
	intent.UpdatedAt = now
	s.data.Deals[payload.DealID] = payload.IntentID
	return nil
}

func (s *Store) applySettleIntentLocked(payload settleIntentTxPayload) error {
	intent, ok := s.data.Intents[payload.Request.IntentID]
	if !ok {
		return errors.New("replay settle intent not found")
	}
	if payload.Request.User != "" && payload.Request.User != intent.User {
		return errors.New("replay settle intent user mismatch")
	}
	settledAt := payload.SettledAtUnix
	if settledAt == 0 {
		settledAt = time.Now().Unix()
	}
	resp, err := s.settleIntentLocked(intent, settledAt)
	if err != nil {
		return err
	}
	if resp.RefundedFee != payload.Response.RefundedFee || resp.PaidFee != payload.Response.PaidFee || resp.Status != payload.Response.Status {
		return errors.New("replay settle intent response mismatch")
	}
	return nil
}

func (s *Store) applyTerminateDealLocked(payload terminateDealTxPayload) error {
	resp, err := s.terminateDealLocked(payload.Request, payload.Response.TerminatedAtUnix)
	if err != nil {
		return err
	}
	if resp.StorageStatus != payload.Response.StorageStatus || resp.AccessStatus != payload.Response.AccessStatus {
		return errors.New("replay terminate deal response mismatch")
	}
	return nil
}

func (s *Store) applySetAccessPolicyLocked(payload setAccessPolicyTxPayload) error {
	resp, err := s.setAccessPolicyLocked(payload.Request, payload.Response.UpdatedAtUnix)
	if err != nil {
		return err
	}
	if resp.AccessStatus != payload.Response.AccessStatus {
		return errors.New("replay set access policy response mismatch")
	}
	return nil
}

func (s *Store) applyGovernanceDealActionLocked(payload governanceDealActionTxPayload) error {
	if err := s.validateGovernanceOperatorLocked(payload.Request.Operator, payload.Request.Action); err != nil {
		return err
	}
	resp, err := s.governanceDealActionLocked(payload.Request, payload.Response.UpdatedAtUnix)
	if err != nil {
		return err
	}
	if resp.AccessStatus != payload.Response.AccessStatus ||
		resp.GovernanceType != payload.Response.GovernanceType ||
		resp.ModerationStatus != payload.Response.ModerationStatus ||
		resp.StorageStatus != payload.Response.StorageStatus ||
		resp.ModerationExpiresAtUnix != payload.Response.ModerationExpiresAtUnix ||
		resp.AppealDeadlineUnix != payload.Response.AppealDeadlineUnix {
		return errors.New("replay governance deal action response mismatch")
	}
	return nil
}

func (s *Store) applyCommitteeFreezeDealLocked(payload committeeFreezeDealTxPayload) error {
	if err := s.validateGovernanceOperatorLocked(payload.Request.Operator, "freeze"); err != nil {
		return err
	}
	resp, err := s.governanceDealActionLocked(wire.GovernanceDealActionRequest{
		IntentID:      payload.Request.IntentID,
		Operator:      payload.Request.Operator,
		Action:        "freeze",
		ReasonHash:    payload.Request.ReasonHash,
		ExpiresAtUnix: payload.Request.ExpiresAtUnix,
	}, payload.Response.UpdatedAtUnix)
	if err != nil {
		return err
	}
	if resp.GovernanceType != "committee_freeze_deal" ||
		resp.AccessStatus != payload.Response.AccessStatus ||
		resp.ModerationStatus != payload.Response.ModerationStatus ||
		resp.StorageStatus != payload.Response.StorageStatus ||
		resp.ModerationExpiresAtUnix != payload.Response.ModerationExpiresAtUnix {
		return errors.New("replay committee freeze deal response mismatch")
	}
	return nil
}

func (s *Store) applyGovernanceBlockDealLocked(payload governanceBlockDealTxPayload) error {
	if err := s.validateGovernanceOperatorLocked(payload.Request.Operator, "block"); err != nil {
		return err
	}
	resp, err := s.governanceDealActionLocked(wire.GovernanceDealActionRequest{
		IntentID:           payload.Request.IntentID,
		Operator:           payload.Request.Operator,
		Action:             "block",
		ReasonHash:         payload.Request.ReasonHash,
		AppealDeadlineUnix: payload.Request.AppealDeadlineUnix,
		PreserveStorage:    payload.Request.PreserveStorage,
	}, payload.Response.UpdatedAtUnix)
	if err != nil {
		return err
	}
	if resp.GovernanceType != "governance_block_deal" ||
		resp.AccessStatus != payload.Response.AccessStatus ||
		resp.ModerationStatus != payload.Response.ModerationStatus ||
		resp.StorageStatus != payload.Response.StorageStatus ||
		resp.AppealDeadlineUnix != payload.Response.AppealDeadlineUnix {
		return errors.New("replay governance block deal response mismatch")
	}
	return nil
}

func (s *Store) applySubmitDeleteReceiptLocked(payload submitDeleteReceiptTxPayload) error {
	if err := wire.VerifyDeleteReceipt(payload.Request.Receipt); err != nil {
		return err
	}
	resp, err := s.submitDeleteReceiptLocked(payload.Request)
	if err != nil {
		return err
	}
	if resp.DeleteReceiptCount != payload.Response.DeleteReceiptCount || resp.Status != payload.Response.Status {
		return errors.New("replay submit delete receipt response mismatch")
	}
	return nil
}

func (s *Store) applySubmitRetrievalReceiptLocked(payload submitRetrievalReceiptTxPayload) error {
	if err := wire.VerifyRetrievalReceipt(payload.Request.Receipt); err != nil {
		return err
	}
	resp, alreadyRewarded, err := s.submitRetrievalReceiptLocked(payload.Request)
	if err != nil {
		return err
	}
	if alreadyRewarded != payload.AlreadyRewarded {
		return errors.New("replay retrieval receipt reward status mismatch")
	}
	if resp.Reward != payload.Response.Reward || resp.Status != payload.Response.Status {
		return errors.New("replay retrieval receipt response mismatch")
	}
	return nil
}

func (s *Store) applyGenerateChallengesLocked(payload generateChallengesTxPayload) error {
	if len(payload.Challenges) == 0 {
		return errors.New("replay generate challenges missing challenges")
	}
	intent, ok := s.data.Intents[payload.Request.IntentID]
	if !ok {
		return errors.New("replay generate challenges intent not found")
	}
	for _, challenge := range payload.Challenges {
		if challenge.IntentID != intent.IntentID {
			return errors.New("replay generated challenge intent mismatch")
		}
		if challenge.DealID != intent.DealID {
			return errors.New("replay generated challenge deal mismatch")
		}
		if challenge.ChallengeID == "" || challenge.Nonce == "" {
			return errors.New("replay generated challenge missing id or nonce")
		}
		if _, exists := s.data.Challenges[challenge.ChallengeID]; exists {
			continue
		}
		s.data.Challenges[challenge.ChallengeID] = challenge
	}
	return nil
}

func (s *Store) applyStartEpochLocked(payload startEpochTxPayload) error {
	if payload.Epoch.EpochID == "" {
		return errors.New("replay start epoch missing epoch id")
	}
	if len(payload.Epoch.ChallengeIDs) != len(payload.Challenges) {
		return errors.New("replay start epoch challenge count mismatch")
	}
	if _, exists := s.data.Epochs[payload.Epoch.EpochID]; !exists {
		s.data.Epochs[payload.Epoch.EpochID] = payload.Epoch
	}
	seenChallenges := map[string]bool{}
	for _, challengeID := range payload.Epoch.ChallengeIDs {
		seenChallenges[challengeID] = true
	}
	for _, challenge := range payload.Challenges {
		if !seenChallenges[challenge.ChallengeID] {
			return errors.New("replay start epoch includes challenge outside epoch")
		}
		if challenge.EpochID != payload.Epoch.EpochID {
			return errors.New("replay start epoch challenge epoch mismatch")
		}
		if challenge.Reward != payload.Epoch.RewardPerProof {
			return errors.New("replay start epoch challenge reward mismatch")
		}
		s.data.Challenges[challenge.ChallengeID] = challenge
	}
	return nil
}

func (s *Store) applySubmitProofLocked(payload submitProofTxPayload) error {
	proof := payload.Request.Proof
	if err := wire.VerifyProof(proof); err != nil {
		return err
	}
	challenge, ok := s.data.Challenges[proof.ChallengeID]
	if !ok {
		return errors.New("replay submit proof challenge not found")
	}
	if challenge.EpochID != "" {
		epoch, ok := s.data.Epochs[challenge.EpochID]
		if ok && epoch.Status == "finalized" {
			return errors.New("replay submit proof epoch already finalized")
		}
	}
	if proof.MinerAddress != challenge.MinerAddress {
		return errors.New("replay proof miner mismatch")
	}
	if proof.MinerPublicKey != challenge.MinerPublicKey {
		return errors.New("replay proof public key mismatch")
	}
	if _, err := s.registeredMinerLocked(proof.MinerAddress, proof.MinerPublicKey); err != nil {
		return err
	}
	if err := validateStorageProof(challenge, proof); err != nil {
		return err
	}
	_, alreadyRewarded := s.data.Proofs[proof.ChallengeID]
	if alreadyRewarded != payload.AlreadyRewarded {
		return errors.New("replay proof reward status mismatch")
	}
	s.data.Proofs[proof.ChallengeID] = proof
	if !alreadyRewarded {
		reward := s.payableStorageRewardLocked(challenge)
		if reward != payload.Reward {
			return errors.New("replay proof reward mismatch")
		}
		stats := s.minerStatsLocked(proof.MinerAddress)
		stats.ProofSuccess++
		stats.Rewards += reward
		s.data.Miners[proof.MinerAddress] = stats
		s.payStorageRewardLocked(challenge, proof.MinerAddress, reward)
	}
	return nil
}

func (s *Store) applyFinalizeEpochLocked(payload finalizeEpochTxPayload) error {
	epoch, ok := s.data.Epochs[payload.Response.EpochID]
	if !ok {
		return errors.New("replay finalize epoch not found")
	}
	if epoch.Status == "finalized" {
		return nil
	}
	resp := s.settleEpochWithoutTxLocked(epoch)
	if resp.AcceptedProofs != payload.Response.AcceptedProofs || resp.MissedProofs != payload.Response.MissedProofs {
		return errors.New("replay finalize epoch result mismatch")
	}
	if len(payload.RepairTasks) == 0 {
		payload.RepairTasks = payload.Response.RepairTasks
	}
	if len(payload.RepairTasks) > 0 {
		return s.applyRepairTasksLocked(payload.RepairTasks)
	}
	return nil
}

func (s *Store) settleEpochWithoutTxLocked(epoch wire.ProofEpoch) wire.FinalizeEpochResponse {
	accepted := 0
	missed := 0
	totalSlashed := uint64(0)
	repairTasks := make([]wire.RepairTask, 0)
	for _, challengeID := range epoch.ChallengeIDs {
		challenge, ok := s.data.Challenges[challengeID]
		if !ok {
			continue
		}
		if _, ok := s.data.Proofs[challengeID]; ok {
			accepted++
			continue
		}
		missed++
		stats := s.minerStatsLocked(challenge.MinerAddress)
		stats.ProofFailure++
		stats.ConsecutiveFailures++
		if stats.Status == wire.MinerStatusActive && stats.ConsecutiveFailures >= defaultMinerDegradeThreshold {
			stats.Status = wire.MinerStatusDegraded
		}
		if stats.Status == wire.MinerStatusDegraded && stats.ConsecutiveFailures >= defaultMinerDegradeThreshold*2 {
			stats.Status = wire.MinerStatusJailed
		}
		slash := epoch.SlashPerMissedProof
		account := s.accountLocked(challenge.MinerAddress)
		if account.LockedStake < slash {
			slash = account.LockedStake
		}
		account.LockedStake -= slash
		stats.Stake = account.LockedStake
		stats.Slashed += slash
		totalSlashed += slash
		s.addSlashedToRepairPoolLocked(slash)
		if stats.Status == wire.MinerStatusJailed && stats.Stake == 0 {
			stats.Status = wire.MinerStatusExiting
			stats.ExitedAtUnix = time.Now().Add(7 * 24 * time.Hour).Unix()
		}
		s.data.Accounts[account.Address] = account
		s.data.Miners[challenge.MinerAddress] = stats
		if task, ok := s.repairTaskForMissedChallengeLocked(challenge); ok {
			if err := s.applyRepairTasksLocked([]wire.RepairTask{task}); err == nil {
				repairTasks = append(repairTasks, task)
			}
		}
	}
	epoch.Status = "finalized"
	epoch.StorageSlashed = totalSlashed
	epoch.RepairTasksCreated = len(repairTasks)
	retrievalTotal := uint64(0)
	repairTotal := uint64(0)
	for _, miner := range s.data.Miners {
		retrievalTotal = saturatingAdd(retrievalTotal, miner.RetrievalRewards)
		repairTotal = saturatingAdd(repairTotal, miner.RepairRewards)
	}
	epoch.RetrievalRewardsPaid = retrievalTotal
	epoch.RepairRewardsPaid = repairTotal
	s.data.Epochs[epoch.EpochID] = epoch
	s.rotateValidatorsLocked(epoch.EpochRound)
	return wire.FinalizeEpochResponse{
		EpochID:              epoch.EpochID,
		Status:               epoch.Status,
		AcceptedProofs:       accepted,
		MissedProofs:         missed,
		StorageRewardsPaid:   epoch.StorageRewardsPaid,
		RetrievalRewardsPaid: retrievalTotal,
		RepairRewardsPaid:    repairTotal,
		StorageSlashed:       totalSlashed,
		RepairTasksCreated:   len(repairTasks),
	}
}

func validateReceiptForReplay(intent *Intent, receipt wire.MinerReceipt) error {
	if err := validateReceipt(intent, receipt); err != nil {
		if err.Error() != "receipt expired" {
			return err
		}
	}
	return nil
}
