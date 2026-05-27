package chain

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"chain/internal/wire"
)

// Transaction payload types for governance proposal operations.

type governanceCreateProposalTxPayload struct {
	Request  wire.CreateGovernanceProposalRequest  `json:"request"`
	Response wire.CreateGovernanceProposalResponse `json:"response"`
}

type governanceCastVoteTxPayload struct {
	Request  wire.CastGovernanceVoteRequest  `json:"request"`
	Response wire.CastGovernanceVoteResponse `json:"response"`
}

type governanceExecuteProposalTxPayload struct {
	Request  wire.ExecuteGovernanceProposalRequest  `json:"request"`
	Response wire.ExecuteGovernanceProposalResponse `json:"response"`
}

// governanceProposalTTLSeconds is the default time-to-live for pending proposals (7 days).
const governanceProposalTTLSeconds = 7 * 24 * 60 * 60

// governanceClockSkewSeconds is the maximum allowed clock skew for signed timestamps.
const governanceClockSkewSeconds = 5 * 60

// CreateGovernanceProposal creates a new signed governance proposal.
func (s *Store) CreateGovernanceProposal(req wire.CreateGovernanceProposalRequest) (wire.CreateGovernanceProposalResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	proposer := normalizeGovernanceOperator(req.Proposer)
	if proposer == "" {
		return wire.CreateGovernanceProposalResponse{}, errors.New("proposer is required")
	}

	// Verify chain_id to prevent cross-chain replay.
	if req.ChainID != s.data.ChainID {
		return wire.CreateGovernanceProposalResponse{}, errors.New("governance proposal chain_id mismatch")
	}

	// Look up proposer in governance operators.
	operator, ok := s.data.GovernanceOperators[proposer]
	if !ok || !operator.Enabled {
		return wire.CreateGovernanceProposalResponse{}, errors.New("governance operator is not authorized")
	}

	// Check permission.
	if err := s.validateGovernanceOperatorLocked(proposer, req.Action); err != nil {
		return wire.CreateGovernanceProposalResponse{}, err
	}

	// Check public key exists.
	if operator.PublicKey == "" {
		return wire.CreateGovernanceProposalResponse{}, errors.New("governance operator has no public key registered")
	}

	// Verify signature by recovering signer and comparing to proposer address.
	if err := wire.VerifyGovernanceProposal(req, proposer); err != nil {
		return wire.CreateGovernanceProposalResponse{}, err
	}

	// Verify nonce for replay protection.
	expectedNonce := s.data.OperatorNonces[proposer]
	if req.Nonce != expectedNonce {
		return wire.CreateGovernanceProposalResponse{}, errors.New("invalid proposer nonce")
	}

	// Validate clock skew.
	now := time.Now().Unix()
	if abs64(req.CreatedAtUnix-now) > governanceClockSkewSeconds {
		return wire.CreateGovernanceProposalResponse{}, errors.New("proposal timestamp outside acceptable clock skew")
	}

	// Validate action.
	if !validGovernanceAction(req.Action) {
		return wire.CreateGovernanceProposalResponse{}, errors.New("invalid governance action")
	}

	// Validate action-specific fields.
	if err := validateGovernanceActionFields(req.Action, req.ExpiresAtUnix, req.AppealDeadlineUnix, now); err != nil {
		return wire.CreateGovernanceProposalResponse{}, err
	}

	if isOperatorManagementAction(req.Action) {
		// Validate operator management fields.
		if err := validateOperatorManagementFields(req.Action, req.TargetOperator, req.TargetPublicKey, req.TargetPermissions, s.data.GovernanceOperators); err != nil {
			return wire.CreateGovernanceProposalResponse{}, err
		}
	} else if isConfigAction(req.Action) {
		// Validate config change fields.
		if err := validateConfigChangeFields(req); err != nil {
			return wire.CreateGovernanceProposalResponse{}, err
		}
	} else if isMiningParamsAction(req.Action) {
		// Validate mining params change fields.
		if err := validateMiningParamsChangeFields(req, s.miningParamsLocked()); err != nil {
			return wire.CreateGovernanceProposalResponse{}, err
		}
	} else {
		// Validate intent exists for deal actions.
		if _, ok := s.data.Intents[req.IntentID]; !ok {
			return wire.CreateGovernanceProposalResponse{}, errors.New("intent not found")
		}
	}

	// Expire stale proposals.
	s.expireGovernanceProposalsLocked(now)

	// Generate proposal ID.
	proposalID, err := randomID("gov_proposal")
	if err != nil {
		return wire.CreateGovernanceProposalResponse{}, errors.New("failed to generate proposal id")
	}

	proposal := governanceProposalFromRequest(req, proposer, proposalID, req.CreatedAtUnix)

	s.data.GovernanceProposals[proposalID] = proposal
	s.data.GovernanceVotes[proposalID] = []wire.GovernanceVote{}
	s.data.OperatorNonces[proposer] = expectedNonce + 1

	resp := wire.CreateGovernanceProposalResponse{Proposal: proposal}
	s.recordTxLocked("governance_create_proposal", proposer, governanceCreateProposalTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.CreateGovernanceProposalResponse{}, err
	}
	return resp, nil
}

func governanceProposalFromRequest(req wire.CreateGovernanceProposalRequest, proposer, proposalID string, createdAtUnix int64) wire.GovernanceProposal {
	return wire.GovernanceProposal{
		ProposalID:                        proposalID,
		Proposer:                          proposer,
		ProposerSignature:                 req.Signature,
		IntentID:                          req.IntentID,
		Action:                            req.Action,
		ReasonHash:                        req.ReasonHash,
		ExpiresAtUnix:                     req.ExpiresAtUnix,
		PreserveStorage:                   req.PreserveStorage,
		AppealDeadlineUnix:                req.AppealDeadlineUnix,
		TargetOperator:                    req.TargetOperator,
		TargetPublicKey:                   req.TargetPublicKey,
		TargetPermissions:                 req.TargetPermissions,
		TargetDataModerationThresholdNum:  req.TargetDataModerationThresholdNum,
		TargetDataModerationThresholdDen:  req.TargetDataModerationThresholdDen,
		TargetOperatorChangeThresholdNum:  req.TargetOperatorChangeThresholdNum,
		TargetOperatorChangeThresholdDen:  req.TargetOperatorChangeThresholdDen,
		TargetStorageReleaseRateBPS:       req.TargetStorageReleaseRateBPS,
		TargetRetrievalReleaseRateBPS:     req.TargetRetrievalReleaseRateBPS,
		TargetValidatorReleaseRateBPS:     req.TargetValidatorReleaseRateBPS,
		TargetStoredBytesWeightBPS:        req.TargetStoredBytesWeightBPS,
		TargetProofScoreWeightBPS:         req.TargetProofScoreWeightBPS,
		TargetAvailabilityWeightBPS:       req.TargetAvailabilityWeightBPS,
		TargetDecentralizationWeightBPS:   req.TargetDecentralizationWeightBPS,
		TargetRetrievalRewardPerMiB:       req.TargetRetrievalRewardPerMiB,
		TargetMaxRetrievalRewardPerWindow: req.TargetMaxRetrievalRewardPerWindow,
		TargetRepairRewardPerShard:        req.TargetRepairRewardPerShard,
		TargetRepairPoolTakeoverBPS:       req.TargetRepairPoolTakeoverBPS,
		TargetRepairPoolSubsidyBPS:        req.TargetRepairPoolSubsidyBPS,
		TargetMinerDegradeThreshold:       req.TargetMinerDegradeThreshold,
		TargetStorageProofSamples:         req.TargetStorageProofSamples,
		TargetValidatorCommissionBPS:      req.TargetValidatorCommissionBPS,
		TargetRetrievalWeightBPS:          req.TargetRetrievalWeightBPS,
		TargetFoundationReleaseRateBPS:    req.TargetFoundationReleaseRateBPS,
		TargetFoundationAddress:           req.TargetFoundationAddress,
		TargetRetrievalAddress:            req.TargetRetrievalAddress,
		TargetStorageAnnualRateBPS:        req.TargetStorageAnnualRateBPS,
		TargetRetrievalAnnualRateBPS:      req.TargetRetrievalAnnualRateBPS,
		TargetValidatorAnnualRateBPS:      req.TargetValidatorAnnualRateBPS,
		TargetFoundationAnnualRateBPS:     req.TargetFoundationAnnualRateBPS,
		TargetReleaseCoefficientBPS:       req.TargetReleaseCoefficientBPS,
		TargetAvailabilityWindowSize:      req.TargetAvailabilityWindowSize,
		TargetAvailabilityThresholdBPS:    req.TargetAvailabilityThresholdBPS,
		TargetBlockProductionRewardBPS:    req.TargetBlockProductionRewardBPS,
		TargetMaxConsensusValidators:      req.TargetMaxConsensusValidators,
		TargetMinConsensusValidators:      req.TargetMinConsensusValidators,
		TargetBlockBytes:                  req.TargetBlockBytes,
		TargetMaxBlockBytes:               req.TargetMaxBlockBytes,
		TargetMaxBlockTxs:                 req.TargetMaxBlockTxs,
		TargetMaxTxBytes:                  req.TargetMaxTxBytes,
		TargetMaxStorageTxBytes:           req.TargetMaxStorageTxBytes,
		ChainID:                           req.ChainID,
		ProposerNonce:                     req.Nonce,
		Status:                            wire.GovProposalPending,
		CreatedAtUnix:                     createdAtUnix,
	}
}

// CastGovernanceVote casts a signed vote on a pending governance proposal.
func (s *Store) CastGovernanceVote(req wire.CastGovernanceVoteRequest) (wire.CastGovernanceVoteResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	voter := normalizeGovernanceOperator(req.Voter)
	if voter == "" {
		return wire.CastGovernanceVoteResponse{}, errors.New("voter is required")
	}

	// Verify chain_id to prevent cross-chain replay.
	if req.ChainID != s.data.ChainID {
		return wire.CastGovernanceVoteResponse{}, errors.New("governance vote chain_id mismatch")
	}

	// Look up proposal.
	proposal, ok := s.data.GovernanceProposals[req.ProposalID]
	if !ok {
		return wire.CastGovernanceVoteResponse{}, errors.New("proposal not found")
	}
	if proposal.Status != wire.GovProposalPending {
		return wire.CastGovernanceVoteResponse{}, errors.New("proposal is not pending")
	}

	now := time.Now().Unix()

	// Check proposal expiration (7-day TTL).
	if proposal.CreatedAtUnix+governanceProposalTTLSeconds < now {
		proposal.Status = wire.GovProposalExpired
		s.data.GovernanceProposals[req.ProposalID] = proposal
		return wire.CastGovernanceVoteResponse{}, errors.New("proposal has expired")
	}

	// Look up voter.
	operator, ok := s.data.GovernanceOperators[voter]
	if !ok || !operator.Enabled {
		return wire.CastGovernanceVoteResponse{}, errors.New("voter is not an enabled governance operator")
	}
	if operator.PublicKey == "" {
		return wire.CastGovernanceVoteResponse{}, errors.New("voter has no public key registered")
	}

	// Verify signature by recovering signer and comparing to voter address.
	if err := wire.VerifyGovernanceVote(req, voter); err != nil {
		return wire.CastGovernanceVoteResponse{}, err
	}

	// Verify nonce for replay protection.
	expectedVoteNonce := s.data.OperatorNonces[voter]
	if req.Nonce != expectedVoteNonce {
		return wire.CastGovernanceVoteResponse{}, errors.New("invalid voter nonce")
	}

	// Validate clock skew.
	if abs64(req.CreatedAtUnix-now) > governanceClockSkewSeconds {
		return wire.CastGovernanceVoteResponse{}, errors.New("vote timestamp outside acceptable clock skew")
	}

	// Check double voting.
	votes := s.data.GovernanceVotes[req.ProposalID]
	for _, v := range votes {
		if normalizeGovernanceOperator(v.Voter) == voter {
			return wire.CastGovernanceVoteResponse{}, errors.New("voter has already voted on this proposal")
		}
	}

	// Record vote.
	vote := wire.GovernanceVote{
		ProposalID:     req.ProposalID,
		Voter:          voter,
		VoterSignature: req.Signature,
		Approve:        req.Approve,
		CreatedAtUnix:  req.CreatedAtUnix,
	}
	s.data.GovernanceVotes[req.ProposalID] = append(s.data.GovernanceVotes[req.ProposalID], vote)
	s.data.OperatorNonces[voter] = expectedVoteNonce + 1

	// Count votes.
	approveCount, rejectCount := s.countGovernanceVotesLocked(req.ProposalID)
	threshold := s.governanceThresholdLocked(proposal.Action)

	// Auto-reject if threshold unreachable.
	totalEnabled := s.countEnabledOperatorsLocked()
	remaining := totalEnabled - approveCount - rejectCount
	executed := false
	if approveCount >= threshold {
		// Threshold met — auto-execute.
		execResult, err := s.executeGovernanceProposalLocked(proposal, req.CreatedAtUnix)
		if err == nil {
			executed = true
			_ = execResult // recorded via governanceDealActionLocked
		}
	} else if approveCount+remaining < threshold {
		// Threshold unreachable — auto-reject.
		proposal.Status = wire.GovProposalRejected
		s.data.GovernanceProposals[req.ProposalID] = proposal
	}

	resp := wire.CastGovernanceVoteResponse{
		Vote:         vote,
		ApproveCount: approveCount,
		RejectCount:  rejectCount,
		Threshold:    threshold,
		Executed:     executed,
	}
	s.recordTxLocked("governance_cast_vote", voter, governanceCastVoteTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.CastGovernanceVoteResponse{}, err
	}
	return resp, nil
}

// ExecuteGovernanceProposal executes a proposal that has reached the approval threshold.
// The caller must be an enabled governance operator and provide a valid signature.
// The original proposal signature is also re-verified to prevent tampering.
func (s *Store) ExecuteGovernanceProposal(req wire.ExecuteGovernanceProposalRequest) (wire.ExecuteGovernanceProposalResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	executor := normalizeGovernanceOperator(req.Executor)
	if executor == "" {
		return wire.ExecuteGovernanceProposalResponse{}, errors.New("executor is required")
	}

	// Verify chain_id to prevent cross-chain replay.
	if req.ChainID != s.data.ChainID {
		return wire.ExecuteGovernanceProposalResponse{}, errors.New("governance execute chain_id mismatch")
	}

	// Verify executor is an enabled governance operator.
	operator, ok := s.data.GovernanceOperators[executor]
	if !ok || !operator.Enabled {
		return wire.ExecuteGovernanceProposalResponse{}, errors.New("executor is not an enabled governance operator")
	}
	if operator.PublicKey == "" {
		return wire.ExecuteGovernanceProposalResponse{}, errors.New("executor has no public key registered")
	}

	// Verify execute request signature.
	if err := wire.VerifyGovernanceExecute(req, executor); err != nil {
		return wire.ExecuteGovernanceProposalResponse{}, err
	}

	// Verify nonce for replay protection.
	expectedExecNonce := s.data.OperatorNonces[executor]
	if req.Nonce != expectedExecNonce {
		return wire.ExecuteGovernanceProposalResponse{}, errors.New("invalid executor nonce")
	}

	// Validate clock skew.
	now := time.Now().Unix()
	if abs64(req.CreatedAtUnix-now) > governanceClockSkewSeconds {
		return wire.ExecuteGovernanceProposalResponse{}, errors.New("execute timestamp outside acceptable clock skew")
	}

	proposal, ok := s.data.GovernanceProposals[req.ProposalID]
	if !ok {
		return wire.ExecuteGovernanceProposalResponse{}, errors.New("proposal not found")
	}
	if proposal.Status != wire.GovProposalPending {
		return wire.ExecuteGovernanceProposalResponse{}, errors.New("proposal is not pending")
	}

	// Check expiration.
	if proposal.CreatedAtUnix+governanceProposalTTLSeconds < now {
		proposal.Status = wire.GovProposalExpired
		s.data.GovernanceProposals[req.ProposalID] = proposal
		return wire.ExecuteGovernanceProposalResponse{}, errors.New("proposal has expired")
	}

	// Re-verify original proposal signature to prevent tampering.
	originalReq := proposalToCreateRequest(proposal)
	if err := wire.VerifyGovernanceProposal(originalReq, normalizeGovernanceOperator(proposal.Proposer)); err != nil {
		return wire.ExecuteGovernanceProposalResponse{}, errors.New("proposal signature re-verification failed: " + err.Error())
	}

	execResult, err := s.executeGovernanceProposalLocked(proposal, req.CreatedAtUnix)
	if err != nil {
		return wire.ExecuteGovernanceProposalResponse{}, err
	}

	s.data.OperatorNonces[executor] = expectedExecNonce + 1

	resp := wire.ExecuteGovernanceProposalResponse{
		Proposal:         s.data.GovernanceProposals[req.ProposalID],
		GovernanceResult: execResult,
	}
	s.recordTxLocked("governance_execute_proposal", executor, governanceExecuteProposalTxPayload{Request: req, Response: resp})
	if err := s.saveLocked(); err != nil {
		return wire.ExecuteGovernanceProposalResponse{}, err
	}
	return resp, nil
}

// executeGovernanceProposalLocked is the internal helper that validates threshold and
// executes the governance action. Must be called with s.mu held.
func (s *Store) executeGovernanceProposalLocked(proposal wire.GovernanceProposal, now int64) (wire.GovernanceDealActionResponse, error) {
	// Re-validate each voter is still an enabled operator.
	approveCount := 0
	votes := s.data.GovernanceVotes[proposal.ProposalID]
	for _, v := range votes {
		if !v.Approve {
			continue
		}
		voterAddr := normalizeGovernanceOperator(v.Voter)
		op, ok := s.data.GovernanceOperators[voterAddr]
		if !ok || !op.Enabled {
			continue // voter no longer enabled, exclude
		}
		approveCount++
	}

	threshold := s.governanceThresholdLocked(proposal.Action)
	if approveCount < threshold {
		return wire.GovernanceDealActionResponse{}, errors.New("insufficient approval votes: have " +
			strconv.Itoa(approveCount) + " need " + strconv.Itoa(threshold))
	}

	// Mark proposal as executed.
	proposal.Status = wire.GovProposalExecuted
	s.data.GovernanceProposals[proposal.ProposalID] = proposal

	// Route operator management actions to dedicated handler.
	if isOperatorManagementAction(proposal.Action) {
		return s.executeOperatorManagementLocked(proposal, now)
	}

	// Route config change actions to dedicated handler.
	if isConfigAction(proposal.Action) {
		return s.executeConfigChangeLocked(proposal, now)
	}

	// Route mining params change actions to dedicated handler.
	if isMiningParamsAction(proposal.Action) {
		return s.executeMiningParamsChangeLocked(proposal, now)
	}

	// Execute the deal action using the existing internal engine.
	govReq := wire.GovernanceDealActionRequest{
		IntentID:           proposal.IntentID,
		Operator:           proposal.Proposer,
		Action:             proposal.Action,
		ReasonHash:         proposal.ReasonHash,
		ExpiresAtUnix:      proposal.ExpiresAtUnix,
		PreserveStorage:    proposal.PreserveStorage,
		AppealDeadlineUnix: proposal.AppealDeadlineUnix,
	}
	return s.governanceDealActionLocked(govReq, now)
}

// executeOperatorManagementLocked handles add/remove/update operator actions.
// Must be called with s.mu held.
func (s *Store) executeOperatorManagementLocked(proposal wire.GovernanceProposal, now int64) (wire.GovernanceDealActionResponse, error) {
	switch proposal.Action {
	case "add_operator":
		// Derive operator address from ECDSA public key.
		key := wire.GovernanceOperatorAddress(proposal.TargetPublicKey)
		if key == "" {
			return wire.GovernanceDealActionResponse{}, errors.New("invalid target public key")
		}
		// If TargetOperator was provided, validate it matches the derived address.
		if proposal.TargetOperator != "" {
			specified := normalizeGovernanceOperator(proposal.TargetOperator)
			if specified != "" && !strings.EqualFold(specified, key) {
				return wire.GovernanceDealActionResponse{}, errors.New("target_operator does not match derived address from public key")
			}
		}
		permissions := proposal.TargetPermissions
		if permissions == nil {
			permissions = []string{}
		}
		s.data.GovernanceOperators[key] = wire.GovernanceOperator{
			Operator:      key,
			PublicKey:     proposal.TargetPublicKey,
			Permissions:   permissions,
			Enabled:       true,
			CreatedAtUnix: now,
		}
	case "remove_operator":
		key := normalizeGovernanceOperator(proposal.TargetOperator)
		if key == "" {
			return wire.GovernanceDealActionResponse{}, errors.New("invalid target operator")
		}
		op, ok := s.data.GovernanceOperators[key]
		if !ok || !op.Enabled {
			return wire.GovernanceDealActionResponse{}, errors.New("operator not found or already disabled")
		}
		op.Enabled = false
		s.data.GovernanceOperators[key] = op
	case "update_operator":
		key := normalizeGovernanceOperator(proposal.TargetOperator)
		if key == "" {
			return wire.GovernanceDealActionResponse{}, errors.New("invalid target operator")
		}
		op, ok := s.data.GovernanceOperators[key]
		if !ok || !op.Enabled {
			return wire.GovernanceDealActionResponse{}, errors.New("operator not found or not enabled")
		}
		// Key rotation is not allowed via update_operator because address = f(pubkey).
		// Use remove_operator + add_operator for key rotation.
		if proposal.TargetPublicKey != "" && proposal.TargetPublicKey != op.PublicKey {
			return wire.GovernanceDealActionResponse{}, errors.New("key rotation requires remove_operator + add_operator")
		}
		if len(proposal.TargetPermissions) > 0 {
			op.Permissions = append([]string(nil), proposal.TargetPermissions...)
		}
		s.data.GovernanceOperators[key] = op
	default:
		return wire.GovernanceDealActionResponse{}, errors.New("invalid operator management action")
	}

	return wire.GovernanceDealActionResponse{
		GovernanceType: "governance_" + proposal.Action,
		UpdatedAtUnix:  now,
	}, nil
}

// executeConfigChangeLocked handles update_config actions.
// Must be called with s.mu held.
func (s *Store) executeConfigChangeLocked(proposal wire.GovernanceProposal, now int64) (wire.GovernanceDealActionResponse, error) {
	if proposal.TargetDataModerationThresholdNum > 0 && proposal.TargetDataModerationThresholdDen > 0 {
		s.data.DataModerationThresholdNum = proposal.TargetDataModerationThresholdNum
		s.data.DataModerationThresholdDen = proposal.TargetDataModerationThresholdDen
	}
	if proposal.TargetOperatorChangeThresholdNum > 0 && proposal.TargetOperatorChangeThresholdDen > 0 {
		s.data.OperatorChangeThresholdNum = proposal.TargetOperatorChangeThresholdNum
		s.data.OperatorChangeThresholdDen = proposal.TargetOperatorChangeThresholdDen
	}
	if proposal.TargetFoundationAddress != "" {
		s.data.FoundationAddress = wire.NormalizeAddress(proposal.TargetFoundationAddress)
	}
	if proposal.TargetRetrievalAddress != "" {
		s.data.RetrievalAddress = wire.NormalizeAddress(proposal.TargetRetrievalAddress)
	}

	return wire.GovernanceDealActionResponse{
		GovernanceType: "governance_update_config",
		UpdatedAtUnix:  now,
	}, nil
}

// executeMiningParamsChangeLocked handles update_mining_params actions.
// Must be called with s.mu held.
func (s *Store) executeMiningParamsChangeLocked(proposal wire.GovernanceProposal, now int64) (wire.GovernanceDealActionResponse, error) {
	if s.data.MiningParams == nil {
		defaults := DefaultMiningParams()
		s.data.MiningParams = &defaults
	}
	p := s.data.MiningParams

	applyIfNonZero(&p.StorageReleaseRateBPS, proposal.TargetStorageReleaseRateBPS)
	applyIfNonZero(&p.RetrievalReleaseRateBPS, proposal.TargetRetrievalReleaseRateBPS)
	applyIfNonZero(&p.ValidatorReleaseRateBPS, proposal.TargetValidatorReleaseRateBPS)
	applyIfNonZero(&p.FoundationReleaseRateBPS, proposal.TargetFoundationReleaseRateBPS)
	applyIfNonZero(&p.StoredBytesWeightBPS, proposal.TargetStoredBytesWeightBPS)
	applyIfNonZero(&p.ProofScoreWeightBPS, proposal.TargetProofScoreWeightBPS)
	applyIfNonZero(&p.AvailabilityWeightBPS, proposal.TargetAvailabilityWeightBPS)
	applyIfNonZero(&p.DecentralizationWeightBPS, proposal.TargetDecentralizationWeightBPS)
	applyIfNonZero(&p.RetrievalRewardPerMiB, proposal.TargetRetrievalRewardPerMiB)
	applyIfNonZero(&p.MaxRetrievalRewardPerWindow, proposal.TargetMaxRetrievalRewardPerWindow)
	applyIfNonZero(&p.RepairRewardPerShard, proposal.TargetRepairRewardPerShard)
	applyIfNonZero(&p.RepairPoolTakeoverBPS, proposal.TargetRepairPoolTakeoverBPS)
	applyIfNonZero(&p.RepairPoolSubsidyBPS, proposal.TargetRepairPoolSubsidyBPS)
	applyIfNonZero(&p.MinerDegradeThreshold, proposal.TargetMinerDegradeThreshold)
	if proposal.TargetStorageProofSamples > 0 {
		p.StorageProofSamples = proposal.TargetStorageProofSamples
	}
	applyIfNonZero(&p.ValidatorCommissionBPS, proposal.TargetValidatorCommissionBPS)
	applyIfNonZero(&p.RetrievalWeightBPS, proposal.TargetRetrievalWeightBPS)
	applyIfNonZero(&p.StorageAnnualRateBPS, proposal.TargetStorageAnnualRateBPS)
	applyIfNonZero(&p.RetrievalAnnualRateBPS, proposal.TargetRetrievalAnnualRateBPS)
	applyIfNonZero(&p.ValidatorAnnualRateBPS, proposal.TargetValidatorAnnualRateBPS)
	applyIfNonZero(&p.FoundationAnnualRateBPS, proposal.TargetFoundationAnnualRateBPS)
	applyIfNonZero(&p.ReleaseCoefficientBPS, proposal.TargetReleaseCoefficientBPS)
	applyIfNonZero(&p.AvailabilityWindowSize, proposal.TargetAvailabilityWindowSize)
	applyIfNonZero(&p.AvailabilityThresholdBPS, proposal.TargetAvailabilityThresholdBPS)
	applyIfNonZero(&p.BlockProductionRewardBPS, proposal.TargetBlockProductionRewardBPS)
	applyIfNonZero(&p.MaxConsensusValidators, proposal.TargetMaxConsensusValidators)
	applyIfNonZero(&p.MinConsensusValidators, proposal.TargetMinConsensusValidators)
	applyIfNonZero(&p.TargetBlockBytes, proposal.TargetBlockBytes)
	applyIfNonZero(&p.MaxBlockBytes, proposal.TargetMaxBlockBytes)
	applyIfNonZero(&p.MaxBlockTxs, proposal.TargetMaxBlockTxs)
	applyIfNonZero(&p.MaxTxBytes, proposal.TargetMaxTxBytes)
	applyIfNonZero(&p.MaxStorageTxBytes, proposal.TargetMaxStorageTxBytes)

	// Post-application bounds validation (defense-in-depth: rejects proposals
	// created before bounds were enforced).
	weightSum := p.StoredBytesWeightBPS + p.ProofScoreWeightBPS + p.AvailabilityWeightBPS + p.DecentralizationWeightBPS
	if weightSum > maxWeightBPSSum {
		return wire.GovernanceDealActionResponse{}, fmt.Errorf("mining params: weight BPS sum %d exceeds maximum %d", weightSum, maxWeightBPSSum)
	}
	for _, check := range []struct {
		name  string
		value uint64
		max   uint64
	}{
		{"storage_annual_rate_bps", p.StorageAnnualRateBPS, maxAnnualReleaseRateBPS},
		{"retrieval_annual_rate_bps", p.RetrievalAnnualRateBPS, maxAnnualReleaseRateBPS},
		{"validator_annual_rate_bps", p.ValidatorAnnualRateBPS, maxAnnualReleaseRateBPS},
		{"foundation_annual_rate_bps", p.FoundationAnnualRateBPS, maxAnnualReleaseRateBPS},
	} {
		if check.value > check.max {
			return wire.GovernanceDealActionResponse{}, fmt.Errorf("mining params: %s %d exceeds maximum %d", check.name, check.value, check.max)
		}
	}
	if p.ReleaseCoefficientBPS != 0 && (p.ReleaseCoefficientBPS < minReleaseCoefficientBPS || p.ReleaseCoefficientBPS > maxReleaseCoefficientBPS) {
		return wire.GovernanceDealActionResponse{}, fmt.Errorf("mining params: release_coefficient_bps %d out of range [%d, %d]", p.ReleaseCoefficientBPS, minReleaseCoefficientBPS, maxReleaseCoefficientBPS)
	}
	if p.StorageProofSamples < minStorageProofSamples || p.StorageProofSamples > maxStorageProofSamples {
		return wire.GovernanceDealActionResponse{}, fmt.Errorf("mining params: storage_proof_samples %d out of range [%d, %d]", p.StorageProofSamples, minStorageProofSamples, maxStorageProofSamples)
	}
	if p.MinerDegradeThreshold < minMinerDegradeThreshold || p.MinerDegradeThreshold > maxMinerDegradeThreshold {
		return wire.GovernanceDealActionResponse{}, fmt.Errorf("mining params: miner_degrade_threshold %d out of range [%d, %d]", p.MinerDegradeThreshold, minMinerDegradeThreshold, maxMinerDegradeThreshold)
	}

	return wire.GovernanceDealActionResponse{
		GovernanceType: "governance_update_mining_params",
		UpdatedAtUnix:  now,
	}, nil
}

// CancelGovernanceProposal allows the proposer to cancel their own pending proposal.
func (s *Store) CancelGovernanceProposal(req wire.CreateGovernanceProposalRequest) (wire.CreateGovernanceProposalResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	proposer := normalizeGovernanceOperator(req.Proposer)
	if proposer == "" {
		return wire.CreateGovernanceProposalResponse{}, errors.New("proposer is required")
	}

	// Look up the proposal by matching fields (find pending proposal by proposer).
	var proposalID string
	for id, p := range s.data.GovernanceProposals {
		if p.Status != wire.GovProposalPending || normalizeGovernanceOperator(p.Proposer) != proposer {
			continue
		}
		if p.Action != req.Action {
			continue
		}
		if isOperatorManagementAction(p.Action) {
			// Match operator management proposals by target_operator.
			if normalizeGovernanceOperator(p.TargetOperator) == normalizeGovernanceOperator(req.TargetOperator) {
				proposalID = id
				break
			}
		} else if isConfigAction(p.Action) || isMiningParamsAction(p.Action) {
			// Config and mining params proposals have no intent_id; match by action.
			proposalID = id
			break
		} else {
			// Match deal action proposals by intent_id.
			if p.IntentID == req.IntentID {
				proposalID = id
				break
			}
		}
	}
	if proposalID == "" {
		return wire.CreateGovernanceProposalResponse{}, errors.New("no matching pending proposal found")
	}

	proposal := s.data.GovernanceProposals[proposalID]

	// Verify the cancellation signature (same key as creation).
	if _, ok := s.data.GovernanceOperators[proposer]; !ok {
		return wire.CreateGovernanceProposalResponse{}, errors.New("proposer is not a governance operator")
	}
	if err := wire.VerifyGovernanceProposal(req, proposer); err != nil {
		return wire.CreateGovernanceProposalResponse{}, err
	}

	proposal.Status = wire.GovProposalCancelled
	s.data.GovernanceProposals[proposalID] = proposal

	resp := wire.CreateGovernanceProposalResponse{Proposal: proposal}
	if err := s.saveLocked(); err != nil {
		return wire.CreateGovernanceProposalResponse{}, err
	}
	return resp, nil
}

// GovernanceProposals lists proposals with optional status and intent filtering.
func (s *Store) GovernanceProposals(status, intentID string) wire.GovernanceProposalListResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	var proposals []wire.GovernanceProposal
	votesMap := make(map[string][]wire.GovernanceVote)

	// Collect proposal IDs for sorted iteration.
	ids := make([]string, 0, len(s.data.GovernanceProposals))
	for id := range s.data.GovernanceProposals {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		p := s.data.GovernanceProposals[id]

		// Apply filters.
		if status != "" && p.Status != status {
			continue
		}
		if intentID != "" && p.IntentID != intentID {
			continue
		}

		// Lazy expiration check for pending proposals.
		if p.Status == wire.GovProposalPending && p.CreatedAtUnix+governanceProposalTTLSeconds < now {
			p.Status = wire.GovProposalExpired
			s.data.GovernanceProposals[id] = p
		}

		proposals = append(proposals, p)
		if v, ok := s.data.GovernanceVotes[id]; ok && len(v) > 0 {
			votesMap[id] = v
		}
	}

	if proposals == nil {
		proposals = []wire.GovernanceProposal{}
	}
	return wire.GovernanceProposalListResponse{
		Proposals: proposals,
		Votes:     votesMap,
	}
}

// GovernanceOperators lists all governance operators and the current threshold.
func (s *Store) GovernanceOperators() wire.GovernanceOperatorListResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	operators := make([]wire.GovernanceOperator, 0, len(s.data.GovernanceOperators))
	for _, op := range s.data.GovernanceOperators {
		op.Nonce = s.data.OperatorNonces[normalizeGovernanceOperator(op.Operator)]
		operators = append(operators, op)
	}
	sort.Slice(operators, func(i, j int) bool {
		return operators[i].Operator < operators[j].Operator
	})

	return wire.GovernanceOperatorListResponse{
		Operators:                  operators,
		DataModerationThreshold:    s.governanceThresholdLocked("freeze"),
		OperatorChangeThreshold:    s.governanceThresholdLocked("add_operator"),
		DataModerationThresholdNum: s.data.DataModerationThresholdNum,
		DataModerationThresholdDen: s.data.DataModerationThresholdDen,
		OperatorChangeThresholdNum: s.data.OperatorChangeThresholdNum,
		OperatorChangeThresholdDen: s.data.OperatorChangeThresholdDen,
	}
}

// ── Internal helpers ──

// expireGovernanceProposalsLocked marks expired pending proposals.
func (s *Store) expireGovernanceProposalsLocked(now int64) {
	for id, p := range s.data.GovernanceProposals {
		if p.Status == wire.GovProposalPending && p.CreatedAtUnix+governanceProposalTTLSeconds < now {
			p.Status = wire.GovProposalExpired
			s.data.GovernanceProposals[id] = p
		}
	}
}

// countGovernanceVotesLocked counts valid approval and rejection votes for a proposal.
// Only votes from currently enabled operators are counted.
func (s *Store) countGovernanceVotesLocked(proposalID string) (approve, reject int) {
	votes := s.data.GovernanceVotes[proposalID]
	for _, v := range votes {
		voterAddr := normalizeGovernanceOperator(v.Voter)
		op, ok := s.data.GovernanceOperators[voterAddr]
		if !ok || !op.Enabled {
			continue
		}
		if v.Approve {
			approve++
		} else {
			reject++
		}
	}
	return
}

// countEnabledOperatorsLocked returns the number of enabled governance operators.
func (s *Store) countEnabledOperatorsLocked() int {
	count := 0
	for _, op := range s.data.GovernanceOperators {
		if op.Enabled {
			count++
		}
	}
	return count
}

// governanceThresholdLocked computes the BFT threshold for enabled operators based on action type.
// Data moderation actions use DataModerationThreshold (default 1/3).
// Operator/config changes use OperatorChangeThreshold (default 2/3).
func (s *Store) governanceThresholdLocked(action string) int {
	n := s.countEnabledOperatorsLocked()
	if n == 0 {
		return 0
	}
	num := s.data.DataModerationThresholdNum
	den := s.data.DataModerationThresholdDen
	if isOperatorManagementAction(action) || isConfigAction(action) || isMiningParamsAction(action) {
		num = s.data.OperatorChangeThresholdNum
		den = s.data.OperatorChangeThresholdDen
	}
	if den <= 0 {
		return n // fallback: require unanimous
	}
	// ceiling(n * num / den)
	return (n*num + den - 1) / den
}

// validGovernanceAction checks if the action string is valid.
func validGovernanceAction(action string) bool {
	switch action {
	case "freeze", "block", "legal_hold", "appeal",
		"add_operator", "remove_operator", "update_operator",
		"update_config", "update_mining_params":
		return true
	}
	return false
}

// isOperatorManagementAction returns true for actions that manage governance operators.
func isOperatorManagementAction(action string) bool {
	switch action {
	case "add_operator", "remove_operator", "update_operator":
		return true
	}
	return false
}

// isConfigAction returns true for actions that change governance configuration.
func isConfigAction(action string) bool {
	return action == "update_config"
}

// isMiningParamsAction returns true for actions that change mining parameters.
func isMiningParamsAction(action string) bool {
	return action == "update_mining_params"
}

// validateGovernanceActionFields validates action-specific fields.
func validateGovernanceActionFields(action string, expiresAtUnix, appealDeadlineUnix, now int64) error {
	switch action {
	case "freeze":
		if expiresAtUnix <= now {
			return errors.New("freeze action requires a future expires_at_unix")
		}
	case "block":
		if appealDeadlineUnix > 0 && appealDeadlineUnix <= now {
			return errors.New("block action requires appeal_deadline_unix to be in the future")
		}
	}
	return nil
}

// validateOperatorManagementFields validates fields specific to operator management actions.
func validateOperatorManagementFields(action string, targetOperator, targetPublicKey string, targetPermissions []string, operators map[string]wire.GovernanceOperator) error {
	switch action {
	case "add_operator":
		if targetPublicKey == "" {
			return errors.New("add_operator requires target_public_key")
		}
		// Derive address from ECDSA public key and check for duplicates.
		derivedAddr := wire.GovernanceOperatorAddress(targetPublicKey)
		if derivedAddr == "" {
			return errors.New("add_operator target_public_key is not a valid ECDSA public key")
		}
		if existing, ok := operators[derivedAddr]; ok && existing.Enabled {
			return errors.New("operator already exists and is enabled")
		}
		// If TargetOperator was provided, validate it matches the derived address.
		if targetOperator != "" {
			specified := normalizeGovernanceOperator(targetOperator)
			if specified != "" && !strings.EqualFold(specified, derivedAddr) {
				return errors.New("target_operator does not match derived address from public key")
			}
		}
	case "remove_operator":
		if targetOperator == "" {
			return errors.New("remove_operator requires target_operator")
		}
		key := normalizeGovernanceOperator(targetOperator)
		existing, ok := operators[key]
		if !ok || !existing.Enabled {
			return errors.New("operator not found or already disabled")
		}
	case "update_operator":
		if targetOperator == "" {
			return errors.New("update_operator requires target_operator")
		}
		if len(targetPermissions) == 0 {
			return errors.New("update_operator requires target_permissions (key rotation requires remove_operator + add_operator)")
		}
		key := normalizeGovernanceOperator(targetOperator)
		existing, ok := operators[key]
		if !ok || !existing.Enabled {
			return errors.New("operator not found or not enabled")
		}
		// Reject key rotation via update_operator.
		if targetPublicKey != "" && targetPublicKey != existing.PublicKey {
			return errors.New("key rotation requires remove_operator + add_operator")
		}
	}
	return nil
}

// validateConfigChangeFields validates fields specific to update_config actions.
func validateConfigChangeFields(req wire.CreateGovernanceProposalRequest) error {
	hasDataMod := req.TargetDataModerationThresholdNum > 0 && req.TargetDataModerationThresholdDen > 0
	hasOpChange := req.TargetOperatorChangeThresholdNum > 0 && req.TargetOperatorChangeThresholdDen > 0
	hasFoundationAddr := req.TargetFoundationAddress != ""
	hasRetrievalAddr := req.TargetRetrievalAddress != ""
	if !hasDataMod && !hasOpChange && !hasFoundationAddr && !hasRetrievalAddr {
		return errors.New("update_config requires at least one threshold pair (num/den), target_foundation_address, or target_retrieval_address")
	}
	if hasDataMod {
		if req.TargetDataModerationThresholdNum > req.TargetDataModerationThresholdDen {
			return errors.New("data moderation threshold numerator cannot exceed denominator")
		}
		if req.TargetDataModerationThresholdDen <= 0 {
			return errors.New("data moderation threshold denominator must be positive")
		}
	}
	if hasOpChange {
		if req.TargetOperatorChangeThresholdNum > req.TargetOperatorChangeThresholdDen {
			return errors.New("operator change threshold numerator cannot exceed denominator")
		}
		if req.TargetOperatorChangeThresholdDen <= 0 {
			return errors.New("operator change threshold denominator must be positive")
		}
	}
	return nil
}

// validateMiningParamsChangeFields validates fields specific to update_mining_params actions.
// At least one target field must be non-zero. currentParams is used for weight sum validation
// when only some weight fields are being updated.
func validateMiningParamsChangeFields(req wire.CreateGovernanceProposalRequest, currentParams *MiningParams) error {
	if req.TargetStorageReleaseRateBPS != 0 ||
		req.TargetRetrievalReleaseRateBPS != 0 ||
		req.TargetValidatorReleaseRateBPS != 0 ||
		req.TargetFoundationReleaseRateBPS != 0 ||
		req.TargetStoredBytesWeightBPS != 0 ||
		req.TargetProofScoreWeightBPS != 0 ||
		req.TargetAvailabilityWeightBPS != 0 ||
		req.TargetDecentralizationWeightBPS != 0 ||
		req.TargetRetrievalRewardPerMiB != 0 ||
		req.TargetMaxRetrievalRewardPerWindow != 0 ||
		req.TargetRepairRewardPerShard != 0 ||
		req.TargetRepairPoolTakeoverBPS != 0 ||
		req.TargetRepairPoolSubsidyBPS != 0 ||
		req.TargetMinerDegradeThreshold != 0 ||
		req.TargetStorageProofSamples != 0 ||
		req.TargetValidatorCommissionBPS != 0 ||
		req.TargetRetrievalWeightBPS != 0 ||
		req.TargetStorageAnnualRateBPS != 0 ||
		req.TargetRetrievalAnnualRateBPS != 0 ||
		req.TargetValidatorAnnualRateBPS != 0 ||
		req.TargetFoundationAnnualRateBPS != 0 ||
		req.TargetReleaseCoefficientBPS != 0 ||
		req.TargetAvailabilityWindowSize != 0 ||
		req.TargetAvailabilityThresholdBPS != 0 ||
		req.TargetBlockProductionRewardBPS != 0 ||
		req.TargetMaxConsensusValidators != 0 ||
		req.TargetMinConsensusValidators != 0 ||
		req.TargetBlockBytes != 0 ||
		req.TargetMaxBlockBytes != 0 ||
		req.TargetMaxBlockTxs != 0 ||
		req.TargetMaxTxBytes != 0 ||
		req.TargetMaxStorageTxBytes != 0 {
		return validateMiningParamBounds(req, currentParams)
	}
	return errors.New("update_mining_params requires at least one non-zero target field")
}

func validateMiningParamSizeTargets(req wire.CreateGovernanceProposalRequest) error {
	targetBlockBytes := nonZeroOr(req.TargetBlockBytes, defaultTargetBlockBytes)
	maxBlockBytes := nonZeroOr(req.TargetMaxBlockBytes, defaultMaxBlockBytes)
	maxBlockTxs := nonZeroOr(req.TargetMaxBlockTxs, defaultMaxBlockTxs)
	maxTxBytes := nonZeroOr(req.TargetMaxTxBytes, defaultMaxTxBytes)
	maxStorageTxBytes := nonZeroOr(req.TargetMaxStorageTxBytes, defaultMaxStorageTxBytes)
	if targetBlockBytes < defaultBlockSizeHeadroom {
		return errors.New("target block bytes is too small")
	}
	if targetBlockBytes > maxBlockBytes {
		return errors.New("target block bytes cannot exceed max block bytes")
	}
	if maxBlockBytes < defaultBlockSizeHeadroom {
		return errors.New("max block bytes is too small")
	}
	if maxBlockBytes > defaultMaxBlockBytes {
		return errors.New("max block bytes exceeds node transport limit")
	}
	if maxBlockTxs == 0 {
		return errors.New("max block txs must be positive")
	}
	if maxTxBytes == 0 || maxStorageTxBytes == 0 {
		return errors.New("transaction byte limits must be positive")
	}
	if maxTxBytes > maxBlockBytes || maxStorageTxBytes > maxBlockBytes {
		return errors.New("transaction byte limits cannot exceed max block bytes")
	}
	return nil
}

// validateMiningParamBounds validates all mining parameter bounds including
// block size limits, economic parameters, and cross-parameter invariants.
func validateMiningParamBounds(req wire.CreateGovernanceProposalRequest, currentParams *MiningParams) error {
	// Block size validation (existing).
	if err := validateMiningParamSizeTargets(req); err != nil {
		return err
	}

	// Annual release rates: each non-zero rate must be <= maxAnnualReleaseRateBPS.
	for _, check := range []struct {
		name  string
		value uint64
	}{
		{"storage_annual_rate_bps", req.TargetStorageAnnualRateBPS},
		{"retrieval_annual_rate_bps", req.TargetRetrievalAnnualRateBPS},
		{"validator_annual_rate_bps", req.TargetValidatorAnnualRateBPS},
		{"foundation_annual_rate_bps", req.TargetFoundationAnnualRateBPS},
	} {
		if check.value != 0 && check.value > maxAnnualReleaseRateBPS {
			return fmt.Errorf("%s exceeds maximum %d BPS", check.name, maxAnnualReleaseRateBPS)
		}
	}

	// Release coefficient: if non-zero, must be in [min, max].
	if req.TargetReleaseCoefficientBPS != 0 {
		if req.TargetReleaseCoefficientBPS < minReleaseCoefficientBPS {
			return fmt.Errorf("release_coefficient_bps must be >= %d", minReleaseCoefficientBPS)
		}
		if req.TargetReleaseCoefficientBPS > maxReleaseCoefficientBPS {
			return fmt.Errorf("release_coefficient_bps must be <= %d", maxReleaseCoefficientBPS)
		}
	}

	// Weight BPS sum: use proposed values where non-zero, fall back to current.
	if currentParams != nil && (req.TargetStoredBytesWeightBPS != 0 || req.TargetProofScoreWeightBPS != 0 ||
		req.TargetAvailabilityWeightBPS != 0 || req.TargetDecentralizationWeightBPS != 0) {
		sb := nonZeroOr(req.TargetStoredBytesWeightBPS, currentParams.StoredBytesWeightBPS)
		ps := nonZeroOr(req.TargetProofScoreWeightBPS, currentParams.ProofScoreWeightBPS)
		av := nonZeroOr(req.TargetAvailabilityWeightBPS, currentParams.AvailabilityWeightBPS)
		dc := nonZeroOr(req.TargetDecentralizationWeightBPS, currentParams.DecentralizationWeightBPS)
		if sb+ps+av+dc > maxWeightBPSSum {
			return fmt.Errorf("weight BPS sum %d exceeds maximum %d", sb+ps+av+dc, maxWeightBPSSum)
		}
	}

	// Individual BPS parameters.
	for _, check := range []struct {
		name  string
		value uint64
	}{
		{"stored_bytes_weight_bps", req.TargetStoredBytesWeightBPS},
		{"proof_score_weight_bps", req.TargetProofScoreWeightBPS},
		{"availability_weight_bps", req.TargetAvailabilityWeightBPS},
		{"decentralization_weight_bps", req.TargetDecentralizationWeightBPS},
		{"validator_commission_bps", req.TargetValidatorCommissionBPS},
		{"block_production_reward_bps", req.TargetBlockProductionRewardBPS},
		{"availability_threshold_bps", req.TargetAvailabilityThresholdBPS},
		{"retrieval_weight_bps", req.TargetRetrievalWeightBPS},
		{"repair_pool_takeover_bps", req.TargetRepairPoolTakeoverBPS},
		{"repair_pool_subsidy_bps", req.TargetRepairPoolSubsidyBPS},
	} {
		if check.value != 0 && check.value > maxWeightBPSSum {
			return fmt.Errorf("%s exceeds maximum %d BPS", check.name, maxWeightBPSSum)
		}
	}

	// Storage proof samples.
	if req.TargetStorageProofSamples != 0 {
		if req.TargetStorageProofSamples < minStorageProofSamples {
			return fmt.Errorf("storage_proof_samples must be >= %d", minStorageProofSamples)
		}
		if req.TargetStorageProofSamples > maxStorageProofSamples {
			return fmt.Errorf("storage_proof_samples must be <= %d", maxStorageProofSamples)
		}
	}

	// Miner degrade threshold.
	if req.TargetMinerDegradeThreshold != 0 {
		if req.TargetMinerDegradeThreshold < minMinerDegradeThreshold {
			return fmt.Errorf("miner_degrade_threshold must be >= %d", minMinerDegradeThreshold)
		}
		if req.TargetMinerDegradeThreshold > maxMinerDegradeThreshold {
			return fmt.Errorf("miner_degrade_threshold must be <= %d", maxMinerDegradeThreshold)
		}
	}

	// Consensus validator limits.
	if req.TargetMaxConsensusValidators != 0 && req.TargetMaxConsensusValidators > maxConsensusValidatorsLimit {
		return fmt.Errorf("max_consensus_validators exceeds limit %d", maxConsensusValidatorsLimit)
	}
	if req.TargetMaxConsensusValidators != 0 && req.TargetMinConsensusValidators != 0 &&
		req.TargetMinConsensusValidators > req.TargetMaxConsensusValidators {
		return errors.New("min_consensus_validators cannot exceed max_consensus_validators")
	}

	return nil
}

func nonZeroOr(value uint64, fallback uint64) uint64 {
	if value != 0 {
		return value
	}
	return fallback
}

// proposalToCreateRequest reconstructs the original CreateGovernanceProposalRequest
// from a stored GovernanceProposal for signature re-verification.
func proposalToCreateRequest(p wire.GovernanceProposal) wire.CreateGovernanceProposalRequest {
	return wire.CreateGovernanceProposalRequest{
		Proposer:                          p.Proposer,
		ChainID:                           p.ChainID,
		IntentID:                          p.IntentID,
		Action:                            p.Action,
		ReasonHash:                        p.ReasonHash,
		ExpiresAtUnix:                     p.ExpiresAtUnix,
		PreserveStorage:                   p.PreserveStorage,
		AppealDeadlineUnix:                p.AppealDeadlineUnix,
		TargetOperator:                    p.TargetOperator,
		TargetPublicKey:                   p.TargetPublicKey,
		TargetPermissions:                 p.TargetPermissions,
		TargetDataModerationThresholdNum:  p.TargetDataModerationThresholdNum,
		TargetDataModerationThresholdDen:  p.TargetDataModerationThresholdDen,
		TargetOperatorChangeThresholdNum:  p.TargetOperatorChangeThresholdNum,
		TargetOperatorChangeThresholdDen:  p.TargetOperatorChangeThresholdDen,
		TargetStorageReleaseRateBPS:       p.TargetStorageReleaseRateBPS,
		TargetRetrievalReleaseRateBPS:     p.TargetRetrievalReleaseRateBPS,
		TargetValidatorReleaseRateBPS:     p.TargetValidatorReleaseRateBPS,
		TargetStoredBytesWeightBPS:        p.TargetStoredBytesWeightBPS,
		TargetProofScoreWeightBPS:         p.TargetProofScoreWeightBPS,
		TargetAvailabilityWeightBPS:       p.TargetAvailabilityWeightBPS,
		TargetDecentralizationWeightBPS:   p.TargetDecentralizationWeightBPS,
		TargetRetrievalRewardPerMiB:       p.TargetRetrievalRewardPerMiB,
		TargetMaxRetrievalRewardPerWindow: p.TargetMaxRetrievalRewardPerWindow,
		TargetRepairRewardPerShard:        p.TargetRepairRewardPerShard,
		TargetRepairPoolTakeoverBPS:       p.TargetRepairPoolTakeoverBPS,
		TargetRepairPoolSubsidyBPS:        p.TargetRepairPoolSubsidyBPS,
		TargetMinerDegradeThreshold:       p.TargetMinerDegradeThreshold,
		TargetStorageProofSamples:         p.TargetStorageProofSamples,
		TargetValidatorCommissionBPS:      p.TargetValidatorCommissionBPS,
		TargetRetrievalWeightBPS:          p.TargetRetrievalWeightBPS,
		TargetFoundationReleaseRateBPS:    p.TargetFoundationReleaseRateBPS,
		TargetFoundationAddress:           p.TargetFoundationAddress,
		TargetRetrievalAddress:            p.TargetRetrievalAddress,
		TargetStorageAnnualRateBPS:        p.TargetStorageAnnualRateBPS,
		TargetRetrievalAnnualRateBPS:      p.TargetRetrievalAnnualRateBPS,
		TargetValidatorAnnualRateBPS:      p.TargetValidatorAnnualRateBPS,
		TargetFoundationAnnualRateBPS:     p.TargetFoundationAnnualRateBPS,
		TargetReleaseCoefficientBPS:       p.TargetReleaseCoefficientBPS,
		TargetAvailabilityWindowSize:      p.TargetAvailabilityWindowSize,
		TargetAvailabilityThresholdBPS:    p.TargetAvailabilityThresholdBPS,
		TargetBlockProductionRewardBPS:    p.TargetBlockProductionRewardBPS,
		TargetMaxConsensusValidators:      p.TargetMaxConsensusValidators,
		TargetMinConsensusValidators:      p.TargetMinConsensusValidators,
		TargetBlockBytes:                  p.TargetBlockBytes,
		TargetMaxBlockBytes:               p.TargetMaxBlockBytes,
		TargetMaxBlockTxs:                 p.TargetMaxBlockTxs,
		TargetMaxTxBytes:                  p.TargetMaxTxBytes,
		TargetMaxStorageTxBytes:           p.TargetMaxStorageTxBytes,
		Signature:                         p.ProposerSignature,
		Nonce:                             p.ProposerNonce,
		CreatedAtUnix:                     p.CreatedAtUnix,
	}
}

// abs64 returns the absolute value of an int64.
func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
