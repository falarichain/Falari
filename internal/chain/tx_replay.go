package chain

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"

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
	CommittedAtUnix   int64                   `json:"committed_at_unix,omitempty"`
	RepairChallenges  []wire.StorageChallenge `json:"repair_challenges,omitempty"`
}

type finalizeDealTxPayload struct {
	Request         wire.FinalizeRequest `json:"request"`
	IntentID        string               `json:"intent_id"`
	DealID          string               `json:"deal_id"`
	User            string               `json:"user"`
	ManifestRoot    string               `json:"manifest_root"`
	FinalizedAtUnix int64                `json:"finalized_at_unix,omitempty"`
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
	Request                  wire.SubmitProofRequest `json:"request"`
	Reward                   uint64                  `json:"reward"`
	SettledStoragePoolReward uint64                  `json:"settled_storage_pool_reward,omitempty"`
	AlreadyRewarded          bool                    `json:"already_rewarded"`
	BonusReleased            bool                    `json:"bonus_released,omitempty"`
	BonusExpired             bool                    `json:"bonus_expired,omitempty"`
	SubmittedAtUnix          int64                   `json:"submitted_at_unix"`
}

type deregisterMinerTxPayload struct {
	Request      wire.DeregisterMinerRequest `json:"request"`
	ExitedAtUnix int64                       `json:"exited_at_unix"`
}

type finalizeEpochTxPayload struct {
	Request     wire.FinalizeEpochRequest  `json:"request,omitempty"`
	Response    wire.FinalizeEpochResponse `json:"response"`
	RepairTasks []wire.RepairTask          `json:"repair_tasks,omitempty"`
}

func (s *Store) applyBlockTransactionsLocked(block wire.Block) error {
	blockSnapshot, err := cloneStateForRollback(s.data)
	if err != nil {
		return err
	}
	restoreBlock := func(err error) error {
		s.data = blockSnapshot
		return err
	}
	for _, tx := range block.Transactions {
		if s.data.ConfirmedTxs[tx.TxID] {
			return restoreBlock(errors.New("block contains already confirmed transaction"))
		}
		if err := s.validateTransactionFeeLocked(tx); err != nil {
			return restoreBlock(err)
		}
		if tx.AgentKeyID != "" {
			if err := wire.VerifyTransactionSignature(tx, s.data.ChainID); err != nil {
				return restoreBlock(err)
			}
		}
		if err := s.validateAgentKeyTxLocked(tx); err != nil {
			return restoreBlock(err)
		}
		if !s.data.AppliedTxs[tx.TxID] {
			if err := s.applyTransactionLocked(tx); err != nil {
				return restoreBlock(err)
			}
			s.data.AppliedTxs[tx.TxID] = true
		}
		if err := s.chargeTransactionFeeLocked(tx, block.ProducerAddress); err != nil {
			return restoreBlock(err)
		}
		s.data.ConfirmedTxs[tx.TxID] = true
	}
	return nil
}

func (s *Store) applyPendingTransactionsForBlockLocked(txs []wire.Transaction, producerAddress string) ([]wire.Transaction, error) {
	var applied []wire.Transaction
	for _, tx := range txs {
		if s.data.ConfirmedTxs[tx.TxID] {
			continue
		}
		txSnapshot, err := cloneStateForRollback(s.data)
		if err != nil {
			return nil, err
		}
		restoreTx := func() {
			s.data = txSnapshot
		}
		if err := s.validateTransactionFeeLocked(tx); err != nil {
			restoreTx()
			continue
		}
		if tx.AgentKeyID != "" {
			if err := wire.VerifyTransactionSignature(tx, s.data.ChainID); err != nil {
				restoreTx()
				continue
			}
		}
		if err := s.validateAgentKeyTxLocked(tx); err != nil {
			restoreTx()
			continue
		}
		if !s.data.AppliedTxs[tx.TxID] {
			if err := s.applyTransactionLocked(tx); err != nil {
				restoreTx()
				continue
			}
			s.data.AppliedTxs[tx.TxID] = true
		}
		if err := s.chargeTransactionFeeLocked(tx, producerAddress); err != nil {
			restoreTx()
			continue
		}
		applied = append(applied, tx)
	}
	return applied, nil
}

func cloneStateForRollback(state State) (State, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return State{}, err
	}
	var cloned State
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return State{}, err
	}
	return cloned, nil
}

func (s *Store) applyTransactionLocked(tx wire.Transaction) error {
	switch tx.Type {
	case "faucet":
		// Faucet has been removed; skip legacy faucet transactions
		return nil
	case "genesis_credit":
		// Replay genesis_credit: credit balance on this node
		var payload struct {
			Address string `json:"address"`
			Amount  uint64 `json:"amount"`
		}
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		acc := s.accountLocked(payload.Address)
		acc.Balance += payload.Amount
		s.data.Accounts[payload.Address] = acc
		return nil
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
		return s.applyValidatorRegistrationLocked(req, tx.CreatedAtUnix)
	case "register_miner":
		var req wire.RegisterMinerRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			return err
		}
		return s.applyMinerRegistrationLocked(req, tx.CreatedAtUnix)
	case "deregister_miner":
		var payload deregisterMinerTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyMinerDeregistrationLocked(payload)
	case "claim_mining_rewards":
		var req wire.ClaimMiningRewardsRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			return err
		}
		return s.applyClaimMiningRewardsRequestLocked(req, tx.CreatedAtUnix)
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
	case "register_agent_key":
		var payload registerAgentKeyTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyRegisterAgentKeyLocked(payload)
	case "revoke_agent_key":
		var payload revokeAgentKeyTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyRevokeAgentKeyLocked(payload)
	case "extend_agent_key":
		var payload extendAgentKeyTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyExtendAgentKeyLocked(payload)
	case "topup_agent_key":
		var payload topupAgentKeyTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyTopupAgentKeyLocked(payload)
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
	case "direct_governance_action":
		var payload directGovernanceActionTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyDirectGovernanceActionLocked(payload)
	case "direct_action_review_vote":
		var payload directActionReviewVoteTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyDirectActionReviewVoteLocked(payload)
	case "governance_create_proposal":
		var payload governanceCreateProposalTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyGovernanceCreateProposalLocked(payload)
	case "governance_cast_vote":
		var payload governanceCastVoteTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyGovernanceCastVoteLocked(payload)
	case "governance_execute_proposal":
		var payload governanceExecuteProposalTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyGovernanceExecuteProposalLocked(payload)
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
	case "delegate_stake":
		var payload delegateStakeTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyDelegateStakeLocked(payload)
	case "undelegate_stake":
		var payload undelegateStakeTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyUndelegateStakeLocked(payload)
	case "create_multisig":
		var payload createMultisigTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyCreateMultisigLocked(payload)
	case "multisig_exec":
		var payload multisigExecTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return err
		}
		return s.applyMultisigExecLocked(payload)
	case "bridge_out":
		var req wire.BridgeOutRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			return err
		}
		return s.applyBridgeOutLocked(req)
	case "bridge_in_claim":
		var req wire.BridgeInClaimRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			return err
		}
		return s.applyBridgeInClaimLocked(req)
	case "bridge_set_config":
		var req wire.BridgeSetConfigRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			return err
		}
		return s.applyBridgeSetConfigLocked(req)
	default:
		return nil
	}
}

func (s *Store) applyTransferLocked(req wire.TransferRequest) (wire.Account, wire.Account, error) {
	fromAddress := wire.NormalizeAddress(req.From)
	from := s.accountLocked(fromAddress)
	if req.Signature == "" {
		return wire.Account{}, wire.Account{}, errors.New("transfer requires signature")
	}
	recoveredPublicKey, err := wire.RecoverTransferPublicKey(req, s.data.ChainID)
	if err != nil {
		return wire.Account{}, wire.Account{}, err
	}
	if req.PublicKey != "" && !strings.EqualFold(req.PublicKey, recoveredPublicKey) {
		return wire.Account{}, wire.Account{}, errors.New("transfer public key does not match signature")
	}
	req.PublicKey = recoveredPublicKey
	if err := wire.VerifyTransferSignature(req, s.data.ChainID); err != nil {
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
	return s.applySignedTransferLocked(req, req.PublicKey)
}

func (s *Store) verifyTransferTxLocked(tx wire.Transaction) error {
	var req wire.TransferRequest
	if err := json.Unmarshal(tx.Payload, &req); err != nil {
		return err
	}
	if req.Signature == "" {
		return errors.New("transfer requires signature")
	}
	if err := wire.VerifyTransferSignature(req, s.data.ChainID); err != nil {
		return err
	}
	from := s.accountLocked(wire.NormalizeAddress(req.From))
	if req.Fee < s.data.FeeMarket.BaseFee {
		return errors.New("transfer fee below current base fee")
	}
	if req.Nonce < from.Nonce {
		return errors.New("invalid transfer nonce")
	}
	return nil
}

func (s *Store) applySignedTransferLocked(req wire.TransferRequest, publicKey string) (wire.Account, wire.Account, error) {
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

func (s *Store) applyValidatorRegistrationLocked(req wire.RegisterValidatorRequest, registeredAt int64) error {
	if registeredAt <= 0 {
		return errors.New("replay validator registration missing transaction timestamp")
	}
	req.OwnerAddress = wire.NormalizeAddress(req.OwnerAddress)
	req.OperatorAddress = wire.NormalizeAddress(req.OperatorAddress)

	if err := s.verifyAccountRequestLocked(req.ChainID, req.OwnerAddress, req.Nonce, func() error {
		return wire.VerifyValidatorRegistration(req)
	}); err != nil {
		return err
	}

	existing := s.validatorLocked(req.OwnerAddress)

	// State guard: reject re-registration in terminal/penalty states.
	switch existing.Status {
	case wire.ValidatorStatusExiting:
		return errors.New("replay validator registration: validator is exiting")
	case wire.ValidatorStatusExited:
		return errors.New("replay validator registration: validator has exited")
	case wire.ValidatorStatusSlashed:
		return errors.New("replay validator registration: validator has been slashed")
	case wire.ValidatorStatusJailed:
		return errors.New("replay validator registration: validator is jailed")
	}

	account := s.accountLocked(req.OwnerAddress)
	if req.Stake < MinValidatorStake {
		return errors.New("replay validator registration below minimum required stake")
	}
	if req.Stake > existing.Stake {
		additionalStake := req.Stake - existing.Stake
		if account.Balance < additionalStake {
			return errors.New("replay validator registration has insufficient stake balance")
		}
		account.Balance -= additionalStake
		account.LockedStake += additionalStake
	}
	existing.OwnerAddress = req.OwnerAddress
	existing.OperatorAddress = req.OperatorAddress
	existing.OperatorPublicKey = req.OperatorPublicKey
	existing.Endpoint = req.Endpoint
	existing.Stake = req.Stake
	existing.SelfStake = req.Stake
	existing.Status = wire.ValidatorStatusActive
	if existing.RegisteredAtUnix == 0 {
		existing.RegisteredAtUnix = registeredAt
	}
	s.consumeAccountNonceLocked(req.OwnerAddress)
	s.data.Accounts[account.Address] = account
	s.data.Validators[req.OwnerAddress] = existing
	s.data.ConsensusValidators[req.OwnerAddress] = true
	if s.data.OperatorMap == nil {
		s.data.OperatorMap = map[string]string{}
	}
	s.data.OperatorMap[req.OperatorAddress] = req.OwnerAddress
	return nil
}

func (s *Store) applyMinerRegistrationLocked(req wire.RegisterMinerRequest, registeredAt int64) error {
	if registeredAt <= 0 {
		return errors.New("replay miner registration missing transaction timestamp")
	}
	req.MinerAddress = wire.NormalizeAddress(req.MinerAddress)

	if err := s.verifyAccountRequestLocked(req.ChainID, req.MinerAddress, req.Nonce, func() error {
		return wire.VerifyMinerRegistration(req)
	}); err != nil {
		return err
	}

	existing := s.minerStatsLocked(req.MinerAddress)
	if existing.MinerAddress != "" {
		s.accrueStorageRewardForMinerLocked(req.MinerAddress)
		existing = s.minerStatsLocked(req.MinerAddress)
	}

	// State guard: reject re-registration in terminal/penalty states.
	switch existing.Status {
	case wire.MinerStatusExiting:
		return errors.New("replay miner registration: miner is exiting")
	case wire.MinerStatusExited:
		return errors.New("replay miner registration: miner has exited")
	case wire.MinerStatusJailed:
		return errors.New("replay miner registration: miner is jailed")
	}

	params := s.miningParamsLocked()
	if params.MinCapacityBytes > 0 && req.CapacityBytes < params.MinCapacityBytes {
		return errors.New("replay miner registration: capacity below minimum")
	}

	account := s.accountLocked(req.MinerAddress)
	if req.Stake > existing.Stake {
		additionalStake := req.Stake - existing.Stake
		if account.Balance < additionalStake {
			return errors.New("replay miner registration has insufficient stake balance")
		}
		account.Balance -= additionalStake
		account.LockedStake += additionalStake
	}
	// One-time registration bonus (with cap + pool accounting).
	if !existing.BonusReleased && !existing.BonusExpired {
		if params.RegistrationBonusAmount > 0 {
			capOK := params.MaxBonusAddresses == 0 || s.data.BonusGrantedCount < params.MaxBonusAddresses
			if capOK {
				s.initRewardPoolsLocked()
				if s.data.RewardPools.StorageRemaining >= params.RegistrationBonusAmount {
					account.LockedBonus += params.RegistrationBonusAmount
					s.data.RewardPools.StorageRemaining -= params.RegistrationBonusAmount
					s.data.BonusGrantedCount++
				}
			}
		}
	}
	// Stake requirement: LockedBonus + LockedStake must cover capacity-based stake.
	if params.StakePerTiB > 0 {
		requiredStake := RequiredStakeForCapacity(req.CapacityBytes, params.StakePerTiB)
		totalLocked := account.LockedBonus + account.LockedStake
		if totalLocked < requiredStake {
			return errors.New("replay miner registration: insufficient stake for declared capacity")
		}
	}
	existing.MinerAddress = req.MinerAddress
	existing.PublicKey = req.PublicKey
	existing.Endpoint = req.Endpoint
	existing.CapacityBytes = req.CapacityBytes
	existing.AccessServiceRequired = true
	existing.UploadServiceEnabled = true
	existing.DownloadServiceEnabled = true
	existing.Stake = req.Stake
	existing.Status = "active"
	if existing.StorageRewardIndex == "" {
		existing.StorageRewardIndex = s.data.StorageRewardIndex
	}
	if existing.RegisteredAtUnix == 0 {
		existing.RegisteredAtUnix = registeredAt
	}
	s.consumeAccountNonceLocked(req.MinerAddress)
	s.data.Accounts[req.MinerAddress] = account
	s.data.Miners[req.MinerAddress] = existing
	return nil
}

func (s *Store) applyMinerDeregistrationLocked(payload deregisterMinerTxPayload) error {
	req := payload.Request
	req.MinerAddress = wire.NormalizeAddress(req.MinerAddress)
	if req.MinerAddress == "" {
		return errors.New("replay miner deregistration missing miner address")
	}
	if payload.ExitedAtUnix <= 0 {
		return errors.New("replay miner deregistration missing exit time")
	}
	if err := s.verifyAccountRequestLocked(req.ChainID, req.MinerAddress, req.Nonce, func() error {
		return wire.VerifyDeregisterMiner(req)
	}); err != nil {
		return err
	}
	stats := s.minerStatsLocked(req.MinerAddress)
	if stats.MinerAddress != "" {
		s.accrueStorageRewardForMinerLocked(req.MinerAddress)
		stats = s.minerStatsLocked(req.MinerAddress)
	}
	if stats.Status == wire.MinerStatusExited {
		return errors.New("replay miner deregistration: miner already exited")
	}
	if stats.Status != wire.MinerStatusExiting {
		stats.Status = wire.MinerStatusExiting
		stats.ExitedAtUnix = payload.ExitedAtUnix
	}
	s.consumeAccountNonceLocked(req.MinerAddress)
	s.data.Miners[req.MinerAddress] = stats
	return nil
}

func (s *Store) applyClaimMiningRewardsRequestLocked(req wire.ClaimMiningRewardsRequest, claimedAt int64) error {
	req.MinerAddress = wire.NormalizeAddress(req.MinerAddress)
	if req.MinerAddress == "" {
		return errors.New("replay claim mining rewards missing miner address")
	}
	if err := s.verifyAccountRequestLocked(req.ChainID, req.MinerAddress, req.Nonce, func() error {
		return wire.VerifyClaimMiningRewards(req)
	}); err != nil {
		return err
	}
	s.applyClaimMiningRewardsLocked(req.MinerAddress, claimedAt)
	s.consumeAccountNonceLocked(req.MinerAddress)
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
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, req.User, "create_intent", req.LockedFee, func(agentPub string) error {
			return wire.VerifyCreateIntentAgent(req, agentPub)
		}); err != nil {
			return err
		}
	} else {
		if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
			return wire.VerifyCreateIntent(req)
		}); err != nil {
			return err
		}
		s.consumeAccountNonceLocked(req.User)
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, req.LockedFee); err != nil {
			return err
		}
	}
	account = s.accountLocked(req.User)
	burnAmount := req.LockedFee * defaultStorageBurnBPS / 10_000
	retrievalAmount := req.LockedFee * defaultStorageRetrievalBPS / 10_000
	foundationAmount := req.LockedFee * defaultStorageFoundationBPS / 10_000
	minerPortion := req.LockedFee - burnAmount - retrievalAmount - foundationAmount
	account.Balance -= req.LockedFee
	account.LockedStorage += minerPortion
	s.data.Accounts[account.Address] = account
	if retrievalAmount > 0 && s.data.RetrievalAddress != "" {
		retAcc := s.accountLocked(s.data.RetrievalAddress)
		retAcc.Balance += retrievalAmount
		s.data.Accounts[retAcc.Address] = retAcc
		s.data.StorageFeePool.TotalToRetrieval = saturatingAdd(s.data.StorageFeePool.TotalToRetrieval, retrievalAmount)
	} else {
		burnAmount += retrievalAmount
	}
	if foundationAmount > 0 && s.data.FoundationAddress != "" {
		fndAcc := s.accountLocked(s.data.FoundationAddress)
		fndAcc.Balance += foundationAmount
		s.data.Accounts[fndAcc.Address] = fndAcc
		s.data.StorageFeePool.TotalToFoundation = saturatingAdd(s.data.StorageFeePool.TotalToFoundation, foundationAmount)
	} else {
		burnAmount += foundationAmount
	}

	createdAt := payload.CreatedAtUnix
	if createdAt <= 0 {
		return errors.New("replay create intent missing create timestamp")
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
			LockedFee:        minerPortion,
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
	s.createDealEscrowLocked(s.data.Intents[payload.IntentID], createdAt)
	if burnAmount > 0 {
		s.data.Intents[payload.IntentID].BurnedFee = burnAmount
		escrow := s.dealEscrowLocked(s.data.Intents[payload.IntentID])
		escrow.BurnedFee = burnAmount
		s.data.DealEscrows[payload.IntentID] = escrow
		s.data.StorageFeePool.TotalBurned = saturatingAdd(s.data.StorageFeePool.TotalBurned, burnAmount)
	}
	s.reserveStorageAssignmentsLocked(assignments)
	return nil
}

func (s *Store) applyBatchCommitLocked(payload batchCommitTxPayload) error {
	req := payload.Request
	committedAt := payload.CommittedAtUnix
	if committedAt <= 0 {
		return errors.New("replay batch commit missing commit timestamp")
	}
	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return errors.New("replay batch commit intent not found")
	}
	if intent.User != req.User {
		return errors.New("replay batch commit user mismatch")
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
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, req.User, "batch_commit", 0, func(agentPub string) error {
			return wire.VerifyBatchCommitAgent(req, agentPub)
		}); err != nil {
			return err
		}
	} else {
		if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
			return wire.VerifyBatchCommit(req)
		}); err != nil {
			return err
		}
		s.consumeAccountNonceLocked(req.User)
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, 0); err != nil {
			return err
		}
	}
	repairChallengesByID := map[string]wire.StorageChallenge{}
	for _, challenge := range payload.RepairChallenges {
		if challenge.RepairID == "" {
			return errors.New("replay repair proof challenge missing repair id")
		}
		repairChallengesByID[challenge.RepairID] = challenge
	}
	for _, receipt := range req.Receipts {
		s.accrueStorageRewardForMinerLocked(receipt.MinerAddress)
		miner := s.minerStatsLocked(receipt.MinerAddress)
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
				challenge, ok := repairChallengesByID[repairTask.RepairID]
				if !ok {
					return errors.New("replay repair commit missing proof challenge")
				}
				if _, err := s.applyRepairProofChallengeLocked(repairTask, receipt, challenge); err != nil {
					return err
				}
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
				challenge, ok := repairChallengesByID[repairTask.RepairID]
				if !ok {
					return errors.New("replay repair commit missing proof challenge")
				}
				if _, err := s.applyRepairProofChallengeLocked(repairTask, receipt, challenge); err != nil {
					return err
				}
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
	intent.UpdatedAt = committedAt
	return nil
}

func (s *Store) applyFinalizeDealLocked(payload finalizeDealTxPayload) error {
	now := payload.FinalizedAtUnix
	if now <= 0 {
		return errors.New("replay finalize deal missing finalize timestamp")
	}
	intent, ok := s.data.Intents[payload.IntentID]
	if !ok {
		return errors.New("replay finalize deal intent not found")
	}
	if payload.User != "" && intent.User != payload.User {
		return errors.New("replay finalize deal user mismatch")
	}
	req := payload.Request
	if req.IntentID == "" {
		req = wire.FinalizeRequest{ChainID: "", IntentID: payload.IntentID, User: payload.User, ManifestRoot: payload.ManifestRoot}
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, req.User, "finalize", 0, func(agentPub string) error {
			return wire.VerifyFinalizeAgent(req, agentPub)
		}); err != nil {
			return err
		}
	} else {
		if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
			return wire.VerifyFinalize(req)
		}); err != nil {
			return err
		}
		s.consumeAccountNonceLocked(req.User)
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, 0); err != nil {
			return err
		}
	}
	intent.DealID = payload.DealID
	intent.Status = wire.StatusFinalized
	intent.StorageStatus = wire.StorageStatusActive
	intent.AccessStatus = defaultAccessStatus(intent.IntentView)
	intent.ModerationStatus = wire.ModerationStatusNone
	if intent.Policy.Duration > 0 {
		intent.ExpiresAtUnix = now + intent.Policy.Duration
	}
	intent.UpdatedAt = now
	s.activateDealEscrowLocked(intent, now)
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
	req := payload.Request
	if req.User == "" {
		req.User = intent.User
	}
	if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
		return wire.VerifySettleIntent(req)
	}); err != nil {
		return err
	}
	settledAt := payload.SettledAtUnix
	if settledAt <= 0 {
		return errors.New("replay settle intent missing settle timestamp")
	}
	resp, err := s.settleIntentLocked(intent, settledAt)
	if err != nil {
		return err
	}
	if resp.RefundedFee != payload.Response.RefundedFee || resp.PaidFee != payload.Response.PaidFee || resp.Status != payload.Response.Status {
		return errors.New("replay settle intent response mismatch")
	}
	s.consumeAccountNonceLocked(req.User)
	return nil
}

func (s *Store) applyTerminateDealLocked(payload terminateDealTxPayload) error {
	req := payload.Request
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, req.User, "terminate_deal", 0, func(agentPub string) error {
			return wire.VerifyTerminateDealAgent(req, agentPub)
		}); err != nil {
			return err
		}
	} else {
		if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
			return wire.VerifyTerminateDeal(req)
		}); err != nil {
			return err
		}
	}
	resp, err := s.terminateDealLocked(req, payload.Response.TerminatedAtUnix)
	if err != nil {
		return err
	}
	if resp.StorageStatus != payload.Response.StorageStatus || resp.AccessStatus != payload.Response.AccessStatus {
		return errors.New("replay terminate deal response mismatch")
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, 0); err != nil {
			return err
		}
	} else {
		s.consumeAccountNonceLocked(req.User)
	}
	return nil
}

func (s *Store) applySetAccessPolicyLocked(payload setAccessPolicyTxPayload) error {
	if err := s.verifyAccountRequestLocked(payload.Request.ChainID, payload.Request.User, payload.Request.Nonce, func() error {
		return wire.VerifySetAccessPolicy(payload.Request)
	}); err != nil {
		return err
	}
	resp, err := s.setAccessPolicyLocked(payload.Request, payload.Response.UpdatedAtUnix)
	if err != nil {
		return err
	}
	if resp.AccessStatus != payload.Response.AccessStatus {
		return errors.New("replay set access policy response mismatch")
	}
	s.consumeAccountNonceLocked(payload.Request.User)
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

func (s *Store) applyGovernanceCreateProposalLocked(payload governanceCreateProposalTxPayload) error {
	req := payload.Request

	proposer := normalizeGovernanceOperator(req.Proposer)
	if proposer == "" {
		return errors.New("replay governance create: proposer is required")
	}

	// Verify chain_id to prevent cross-chain replay.
	if req.ChainID != s.data.ChainID {
		return errors.New("replay governance create: chain_id mismatch")
	}

	// Verify proposer is an authorized governance operator.
	operator, ok := s.data.GovernanceOperators[proposer]
	if !ok || !operator.Enabled {
		return errors.New("replay governance create: proposer is not an authorized operator")
	}
	if operator.PublicKey == "" {
		return errors.New("replay governance create: operator has no public key")
	}

	// Verify request signature.
	if err := wire.VerifyGovernanceProposal(req, proposer); err != nil {
		return errors.New("replay governance create: " + err.Error())
	}

	// Verify nonce.
	expectedNonce := s.data.OperatorNonces[proposer]
	if req.Nonce != expectedNonce {
		return errors.New("replay governance create: invalid proposer nonce")
	}

	// Validate action.
	if !validGovernanceAction(req.Action) {
		return errors.New("replay governance create: invalid governance action")
	}

	// Check permission.
	if err := s.validateGovernanceOperatorLocked(proposer, req.Action); err != nil {
		return errors.New("replay governance create: " + err.Error())
	}
	if req.CreatedAtUnix == 0 {
		return errors.New("replay governance create: missing signed timestamp")
	}

	// Validate action-specific fields.
	if err := validateGovernanceActionFields(req.Action, req.ExpiresAtUnix, req.AppealDeadlineUnix, req.CreatedAtUnix); err != nil {
		return errors.New("replay governance create: " + err.Error())
	}

	if isOperatorManagementAction(req.Action) {
		if err := validateOperatorManagementFields(req.Action, req.TargetOperator, req.TargetPublicKey, req.TargetPermissions, s.data.GovernanceOperators); err != nil {
			return errors.New("replay governance create: " + err.Error())
		}
	} else if isConfigAction(req.Action) {
		if err := validateConfigChangeFields(req); err != nil {
			return errors.New("replay governance create: " + err.Error())
		}
	} else if isMiningParamsAction(req.Action) {
		if err := validateMiningParamsChangeFields(req, s.miningParamsLocked()); err != nil {
			return errors.New("replay governance create: " + err.Error())
		}
	} else {
		if _, ok := s.data.Intents[req.IntentID]; !ok {
			return errors.New("replay governance create: intent not found")
		}
	}

	// Validate the response proposal matches the request.
	proposal := payload.Response.Proposal
	if proposal.ProposalID == "" {
		return errors.New("replay governance create: missing proposal id")
	}
	expectedProposal := governanceProposalFromRequest(req, proposer, proposal.ProposalID, req.CreatedAtUnix)
	if !reflect.DeepEqual(proposal, expectedProposal) {
		return errors.New("replay governance create: response proposal mismatch")
	}

	// Consume nonce and write proposal.
	s.data.OperatorNonces[proposer] = expectedNonce + 1
	s.data.GovernanceProposals[proposal.ProposalID] = proposal
	s.data.GovernanceVotes[proposal.ProposalID] = []wire.GovernanceVote{}
	return nil
}

func (s *Store) applyGovernanceCastVoteLocked(payload governanceCastVoteTxPayload) error {
	req := payload.Request

	voter := normalizeGovernanceOperator(req.Voter)
	if voter == "" {
		return errors.New("replay governance vote: voter is required")
	}

	// Verify chain_id to prevent cross-chain replay.
	if req.ChainID != s.data.ChainID {
		return errors.New("replay governance vote: chain_id mismatch")
	}

	// Look up proposal — must exist and be pending.
	proposal, ok := s.data.GovernanceProposals[req.ProposalID]
	if !ok {
		return errors.New("replay governance vote: proposal not found")
	}
	if proposal.Status != wire.GovProposalPending {
		return errors.New("replay governance vote: proposal is not pending")
	}

	// Verify voter is an authorized operator.
	operator, ok := s.data.GovernanceOperators[voter]
	if !ok || !operator.Enabled {
		return errors.New("replay governance vote: voter is not an authorized operator")
	}
	if operator.PublicKey == "" {
		return errors.New("replay governance vote: voter has no public key")
	}

	// Verify vote request signature.
	if err := wire.VerifyGovernanceVote(req, voter); err != nil {
		return errors.New("replay governance vote: " + err.Error())
	}

	// Verify nonce.
	expectedNonce := s.data.OperatorNonces[voter]
	if req.Nonce != expectedNonce {
		return errors.New("replay governance vote: invalid voter nonce")
	}
	if req.CreatedAtUnix == 0 {
		return errors.New("replay governance vote: missing signed timestamp")
	}

	// Check double voting.
	votes := s.data.GovernanceVotes[req.ProposalID]
	for _, v := range votes {
		if normalizeGovernanceOperator(v.Voter) == voter {
			return errors.New("replay governance vote: voter already voted")
		}
	}

	// Record vote using the verified request data.
	vote := wire.GovernanceVote{
		ProposalID:     req.ProposalID,
		Voter:          voter,
		VoterSignature: req.Signature,
		Approve:        req.Approve,
		CreatedAtUnix:  req.CreatedAtUnix,
	}
	if payload.Response.Vote != vote {
		return errors.New("replay governance vote: response vote mismatch")
	}

	// Recompute vote counts without mutating state first.
	approveCount, rejectCount := 0, 0
	for _, existing := range append(append([]wire.GovernanceVote(nil), votes...), vote) {
		voterAddr := normalizeGovernanceOperator(existing.Voter)
		op, ok := s.data.GovernanceOperators[voterAddr]
		if !ok || !op.Enabled {
			continue
		}
		if existing.Approve {
			approveCount++
		} else {
			rejectCount++
		}
	}
	threshold := s.governanceThresholdLocked(proposal.Action)

	totalEnabled := s.countEnabledOperatorsLocked()
	remaining := totalEnabled - approveCount - rejectCount
	executed := approveCount >= threshold
	if approveCount >= threshold {
		originalReq := proposalToCreateRequest(proposal)
		if err := wire.VerifyGovernanceProposal(originalReq, normalizeGovernanceOperator(proposal.Proposer)); err != nil {
			return errors.New("replay governance vote: proposal signature re-verification failed: " + err.Error())
		}
	}

	if approveCount != payload.Response.ApproveCount ||
		rejectCount != payload.Response.RejectCount ||
		threshold != payload.Response.Threshold ||
		executed != payload.Response.Executed {
		return errors.New("replay governance vote: response mismatch")
	}

	s.data.GovernanceVotes[req.ProposalID] = append(s.data.GovernanceVotes[req.ProposalID], vote)
	s.data.OperatorNonces[voter] = expectedNonce + 1

	if executed {
		if _, err := s.executeGovernanceProposalLocked(proposal, req.CreatedAtUnix); err != nil {
			return errors.New("replay governance vote: execute failed: " + err.Error())
		}
	} else if approveCount+remaining < threshold {
		proposal.Status = wire.GovProposalRejected
		s.data.GovernanceProposals[req.ProposalID] = proposal
	}

	return nil
}

func (s *Store) applyGovernanceExecuteProposalLocked(payload governanceExecuteProposalTxPayload) error {
	req := payload.Request

	executor := normalizeGovernanceOperator(req.Executor)
	if executor == "" {
		return errors.New("replay governance execute: executor is required")
	}

	// Verify chain_id to prevent cross-chain replay.
	if req.ChainID != s.data.ChainID {
		return errors.New("replay governance execute: chain_id mismatch")
	}

	// Verify executor is an authorized operator.
	operator, ok := s.data.GovernanceOperators[executor]
	if !ok || !operator.Enabled {
		return errors.New("replay governance execute: executor is not an authorized operator")
	}
	if operator.PublicKey == "" {
		return errors.New("replay governance execute: executor has no public key")
	}

	// Verify execute request signature.
	if err := wire.VerifyGovernanceExecute(req, executor); err != nil {
		return errors.New("replay governance execute: " + err.Error())
	}

	// Verify nonce.
	expectedNonce := s.data.OperatorNonces[executor]
	if req.Nonce != expectedNonce {
		return errors.New("replay governance execute: invalid executor nonce")
	}
	if req.CreatedAtUnix == 0 {
		return errors.New("replay governance execute: missing signed timestamp")
	}

	// Verify proposal exists and is pending.
	proposal, ok := s.data.GovernanceProposals[req.ProposalID]
	if !ok {
		return errors.New("replay governance execute: proposal not found")
	}
	if proposal.Status != wire.GovProposalPending {
		return errors.New("replay governance execute: proposal is not pending")
	}
	if proposal.CreatedAtUnix+governanceProposalTTLSeconds < req.CreatedAtUnix {
		return errors.New("replay governance execute: proposal has expired")
	}

	// Re-verify original proposal signature to prevent tampering.
	originalReq := proposalToCreateRequest(proposal)
	if err := wire.VerifyGovernanceProposal(originalReq, normalizeGovernanceOperator(proposal.Proposer)); err != nil {
		return errors.New("replay governance execute: proposal signature re-verification failed: " + err.Error())
	}

	// Re-count votes and verify threshold.
	approveCount := 0
	for _, v := range s.data.GovernanceVotes[proposal.ProposalID] {
		if !v.Approve {
			continue
		}
		voterAddr := normalizeGovernanceOperator(v.Voter)
		op, ok := s.data.GovernanceOperators[voterAddr]
		if !ok || !op.Enabled {
			continue
		}
		approveCount++
	}
	threshold := s.governanceThresholdLocked(proposal.Action)
	if approveCount < threshold {
		return errors.New("replay governance execute: insufficient approval votes")
	}

	// Re-execute the governance action and compare result.
	now := req.CreatedAtUnix
	execSnapshot, err := cloneStateForRollback(s.data)
	if err != nil {
		return err
	}
	execResult, err := s.executeGovernanceProposalLocked(proposal, now)
	if err != nil {
		s.data = execSnapshot
		return errors.New("replay governance execute: " + err.Error())
	}
	if execResult != payload.Response.GovernanceResult {
		s.data = execSnapshot
		return errors.New("replay governance execute: result mismatch")
	}
	s.data.OperatorNonces[executor] = expectedNonce + 1

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
	// Operator auth verification (soft fork: legacy txs with empty operator pass).
	if payload.Request.OperatorAddress != "" {
		operatorAddr := normalizeGovernanceOperator(payload.Request.OperatorAddress)
		if operatorAddr == "" {
			return errors.New("replay start epoch: invalid operator address")
		}
		if payload.Request.ChainID != s.data.ChainID {
			return errors.New("replay start epoch: chain_id mismatch")
		}
		operator, ok := s.data.GovernanceOperators[operatorAddr]
		if !ok || !operator.Enabled {
			return errors.New("replay start epoch: operator not authorized")
		}
		if err := s.validateGovernanceOperatorLocked(operatorAddr, "start_epoch"); err != nil {
			return errors.New("replay start epoch: " + err.Error())
		}
		expectedNonce := s.data.OperatorNonces[operatorAddr]
		if payload.Request.Nonce != expectedNonce {
			return errors.New("replay start epoch: operator nonce mismatch")
		}
		if payload.Request.CreatedAtUnix == 0 {
			return errors.New("replay start epoch: missing created_at_unix")
		}
		if err := wire.VerifyStartEpochRequest(payload.Request, operatorAddr); err != nil {
			return errors.New("replay start epoch: " + err.Error())
		}
		s.data.OperatorNonces[operatorAddr] = expectedNonce + 1
	}

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
		settled := s.settleStorageRewardForMinerLocked(proof.MinerAddress, payload.SubmittedAtUnix)
		if settled != payload.SettledStoragePoolReward {
			return errors.New("replay proof storage pool settlement mismatch")
		}
		reward := s.payableStorageRewardLocked(challenge)
		if reward != payload.Reward {
			return errors.New("replay proof reward mismatch")
		}
		stats := s.minerStatsLocked(proof.MinerAddress)
		stats.ProofSuccess++
		stats.ConsecutiveFailures = 0
		s.clearPendingShardRepairsForMinerLocked(proof.MinerAddress)
		stats.Rewards = saturatingAdd(stats.Rewards, reward)
		stats.StorageRewards = saturatingAdd(stats.StorageRewards, reward)
		if stats.Status == wire.MinerStatusDegraded {
			stats.Status = wire.MinerStatusActive
		}
		s.data.Miners[proof.MinerAddress] = stats

		// Replay bonus expiry if recorded (release slot for new registrations).
		if payload.BonusExpired && !stats.BonusExpired && !stats.BonusReleased {
			account := s.accountLocked(proof.MinerAddress)
			stats.BonusExpired = true
			s.initRewardPoolsLocked()
			s.data.RewardPools.StorageRemaining = saturatingAdd(s.data.RewardPools.StorageRemaining, account.LockedBonus)
			account.LockedBonus = 0
			if s.data.BonusGrantedCount > 0 {
				s.data.BonusGrantedCount--
			}
			s.data.Accounts[proof.MinerAddress] = account
			s.data.Miners[proof.MinerAddress] = stats
		}

		// Replay bonus release if recorded (no pool deduction — reserved at grant time).
		if payload.BonusReleased && !stats.BonusReleased && !stats.BonusExpired {
			account := s.accountLocked(proof.MinerAddress)
			account.Balance += account.LockedBonus
			account.LockedBonus = 0
			s.data.Accounts[proof.MinerAddress] = account
			stats.BonusReleased = true
			s.data.Miners[proof.MinerAddress] = stats
		}

		s.payStorageRewardLocked(challenge, proof.MinerAddress, reward)
	}
	if challenge.RepairID != "" {
		s.completeRepairTaskAfterProofLocked(challenge.RepairID, challenge.ChallengeID)
	}
	return nil
}

func (s *Store) applyFinalizeEpochLocked(payload finalizeEpochTxPayload) error {
	// Operator auth verification (soft fork: legacy txs with empty operator pass).
	if payload.Request.OperatorAddress != "" {
		operatorAddr := normalizeGovernanceOperator(payload.Request.OperatorAddress)
		if operatorAddr == "" {
			return errors.New("replay finalize epoch: invalid operator address")
		}
		if payload.Request.ChainID != s.data.ChainID {
			return errors.New("replay finalize epoch: chain_id mismatch")
		}
		operator, ok := s.data.GovernanceOperators[operatorAddr]
		if !ok || !operator.Enabled {
			return errors.New("replay finalize epoch: operator not authorized")
		}
		if err := s.validateGovernanceOperatorLocked(operatorAddr, "finalize_epoch"); err != nil {
			return errors.New("replay finalize epoch: " + err.Error())
		}
		expectedNonce := s.data.OperatorNonces[operatorAddr]
		if payload.Request.Nonce != expectedNonce {
			return errors.New("replay finalize epoch: operator nonce mismatch")
		}
		if payload.Request.CreatedAtUnix == 0 {
			return errors.New("replay finalize epoch: missing created_at_unix")
		}
		if err := wire.VerifyFinalizeEpochRequest(payload.Request, operatorAddr); err != nil {
			return errors.New("replay finalize epoch: " + err.Error())
		}
		s.data.OperatorNonces[operatorAddr] = expectedNonce + 1
	}

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
		threshold := s.miningParamsLocked().MinerDegradeThreshold
		if stats.Status == wire.MinerStatusActive && stats.ConsecutiveFailures >= threshold {
			stats.Status = wire.MinerStatusDegraded
		}
		if stats.Status == wire.MinerStatusDegraded && stats.ConsecutiveFailures >= threshold*2 {
			stats.Status = wire.MinerStatusJailed
		}
		slash := epoch.SlashPerMissedProof
		account := s.accountLocked(challenge.MinerAddress)

		// 1. Slash from LockedBonus first (system-incentive layer).
		fromBonus := slash
		if fromBonus > account.LockedBonus {
			fromBonus = account.LockedBonus
		}
		account.LockedBonus -= fromBonus

		// 2. Then from LockedStake (personal commitment layer).
		fromStake := slash - fromBonus
		if fromStake > account.LockedStake {
			fromStake = account.LockedStake
		}
		account.LockedStake -= fromStake

		actualSlash := fromBonus + fromStake
		stats.Stake = account.LockedStake
		stats.Slashed += actualSlash
		totalSlashed += actualSlash
		s.addSlashedToPermanentFundLocked(actualSlash)

		// Auto-exit: bonus and stake both depleted.
		if account.LockedBonus == 0 && account.LockedStake == 0 && actualSlash > 0 {
			stats.Status = wire.MinerStatusExiting
			exitBase := epoch.DeadlineUnix
			if exitBase == 0 {
				exitBase = epoch.StartedAtUnix
			}
			stats.ExitedAtUnix = exitBase + 7*24*60*60
		}
		s.data.Accounts[account.Address] = account
		s.data.Miners[challenge.MinerAddress] = stats
		if task, created := s.trackMissedProofForRepairLocked(challenge, epoch.EpochRound); created {
			if err := s.applyRepairTasksLocked([]wire.RepairTask{task}); err == nil {
				repairTasks = append(repairTasks, task)
			}
		}
	}
	// Check DHT/retrieval obligations before finalizing.
	s.checkDHTObligationsLocked()
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

func (s *Store) applyDirectGovernanceActionLocked(payload directGovernanceActionTxPayload) error {
	resp, err := s.DirectGovernanceAction(payload.Request)
	if err != nil {
		return err
	}
	if resp.Record.ActionID != payload.Response.Record.ActionID {
		return errors.New("replay direct governance action: action_id mismatch")
	}
	if resp.GovernanceResult.ModerationStatus != payload.Response.GovernanceResult.ModerationStatus {
		return errors.New("replay direct governance action: moderation_status mismatch")
	}
	return nil
}

func (s *Store) applyDirectActionReviewVoteLocked(payload directActionReviewVoteTxPayload) error {
	resp, err := s.CastDirectActionReviewVote(payload.Request)
	if err != nil {
		return err
	}
	if resp.Rejected != payload.Response.Rejected {
		return errors.New("replay direct action review vote: rejected mismatch")
	}
	return nil
}
