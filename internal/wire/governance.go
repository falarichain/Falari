package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// governanceProposalSigningPayload is the canonical payload for proposal signatures.
// The Signature field is excluded — it is what we are computing.
type governanceProposalSigningPayload struct {
	Proposer                          string   `json:"proposer"`
	ChainID                           string   `json:"chain_id"`
	IntentID                          string   `json:"intent_id,omitempty"`
	Action                            string   `json:"action"`
	ReasonHash                        string   `json:"reason_hash"`
	ExpiresAtUnix                     int64    `json:"expires_at_unix,omitempty"`
	PreserveStorage                   bool     `json:"preserve_storage,omitempty"`
	AppealDeadlineUnix                int64    `json:"appeal_deadline_unix,omitempty"`
	TargetOperator                    string   `json:"target_operator,omitempty"`
	TargetPublicKey                   string   `json:"target_public_key,omitempty"`
	TargetPermissions                 []string `json:"target_permissions,omitempty"`
	TargetDataModerationThresholdNum  int      `json:"target_data_moderation_threshold_num,omitempty"`
	TargetDataModerationThresholdDen  int      `json:"target_data_moderation_threshold_den,omitempty"`
	TargetOperatorChangeThresholdNum  int      `json:"target_operator_change_threshold_num,omitempty"`
	TargetOperatorChangeThresholdDen  int      `json:"target_operator_change_threshold_den,omitempty"`
	TargetStorageReleaseRateBPS       uint64   `json:"target_storage_release_rate_bps,omitempty"`
	TargetRetrievalReleaseRateBPS     uint64   `json:"target_retrieval_release_rate_bps,omitempty"`
	TargetStoredBytesWeightBPS        uint64   `json:"target_stored_bytes_weight_bps,omitempty"`
	TargetProofScoreWeightBPS         uint64   `json:"target_proof_score_weight_bps,omitempty"`
	TargetAvailabilityWeightBPS       uint64   `json:"target_availability_weight_bps,omitempty"`
	TargetDecentralizationWeightBPS   uint64   `json:"target_decentralization_weight_bps,omitempty"`
	TargetRetrievalRewardPerMiB       uint64   `json:"target_retrieval_reward_per_mib,omitempty"`
	TargetMaxRetrievalRewardPerWindow uint64   `json:"target_max_retrieval_reward_per_window,omitempty"`
	TargetRepairRewardPerShard        uint64   `json:"target_repair_reward_per_shard,omitempty"`
	TargetRepairPoolTakeoverBPS       uint64   `json:"target_repair_pool_takeover_bps,omitempty"`
	TargetRepairPoolSubsidyBPS        uint64   `json:"target_repair_pool_subsidy_bps,omitempty"`
	TargetMinerDegradeThreshold       uint64   `json:"target_miner_degrade_threshold,omitempty"`
	TargetStorageProofSamples         int      `json:"target_storage_proof_samples,omitempty"`
	TargetValidatorCommissionBPS      uint64   `json:"target_validator_commission_bps,omitempty"`
	TargetRetrievalWeightBPS          uint64   `json:"target_retrieval_weight_bps,omitempty"`
	TargetFoundationReleaseRateBPS    uint64   `json:"target_foundation_release_rate_bps,omitempty"`
	TargetFoundationAddress           string   `json:"target_foundation_address,omitempty"`
	TargetRetrievalAddress            string   `json:"target_retrieval_address,omitempty"`
	TargetStorageRewardPerBlock       uint64   `json:"target_storage_reward_per_block,omitempty"`
	TargetRetrievalAnnualRateBPS      uint64   `json:"target_retrieval_annual_rate_bps,omitempty"`
	TargetFoundationAnnualRateBPS     uint64   `json:"target_foundation_annual_rate_bps,omitempty"`
	TargetAvailabilityWindowSize      uint64   `json:"target_availability_window_size,omitempty"`
	TargetAvailabilityThresholdBPS    uint64   `json:"target_availability_threshold_bps,omitempty"`
	TargetBlockProductionRewardBPS    uint64   `json:"target_block_production_reward_bps,omitempty"`
	TargetValidatorRewardPerBlock     uint64   `json:"target_validator_reward_per_block,omitempty"`
	TargetMaxConsensusValidators      uint64   `json:"target_max_consensus_validators,omitempty"`
	TargetMinConsensusValidators      uint64   `json:"target_min_consensus_validators,omitempty"`
	TargetBlockBytes                  uint64   `json:"target_block_bytes,omitempty"`
	TargetMaxBlockBytes               uint64   `json:"target_max_block_bytes,omitempty"`
	TargetMaxBlockTxs                 uint64   `json:"target_max_block_txs,omitempty"`
	TargetMaxTxBytes                  uint64   `json:"target_max_tx_bytes,omitempty"`
	TargetMaxStorageTxBytes           uint64   `json:"target_max_storage_tx_bytes,omitempty"`
	Nonce                             uint64   `json:"nonce"`
	CreatedAtUnix                     int64    `json:"created_at_unix"`
}

// governanceVoteSigningPayload is the canonical payload for vote signatures.
type governanceVoteSigningPayload struct {
	ProposalID    string `json:"proposal_id"`
	Voter         string `json:"voter"`
	Approve       bool   `json:"approve"`
	ChainID       string `json:"chain_id"`
	Nonce         uint64 `json:"nonce"`
	CreatedAtUnix int64  `json:"created_at_unix"`
}

// GovernanceProposalPayload returns the JSON-encoded signing payload for a proposal.
func GovernanceProposalPayload(req CreateGovernanceProposalRequest) ([]byte, error) {
	payload := governanceProposalSigningPayload{
		Proposer:                          req.Proposer,
		ChainID:                           req.ChainID,
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
		TargetStorageRewardPerBlock:       req.TargetStorageRewardPerBlock,
		TargetRetrievalAnnualRateBPS:      req.TargetRetrievalAnnualRateBPS,
		TargetFoundationAnnualRateBPS:     req.TargetFoundationAnnualRateBPS,
		TargetAvailabilityWindowSize:      req.TargetAvailabilityWindowSize,
		TargetAvailabilityThresholdBPS:    req.TargetAvailabilityThresholdBPS,
		TargetBlockProductionRewardBPS:    req.TargetBlockProductionRewardBPS,
		TargetValidatorRewardPerBlock:     req.TargetValidatorRewardPerBlock,
		TargetMaxConsensusValidators:      req.TargetMaxConsensusValidators,
		TargetMinConsensusValidators:      req.TargetMinConsensusValidators,
		TargetBlockBytes:                  req.TargetBlockBytes,
		TargetMaxBlockBytes:               req.TargetMaxBlockBytes,
		TargetMaxBlockTxs:                 req.TargetMaxBlockTxs,
		TargetMaxTxBytes:                  req.TargetMaxTxBytes,
		TargetMaxStorageTxBytes:           req.TargetMaxStorageTxBytes,
		Nonce:                             req.Nonce,
		CreatedAtUnix:                     req.CreatedAtUnix,
	}
	return json.Marshal(payload)
}

// GovernanceProposalHash returns the Keccak256 hash of the proposal signing payload.
func GovernanceProposalHash(req CreateGovernanceProposalRequest) ([]byte, error) {
	payload, err := GovernanceProposalPayload(req)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

// SignGovernanceProposal signs the proposal request with the given ECDSA private key.
func SignGovernanceProposal(req *CreateGovernanceProposalRequest, privateKey *ecdsa.PrivateKey) error {
	hash, err := GovernanceProposalHash(*req)
	if err != nil {
		return err
	}
	signature, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return err
	}
	req.Signature = encodeHex(signature)
	return nil
}

// recoverGovernanceProposalSigner recovers the public key and address from a proposal signature.
func recoverGovernanceProposalSigner(req CreateGovernanceProposalRequest) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	if len(signature) != 65 {
		return nil, "", errors.New("invalid governance proposal signature size")
	}
	hash, err := GovernanceProposalHash(req)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}

// RecoverGovernanceProposalPublicKey recovers the hex-encoded public key from a proposal signature.
func RecoverGovernanceProposalPublicKey(req CreateGovernanceProposalRequest) (string, error) {
	publicKey, _, err := recoverGovernanceProposalSigner(req)
	if err != nil {
		return "", err
	}
	return encodeHex(ethcrypto.FromECDSAPub(publicKey)), nil
}

// GovernanceOperatorAddress derives the Ethereum address from a hex-encoded ECDSA public key.
// Returns empty string if the public key is invalid.
func GovernanceOperatorAddress(hexPublicKey string) string {
	pubBytes, err := decodeHex(hexPublicKey)
	if err != nil {
		return ""
	}
	pub, err := ethcrypto.UnmarshalPubkey(pubBytes)
	if err != nil {
		return ""
	}
	return AccountAddress(pub)
}

// VerifyGovernanceProposal verifies the proposal signature by recovering the signer
// and comparing the derived address against the expected address.
func VerifyGovernanceProposal(req CreateGovernanceProposalRequest, expectedAddress string) error {
	_, recoveredAddress, err := recoverGovernanceProposalSigner(req)
	if err != nil {
		return err
	}
	if !strings.EqualFold(recoveredAddress, expectedAddress) {
		return errors.New("governance proposal signature does not match proposer address")
	}
	return nil
}

// GovernanceVotePayload returns the JSON-encoded signing payload for a vote.
func GovernanceVotePayload(req CastGovernanceVoteRequest) ([]byte, error) {
	payload := governanceVoteSigningPayload{
		ProposalID:    req.ProposalID,
		Voter:         req.Voter,
		Approve:       req.Approve,
		ChainID:       req.ChainID,
		Nonce:         req.Nonce,
		CreatedAtUnix: req.CreatedAtUnix,
	}
	return json.Marshal(payload)
}

// GovernanceVoteHash returns the Keccak256 hash of the vote signing payload.
func GovernanceVoteHash(req CastGovernanceVoteRequest) ([]byte, error) {
	payload, err := GovernanceVotePayload(req)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

// SignGovernanceVote signs the vote request with the given ECDSA private key.
func SignGovernanceVote(req *CastGovernanceVoteRequest, privateKey *ecdsa.PrivateKey) error {
	hash, err := GovernanceVoteHash(*req)
	if err != nil {
		return err
	}
	signature, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return err
	}
	req.Signature = encodeHex(signature)
	return nil
}

// recoverGovernanceVoteSigner recovers the public key and address from a vote signature.
func recoverGovernanceVoteSigner(req CastGovernanceVoteRequest) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	if len(signature) != 65 {
		return nil, "", errors.New("invalid governance vote signature size")
	}
	hash, err := GovernanceVoteHash(req)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}

// RecoverGovernanceVotePublicKey recovers the hex-encoded public key from a vote signature.
func RecoverGovernanceVotePublicKey(req CastGovernanceVoteRequest) (string, error) {
	publicKey, _, err := recoverGovernanceVoteSigner(req)
	if err != nil {
		return "", err
	}
	return encodeHex(ethcrypto.FromECDSAPub(publicKey)), nil
}

// VerifyGovernanceVote verifies the vote signature by recovering the signer
// and comparing the derived address against the expected address.
func VerifyGovernanceVote(req CastGovernanceVoteRequest, expectedAddress string) error {
	_, recoveredAddress, err := recoverGovernanceVoteSigner(req)
	if err != nil {
		return err
	}
	if !strings.EqualFold(recoveredAddress, expectedAddress) {
		return errors.New("governance vote signature does not match voter address")
	}
	return nil
}

// governanceExecuteSigningPayload is the canonical payload for execute-request signatures.
type governanceExecuteSigningPayload struct {
	ProposalID    string `json:"proposal_id"`
	Executor      string `json:"executor"`
	ChainID       string `json:"chain_id"`
	Nonce         uint64 `json:"nonce"`
	CreatedAtUnix int64  `json:"created_at_unix"`
}

// GovernanceExecuteHash returns the Keccak256 hash of the execute signing payload.
func GovernanceExecuteHash(req ExecuteGovernanceProposalRequest) ([]byte, error) {
	p, err := json.Marshal(governanceExecuteSigningPayload{
		ProposalID:    req.ProposalID,
		Executor:      NormalizeAddress(req.Executor),
		ChainID:       req.ChainID,
		Nonce:         req.Nonce,
		CreatedAtUnix: req.CreatedAtUnix,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

// SignGovernanceExecute signs the execute request with the given ECDSA private key.
func SignGovernanceExecute(req *ExecuteGovernanceProposalRequest, privateKey *ecdsa.PrivateKey) error {
	hash, err := GovernanceExecuteHash(*req)
	if err != nil {
		return err
	}
	signature, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return err
	}
	req.Signature = encodeHex(signature)
	return nil
}

// VerifyGovernanceExecute verifies the execute-request signature by recovering the signer
// and comparing the derived address against the expected address.
func VerifyGovernanceExecute(req ExecuteGovernanceProposalRequest, expectedAddress string) error {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return err
	}
	if len(signature) != 65 {
		return errors.New("invalid governance execute signature size")
	}
	hash, err := GovernanceExecuteHash(req)
	if err != nil {
		return err
	}
	pub, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return err
	}
	addr := AccountAddress(pub)
	if !strings.EqualFold(addr, expectedAddress) {
		return errors.New("governance execute signature does not match executor address")
	}
	return nil
}
