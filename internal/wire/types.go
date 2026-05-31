package wire

import "encoding/json"

const (
	StatusUploading = "uploading"
	StatusPartial   = "partial"
	StatusFinalized = "finalized"
	StatusExpired   = "expired"
	StatusDeleted   = "deleted"
)

const (
	StorageStatusPending     = "pending"
	StorageStatusActive      = "active"
	StorageStatusExpired     = "expired"
	StorageStatusTerminating = "terminating"
	StorageStatusDeleted     = "deleted"
)

const (
	AccessStatusPublic    = "public"
	AccessStatusPrivate   = "private"
	AccessStatusSuspended = "suspended"
	AccessStatusBlocked   = "blocked"
)

const (
	ModerationStatusNone      = "none"
	ModerationStatusFrozen    = "frozen"
	ModerationStatusBlocked   = "blocked"
	ModerationStatusLegalHold = "legal_hold"
	ModerationStatusAppealed  = "appealed"
)

const (
	GovProposalPending   = "pending"
	GovProposalExecuted  = "executed"
	GovProposalRejected  = "rejected"
	GovProposalExpired   = "expired"
	GovProposalCancelled = "cancelled"
)

const (
	BridgeStatusLocked  = "locked"
	BridgeStatusPending = "pending"
	BridgeStatusClaimed = "claimed"
)

type StoragePolicy struct {
	Class          string `json:"class"`
	Duration       int64  `json:"duration"`
	Redundancy     string `json:"redundancy"`
	Renewable      bool   `json:"renewable,omitempty"`
	AutoRenew      bool   `json:"auto_renew,omitempty"`
	DeletionPolicy string `json:"deletion_policy,omitempty"`
}

const (
	DeletionPolicyStandard  = "standard"
	DeletionPolicyRetain    = "retain_evidence"
	DeletionPolicyImmediate = "immediate"
)

type ErasurePolicy struct {
	DataShards   int `json:"data_shards"`
	ParityShards int `json:"parity_shards"`
	ShardSize    int `json:"shard_size"`
}

type EncryptionMetadata struct {
	Algorithm            string `json:"algorithm"`
	KeyHash              string `json:"key_hash"`
	NonceBase64          string `json:"nonce_base64"`
	PlaintextSize        int64  `json:"plaintext_size"`
	PlaintextSegmentSize int64  `json:"plaintext_segment_size"`
}

type SegmentPlan struct {
	SegmentID   int      `json:"segment_id"`
	SegmentRoot string   `json:"segment_root"`
	ShardHashes []string `json:"shard_hashes"`
	ShardCIDs   []string `json:"shard_cids,omitempty"`
}

// RepairPool pairs two consecutive segments and holds their cross-parity
// shards. Cross-parity shard[j] = segA.shard[j] XOR segB.shard[j], enabling
// single-shard repair with only 2 downloads instead of k.
type RepairPool struct {
	PoolID      int             `json:"pool_id"`
	SegmentIDs  [2]int          `json:"segment_ids"`
	CrossParity CrossParityPlan `json:"cross_parity"`
}

// CrossParityPlan stores the hashes and metadata for cross-parity shards
// computed from a pair of segments. Cross-parity receipts are stored in
// Intent.Receipts under negative segment IDs: pool P → segmentID -(P+1).
type CrossParityPlan struct {
	ShardHashes []string `json:"shard_hashes"`
	ShardCIDs   []string `json:"shard_cids,omitempty"`
	ShardSize   int64    `json:"shard_size"`
}

type StorageAssignment struct {
	SegmentID    int    `json:"segment_id"`
	ShardIndex   int    `json:"shard_index"`
	MinerAddress string `json:"miner_address"`
	Endpoint     string `json:"endpoint,omitempty"`
	ShardHash    string `json:"shard_hash,omitempty"`
	ShardCID     string `json:"shard_cid,omitempty"`
	ShardSize    int64  `json:"shard_size"`
}

type CreateIntentRequest struct {
	ChainID        string              `json:"chain_id,omitempty"`
	User           string              `json:"user"`
	FileName       string              `json:"file_name"`
	FileSize       int64               `json:"file_size"`
	SegmentSize    int64               `json:"segment_size"`
	FileRoot       string              `json:"file_root"`
	SegmentRoots   []string            `json:"segment_roots"`
	Segments       []SegmentPlan       `json:"segments"`
	RepairPools    []RepairPool        `json:"repair_pools,omitempty"`
	Erasure        ErasurePolicy       `json:"erasure"`
	Encryption     *EncryptionMetadata `json:"encryption,omitempty"`
	Policy         StoragePolicy       `json:"policy"`
	LockedFee      uint64              `json:"locked_fee"`
	DeadlineUnix   int64               `json:"deadline_unix"`
	Nonce          uint64              `json:"nonce,omitempty"`
	Signature      string              `json:"signature,omitempty"`
	PublicKey      string              `json:"public_key,omitempty"`
	AgentKeyID     string              `json:"agent_key_id,omitempty"`
	AgentNonce     uint64              `json:"agent_nonce,omitempty"`
	AgentPublicKey string              `json:"agent_public_key,omitempty"`
	AgentSignature string              `json:"agent_signature,omitempty"`
}

type CreateIntentResponse struct {
	IntentID      string              `json:"intent_id"`
	Status        string              `json:"status"`
	RequiredFee   uint64              `json:"required_fee"`
	LockedFee     uint64              `json:"locked_fee"`
	BurnedFee     uint64              `json:"burned_fee,omitempty"`
	RetrievalFee  uint64              `json:"retrieval_fee,omitempty"`
	FoundationFee uint64              `json:"foundation_fee,omitempty"`
	Assignments   []StorageAssignment `json:"assignments,omitempty"`
}

type StoragePricing struct {
	BasePrice         uint64 `json:"base_price"`
	MinimumFee        uint64 `json:"minimum_fee"`
	PermanentDuration int64  `json:"permanent_duration"`
}

type PermanentStorageFund struct {
	IntentID             string `json:"intent_id"`
	User                 string `json:"user"`
	Balance              uint64 `json:"balance"`
	Contributed          uint64 `json:"contributed"`
	Paid                 uint64 `json:"paid"`
	Burned               uint64 `json:"burned,omitempty"`
	SustainableDailyRate uint64 `json:"sustainable_daily_rate,omitempty"`
	InitialDailyRate     uint64 `json:"initial_daily_rate,omitempty"`
	CreatedAtUnix        int64  `json:"created_at_unix"`
	UpdatedAtUnix        int64  `json:"updated_at_unix"`
	LastPayoutUnix       int64  `json:"last_payout_unix,omitempty"`
	Closed               bool   `json:"closed,omitempty"`
	ClosedReason         string `json:"closed_reason,omitempty"`
	ClosedAtUnix         int64  `json:"closed_at_unix,omitempty"`
	TransferredToPool    uint64 `json:"transferred_to_pool,omitempty"`
}

type PermanentFundTopUpRequest struct {
	ChainID   string `json:"chain_id,omitempty"`
	IntentID  string `json:"intent_id"`
	User      string `json:"user"`
	Amount    uint64 `json:"amount"`
	Nonce     uint64 `json:"nonce,omitempty"`
	Signature string `json:"signature,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
}

type PermanentFundTopUpResponse struct {
	Fund PermanentStorageFund `json:"fund"`
}

type StorageFeePool struct {
	TotalLocked             uint64 `json:"total_locked"`
	TotalPaid               uint64 `json:"total_paid"`
	TotalRefunded           uint64 `json:"total_refunded"`
	TotalBurned             uint64 `json:"total_burned,omitempty"`
	TotalToRetrieval        uint64 `json:"total_to_retrieval,omitempty"`
	TotalToFoundation       uint64 `json:"total_to_foundation,omitempty"`
	TransferredToRewardPool uint64 `json:"transferred_to_reward_pool,omitempty"`
	RepairPoolTransferred   uint64 `json:"repair_pool_transferred,omitempty"`
	PermanentFundBalance    uint64 `json:"permanent_fund_balance"`
	InsuranceReserve        uint64 `json:"insurance_reserve,omitempty"`
}

type DealEscrow struct {
	IntentID          string `json:"intent_id"`
	User              string `json:"user"`
	LockedFee         uint64 `json:"locked_fee"`
	PaidFee           uint64 `json:"paid_fee"`
	RefundedFee       uint64 `json:"refunded_fee"`
	BurnedFee         uint64 `json:"burned_fee,omitempty"`
	AccruedFee        uint64 `json:"accrued_fee,omitempty"`
	StartAtUnix       int64  `json:"start_at_unix,omitempty"`
	ExpiresAtUnix     int64  `json:"expires_at_unix,omitempty"`
	LastAccruedAtUnix int64  `json:"last_accrued_at_unix,omitempty"`
	Status            string `json:"status"`
	Permanent         bool   `json:"permanent,omitempty"`
}

type ChainStatusResponse struct {
	ChainID                  string         `json:"chain_id,omitempty"`
	Status                   string         `json:"status"`
	Height                   uint64         `json:"height"`
	LatestBlockHash          string         `json:"latest_block_hash,omitempty"`
	LatestBlockTimeUnix      int64          `json:"latest_block_time_unix,omitempty"`
	LatestFinalizedHeight    uint64         `json:"latest_finalized_height,omitempty"`
	PendingTransactions      int            `json:"pending_transactions"`
	Accounts                 int            `json:"accounts"`
	Intents                  int            `json:"intents"`
	UploadingIntents         int            `json:"uploading_intents"`
	PartialIntents           int            `json:"partial_intents"`
	FinalizedIntents         int            `json:"finalized_intents"`
	ExpiredIntents           int            `json:"expired_intents"`
	Deals                    int            `json:"deals"`
	Collections              int            `json:"collections"`
	DataRecords              int            `json:"data_records"`
	Miners                   int            `json:"miners"`
	ActiveMiners             int            `json:"active_miners"`
	CapacityBytes            uint64         `json:"capacity_bytes"`
	UsedBytes                uint64         `json:"used_bytes"`
	ReservedBytes            uint64         `json:"reserved_bytes"`
	Validators               int            `json:"validators"`
	ConsensusValidators      int            `json:"consensus_validators"`
	EpochRound               uint64         `json:"epoch_round,omitempty"`
	EpochsFinalized          uint64         `json:"epochs_finalized,omitempty"`
	PendingChallenges        int            `json:"pending_challenges"`
	ActiveEpochs             int            `json:"active_epochs"`
	PendingRepairTasks       int            `json:"pending_repair_tasks"`
	CompletedRepairTasks     int            `json:"completed_repair_tasks"`
	DealsAtRisk              int            `json:"deals_at_risk,omitempty"`
	DealsCritical            int            `json:"deals_critical,omitempty"`
	TotalStorageRewards      uint64         `json:"total_storage_rewards,omitempty"`
	TotalRetrievalRewards    uint64         `json:"total_retrieval_rewards,omitempty"`
	TotalRepairRewards       uint64         `json:"total_repair_rewards,omitempty"`
	TotalMiningPending       uint64         `json:"total_mining_pending,omitempty"`
	PendingMiningBuckets     int            `json:"pending_mining_buckets,omitempty"`
	StorageFeePool           StorageFeePool `json:"storage_fee_pool,omitempty"`
	TotalSlashed             uint64         `json:"total_slashed,omitempty"`
	TotalSupply              uint64         `json:"total_supply,omitempty"`
	StoragePoolRemaining     uint64         `json:"storage_pool_remaining,omitempty"`
	RetrievalPoolRemaining   uint64         `json:"retrieval_pool_remaining,omitempty"`
	ValidatorPoolRemaining   uint64         `json:"validator_pool_remaining,omitempty"`
	ValidatorRewardPerBlock  uint64         `json:"validator_reward_per_block,omitempty"`
	StorageRewardPerBlock    uint64         `json:"storage_reward_per_block,omitempty"`
	AverageAvailabilityBPS   uint64         `json:"average_availability_bps,omitempty"`
	ValidatorsBelowThreshold int            `json:"validators_below_threshold,omitempty"`
	PermanentFundRemaining      uint64         `json:"repair_pool_remaining,omitempty"`
	FoundationPoolRemaining  uint64         `json:"foundation_pool_remaining,omitempty"`
	FoundationAddress        string         `json:"foundation_address,omitempty"`
	RetrievalAddress         string         `json:"retrieval_address,omitempty"`
	TokensReleased           uint64         `json:"tokens_released,omitempty"`
	RetrievalReceipts        int            `json:"retrieval_receipts"`
	RetrievalBytes           uint64         `json:"retrieval_bytes"`
	FeeMarket                FeeMarket      `json:"fee_market"`
	StoragePricing           StoragePricing `json:"storage_pricing"`
	PeerCount                int            `json:"peer_count,omitempty"`
	Peers                    []string       `json:"peers,omitempty"`
	LibP2PEnabled            bool           `json:"libp2p_enabled,omitempty"`
	LibP2PID                 string         `json:"libp2p_id,omitempty"`
	LibP2PAddrs              []string       `json:"libp2p_addrs,omitempty"`
	BonusGrantedCount        uint64         `json:"bonus_granted_count,omitempty"`
	MaxBonusAddresses        uint64         `json:"max_bonus_addresses,omitempty"`
	RegistrationBonusAmount  uint64         `json:"registration_bonus_amount,omitempty"`
	StakePerTiB              uint64         `json:"stake_per_tib,omitempty"`
	MinCapacityBytes         uint64         `json:"min_capacity_bytes,omitempty"`
	MinerNFTTemplate         string         `json:"miner_nft_template,omitempty"`
	MinerNFTContentType      string         `json:"miner_nft_content_type,omitempty"`
	MinerNFTTemplateHash     string         `json:"miner_nft_template_hash,omitempty"`
}

type StorageNodeStatusResponse struct {
	Status                 string                    `json:"status"`
	Address                string                    `json:"address"`
	PublicKey              string                    `json:"public_key"`
	ShardCount             int                       `json:"shard_count"`
	StoredBytes            uint64                    `json:"stored_bytes"`
	DataDir                string                    `json:"data_dir,omitempty"`
	AccessServiceRequired  bool                      `json:"access_service_required,omitempty"`
	UploadServiceEnabled   bool                      `json:"upload_service_enabled,omitempty"`
	DownloadServiceEnabled bool                      `json:"download_service_enabled,omitempty"`
	PeerID                 string                    `json:"peer_id,omitempty"`
	PeerAddrs              []string                  `json:"peer_addrs,omitempty"`
	TransportStats         StorageTransportStats     `json:"transport_stats"`
	RecentProviderMemories []ProviderTransportMemory `json:"recent_provider_memories,omitempty"`
	DHTEnabled             bool                      `json:"dht_enabled,omitempty"`
	DHTPeers               int                       `json:"dht_peers,omitempty"`
	DHTShardCount          int                       `json:"dht_shard_count,omitempty"`
	BlacklistCount         int                       `json:"blacklist_count,omitempty"`
}

type StorageTransportStats struct {
	LibP2PFetchSuccess uint64 `json:"libp2p_fetch_success"`
	LibP2PFetchErrors  uint64 `json:"libp2p_fetch_errors"`
	HTTPFallbacks      uint64 `json:"http_fallbacks"`
	HTTPBlockFetchHits uint64 `json:"http_block_fetch_hits"`
	HTTPShardFetchHits uint64 `json:"http_shard_fetch_hits"`
	LibP2PServeHits    uint64 `json:"libp2p_serve_hits"`
	HTTPBlockServeHits uint64 `json:"http_block_serve_hits"`
	HTTPShardServeHits uint64 `json:"http_shard_serve_hits"`
}

type ProviderTransportMemory struct {
	ProviderKey         string `json:"provider_key"`
	MinerAddress        string `json:"miner_address,omitempty"`
	Endpoint            string `json:"endpoint,omitempty"`
	PeerID              string `json:"peer_id,omitempty"`
	LastTransport       string `json:"last_transport,omitempty"`
	LastOutcome         string `json:"last_outcome,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	LastSuccessUnix     int64  `json:"last_success_unix,omitempty"`
	LastFailureUnix     int64  `json:"last_failure_unix,omitempty"`
	ConsecutiveFailures uint64 `json:"consecutive_failures,omitempty"`
	CooldownUntilUnix   int64  `json:"cooldown_until_unix,omitempty"`
	UpdatedAtUnix       int64  `json:"updated_at_unix,omitempty"`
}

type StorageQuoteRequest struct {
	FileSize    int64         `json:"file_size"`
	SegmentSize int64         `json:"segment_size,omitempty"`
	Erasure     ErasurePolicy `json:"erasure"`
	Policy      StoragePolicy `json:"policy"`
	RepairPools []RepairPool  `json:"repair_pools,omitempty"`
}

type StorageQuoteResponse struct {
	Pricing               StoragePricing `json:"pricing"`
	FileSize              int64          `json:"file_size"`
	RedundantBytes        uint64         `json:"redundant_bytes"`
	Duration              int64          `json:"duration"`
	TotalMiBDays          uint64         `json:"total_mib_days"`
	UtilizationBPS        uint64         `json:"utilization_bps"`
	UtilizationMultiplier uint64         `json:"utilization_multiplier"`
	RequiredFee           uint64         `json:"required_fee"`
	ActiveCapacityBytes   uint64         `json:"active_capacity_bytes"`
	ActiveUsedBytes       uint64         `json:"active_used_bytes"`
}

type UploadRequest struct {
	IntentID    string `json:"intent_id"`
	User        string `json:"user"`
	FileRoot    string `json:"file_root"`
	SegmentID   int    `json:"segment_id"`
	SegmentRoot string `json:"segment_root"`
	ShardIndex  int    `json:"shard_index"`
	ShardID     string `json:"shard_id"`
	ShardHash   string `json:"shard_hash"`
	ShardCID    string `json:"shard_cid,omitempty"`
	ShardSize   int64  `json:"shard_size"`
	PolicyHash  string `json:"policy_hash"`
	DataBase64  string `json:"data_base64"`
}

type MinerReceipt struct {
	Version          int    `json:"version"`
	MinerAddress     string `json:"miner_address"`
	MinerPublicKey   string `json:"miner_public_key"`
	User             string `json:"user"`
	IntentID         string `json:"intent_id"`
	FileRoot         string `json:"file_root"`
	SegmentID        int    `json:"segment_id"`
	SegmentRoot      string `json:"segment_root"`
	ShardIndex       int    `json:"shard_index"`
	ShardID          string `json:"shard_id"`
	ShardHash        string `json:"shard_hash"`
	ShardCID         string `json:"shard_cid,omitempty"`
	ShardSize        int64  `json:"shard_size"`
	SectorCommitment string `json:"sector_commitment"`
	MinerSeal        string `json:"miner_seal,omitempty"`
	ExpiresAtUnix    int64  `json:"expires_at_unix"`
	MinerEndpoint    string `json:"miner_endpoint,omitempty"`
	Signature        string `json:"signature"`
}

type BatchCommitRequest struct {
	ChainID        string         `json:"chain_id,omitempty"`
	IntentID       string         `json:"intent_id"`
	User           string         `json:"user"`
	Receipts       []MinerReceipt `json:"receipts"`
	Nonce          uint64         `json:"nonce,omitempty"`
	Signature      string         `json:"signature,omitempty"`
	PublicKey      string         `json:"public_key,omitempty"`
	AgentKeyID     string         `json:"agent_key_id,omitempty"`
	AgentNonce     uint64         `json:"agent_nonce,omitempty"`
	AgentPublicKey string         `json:"agent_public_key,omitempty"`
	AgentSignature string         `json:"agent_signature,omitempty"`
}

type BatchCommitResponse struct {
	IntentID          string `json:"intent_id"`
	Status            string `json:"status"`
	CommittedSegments int    `json:"committed_segments"`
	UploadedSize      int64  `json:"uploaded_size"`
}

type FinalizeRequest struct {
	ChainID        string `json:"chain_id,omitempty"`
	IntentID       string `json:"intent_id"`
	User           string `json:"user"`
	ManifestRoot   string `json:"manifest_root"`
	Nonce          uint64 `json:"nonce,omitempty"`
	Signature      string `json:"signature,omitempty"`
	PublicKey      string `json:"public_key,omitempty"`
	AgentKeyID     string `json:"agent_key_id,omitempty"`
	AgentNonce     uint64 `json:"agent_nonce,omitempty"`
	AgentPublicKey string `json:"agent_public_key,omitempty"`
	AgentSignature string `json:"agent_signature,omitempty"`
}

type FinalizeResponse struct {
	IntentID string `json:"intent_id"`
	DealID   string `json:"deal_id"`
	Status   string `json:"status"`
}

type IntentView struct {
	IntentID                string              `json:"intent_id"`
	User                    string              `json:"user"`
	FileName                string              `json:"file_name"`
	FileSize                int64               `json:"file_size"`
	SegmentSize             int64               `json:"segment_size"`
	FileRoot                string              `json:"file_root"`
	SegmentRoots            []string            `json:"segment_roots"`
	Segments                []SegmentPlan       `json:"segments"`
	RepairPools             []RepairPool        `json:"repair_pools,omitempty"`
	Assignments             []StorageAssignment `json:"assignments,omitempty"`
	Erasure                 ErasurePolicy       `json:"erasure"`
	Encryption              *EncryptionMetadata `json:"encryption,omitempty"`
	Policy                  StoragePolicy       `json:"policy"`
	LockedFee               uint64              `json:"locked_fee"`
	PaidFee                 uint64              `json:"paid_fee,omitempty"`
	RefundedFee             uint64              `json:"refunded_fee,omitempty"`
	BurnedFee               uint64              `json:"burned_fee,omitempty"`
	PermanentFundBalance    uint64              `json:"permanent_fund_balance,omitempty"`
	PermanentFundPaid       uint64              `json:"permanent_fund_paid,omitempty"`
	UploadedSize            int64               `json:"uploaded_size"`
	CommittedSegments       int                 `json:"committed_segments"`
	Status                  string              `json:"status"`
	StorageStatus           string              `json:"storage_status,omitempty"`
	AccessStatus            string              `json:"access_status,omitempty"`
	ModerationStatus        string              `json:"moderation_status,omitempty"`
	BlockedReasonHash       string              `json:"blocked_reason_hash,omitempty"`
	AccessUpdatedAtUnix     int64               `json:"access_updated_at_unix,omitempty"`
	ModerationExpiresAtUnix int64               `json:"moderation_expires_at_unix,omitempty"`
	AppealDeadlineUnix      int64               `json:"appeal_deadline_unix,omitempty"`
	ExpiresAtUnix           int64               `json:"expires_at_unix,omitempty"`
	TerminatedAtUnix        int64               `json:"terminated_at_unix,omitempty"`
	DeleteTaskCount         int                 `json:"delete_task_count,omitempty"`
	DeleteReceiptCount      int                 `json:"delete_receipt_count,omitempty"`
	DealID                  string              `json:"deal_id,omitempty"`
}

type SettleIntentRequest struct {
	ChainID   string `json:"chain_id,omitempty"`
	IntentID  string `json:"intent_id"`
	User      string `json:"user,omitempty"`
	Nonce     uint64 `json:"nonce,omitempty"`
	Signature string `json:"signature,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
}

type SettleIntentResponse struct {
	IntentID    string `json:"intent_id"`
	Status      string `json:"status"`
	RefundedFee uint64 `json:"refunded_fee"`
	PaidFee     uint64 `json:"paid_fee"`
}

type RenewDealRequest struct {
	ChainID        string `json:"chain_id,omitempty"`
	IntentID       string `json:"intent_id"`
	User           string `json:"user"`
	Duration       int64  `json:"duration"`
	Nonce          uint64 `json:"nonce,omitempty"`
	Signature      string `json:"signature,omitempty"`
	PublicKey      string `json:"public_key,omitempty"`
	AgentKeyID     string `json:"agent_key_id,omitempty"`
	AgentNonce     uint64 `json:"agent_nonce,omitempty"`
	AgentPublicKey string `json:"agent_public_key,omitempty"`
	AgentSignature string `json:"agent_signature,omitempty"`
}

type RenewDealResponse struct {
	IntentID      string `json:"intent_id"`
	Status        string `json:"status"`
	ExpiresAtUnix int64  `json:"expires_at_unix"`
	NewLockedFee  uint64 `json:"new_locked_fee"`
	PaidAmount    uint64 `json:"paid_amount"`
	GraceUsed     bool   `json:"grace_used,omitempty"`
}

type TerminateDealRequest struct {
	ChainID        string `json:"chain_id,omitempty"`
	IntentID       string `json:"intent_id"`
	User           string `json:"user"`
	Reason         string `json:"reason,omitempty"`
	Nonce          uint64 `json:"nonce,omitempty"`
	Signature      string `json:"signature,omitempty"`
	PublicKey      string `json:"public_key,omitempty"`
	AgentKeyID     string `json:"agent_key_id,omitempty"`
	AgentNonce     uint64 `json:"agent_nonce,omitempty"`
	AgentPublicKey string `json:"agent_public_key,omitempty"`
	AgentSignature string `json:"agent_signature,omitempty"`
}

type TerminateDealResponse struct {
	IntentID         string       `json:"intent_id"`
	Status           string       `json:"status"`
	StorageStatus    string       `json:"storage_status"`
	AccessStatus     string       `json:"access_status"`
	RefundedFee      uint64       `json:"refunded_fee"`
	BurnedFee        uint64       `json:"burned_fee,omitempty"`
	DeleteTasks      []DeleteTask `json:"delete_tasks,omitempty"`
	TerminatedAtUnix int64        `json:"terminated_at_unix"`
}

type SetAccessPolicyRequest struct {
	ChainID      string `json:"chain_id,omitempty"`
	IntentID     string `json:"intent_id"`
	User         string `json:"user"`
	AccessStatus string `json:"access_status"`
	ReasonHash   string `json:"reason_hash,omitempty"`
	Nonce        uint64 `json:"nonce,omitempty"`
	Signature    string `json:"signature,omitempty"`
	PublicKey    string `json:"public_key,omitempty"`
}

type SetAccessPolicyResponse struct {
	IntentID         string `json:"intent_id"`
	AccessStatus     string `json:"access_status"`
	ModerationStatus string `json:"moderation_status,omitempty"`
	UpdatedAtUnix    int64  `json:"updated_at_unix"`
}

type GovernanceDealActionRequest struct {
	IntentID           string `json:"intent_id"`
	Operator           string `json:"operator"`
	Action             string `json:"action"`
	ReasonHash         string `json:"reason_hash"`
	ExpiresAtUnix      int64  `json:"expires_at_unix,omitempty"`
	AppealDeadlineUnix int64  `json:"appeal_deadline_unix,omitempty"`
	PreserveStorage    bool   `json:"preserve_storage,omitempty"`
}

type GovernanceDealActionResponse struct {
	IntentID                string `json:"intent_id"`
	GovernanceType          string `json:"governance_type,omitempty"`
	AccessStatus            string `json:"access_status"`
	ModerationStatus        string `json:"moderation_status"`
	StorageStatus           string `json:"storage_status"`
	BlockedReasonHash       string `json:"blocked_reason_hash,omitempty"`
	ModerationExpiresAtUnix int64  `json:"moderation_expires_at_unix,omitempty"`
	AppealDeadlineUnix      int64  `json:"appeal_deadline_unix,omitempty"`
	UpdatedAtUnix           int64  `json:"updated_at_unix"`
}

type DeleteTask struct {
	TaskID           string `json:"task_id"`
	IntentID         string `json:"intent_id"`
	ShardHash        string `json:"shard_hash"`
	MinerAddress     string `json:"miner_address"`
	MinerPublicKey   string `json:"miner_public_key"`
	Status           string `json:"status"`
	Reason           string `json:"reason,omitempty"`
	RetainPhysical   bool   `json:"retain_physical,omitempty"`
	ActiveReferences int    `json:"active_references,omitempty"`
	CreatedAtUnix    int64  `json:"created_at_unix"`
	CompletedAtUnix  int64  `json:"completed_at_unix,omitempty"`
	ReviewStatus     string `json:"review_status,omitempty"` // "pending_review" for direct actions
	ActionID         string `json:"action_id,omitempty"`     // Links to DirectActionRecord
}

type DeleteTaskResponse struct {
	Tasks []DeleteTask `json:"tasks"`
}

type GovernanceAuditRecord struct {
	AuditID                 string `json:"audit_id"`
	IntentID                string `json:"intent_id"`
	Operator                string `json:"operator"`
	Action                  string `json:"action"`
	GovernanceType          string `json:"governance_type,omitempty"`
	ReasonHash              string `json:"reason_hash"`
	PreserveStorage         bool   `json:"preserve_storage,omitempty"`
	AccessStatus            string `json:"access_status"`
	ModerationStatus        string `json:"moderation_status"`
	StorageStatus           string `json:"storage_status"`
	ModerationExpiresAtUnix int64  `json:"moderation_expires_at_unix,omitempty"`
	AppealDeadlineUnix      int64  `json:"appeal_deadline_unix,omitempty"`
	RecordedAtUnix          int64  `json:"recorded_at_unix"`
}

type CommitteeFreezeDealRequest struct {
	IntentID      string `json:"intent_id"`
	Operator      string `json:"operator"`
	ReasonHash    string `json:"reason_hash"`
	ExpiresAtUnix int64  `json:"expires_at_unix"`
}

type GovernanceBlockDealRequest struct {
	IntentID           string `json:"intent_id"`
	Operator           string `json:"operator"`
	ReasonHash         string `json:"reason_hash"`
	PreserveStorage    bool   `json:"preserve_storage,omitempty"`
	AppealDeadlineUnix int64  `json:"appeal_deadline_unix,omitempty"`
}

type GovernanceAuditResponse struct {
	Records []GovernanceAuditRecord `json:"records"`
}

type GovernanceOperator struct {
	Operator      string   `json:"operator"`
	PublicKey     string   `json:"public_key,omitempty"`
	Permissions   []string `json:"permissions"`
	Enabled       bool     `json:"enabled"`
	CreatedAtUnix int64    `json:"created_at_unix,omitempty"`
	Nonce         uint64   `json:"nonce,omitempty"`
}

type DeleteReceipt struct {
	IntentID       string `json:"intent_id"`
	ShardHash      string `json:"shard_hash"`
	MinerAddress   string `json:"miner_address"`
	MinerPublicKey string `json:"miner_public_key"`
	DeletedAtUnix  int64  `json:"deleted_at_unix"`
	Signature      string `json:"signature"`
}

type SubmitDeleteReceiptRequest struct {
	Receipt DeleteReceipt `json:"receipt"`
}

type SubmitDeleteReceiptResponse struct {
	IntentID           string `json:"intent_id"`
	ShardHash          string `json:"shard_hash"`
	MinerAddress       string `json:"miner_address"`
	DeleteReceiptCount int    `json:"delete_receipt_count"`
	Status             string `json:"status"`
}

type RetrievalReceipt struct {
	ReceiptID       string `json:"receipt_id"`
	RequestID       string `json:"request_id"`
	IntentID        string `json:"intent_id"`
	ShardHash       string `json:"shard_hash"`
	User            string `json:"user"`
	ClientAddress   string `json:"client_address"`
	MinerAddress    string `json:"miner_address"`
	MinerPublicKey  string `json:"miner_public_key"`
	BytesServed     uint64 `json:"bytes_served"`
	ServedAtUnix    int64  `json:"served_at_unix"`
	ClientSignature string `json:"client_signature"`
	MinerSignature  string `json:"miner_signature"`
}

type SubmitRetrievalReceiptRequest struct {
	Receipt RetrievalReceipt `json:"receipt"`
}

type SubmitRetrievalReceiptResponse struct {
	ReceiptID    string `json:"receipt_id"`
	IntentID     string `json:"intent_id"`
	MinerAddress string `json:"miner_address"`
	BytesServed  uint64 `json:"bytes_served"`
	Reward       uint64 `json:"reward"`
	Status       string `json:"status"`
}

type RetrievalReceiptResponse struct {
	Receipts []RetrievalReceipt `json:"receipts"`
}

type UploadPlan struct {
	IntentID          string              `json:"intent_id"`
	User              string              `json:"user"`
	FileName          string              `json:"file_name"`
	FileSize          int64               `json:"file_size"`
	SegmentSize       int64               `json:"segment_size"`
	FileRoot          string              `json:"file_root"`
	SegmentRoots      []string            `json:"segment_roots"`
	Segments          []SegmentPlan       `json:"segments"`
	RepairPools       []RepairPool        `json:"repair_pools,omitempty"`
	Assignments       []StorageAssignment `json:"assignments,omitempty"`
	Erasure           ErasurePolicy       `json:"erasure"`
	Encryption        *EncryptionMetadata `json:"encryption,omitempty"`
	Policy            StoragePolicy       `json:"policy"`
	LockedFee         uint64              `json:"locked_fee"`
	Receipts          []MinerReceipt          `json:"receipts"`
	CommittedSegments []int                   `json:"committed_segments"`
	CommittedShards   []ShardRef              `json:"committed_shards,omitempty"`
	ProviderCache     []StorageProviderRecord  `json:"provider_cache,omitempty"`
}

type StorageManifestResponse struct {
	IntentID     string     `json:"intent_id"`
	Status       string     `json:"status"`
	DealID       string     `json:"deal_id,omitempty"`
	Complete     bool       `json:"complete"`
	ReceiptCount int        `json:"receipt_count"`
	Plan         UploadPlan `json:"plan"`
}

type DataCollection struct {
	CollectionID  string            `json:"collection_id"`
	User          string            `json:"user"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAtUnix int64             `json:"created_at_unix"`
	UpdatedAtUnix int64             `json:"updated_at_unix"`
}

type DataRecord struct {
	RecordID      string            `json:"record_id"`
	CollectionID  string            `json:"collection_id"`
	User          string            `json:"user"`
	IntentID      string            `json:"intent_id"`
	DealID        string            `json:"deal_id,omitempty"`
	ParentRecord  string            `json:"parent_record,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Key           string            `json:"key,omitempty"`
	FileRoot      string            `json:"file_root"`
	ManifestRoot  string            `json:"manifest_root,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAtUnix int64             `json:"created_at_unix"`
}

type CollectionRecordFilter struct {
	Kind         string `json:"kind,omitempty"`
	Key          string `json:"key,omitempty"`
	ParentRecord string `json:"parent_record,omitempty"`
	AfterUnix    int64  `json:"after_unix,omitempty"`
	BeforeUnix   int64  `json:"before_unix,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Reverse      bool   `json:"reverse,omitempty"`
}

type CreateCollectionRequest struct {
	ChainID     string            `json:"chain_id,omitempty"`
	User        string            `json:"user"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Nonce       uint64            `json:"nonce,omitempty"`
	PublicKey   string            `json:"public_key,omitempty"`
	Signature   string            `json:"signature,omitempty"`
}

type CreateCollectionResponse struct {
	Collection DataCollection `json:"collection"`
}

type AppendRecordRequest struct {
	ChainID      string            `json:"chain_id,omitempty"`
	CollectionID string            `json:"collection_id"`
	User         string            `json:"user"`
	IntentID     string            `json:"intent_id"`
	ParentRecord string            `json:"parent_record,omitempty"`
	Kind         string            `json:"kind,omitempty"`
	Key          string            `json:"key,omitempty"`
	ManifestRoot string            `json:"manifest_root,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Nonce        uint64            `json:"nonce,omitempty"`
	PublicKey    string            `json:"public_key,omitempty"`
	Signature    string            `json:"signature,omitempty"`
}

type AppendRecordResponse struct {
	Record DataRecord `json:"record"`
}

type DataRecordResponse struct {
	Record DataRecord `json:"record"`
}

type DataRecordManifestResponse struct {
	Record   DataRecord              `json:"record"`
	Manifest StorageManifestResponse `json:"manifest"`
}

type CollectionResponse struct {
	Collection DataCollection `json:"collection"`
}

type CollectionRecordsResponse struct {
	Collection DataCollection         `json:"collection"`
	Filter     CollectionRecordFilter `json:"filter,omitempty"`
	Records    []DataRecord           `json:"records"`
}

type UserCollectionsResponse struct {
	User        string           `json:"user"`
	Collections []DataCollection `json:"collections"`
}

type ShardRef struct {
	SegmentID  int `json:"segment_id"`
	ShardIndex int `json:"shard_index"`
}

type GenerateChallengeRequest struct {
	IntentID string `json:"intent_id"`
	Count    int    `json:"count"`
}

type GenerateChallengeResponse struct {
	Challenges []StorageChallenge `json:"challenges"`
}

type ListChallengesResponse struct {
	Challenges []StorageChallenge `json:"challenges"`
}

type RepairTask struct {
	IntentID            string            `json:"intent_id"`
	RepairID            string            `json:"repair_id,omitempty"`
	SegmentID           int               `json:"segment_id"`
	ShardIndex          int               `json:"shard_index,omitempty"`
	OldMinerAddress     string            `json:"old_miner_address,omitempty"`
	Reason              string            `json:"reason,omitempty"`
	Status              string            `json:"status,omitempty"`
	ProofChallengeID    string            `json:"proof_challenge_id,omitempty"`
	ProofVerified       bool              `json:"proof_verified,omitempty"`
	AvailableShards     int               `json:"available_shards"`
	RequiredShards      int               `json:"required_shards"`
	TargetShards        int               `json:"target_shards"`
	MissingShardIndexes []int             `json:"missing_shard_indexes"`
	Assignment          StorageAssignment `json:"assignment,omitempty"`
	SourceReceipts      []MinerReceipt    `json:"source_receipts,omitempty"`
	// RepairMode indicates the repair strategy: "local" (default, RS
	// reconstruct) or "cross_parity" (XOR from pool peer + cross-parity).
	RepairMode    string `json:"repair_mode,omitempty"`
	PoolID        int    `json:"pool_id,omitempty"`
	PeerSegmentID int    `json:"peer_segment_id,omitempty"`
}

// PendingShardRepair tracks a shard that has been missed but has not yet
// reached the repair delay threshold. Once ConsecutiveMisses >=
// RepairDelayEpochs the pending entry is promoted to a full RepairTask.
type PendingShardRepair struct {
	IntentID              string `json:"intent_id"`
	SegmentID             int    `json:"segment_id"`
	ShardIndex            int    `json:"shard_index"`
	MinerAddress          string `json:"miner_address"`
	FirstMissedEpochRound uint64 `json:"first_missed_epoch_round"`
	ConsecutiveMisses     uint64 `json:"consecutive_misses"`
}

type RepairPlanResponse struct {
	IntentID string       `json:"intent_id"`
	Tasks    []RepairTask `json:"tasks"`
}

const (
	DealHealthOK       = "ok"
	DealHealthAtRisk   = "at_risk"
	DealHealthCritical = "critical"
)

type DealHealth struct {
	IntentID          string   `json:"intent_id"`
	Status            string   `json:"status"`
	TotalShards       int      `json:"total_shards"`
	AvailableShards   int      `json:"available_shards"`
	MissingShards     int      `json:"missing_shards"`
	MinRequiredShards int      `json:"min_required_shards"`
	MissingMinerAddrs []string `json:"missing_miner_addrs,omitempty"`
	LastCheckedAtUnix int64    `json:"last_checked_at_unix"`
}

type DealHealthResponse struct {
	Healths []DealHealth `json:"healths"`
}

type ProviderShard struct {
	ShardHash string `json:"shard_hash"`
	ShardCID  string `json:"shard_cid,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

type StorageProviderRecord struct {
	MinerAddress           string          `json:"miner_address"`
	PublicKey              string          `json:"public_key"`
	Endpoint               string          `json:"endpoint,omitempty"`
	PeerID                 string          `json:"peer_id,omitempty"`
	PeerAddrs              []string        `json:"peer_addrs,omitempty"`
	CapacityBytes          uint64          `json:"capacity_bytes,omitempty"`
	StoredBytes            uint64          `json:"stored_bytes,omitempty"`
	ShardCount             int             `json:"shard_count,omitempty"`
	AccessServiceRequired  bool            `json:"access_service_required,omitempty"`
	UploadServiceEnabled   bool            `json:"upload_service_enabled,omitempty"`
	DownloadServiceEnabled bool            `json:"download_service_enabled,omitempty"`
	ShardHashes            []string        `json:"shard_hashes,omitempty"`
	Shards                 []ProviderShard `json:"shards,omitempty"`
	LastSeenUnix           int64           `json:"last_seen_unix"`
	ExpiresAtUnix          int64           `json:"expires_at_unix"`
	HealthScoreBPS         uint64          `json:"health_score_bps,omitempty"`
	ProofSuccess           uint64          `json:"proof_success,omitempty"`
	ProofFailure           uint64          `json:"proof_failure,omitempty"`
	ProviderSource         string          `json:"provider_source,omitempty"`
	ProviderRecordLive     bool            `json:"provider_record_live,omitempty"`
	Signature              string          `json:"signature"`
}

type StorageProviderAnnouncement struct {
	Provider StorageProviderRecord `json:"provider"`
}

type StorageProvidersResponse struct {
	ShardHash string                  `json:"shard_hash,omitempty"`
	ShardCID  string                  `json:"shard_cid,omitempty"`
	IntentID  string                  `json:"intent_id,omitempty"`
	Providers []StorageProviderRecord `json:"providers"`
}

type StorageRoute struct {
	MinerAddress           string   `json:"miner_address"`
	ShardHash              string   `json:"shard_hash,omitempty"`
	ShardCID               string   `json:"shard_cid,omitempty"`
	Transport              string   `json:"transport"`
	Endpoint               string   `json:"endpoint,omitempty"`
	PeerID                 string   `json:"peer_id,omitempty"`
	PeerAddrs              []string `json:"peer_addrs,omitempty"`
	AccessServiceRequired  bool     `json:"access_service_required,omitempty"`
	UploadServiceEnabled   bool     `json:"upload_service_enabled,omitempty"`
	DownloadServiceEnabled bool     `json:"download_service_enabled,omitempty"`
	HealthScoreBPS         uint64   `json:"health_score_bps,omitempty"`
	ProviderRecordLive     bool     `json:"provider_record_live,omitempty"`
	ProviderSource         string   `json:"provider_source,omitempty"`
	Priority               int      `json:"priority"`
}

type StorageRoutesResponse struct {
	ShardHash string         `json:"shard_hash,omitempty"`
	ShardCID  string         `json:"shard_cid,omitempty"`
	IntentID  string         `json:"intent_id,omitempty"`
	Routes    []StorageRoute `json:"routes"`
}

type CreateRepairRequest struct {
	IntentID          string   `json:"intent_id"`
	UnavailableMiners []string `json:"unavailable_miners,omitempty"`
	IncludeMissing    bool     `json:"include_missing"`
}

type CreateRepairResponse struct {
	IntentID string       `json:"intent_id"`
	Tasks    []RepairTask `json:"tasks"`
}

type StorageChallenge struct {
	ChallengeID      string      `json:"challenge_id"`
	EpochID          string      `json:"epoch_id,omitempty"`
	RepairID         string      `json:"repair_id,omitempty"`
	ProofType        string      `json:"proof_type,omitempty"`
	IntentID         string      `json:"intent_id"`
	DealID           string      `json:"deal_id"`
	SegmentID        int         `json:"segment_id"`
	SegmentRoot      string      `json:"segment_root"`
	ShardIndex       int         `json:"shard_index"`
	ShardHash        string      `json:"shard_hash"`
	ShardSize        int64       `json:"shard_size"`
	SectorCommitment string      `json:"sector_commitment"`
	MinerSeal        string      `json:"miner_seal,omitempty"`
	LeafSize         int         `json:"leaf_size"`
	LeafIndex        int         `json:"leaf_index"`
	LeafIndices      []int       `json:"leaf_indices,omitempty"`
	SampleCount      int         `json:"sample_count,omitempty"`
	Difficulty       uint64      `json:"difficulty,omitempty"`
	PayloadBytes     int64       `json:"payload_bytes,omitempty"`
	MinerAddress     string      `json:"miner_address"`
	MinerPublicKey   string      `json:"miner_public_key"`
	MinerEndpoint    string      `json:"miner_endpoint,omitempty"`
	Nonce            string      `json:"nonce"`
	ChallengeSeed    string      `json:"challenge_seed,omitempty"`
	ChallengeHash    string      `json:"challenge_hash,omitempty"`
	LeafRanges       []LeafRange `json:"leaf_ranges,omitempty"`
	ExpiresAtUnix    int64       `json:"expires_at_unix"`
	Reward           uint64      `json:"reward"`
}

type ProveRequest struct {
	Challenge StorageChallenge `json:"challenge"`
}

type LeafRange struct {
	LeafIndex int   `json:"leaf_index"`
	Offset    int64 `json:"offset"`
	Length    int   `json:"length"`
}

type StorageProof struct {
	ChallengeID        string     `json:"challenge_id"`
	ProofType          string     `json:"proof_type,omitempty"`
	ChallengeHash      string     `json:"challenge_hash,omitempty"`
	MinerAddress       string     `json:"miner_address"`
	MinerPublicKey     string     `json:"miner_public_key"`
	ShardHash          string     `json:"shard_hash"`
	ShardSize          int64      `json:"shard_size"`
	SectorCommitment   string     `json:"sector_commitment"`
	MinerSeal          string     `json:"miner_seal,omitempty"`
	LeafSize           int        `json:"leaf_size"`
	LeafIndex          int        `json:"leaf_index"`
	LeafIndices        []int      `json:"leaf_indices,omitempty"`
	LeafHash           string     `json:"leaf_hash"`
	LeafHashes         []string   `json:"leaf_hashes,omitempty"`
	LeafDataBase64     string     `json:"leaf_data_base64,omitempty"`
	LeafPayloadsBase64 []string   `json:"leaf_payloads_base64,omitempty"`
	MerklePath         []string   `json:"merkle_path"`
	MerklePaths        [][]string `json:"merkle_paths,omitempty"`
	ProofHash          string     `json:"proof_hash"`
	Signature          string     `json:"signature"`
}

type SubmitProofRequest struct {
	Proof StorageProof `json:"proof"`
}

type SubmitProofResponse struct {
	ChallengeID              string `json:"challenge_id"`
	MinerAddress             string `json:"miner_address"`
	Status                   string `json:"status"`
	Reward                   uint64 `json:"reward"`
	SettledStoragePoolReward uint64 `json:"settled_storage_pool_reward,omitempty"`
}

type StartEpochRequest struct {
	IntentID            string `json:"intent_id,omitempty"`
	ChallengesPerDeal   int    `json:"challenges_per_deal"`
	DurationSeconds     int64  `json:"duration_seconds"`
	RewardPerProof      uint64 `json:"reward_per_proof"`
	SlashPerMissedProof uint64 `json:"slash_per_missed_proof"`
	// Operator identity fields for block replay auth.
	OperatorAddress string `json:"operator_address,omitempty"`
	ChainID         string `json:"chain_id,omitempty"`
	Nonce           uint64 `json:"nonce,omitempty"`
	Signature       string `json:"signature,omitempty"`
	CreatedAtUnix   int64  `json:"created_at_unix,omitempty"`
}

type ProofEpoch struct {
	EpochID              string   `json:"epoch_id"`
	EpochRound           uint64   `json:"epoch_round,omitempty"`
	IntentID             string   `json:"intent_id,omitempty"`
	ChallengeIDs         []string `json:"challenge_ids"`
	StartedAtUnix        int64    `json:"started_at_unix"`
	DeadlineUnix         int64    `json:"deadline_unix"`
	Status               string   `json:"status"`
	RewardPerProof       uint64   `json:"reward_per_proof"`
	SlashPerMissedProof  uint64   `json:"slash_per_missed_proof"`
	StorageRewardsPaid   uint64   `json:"storage_rewards_paid,omitempty"`
	RetrievalRewardsPaid uint64   `json:"retrieval_rewards_paid,omitempty"`
	RepairRewardsPaid    uint64   `json:"repair_rewards_paid,omitempty"`
	StorageSlashed       uint64   `json:"storage_slashed,omitempty"`
	RepairTasksCreated   int      `json:"repair_tasks_created,omitempty"`
}

type StartEpochResponse struct {
	Epoch      ProofEpoch         `json:"epoch"`
	Challenges []StorageChallenge `json:"challenges"`
}

type FinalizeEpochRequest struct {
	EpochID string `json:"epoch_id"`
	// Operator identity fields for block replay auth.
	OperatorAddress string `json:"operator_address,omitempty"`
	ChainID         string `json:"chain_id,omitempty"`
	Nonce           uint64 `json:"nonce,omitempty"`
	Signature       string `json:"signature,omitempty"`
	CreatedAtUnix   int64  `json:"created_at_unix,omitempty"`
}

type FinalizeEpochResponse struct {
	EpochID              string       `json:"epoch_id"`
	Status               string       `json:"status"`
	AcceptedProofs       int          `json:"accepted_proofs"`
	MissedProofs         int          `json:"missed_proofs"`
	RepairTasks          []RepairTask `json:"repair_tasks,omitempty"`
	StorageRewardsPaid   uint64       `json:"storage_rewards_paid,omitempty"`
	RetrievalRewardsPaid uint64       `json:"retrieval_rewards_paid,omitempty"`
	RepairRewardsPaid    uint64       `json:"repair_rewards_paid,omitempty"`
	StorageSlashed       uint64       `json:"storage_slashed,omitempty"`
	RepairTasksCreated   int          `json:"repair_tasks_created,omitempty"`
}

type MiningRewardVestingBucket struct {
	BucketID           string            `json:"bucket_id"`
	Address            string            `json:"address"`
	DayUnix            int64             `json:"day_unix"`
	Total              uint64            `json:"total"`
	Released           uint64            `json:"released"`
	CreatedAtUnix      int64             `json:"created_at_unix"`
	LastReleasedAtUnix int64             `json:"last_released_at_unix,omitempty"`
	Sources            map[string]uint64 `json:"sources,omitempty"`
}

type EpochRewardsResponse struct {
	EpochID              string `json:"epoch_id"`
	EpochRound           uint64 `json:"epoch_round,omitempty"`
	StorageRewardsPaid   uint64 `json:"storage_rewards_paid,omitempty"`
	RetrievalRewardsPaid uint64 `json:"retrieval_rewards_paid,omitempty"`
	RepairRewardsPaid    uint64 `json:"repair_rewards_paid,omitempty"`
	StorageSlashed       uint64 `json:"storage_slashed,omitempty"`
	PendingMiningRewards uint64 `json:"pending_mining_rewards,omitempty"`
}

type StateSnapshot struct {
	Height                uint64 `json:"height"`
	EpochRound            uint64 `json:"epoch_round"`
	Accounts              int    `json:"accounts"`
	Intents               int    `json:"intents"`
	FinalizedIntents      int    `json:"finalized_intents"`
	Deals                 int    `json:"deals"`
	MinersActive          int    `json:"miners_active"`
	MinersDegraded        int    `json:"miners_degraded"`
	MinersJailed          int    `json:"miners_jailed"`
	MinersExiting         int    `json:"miners_exiting"`
	ValidatorsActive      int    `json:"validators_active"`
	ValidatorsSlashed     int    `json:"validators_slashed"`
	ConsensusValidators   int    `json:"consensus_validators"`
	TotalStakeLocked      uint64 `json:"total_stake_locked"`
	TotalStorageLocked    uint64 `json:"total_storage_locked"`
	TotalMiningPending    uint64 `json:"total_mining_pending,omitempty"`
	TotalTokenSupply      uint64 `json:"total_token_supply"`
	PendingMiningBuckets  int    `json:"pending_mining_buckets,omitempty"`
	PendingChallenges     int    `json:"pending_challenges"`
	ActiveEpochs          int    `json:"active_epochs"`
	PendingRepairTasks    int    `json:"pending_repair_tasks"`
	DealsAtRisk           int    `json:"deals_at_risk,omitempty"`
	DealsCritical         int    `json:"deals_critical,omitempty"`
	TotalStorageRewards   uint64 `json:"total_storage_rewards,omitempty"`
	TotalRetrievalRewards uint64 `json:"total_retrieval_rewards,omitempty"`
	TotalRepairRewards    uint64 `json:"total_repair_rewards,omitempty"`
	TotalSlashed          uint64 `json:"total_slashed,omitempty"`
	SnapshotAtUnix        int64  `json:"snapshot_at_unix"`
}

const (
	MinerStatusActive   = "active"
	MinerStatusDegraded = "degraded"
	MinerStatusJailed   = "jailed"
	MinerStatusExiting  = "exiting"
	MinerStatusExited   = "exited"
)

const (
	ValidatorStatusActive  = "active"
	ValidatorStatusJailed  = "jailed"
	ValidatorStatusSlashed = "slashed"
	ValidatorStatusExiting = "exiting"
	ValidatorStatusExited  = "exited"
)

const (
	TokenDecimals        = 8
	TokenSymbol          = "GF"
	TokenUnit     uint64 = 100_000_000 // 10^8, one GF in smallest units

	TokenTotalSupply           uint64 = 10_000_000_000 * TokenUnit
	TokenMiningSupply          uint64 = 9_000_000_000 * TokenUnit
	TokenStoragePoolInitial    uint64 = 6_000_000_000 * TokenUnit
	TokenRetrievalPoolInitial  uint64 = 600_000_000 * TokenUnit
	TokenValidatorPoolInitial  uint64 = 1_200_000_000 * TokenUnit
	TokenPermanentFundPoolInitial uint64 = 1_200_000_000 * TokenUnit
	TokenFoundationPoolInitial uint64 = 1_000_000_000 * TokenUnit

	MinDelegationAmount    uint64 = 1000 * TokenUnit
	UnbondingPeriodSeconds int64  = 7 * 86400
)

type RewardPools struct {
	StoragePoolRemaining    uint64 `json:"storage_pool_remaining"`
	RetrievalPoolRemaining  uint64 `json:"retrieval_pool_remaining"`
	ValidatorPoolRemaining  uint64 `json:"validator_pool_remaining"`
	PermanentFundRemaining     uint64 `json:"repair_pool_remaining"`
	FoundationPoolRemaining uint64 `json:"foundation_pool_remaining"`
	TokensReleased          uint64 `json:"tokens_released"`
}

type MinerStats struct {
	MinerAddress            string `json:"miner_address"`
	MinerID                 uint64 `json:"miner_id,omitempty"`
	PublicKey               string `json:"public_key"`
	Endpoint                string `json:"endpoint"`
	CapacityBytes           uint64 `json:"capacity_bytes"`
	UsedBytes               uint64 `json:"used_bytes"`
	ReservedBytes           uint64 `json:"reserved_bytes,omitempty"`
	AccessServiceRequired   bool   `json:"access_service_required,omitempty"`
	UploadServiceEnabled    bool   `json:"upload_service_enabled,omitempty"`
	DownloadServiceEnabled  bool   `json:"download_service_enabled,omitempty"`
	Stake                   uint64 `json:"stake"`
	Status                  string `json:"status"`
	RegisteredAtUnix        int64  `json:"registered_at_unix"`
	ProofSuccess            uint64 `json:"proof_success"`
	ProofFailure            uint64 `json:"proof_failure"`
	ConsecutiveFailures     uint64 `json:"consecutive_failures,omitempty"`
	Rewards                 uint64 `json:"rewards"`
	StorageRewards          uint64 `json:"storage_rewards,omitempty"`
	RetrievalSuccess        uint64 `json:"retrieval_success,omitempty"`
	RetrievalBytes          uint64 `json:"retrieval_bytes,omitempty"`
	RetrievalRewards        uint64 `json:"retrieval_rewards,omitempty"`
	RepairRewards           uint64 `json:"repair_rewards,omitempty"`
	PendingMiningRewards    uint64 `json:"pending_mining_rewards,omitempty"`
	VestingMiningRewards    uint64 `json:"vesting_mining_rewards,omitempty"`
	ClaimableMiningRewards  uint64 `json:"claimable_mining_rewards,omitempty"`
	UnsettledStorageRewards uint64 `json:"unsettled_storage_rewards,omitempty"`
	EstimatedStorageRewards uint64 `json:"estimated_storage_rewards,omitempty"`
	StorageRewardAccrued    uint64 `json:"storage_reward_accrued,omitempty"`
	StorageRewardIndex      string `json:"storage_reward_index,omitempty"`
	Slashed                 uint64 `json:"slashed"`
	ExitedAtUnix            int64  `json:"exited_at_unix,omitempty"`
	EffectiveWeight         uint64 `json:"effective_weight,omitempty"`
	DelegatorCount          int    `json:"delegator_count,omitempty"`
	SpeedScore              uint64 `json:"speed_score,omitempty"`
	AntiSpamScore           uint64 `json:"anti_spam_score,omitempty"`
	DHTPublishCount         uint64 `json:"dht_publish_count,omitempty"`
	DHTServeHits            uint64 `json:"dht_serve_hits,omitempty"`
	DHTServeMisses          uint64 `json:"dht_serve_misses,omitempty"`
	DHTLastPublishUnix      int64  `json:"dht_last_publish_unix,omitempty"`
	RetrievalObligMet       bool   `json:"retrieval_oblig_met,omitempty"`
	BonusReleased           bool   `json:"bonus_released,omitempty"`
	BonusExpired            bool   `json:"bonus_expired,omitempty"`
	LockedBonus             uint64 `json:"locked_bonus,omitempty"`
	LastCapacityAdjustUnix  int64  `json:"last_capacity_adjust_unix,omitempty"`
}

// MinerShardsResponse lists all shard hashes currently assigned to a miner
// across all committed intent receipts.
type MinerShardsResponse struct {
	MinerAddress string   `json:"miner_address"`
	ShardHashes  []string `json:"shard_hashes"`
	ShardCount   int      `json:"shard_count"`
}

type RetrievalRateWindow struct {
	User           string `json:"user"`
	IntentID       string `json:"intent_id"`
	Count          uint64 `json:"count"`
	BytesSum       uint64 `json:"bytes_sum"`
	DecayBase      uint64 `json:"decay_base"`
	OpenedAt       int64  `json:"opened_at"`
	SpeedSampledAt int64  `json:"speed_sampled_at,omitempty"`
	SampleCount    uint64 `json:"sample_count"`
	SampleBytes    uint64 `json:"sample_bytes"`
}

type RegisterMinerRequest struct {
	MinerAddress  string `json:"miner_address"`
	PublicKey     string `json:"public_key"`
	Endpoint      string `json:"endpoint"`
	CapacityBytes uint64 `json:"capacity_bytes"`
	Stake         uint64 `json:"stake"`
	ChainID       string `json:"chain_id"`
	Nonce         uint64 `json:"nonce,omitempty"`
	Signature     string `json:"signature"`
}

type RegisterMinerResponse struct {
	Miner MinerStats `json:"miner"`
}

type AdjustCapacityRequest struct {
	MinerAddress     string `json:"miner_address"`
	NewCapacityBytes uint64 `json:"new_capacity_bytes"`
	ChainID          string `json:"chain_id"`
	Nonce            uint64 `json:"nonce,omitempty"`
	Signature        string `json:"signature"`
}

type AdjustCapacityResponse struct {
	Miner          MinerStats `json:"miner"`
	RefundUnbonding uint64    `json:"refund_unbonding,omitempty"`
}

type ClaimMiningRewardsRequest struct {
	MinerAddress string `json:"miner_address"`
	ChainID      string `json:"chain_id"`
	Nonce        uint64 `json:"nonce,omitempty"`
	Signature    string `json:"signature"`
}

type UploadNFTTemplateRequest struct {
	MinerAddress string `json:"miner_address"`
	ContentType  string `json:"content_type"`
	Content      string `json:"content"`
	ChainID      string `json:"chain_id"`
	Nonce        uint64 `json:"nonce,omitempty"`
	Signature    string `json:"signature"`
}

type ClaimMiningRewardsResponse struct {
	MinerAddress           string `json:"miner_address"`
	Claimed                uint64 `json:"claimed"`
	Balance                uint64 `json:"balance"`
	PendingMiningRewards   uint64 `json:"pending_mining_rewards,omitempty"`
	VestingMiningRewards   uint64 `json:"vesting_mining_rewards,omitempty"`
	ClaimableMiningRewards uint64 `json:"claimable_mining_rewards,omitempty"`
}

type Account struct {
	Address              string `json:"address"`
	PublicKey            string `json:"public_key,omitempty"`
	Balance              uint64 `json:"balance"`
	Nonce                uint64 `json:"nonce"`
	LockedStake          uint64 `json:"locked_stake"`
	LockedStorage        uint64 `json:"locked_storage"`
	UnbondingBalance     uint64 `json:"unbonding_balance,omitempty"`
	PendingMiningRewards uint64 `json:"pending_mining_rewards,omitempty"`
	LockedBonus          uint64 `json:"locked_bonus,omitempty"`
}

type TransferRequest struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    uint64 `json:"amount"`
	Nonce     uint64 `json:"nonce,omitempty"`
	Fee       uint64 `json:"fee,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type TransferResponse struct {
	From Account `json:"from"`
	To   Account `json:"to"`
}

type Transaction struct {
	TxID           string          `json:"tx_id"`
	Type           string          `json:"type"`
	From           string          `json:"from,omitempty"`
	Nonce          uint64          `json:"nonce,omitempty"`
	NonceProtected bool            `json:"nonce_protected,omitempty"`
	AgentKeyID     string          `json:"agent_key_id,omitempty"`
	AgentNonce     uint64          `json:"agent_nonce,omitempty"`
	Fee            uint64          `json:"fee,omitempty"`
	PayloadHash    string          `json:"payload_hash"`
	Payload        json.RawMessage `json:"payload"`
	CreatedAtUnix  int64           `json:"created_at_unix"`
	Signature      string          `json:"signature,omitempty"`
	PublicKey      string          `json:"public_key,omitempty"`
	DeadlineUnix   int64           `json:"deadline_unix,omitempty"`
}

type TransactionReceipt struct {
	TransactionHash  string `json:"transaction_hash"`
	TransactionIndex uint64 `json:"transaction_index"`
	BlockHash        string `json:"block_hash,omitempty"`
	BlockNumber      uint64 `json:"block_number,omitempty"`
	From             string `json:"from,omitempty"`
}

type Block struct {
	Height            uint64        `json:"height"`
	Round             uint64        `json:"round,omitempty"`
	TimeUnix          int64         `json:"time_unix"`
	PrevHash          string        `json:"prev_hash"`
	Hash              string        `json:"hash"`
	TxRoot            string        `json:"tx_root"`
	StateRoot         string        `json:"state_root,omitempty"`
	ReceiptsRoot      string        `json:"receipts_root,omitempty"`
	ProducerAddress   string        `json:"producer_address"`
	ProducerPublicKey string        `json:"producer_public_key"`
	Signature         string        `json:"signature"`
	Finality          BlockFinality `json:"finality,omitempty"`
	Transactions      []Transaction `json:"transactions"`
}

type UpgradePlan struct {
	Name       string `json:"name"`
	HaltHeight uint64 `json:"halt_height,omitempty"`
	HaltTime   int64  `json:"halt_time,omitempty"`
	Info       string `json:"info,omitempty"`
}

type ConsensusState struct {
	Height       uint64 `json:"height"`
	Round        uint64 `json:"round"`
	Phase        string `json:"phase"`
	Proposer     string `json:"proposer,omitempty"`
	VotingPower  uint64 `json:"voting_power,omitempty"`
	TotalPower   uint64 `json:"total_power,omitempty"`
	BlockTimeout int64  `json:"block_timeout_ms,omitempty"`
}

type BlockVote struct {
	Height             uint64 `json:"height"`
	BlockHash          string `json:"block_hash"`
	ValidatorAddress   string `json:"validator_address"`
	ValidatorPublicKey string `json:"validator_public_key"`
	Power              uint64 `json:"power"`
	Signature          string `json:"signature"`
}

const (
	ConsensusVotePrevote   = "prevote"
	ConsensusVotePrecommit = "precommit"
)

type ConsensusVote struct {
	Height             uint64 `json:"height"`
	Round              uint64 `json:"round"`
	Type               string `json:"type"`
	BlockHash          string `json:"block_hash"`
	ValidatorAddress   string `json:"validator_address"`
	ValidatorPublicKey string `json:"validator_public_key"`
	Power              uint64 `json:"power"`
	Signature          string `json:"signature"`
}

type SubmitConsensusVoteRequest struct {
	Vote ConsensusVote `json:"vote"`
}

type SubmitConsensusVoteResponse struct {
	Accepted   bool          `json:"accepted"`
	Finalized  bool          `json:"finalized"`
	Vote       ConsensusVote `json:"vote,omitempty"`
	Block      Block         `json:"block,omitempty"`
	Prevotes   BlockFinality `json:"prevotes,omitempty"`
	Precommits BlockFinality `json:"precommits,omitempty"`
}

type ConsensusVotesResponse struct {
	Height uint64          `json:"height,omitempty"`
	Round  uint64          `json:"round,omitempty"`
	Type   string          `json:"type,omitempty"`
	Votes  []ConsensusVote `json:"votes"`
}

type BlockFinality struct {
	Round          uint64      `json:"round"`
	VotingPower    uint64      `json:"voting_power"`
	TotalPower     uint64      `json:"total_power"`
	ThresholdPower uint64      `json:"threshold_power"`
	Finalized      bool        `json:"finalized"`
	Votes          []BlockVote `json:"votes,omitempty"`
}

type BlockResponse struct {
	Block Block `json:"block"`
}

type BlockVoteResponse struct {
	Accepted bool  `json:"accepted"`
	Block    Block `json:"block,omitempty"`
}

type ValidatorEvidence struct {
	EvidenceID         string `json:"evidence_id"`
	Type               string `json:"type"`
	Height             uint64 `json:"height"`
	ValidatorAddress   string `json:"validator_address"`
	ValidatorPublicKey string `json:"validator_public_key"`
	Power              uint64 `json:"power"`
	FirstBlockHash     string `json:"first_block_hash"`
	SecondBlockHash    string `json:"second_block_hash"`
	FirstSignature     string `json:"first_signature"`
	SecondSignature    string `json:"second_signature"`
	Slashed            uint64 `json:"slashed"`
	CreatedAtUnix      int64  `json:"created_at_unix"`
}

type SubmitValidatorEvidenceRequest struct {
	VoteA BlockVote `json:"vote_a"`
	VoteB BlockVote `json:"vote_b"`
}

type SubmitValidatorEvidenceResponse struct {
	Accepted bool              `json:"accepted"`
	Evidence ValidatorEvidence `json:"evidence"`
}

type ProduceBlockResponse struct {
	Produced bool  `json:"produced"`
	Block    Block `json:"block,omitempty"`
}

// FeeMultipliers holds per-transaction-type gas fee multipliers in BPS
// (basis points). 10000 = 1.0x (no multiplier). Applied at validation:
// tx.Fee must be >= BaseFee * multiplier / 10000.
type FeeMultipliers struct {
	BridgeOut         uint64 `json:"bridge_out,omitempty"`
	CreateIntent      uint64 `json:"create_intent,omitempty"`
	UploadNFTTemplate uint64 `json:"upload_nft_template,omitempty"`
	RegisterValidator uint64 `json:"register_validator,omitempty"`
	BatchCommit       uint64 `json:"batch_commit,omitempty"`
}

type FeeMarket struct {
	BaseFee         uint64         `json:"base_fee"`
	TargetBlockTxs  int            `json:"target_block_txs"`
	LastBlockTxs    int            `json:"last_block_txs"`
	UpdatedAtHeight uint64         `json:"updated_at_height"`
	Multipliers     FeeMultipliers `json:"multipliers"`
}

// SetFeeMarketRequest is the operator-authenticated request to update fee
// market parameters. Pointer fields distinguish "not set" from zero.
type SetFeeMarketRequest struct {
	BaseFee        *uint64         `json:"base_fee,omitempty"`
	TargetBlockTxs *int            `json:"target_block_txs,omitempty"`
	Multipliers    *FeeMultipliers `json:"multipliers,omitempty"`
}

type MempoolResponse struct {
	Pending   []Transaction `json:"pending"`
	FeeMarket FeeMarket     `json:"fee_market"`
}

type ValidatorInfo struct {
	OwnerAddress         string `json:"owner_address"`
	OperatorAddress      string `json:"operator_address"`
	OperatorPublicKey    string `json:"operator_public_key"`
	Endpoint             string `json:"endpoint,omitempty"`
	Stake                uint64 `json:"stake"`
	DelegatedStake       uint64 `json:"delegated_stake,omitempty"`
	SelfStake            uint64 `json:"self_stake,omitempty"`
	Status               string `json:"status"`
	Consensus            bool   `json:"consensus"`
	RegisteredAtUnix     int64  `json:"registered_at_unix"`
	ProducedBlocks       uint64 `json:"produced_blocks"`
	Slashed              uint64 `json:"slashed"`
	EvidenceCount        uint64 `json:"evidence_count"`
	DelegatorCount       int    `json:"delegator_count,omitempty"`
	Rewards              uint64 `json:"rewards,omitempty"`
	DelegationRewards    uint64 `json:"delegation_rewards,omitempty"`
	AvailabilityScoreBPS uint64 `json:"availability_score_bps,omitempty"`
	CommissionRateBPS    uint64 `json:"commission_rate_bps,omitempty"`
}

// ValidatorTurnWindow tracks a sliding window of proposer turn results
// for availability scoring. Uses a ring buffer of fixed capacity.
type ValidatorTurnWindow struct {
	Successes uint64 `json:"successes"`
	Misses    uint64 `json:"misses"`
	Turns     []bool `json:"turns,omitempty"`
	Head      int    `json:"head"`
}

type StakeDelegation struct {
	Delegator string `json:"delegator"`
	Validator string `json:"validator"`
	Amount    uint64 `json:"amount"`
	SinceUnix int64  `json:"since_unix"`
}

type DelegateStakeRequest struct {
	ChainID   string `json:"chain_id,omitempty"`
	Delegator string `json:"delegator"`
	Validator string `json:"validator"`
	Amount    uint64 `json:"amount"`
	Nonce     uint64 `json:"nonce,omitempty"`
	Signature string `json:"signature,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
}

type DelegateStakeResponse struct {
	Delegator      string `json:"delegator"`
	Validator      string `json:"validator"`
	Amount         uint64 `json:"amount"`
	DelegatedStake uint64 `json:"delegated_stake"`
}

type UndelegateStakeRequest struct {
	ChainID   string `json:"chain_id,omitempty"`
	Delegator string `json:"delegator"`
	Validator string `json:"validator"`
	Amount    uint64 `json:"amount"`
	Nonce     uint64 `json:"nonce,omitempty"`
	Signature string `json:"signature,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
}

type UndelegateStakeResponse struct {
	Delegator      string `json:"delegator"`
	Validator      string `json:"validator"`
	Released       uint64 `json:"released"`
	DelegatedStake uint64 `json:"delegated_stake"`
}

type RegisterValidatorRequest struct {
	OwnerAddress      string `json:"owner_address"`
	OperatorAddress   string `json:"operator_address"`
	OperatorPublicKey string `json:"operator_public_key"`
	Endpoint          string `json:"endpoint,omitempty"`
	Stake             uint64 `json:"stake"`
	CommissionRateBPS uint64 `json:"commission_rate_bps,omitempty"`
	ChainID           string `json:"chain_id"`
	Nonce             uint64 `json:"nonce,omitempty"`
	Signature         string `json:"signature"`
	OperatorSignature string `json:"operator_signature"`
}

type RegisterValidatorResponse struct {
	Validator ValidatorInfo `json:"validator"`
}

type ListValidatorsResponse struct {
	Validators []ValidatorInfo `json:"validators"`
}

type UnbondingEntry struct {
	ID            string `json:"id"`
	Delegator     string `json:"delegator"`
	Validator     string `json:"validator"`
	Amount        uint64 `json:"amount"`
	CreatedAtUnix int64  `json:"created_at_unix"`
	MaturesAtUnix int64  `json:"matures_at_unix"`
}

type DeregisterMinerRequest struct {
	MinerAddress string `json:"miner_address"`
	ChainID      string `json:"chain_id"`
	Nonce        uint64 `json:"nonce,omitempty"`
	Signature    string `json:"signature"`
}

type DeregisterValidatorRequest struct {
	ValidatorAddress string `json:"validator_address"`
	ChainID          string `json:"chain_id"`
	Nonce            uint64 `json:"nonce,omitempty"`
	Signature        string `json:"signature"`
}

type RotateOperatorRequest struct {
	OwnerAddress         string `json:"owner_address"`
	NewOperatorAddress   string `json:"new_operator_address"`
	NewOperatorPublicKey string `json:"new_operator_public_key"`
	ChainID              string `json:"chain_id"`
	Nonce                uint64 `json:"nonce,omitempty"`
	Signature            string `json:"signature"`
	OperatorSignature    string `json:"operator_signature"`
}

type RotateOperatorResponse struct {
	OwnerAddress      string `json:"owner_address"`
	OperatorAddress   string `json:"operator_address"`
	OperatorPublicKey string `json:"operator_public_key"`
}

type ListUnbondingResponse struct {
	Entries []UnbondingEntry `json:"entries"`
}

const (
	KeyEnvelopeRecipientOwner    = "owner"
	KeyEnvelopeRecipientAddress  = "address"
	KeyEnvelopeRecipientAgent    = "agent"
	KeyEnvelopeRecipientPasscode = "passcode"
)

const (
	ShareModeAddress      = "address"
	ShareModePasscode     = "passcode"
	ShareModeLinkFragment = "link_fragment"
)

type PasscodeKDFParams struct {
	Name        string `json:"name"`
	Salt        string `json:"salt"`
	MemoryKiB   int    `json:"memory_kib"`
	Iterations  int    `json:"iterations"`
	Parallelism int    `json:"parallelism"`
}

type KeyEnvelope struct {
	EnvelopeID       string             `json:"envelope_id"`
	IntentID         string             `json:"intent_id"`
	ShareID          string             `json:"share_id,omitempty"`
	Owner            string             `json:"owner"`
	Recipient        string             `json:"recipient"`
	RecipientType    string             `json:"recipient_type"`
	Algorithm        string             `json:"algorithm"`
	EncryptedDataKey string             `json:"encrypted_data_key"`
	Nonce            string             `json:"nonce,omitempty"`
	KDF              *PasscodeKDFParams `json:"kdf,omitempty"`
	CreatedAtUnix    int64              `json:"created_at_unix"`
	ExpiresAtUnix    int64              `json:"expires_at_unix,omitempty"`
	Revoked          bool               `json:"revoked,omitempty"`
}

type ShareRecord struct {
	ShareID       string `json:"share_id"`
	IntentID      string `json:"intent_id"`
	Owner         string `json:"owner"`
	Mode          string `json:"mode"`
	Recipient     string `json:"recipient,omitempty"`
	EnvelopeID    string `json:"envelope_id"`
	CreatedAtUnix int64  `json:"created_at_unix"`
	ExpiresAtUnix int64  `json:"expires_at_unix,omitempty"`
	Revoked       bool   `json:"revoked,omitempty"`
}

type CreateKeyEnvelopeRequest struct {
	ChainID          string             `json:"chain_id,omitempty"`
	IntentID         string             `json:"intent_id"`
	Owner            string             `json:"owner"`
	Recipient        string             `json:"recipient"`
	RecipientType    string             `json:"recipient_type"`
	Algorithm        string             `json:"algorithm"`
	EncryptedDataKey string             `json:"encrypted_data_key"`
	Nonce            string             `json:"nonce,omitempty"`
	KDF              *PasscodeKDFParams `json:"kdf,omitempty"`
	ExpiresAtUnix    int64              `json:"expires_at_unix,omitempty"`
	AccountNonce     uint64             `json:"account_nonce,omitempty"`
	PublicKey        string             `json:"public_key,omitempty"`
	Signature        string             `json:"signature,omitempty"`
}

type CreateKeyEnvelopeResponse struct {
	Envelope KeyEnvelope `json:"envelope"`
}

type CreateAddressShareRequest struct {
	ChainID          string             `json:"chain_id,omitempty"`
	IntentID         string             `json:"intent_id"`
	Owner            string             `json:"owner"`
	Recipient        string             `json:"recipient"`
	Algorithm        string             `json:"algorithm"`
	EncryptedDataKey string             `json:"encrypted_data_key"`
	Nonce            string             `json:"nonce,omitempty"`
	KDF              *PasscodeKDFParams `json:"kdf,omitempty"`
	ExpiresAtUnix    int64              `json:"expires_at_unix,omitempty"`
	AccountNonce     uint64             `json:"account_nonce,omitempty"`
	PublicKey        string             `json:"public_key,omitempty"`
	Signature        string             `json:"signature,omitempty"`
}

type CreatePasscodeShareRequest struct {
	ChainID          string             `json:"chain_id,omitempty"`
	IntentID         string             `json:"intent_id"`
	Owner            string             `json:"owner"`
	Mode             string             `json:"mode,omitempty"`
	Algorithm        string             `json:"algorithm"`
	EncryptedDataKey string             `json:"encrypted_data_key"`
	Nonce            string             `json:"nonce,omitempty"`
	KDF              *PasscodeKDFParams `json:"kdf"`
	ExpiresAtUnix    int64              `json:"expires_at_unix,omitempty"`
	AccountNonce     uint64             `json:"account_nonce,omitempty"`
	PublicKey        string             `json:"public_key,omitempty"`
	Signature        string             `json:"signature,omitempty"`
}

type CreateShareResponse struct {
	Share    ShareRecord `json:"share"`
	Envelope KeyEnvelope `json:"envelope"`
}

type RevokeShareRequest struct {
	ChainID      string `json:"chain_id,omitempty"`
	ShareID      string `json:"share_id"`
	Owner        string `json:"owner"`
	AccountNonce uint64 `json:"account_nonce,omitempty"`
	PublicKey    string `json:"public_key,omitempty"`
	Signature    string `json:"signature,omitempty"`
}

type ListSharesResponse struct {
	Shares    []ShareRecord `json:"shares"`
	Envelopes []KeyEnvelope `json:"envelopes,omitempty"`
}

type AgentKey struct {
	KeyID       string   `json:"key_id"`
	Name        string   `json:"name"`
	Master      string   `json:"master"`
	AgentPub    string   `json:"agent_pub"`
	Nonce       uint64   `json:"nonce"`
	Permissions []string `json:"permissions"`
	DailyLimit  uint64   `json:"daily_limit"`
	TotalLimit  uint64   `json:"total_limit"`
	UsedToday   uint64   `json:"used_today"`
	UsedTotal   uint64   `json:"used_total"`
	DayResetAt  int64    `json:"day_reset_at"`
	CreatedAt   int64    `json:"created_at"`
	ExpiresAt   int64    `json:"expires_at"`
	Revoked     bool     `json:"revoked"`
}

type RegisterAgentKeyRequest struct {
	ChainID     string   `json:"chain_id,omitempty"`
	Master      string   `json:"master"`
	Name        string   `json:"name"`
	AgentPub    string   `json:"agent_pub"`
	Permissions []string `json:"permissions"`
	DailyLimit  uint64   `json:"daily_limit"`
	TotalLimit  uint64   `json:"total_limit"`
	ExpiresAt   int64    `json:"expires_at"`
	Nonce       uint64   `json:"nonce"`
	Signature   string   `json:"signature"`
}

type RegisterAgentKeyResponse struct {
	Key AgentKey `json:"key"`
}

type RevokeAgentKeyRequest struct {
	ChainID   string `json:"chain_id,omitempty"`
	KeyID     string `json:"key_id"`
	Master    string `json:"master"`
	Nonce     uint64 `json:"nonce"`
	Signature string `json:"signature"`
}

type ListAgentKeysResponse struct {
	Keys []AgentKey `json:"keys"`
}

type ExtendAgentKeyRequest struct {
	ChainID   string `json:"chain_id,omitempty"`
	KeyID     string `json:"key_id"`
	Master    string `json:"master"`
	ExpiresAt int64  `json:"expires_at"`
	Nonce     uint64 `json:"nonce"`
	Signature string `json:"signature"`
}

type ExtendAgentKeyResponse struct {
	Key AgentKey `json:"key"`
}

type TopupAgentKeyRequest struct {
	ChainID    string `json:"chain_id,omitempty"`
	KeyID      string `json:"key_id"`
	Master     string `json:"master"`
	TotalLimit uint64 `json:"total_limit"`
	Nonce      uint64 `json:"nonce"`
	Signature  string `json:"signature"`
}

type TopupAgentKeyResponse struct {
	Key AgentKey `json:"key"`
}

// ── DHT & Governance Blacklist ──

// BlacklistEntry represents a governance-blocked shard.
type BlacklistEntry struct {
	ShardHash     string `json:"shard_hash"`
	IntentID      string `json:"intent_id"`
	Reason        string `json:"reason"`
	ReasonHash    string `json:"reason_hash"`
	Operator      string `json:"operator"`
	BlockedAtUnix int64  `json:"blocked_at_unix"`
	ExpiresAtUnix int64  `json:"expires_at_unix,omitempty"`
	ReviewStatus  string `json:"review_status,omitempty"`  // "pending_review" for direct actions
	ActionID      string `json:"action_id,omitempty"`      // Links to DirectActionRecord
}

// BlacklistResponse is the chain API response for blacklist queries.
type BlacklistResponse struct {
	Entries       []BlacklistEntry `json:"entries"`
	SinceHeight   uint64           `json:"since_height"`
	CurrentHeight uint64           `json:"current_height"`
}

// ── Governance Proposals & Votes ──

// GovernanceProposal is a signed governance action proposal stored in state.
type GovernanceProposal struct {
	ProposalID                       string   `json:"proposal_id"`
	Proposer                         string   `json:"proposer"`
	ProposerSignature                string   `json:"proposer_signature"`
	IntentID                         string   `json:"intent_id,omitempty"`
	Action                           string   `json:"action"`
	ReasonHash                       string   `json:"reason_hash"`
	ExpiresAtUnix                    int64    `json:"expires_at_unix,omitempty"`
	PreserveStorage                  bool     `json:"preserve_storage,omitempty"`
	AppealDeadlineUnix               int64    `json:"appeal_deadline_unix,omitempty"`
	TargetOperator                   string   `json:"target_operator,omitempty"`
	TargetPublicKey                  string   `json:"target_public_key,omitempty"`
	TargetPermissions                []string `json:"target_permissions,omitempty"`
	TargetDataModerationThresholdNum int      `json:"target_data_moderation_threshold_num,omitempty"`
	TargetDataModerationThresholdDen int      `json:"target_data_moderation_threshold_den,omitempty"`
	TargetOperatorChangeThresholdNum int      `json:"target_operator_change_threshold_num,omitempty"`
	TargetOperatorChangeThresholdDen int      `json:"target_operator_change_threshold_den,omitempty"`
	// ── Target mining params (for update_mining_params action) ──
	TargetStorageReleaseRateBPS       uint64 `json:"target_storage_release_rate_bps,omitempty"`
	TargetRetrievalReleaseRateBPS     uint64 `json:"target_retrieval_release_rate_bps,omitempty"`
	TargetStoredBytesWeightBPS        uint64 `json:"target_stored_bytes_weight_bps,omitempty"`
	TargetProofScoreWeightBPS         uint64 `json:"target_proof_score_weight_bps,omitempty"`
	TargetAvailabilityWeightBPS       uint64 `json:"target_availability_weight_bps,omitempty"`
	TargetRetrievalSpeedWeightBPS     uint64 `json:"target_retrieval_speed_weight_bps,omitempty"`
	TargetIPDispersionWeightBPS       uint64 `json:"target_ip_dispersion_weight_bps,omitempty"`
	TargetRetrievalRewardPerMiB       uint64 `json:"target_retrieval_reward_per_mib,omitempty"`
	TargetMaxRetrievalRewardPerWindow uint64 `json:"target_max_retrieval_reward_per_window,omitempty"`
	TargetPermanentFundTakeoverSeconds int64  `json:"target_repair_pool_takeover_seconds,omitempty"`
	TargetMinerDegradeThreshold       uint64 `json:"target_miner_degrade_threshold,omitempty"`
	TargetStorageProofSamples         int    `json:"target_storage_proof_samples,omitempty"`
	TargetValidatorCommissionBPS      uint64 `json:"target_validator_commission_bps,omitempty"`
	TargetRetrievalWeightBPS          uint64 `json:"target_retrieval_weight_bps,omitempty"`
	TargetFoundationReleaseRateBPS    uint64 `json:"target_foundation_release_rate_bps,omitempty"`
	TargetFoundationAddress           string `json:"target_foundation_address,omitempty"`
	TargetRetrievalAddress            string `json:"target_retrieval_address,omitempty"`
	TargetStorageRewardPerBlock       uint64 `json:"target_storage_reward_per_block,omitempty"`
	TargetRetrievalAnnualRateBPS      uint64 `json:"target_retrieval_annual_rate_bps,omitempty"`     // deprecated
	TargetFoundationAnnualRateBPS     uint64 `json:"target_foundation_annual_rate_bps,omitempty"`    // deprecated
	TargetRetrievalRewardPerBlock     uint64 `json:"target_retrieval_reward_per_block,omitempty"`
	TargetFoundationRewardPerBlock    uint64 `json:"target_foundation_reward_per_block,omitempty"`
	TargetAvailabilityWindowSize      uint64 `json:"target_availability_window_size,omitempty"`
	TargetAvailabilityThresholdBPS    uint64 `json:"target_availability_threshold_bps,omitempty"`
	TargetBlockProductionRewardBPS    uint64 `json:"target_block_production_reward_bps,omitempty"`
	TargetValidatorRewardPerBlock     uint64 `json:"target_validator_reward_per_block,omitempty"`
	TargetMaxConsensusValidators      uint64 `json:"target_max_consensus_validators,omitempty"`
	TargetMinConsensusValidators      uint64 `json:"target_min_consensus_validators,omitempty"`
	TargetBlockBytes                  uint64 `json:"target_block_bytes,omitempty"`
	TargetMaxBlockBytes               uint64 `json:"target_max_block_bytes,omitempty"`
	TargetMaxBlockTxs                 uint64 `json:"target_max_block_txs,omitempty"`
	TargetMaxTxBytes                  uint64 `json:"target_max_tx_bytes,omitempty"`
	TargetMaxStorageTxBytes           uint64 `json:"target_max_storage_tx_bytes,omitempty"`
	TargetRegistrationBonusAmount     uint64 `json:"target_registration_bonus_amount,omitempty"`
	TargetMinBonusProofCount          uint64 `json:"target_min_bonus_proof_count,omitempty"`
	TargetMinBonusSuccessRateBPS      uint64 `json:"target_min_bonus_success_rate_bps,omitempty"`
	TargetMinBonusRetrievalCount      uint64 `json:"target_min_bonus_retrieval_count,omitempty"`
	TargetMaxBonusAddresses           uint64 `json:"target_max_bonus_addresses,omitempty"`
	TargetBonusDeadlineSeconds        uint64 `json:"target_bonus_deadline_seconds,omitempty"`
	TargetActivationWindowSeconds     uint64 `json:"target_activation_window_seconds,omitempty"`
	TargetFeeMarketBaseFee            uint64 `json:"target_fee_market_base_fee,omitempty"`
	TargetFeeMarketTargetBlockTxs     int    `json:"target_fee_market_target_block_txs,omitempty"`
	TargetFeeMultiplierBridgeOut      uint64 `json:"target_fee_multiplier_bridge_out,omitempty"`
	TargetFeeMultiplierCreateIntent   uint64 `json:"target_fee_multiplier_create_intent,omitempty"`
	TargetFeeMultiplierUploadNFT      uint64 `json:"target_fee_multiplier_upload_nft_template,omitempty"`
	TargetFeeMultiplierRegisterVal    uint64 `json:"target_fee_multiplier_register_validator,omitempty"`
	TargetFeeMultiplierBatchCommit    uint64 `json:"target_fee_multiplier_batch_commit,omitempty"`
	ChainID                           string `json:"chain_id"`
	ProposerNonce                     uint64 `json:"proposer_nonce"`
	Status                            string `json:"status"`
	CreatedAtUnix                     int64  `json:"created_at_unix"`
}

// GovernanceVote is a signed vote on a governance proposal.
type GovernanceVote struct {
	ProposalID     string `json:"proposal_id"`
	Voter          string `json:"voter"`
	VoterSignature string `json:"voter_signature"`
	Approve        bool   `json:"approve"`
	CreatedAtUnix  int64  `json:"created_at_unix"`
}

// CreateGovernanceProposalRequest is the HTTP request to create a governance proposal.
type CreateGovernanceProposalRequest struct {
	Proposer                         string   `json:"proposer"`
	ChainID                          string   `json:"chain_id"`
	IntentID                         string   `json:"intent_id,omitempty"`
	Action                           string   `json:"action"`
	ReasonHash                       string   `json:"reason_hash"`
	ExpiresAtUnix                    int64    `json:"expires_at_unix,omitempty"`
	PreserveStorage                  bool     `json:"preserve_storage,omitempty"`
	AppealDeadlineUnix               int64    `json:"appeal_deadline_unix,omitempty"`
	TargetOperator                   string   `json:"target_operator,omitempty"`
	TargetPublicKey                  string   `json:"target_public_key,omitempty"`
	TargetPermissions                []string `json:"target_permissions,omitempty"`
	TargetDataModerationThresholdNum int      `json:"target_data_moderation_threshold_num,omitempty"`
	TargetDataModerationThresholdDen int      `json:"target_data_moderation_threshold_den,omitempty"`
	TargetOperatorChangeThresholdNum int      `json:"target_operator_change_threshold_num,omitempty"`
	TargetOperatorChangeThresholdDen int      `json:"target_operator_change_threshold_den,omitempty"`
	// ── Target mining params (for update_mining_params action) ──
	TargetStorageReleaseRateBPS       uint64 `json:"target_storage_release_rate_bps,omitempty"`
	TargetRetrievalReleaseRateBPS     uint64 `json:"target_retrieval_release_rate_bps,omitempty"`
	TargetStoredBytesWeightBPS        uint64 `json:"target_stored_bytes_weight_bps,omitempty"`
	TargetProofScoreWeightBPS         uint64 `json:"target_proof_score_weight_bps,omitempty"`
	TargetAvailabilityWeightBPS       uint64 `json:"target_availability_weight_bps,omitempty"`
	TargetRetrievalSpeedWeightBPS     uint64 `json:"target_retrieval_speed_weight_bps,omitempty"`
	TargetIPDispersionWeightBPS       uint64 `json:"target_ip_dispersion_weight_bps,omitempty"`
	TargetRetrievalRewardPerMiB       uint64 `json:"target_retrieval_reward_per_mib,omitempty"`
	TargetMaxRetrievalRewardPerWindow uint64 `json:"target_max_retrieval_reward_per_window,omitempty"`
	TargetPermanentFundTakeoverSeconds int64  `json:"target_repair_pool_takeover_seconds,omitempty"`
	TargetMinerDegradeThreshold       uint64 `json:"target_miner_degrade_threshold,omitempty"`
	TargetStorageProofSamples         int    `json:"target_storage_proof_samples,omitempty"`
	TargetValidatorCommissionBPS      uint64 `json:"target_validator_commission_bps,omitempty"`
	TargetRetrievalWeightBPS          uint64 `json:"target_retrieval_weight_bps,omitempty"`
	TargetFoundationReleaseRateBPS    uint64 `json:"target_foundation_release_rate_bps,omitempty"`
	TargetFoundationAddress           string `json:"target_foundation_address,omitempty"`
	TargetRetrievalAddress            string `json:"target_retrieval_address,omitempty"`
	TargetStorageRewardPerBlock       uint64 `json:"target_storage_reward_per_block,omitempty"`
	TargetRetrievalAnnualRateBPS      uint64 `json:"target_retrieval_annual_rate_bps,omitempty"`     // deprecated
	TargetFoundationAnnualRateBPS     uint64 `json:"target_foundation_annual_rate_bps,omitempty"`    // deprecated
	TargetRetrievalRewardPerBlock     uint64 `json:"target_retrieval_reward_per_block,omitempty"`
	TargetFoundationRewardPerBlock    uint64 `json:"target_foundation_reward_per_block,omitempty"`
	TargetAvailabilityWindowSize      uint64 `json:"target_availability_window_size,omitempty"`
	TargetAvailabilityThresholdBPS    uint64 `json:"target_availability_threshold_bps,omitempty"`
	TargetBlockProductionRewardBPS    uint64 `json:"target_block_production_reward_bps,omitempty"`
	TargetValidatorRewardPerBlock     uint64 `json:"target_validator_reward_per_block,omitempty"`
	TargetMaxConsensusValidators      uint64 `json:"target_max_consensus_validators,omitempty"`
	TargetMinConsensusValidators      uint64 `json:"target_min_consensus_validators,omitempty"`
	TargetBlockBytes                  uint64 `json:"target_block_bytes,omitempty"`
	TargetMaxBlockBytes               uint64 `json:"target_max_block_bytes,omitempty"`
	TargetMaxBlockTxs                 uint64 `json:"target_max_block_txs,omitempty"`
	TargetMaxTxBytes                  uint64 `json:"target_max_tx_bytes,omitempty"`
	TargetMaxStorageTxBytes           uint64 `json:"target_max_storage_tx_bytes,omitempty"`
	TargetRegistrationBonusAmount     uint64 `json:"target_registration_bonus_amount,omitempty"`
	TargetMinBonusProofCount          uint64 `json:"target_min_bonus_proof_count,omitempty"`
	TargetMinBonusSuccessRateBPS      uint64 `json:"target_min_bonus_success_rate_bps,omitempty"`
	TargetMinBonusRetrievalCount      uint64 `json:"target_min_bonus_retrieval_count,omitempty"`
	TargetMaxBonusAddresses           uint64 `json:"target_max_bonus_addresses,omitempty"`
	TargetBonusDeadlineSeconds        uint64 `json:"target_bonus_deadline_seconds,omitempty"`
	TargetActivationWindowSeconds     uint64 `json:"target_activation_window_seconds,omitempty"`
	TargetFeeMarketBaseFee            uint64 `json:"target_fee_market_base_fee,omitempty"`
	TargetFeeMarketTargetBlockTxs     int    `json:"target_fee_market_target_block_txs,omitempty"`
	TargetFeeMultiplierBridgeOut      uint64 `json:"target_fee_multiplier_bridge_out,omitempty"`
	TargetFeeMultiplierCreateIntent   uint64 `json:"target_fee_multiplier_create_intent,omitempty"`
	TargetFeeMultiplierUploadNFT      uint64 `json:"target_fee_multiplier_upload_nft_template,omitempty"`
	TargetFeeMultiplierRegisterVal    uint64 `json:"target_fee_multiplier_register_validator,omitempty"`
	TargetFeeMultiplierBatchCommit    uint64 `json:"target_fee_multiplier_batch_commit,omitempty"`
	Signature                         string `json:"signature"`
	Nonce                             uint64 `json:"nonce"`
	CreatedAtUnix                     int64  `json:"created_at_unix"`
}

// CreateGovernanceProposalResponse is the response after creating a proposal.
type CreateGovernanceProposalResponse struct {
	Proposal GovernanceProposal `json:"proposal"`
}

// CastGovernanceVoteRequest is the HTTP request to cast a vote on a proposal.
type CastGovernanceVoteRequest struct {
	ProposalID    string `json:"proposal_id"`
	Voter         string `json:"voter"`
	Approve       bool   `json:"approve"`
	ChainID       string `json:"chain_id"`
	Signature     string `json:"signature"`
	Nonce         uint64 `json:"nonce"`
	CreatedAtUnix int64  `json:"created_at_unix"`
}

// CastGovernanceVoteResponse is the response after casting a vote.
type CastGovernanceVoteResponse struct {
	Vote         GovernanceVote `json:"vote"`
	ApproveCount int            `json:"approve_count"`
	RejectCount  int            `json:"reject_count"`
	Threshold    int            `json:"threshold"`
	Executed     bool           `json:"executed"`
}

// ExecuteGovernanceProposalRequest is the HTTP request to execute an approved proposal.
type ExecuteGovernanceProposalRequest struct {
	ProposalID    string `json:"proposal_id"`
	Executor      string `json:"executor"`
	ChainID       string `json:"chain_id"`
	Signature     string `json:"signature"`
	Nonce         uint64 `json:"nonce"`
	CreatedAtUnix int64  `json:"created_at_unix"`
}

// ExecuteGovernanceProposalResponse is the response after executing a proposal.
type ExecuteGovernanceProposalResponse struct {
	Proposal         GovernanceProposal           `json:"proposal"`
	GovernanceResult GovernanceDealActionResponse `json:"governance_result"`
}

// GovernanceProposalListResponse is the response for listing proposals.
type GovernanceProposalListResponse struct {
	Proposals []GovernanceProposal        `json:"proposals"`
	Votes     map[string][]GovernanceVote `json:"votes"`
}

// GovernanceOperatorListResponse is the response for listing governance operators.
type GovernanceOperatorListResponse struct {
	Operators                  []GovernanceOperator `json:"operators"`
	DataModerationThreshold    int                  `json:"data_moderation_threshold"`
	OperatorChangeThreshold    int                  `json:"operator_change_threshold"`
	DataModerationThresholdNum int                  `json:"data_moderation_threshold_num"`
	DataModerationThresholdDen int                  `json:"data_moderation_threshold_den"`
	OperatorChangeThresholdNum int                  `json:"operator_change_threshold_num"`
	OperatorChangeThresholdDen int                  `json:"operator_change_threshold_den"`
}

// ── Multisig Wallet ──

// MultisigWallet represents an on-chain M-of-N multisig wallet.
type MultisigWallet struct {
	Address       string   `json:"address"`
	Signers       []string `json:"signers"`
	Threshold     uint8    `json:"threshold"`
	Nonce         uint64   `json:"nonce"`
	Salt          uint64   `json:"salt"`
	CreatedAtUnix int64    `json:"created_at_unix"`
}

// MultisigCreateRequest is the HTTP request to register a multisig wallet.
type MultisigCreateRequest struct {
	ChainID   string   `json:"chain_id"`
	Signers   []string `json:"signers"`
	Threshold uint8    `json:"threshold"`
	Salt      uint64   `json:"salt"`
	Signature string   `json:"signature"`
}

// MultisigExecRequest is the HTTP request to execute a multisig operation.
type MultisigExecRequest struct {
	Wallet     string              `json:"wallet"`
	Operation  string              `json:"operation"`
	Payload    json.RawMessage     `json:"payload"`
	ChainID    string              `json:"chain_id"`
	Nonce      uint64              `json:"nonce"`
	Fee        uint64              `json:"fee"`
	Signatures []MultisigSignature `json:"signatures"`
}

// MultisigSignature is one signer's contribution to a multisig execution.
type MultisigSignature struct {
	Signer    string `json:"signer"`
	Signature string `json:"signature"`
}

// MultisigTransferPayload is the inner payload for a "transfer" operation.
type MultisigTransferPayload struct {
	To     string `json:"to"`
	Amount uint64 `json:"amount"`
}

// MultisigExecResponse is returned after a successful multisig execution.
type MultisigExecResponse struct {
	Wallet           MultisigWallet    `json:"wallet"`
	TransferResponse *TransferResponse `json:"transfer_response,omitempty"`
}

// MultisigWalletInfo enriches a wallet with its current balance.
type MultisigWalletInfo struct {
	Wallet  MultisigWallet `json:"wallet"`
	Balance uint64         `json:"balance"`
}

// MultisigWalletListResponse is the response for listing multisig wallets.
type MultisigWalletListResponse struct {
	Wallets []MultisigWalletInfo `json:"wallets"`
}

// ──────────────────────────────────────────────────────────────────────────────
// Bridge
// ──────────────────────────────────────────────────────────────────────────────

// BridgeConfig holds the global bridge configuration (singleton).
type BridgeConfig struct {
	Enabled           bool   `json:"enabled"`
	BridgePoolAddress string `json:"bridge_pool_address"`
	RelayerAddress    string `json:"relayer_address"`
	MinBridgeAmount   uint64 `json:"min_bridge_amount"`
	DelaySeconds      int64  `json:"delay_seconds"`
	MaxAmountPerDay   uint64 `json:"max_amount_per_day"`
	CurrentDayAmount  uint64 `json:"current_day_amount"`
	DayStartUnix      int64  `json:"day_start_unix"`
	Paused            bool   `json:"paused"`
}

// BridgeOutbound records a user's bridge-out request (Falari → ETH).
type BridgeOutbound struct {
	Nonce          uint64 `json:"nonce"`
	TxHash         string `json:"tx_hash,omitempty"`
	TargetChainID  string `json:"target_chain_id"`
	Sender         string `json:"sender"`
	Recipient      string `json:"recipient"`
	Amount         uint64 `json:"amount"`
	Fee            uint64 `json:"fee"`
	Status         string `json:"status"`
	LockedAtUnix   int64  `json:"locked_at_unix"`
	ClaimableAfter int64  `json:"claimable_after"`
}

// BridgeInbound records a bridge-in request originating from ETH (ETH → Falari).
type BridgeInbound struct {
	Nonce             uint64 `json:"nonce"`
	SourceTxHash      string `json:"source_tx_hash"`
	SourceBlockNumber uint64 `json:"source_block_number"`
	Recipient         string `json:"recipient"`
	Amount            uint64 `json:"amount"`
	Status            string `json:"status"`
	DetectedAtUnix    int64  `json:"detected_at_unix"`
	ClaimableAfter    int64  `json:"claimable_after"`
}

// BridgeConsumedMessage prevents replay of bridge claims.
type BridgeConsumedMessage struct {
	MessageHash    string `json:"message_hash"`
	ConsumedAtUnix int64  `json:"consumed_at_unix"`
}

// BridgeOutRequest is the user-submitted payload for a bridge_out transaction.
type BridgeOutRequest struct {
	Sender        string `json:"sender"`
	Recipient     string `json:"recipient"`
	TargetChainID string `json:"target_chain_id"`
	Amount        uint64 `json:"amount"`
	Fee           uint64 `json:"fee"`
	Nonce         uint64 `json:"nonce"`
	Signature     string `json:"signature"`
	PublicKey     string `json:"public_key"`
}

// BridgeInClaimRequest is the relayer-submitted payload for a bridge_in_claim transaction.
type BridgeInClaimRequest struct {
	SourceTxHash      string `json:"source_tx_hash"`
	SourceBlockNumber uint64 `json:"source_block_number"`
	Recipient         string `json:"recipient"`
	Amount            uint64 `json:"amount"`
	Nonce             uint64 `json:"nonce"`
	Direction         string `json:"direction"` // "in" (ETH→Falari unlock)
	Signature         string `json:"signature"`
	PublicKey         string `json:"public_key"`
}

// BridgeSetConfigRequest is the governance payload to update bridge configuration.
type BridgeSetConfigRequest struct {
	Action          string  `json:"action"` // "update_config" / "pause" / "unpause"
	RelayerAddress  string  `json:"relayer_address,omitempty"`
	MinBridgeAmount *uint64 `json:"min_bridge_amount,omitempty"`
	MaxAmountPerDay *uint64 `json:"max_amount_per_day,omitempty"`
	DelaySeconds    *int64  `json:"delay_seconds,omitempty"`
	Timestamp       int64   `json:"timestamp"` // unix seconds, replay protection
	Signature       string  `json:"signature"`
	PublicKey       string  `json:"public_key"`
}

// BridgePendingResponse is returned by GET /bridge/pending.
type BridgePendingResponse struct {
	Outbounds []*BridgeOutbound `json:"outbounds"`
	Inbounds  []*BridgeInbound  `json:"inbounds"`
}

// ── Direct Governance Actions (Execute First, Review Later) ──

const (
	// DirectActionPendingReview indicates the action is active but pending committee review.
	DirectActionPendingReview = "pending_review"
	// DirectActionRatified indicates the committee has ratified the action.
	DirectActionRatified = "ratified"
	// DirectActionRejected indicates the committee has rejected the action; it was rolled back.
	DirectActionRejected = "rejected"
	// DirectActionAutoRatified indicates the review window expired without rejection.
	DirectActionAutoRatified = "auto_ratified"
)

// DirectActionReviewWindowSeconds is the default review window (72 hours).
const DirectActionReviewWindowSeconds int64 = 72 * 60 * 60

// DirectActionRecord tracks a governance action that was executed directly by an operator
// and is subject to post-execution committee review.
type DirectActionRecord struct {
	ActionID               string `json:"action_id"`
	IntentID               string `json:"intent_id"`
	Operator               string `json:"operator"`
	Action                 string `json:"action"` // "freeze"|"block"|"legal_hold"
	ReasonHash             string `json:"reason_hash"`
	ExpiresAtUnix          int64  `json:"expires_at_unix,omitempty"`          // for freeze
	PreserveStorage        bool   `json:"preserve_storage,omitempty"`         // for block
	AppealDeadlineUnix     int64  `json:"appeal_deadline_unix,omitempty"`     // for block
	ReviewStatus           string `json:"review_status"`                      // pending_review|ratified|rejected|auto_ratiated
	ReviewDeadlineUnix     int64  `json:"review_deadline_unix"`               // when the review window closes
	CreatedAtUnix          int64  `json:"created_at_unix"`
	RatifiedAtUnix         int64  `json:"ratified_at_unix,omitempty"`
	RejectedAtUnix         int64  `json:"rejected_at_unix,omitempty"`
	// Snapshot of intent state before action, for rollback on rejection.
	PreAccessStatus        string `json:"pre_access_status,omitempty"`
	PreModerationStatus    string `json:"pre_moderation_status,omitempty"`
	PreStorageStatus       string `json:"pre_storage_status,omitempty"`
	PreExpiresAtUnix       int64  `json:"pre_expires_at_unix,omitempty"`
	PreAppealDeadlineUnix  int64  `json:"pre_appeal_deadline_unix,omitempty"`
}

// DirectActionReviewVote is a signed vote on a direct action review.
type DirectActionReviewVote struct {
	ActionID       string `json:"action_id"`
	Voter          string `json:"voter"`
	VoterSignature string `json:"voter_signature"`
	Reject         bool   `json:"reject"` // true = reject the action
	CreatedAtUnix  int64  `json:"created_at_unix"`
}

// DirectGovernanceActionRequest is the HTTP request for an operator to directly execute
// a governance data moderation action (freeze/block/legal_hold) without a proposal.
type DirectGovernanceActionRequest struct {
	Operator           string `json:"operator"`
	ChainID            string `json:"chain_id"`
	IntentID           string `json:"intent_id"`
	Action             string `json:"action"` // "freeze"|"block"|"legal_hold"
	ReasonHash         string `json:"reason_hash"`
	ExpiresAtUnix      int64  `json:"expires_at_unix,omitempty"`      // for freeze
	PreserveStorage    bool   `json:"preserve_storage,omitempty"`     // for block
	AppealDeadlineUnix int64  `json:"appeal_deadline_unix,omitempty"` // for block
	Nonce              uint64 `json:"nonce"`
	Signature          string `json:"signature"`
	CreatedAtUnix      int64  `json:"created_at_unix"`
	AgentKeyID         string `json:"agent_key_id,omitempty"`
	AgentNonce         uint64 `json:"agent_nonce,omitempty"`
	AgentSignature     string `json:"agent_signature,omitempty"`
}

// DirectGovernanceActionResponse is the response for a direct governance action.
type DirectGovernanceActionResponse struct {
	Record             DirectActionRecord               `json:"record"`
	GovernanceResult   GovernanceDealActionResponse     `json:"governance_result"`
	ReviewDeadlineUnix int64                            `json:"review_deadline_unix"`
}

// DirectActionReviewVoteRequest is the HTTP request for an operator to cast a review vote.
type DirectActionReviewVoteRequest struct {
	ActionID       string `json:"action_id"`
	Voter          string `json:"voter"`
	Reject         bool   `json:"reject"`
	ChainID        string `json:"chain_id"`
	Nonce          uint64 `json:"nonce"`
	Signature      string `json:"signature"`
	CreatedAtUnix  int64  `json:"created_at_unix"`
	AgentKeyID     string `json:"agent_key_id,omitempty"`
	AgentNonce     uint64 `json:"agent_nonce,omitempty"`
	AgentSignature string `json:"agent_signature,omitempty"`
}

// DirectActionReviewVoteResponse is the response after casting a review vote.
type DirectActionReviewVoteResponse struct {
	Vote         DirectActionReviewVote `json:"vote"`
	RejectCount  int                    `json:"reject_count"`
	Threshold    int                    `json:"threshold"`
	Rejected     bool                   `json:"rejected"`
}

// DirectActionListResponse is the response for listing direct action records.
type DirectActionListResponse struct {
	Records []DirectActionRecord                `json:"records"`
	Votes   map[string][]DirectActionReviewVote `json:"votes"`
}
