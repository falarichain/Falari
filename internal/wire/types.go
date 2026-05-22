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
	User         string              `json:"user"`
	FileName     string              `json:"file_name"`
	FileSize     int64               `json:"file_size"`
	SegmentSize  int64               `json:"segment_size"`
	FileRoot     string              `json:"file_root"`
	SegmentRoots []string            `json:"segment_roots"`
	Segments     []SegmentPlan       `json:"segments"`
	Erasure      ErasurePolicy       `json:"erasure"`
	Encryption   *EncryptionMetadata `json:"encryption,omitempty"`
	Policy       StoragePolicy       `json:"policy"`
	LockedFee    uint64              `json:"locked_fee"`
	DeadlineUnix int64               `json:"deadline_unix"`
}

type CreateIntentResponse struct {
	IntentID    string              `json:"intent_id"`
	Status      string              `json:"status"`
	RequiredFee uint64              `json:"required_fee"`
	LockedFee   uint64              `json:"locked_fee"`
	Assignments []StorageAssignment `json:"assignments,omitempty"`
}

type StoragePricing struct {
	BasePricePerGiBMonth uint64 `json:"base_price_per_gib_month"`
	MinimumFee           uint64 `json:"minimum_fee"`
	PermanentDuration    int64  `json:"permanent_duration"`
}

type PermanentStorageFund struct {
	IntentID          string `json:"intent_id"`
	User              string `json:"user"`
	Balance           uint64 `json:"balance"`
	Contributed       uint64 `json:"contributed"`
	Paid              uint64 `json:"paid"`
	CreatedAtUnix     int64  `json:"created_at_unix"`
	UpdatedAtUnix     int64  `json:"updated_at_unix"`
	LastPayoutUnix    int64  `json:"last_payout_unix,omitempty"`
	Closed            bool   `json:"closed,omitempty"`
	ClosedReason      string `json:"closed_reason,omitempty"`
	ClosedAtUnix      int64  `json:"closed_at_unix,omitempty"`
	TransferredToPool uint64 `json:"transferred_to_pool,omitempty"`
}

type PermanentFundTopUpRequest struct {
	IntentID string `json:"intent_id"`
	User     string `json:"user"`
	Amount   uint64 `json:"amount"`
}

type PermanentFundTopUpResponse struct {
	Fund PermanentStorageFund `json:"fund"`
}

type ChainStatusResponse struct {
	Status                 string         `json:"status"`
	Height                 uint64         `json:"height"`
	LatestBlockHash        string         `json:"latest_block_hash,omitempty"`
	LatestBlockTimeUnix    int64          `json:"latest_block_time_unix,omitempty"`
	LatestFinalizedHeight  uint64         `json:"latest_finalized_height,omitempty"`
	PendingTransactions    int            `json:"pending_transactions"`
	Accounts               int            `json:"accounts"`
	Intents                int            `json:"intents"`
	UploadingIntents       int            `json:"uploading_intents"`
	PartialIntents         int            `json:"partial_intents"`
	FinalizedIntents       int            `json:"finalized_intents"`
	ExpiredIntents         int            `json:"expired_intents"`
	Deals                  int            `json:"deals"`
	Collections            int            `json:"collections"`
	DataRecords            int            `json:"data_records"`
	Miners                 int            `json:"miners"`
	ActiveMiners           int            `json:"active_miners"`
	CapacityBytes          uint64         `json:"capacity_bytes"`
	UsedBytes              uint64         `json:"used_bytes"`
	ReservedBytes          uint64         `json:"reserved_bytes"`
	Validators             int            `json:"validators"`
	ConsensusValidators    int            `json:"consensus_validators"`
	EpochRound             uint64         `json:"epoch_round,omitempty"`
	EpochsFinalized        uint64         `json:"epochs_finalized,omitempty"`
	PendingChallenges      int            `json:"pending_challenges"`
	ActiveEpochs           int            `json:"active_epochs"`
	PendingRepairTasks     int            `json:"pending_repair_tasks"`
	CompletedRepairTasks   int            `json:"completed_repair_tasks"`
	DealsAtRisk            int            `json:"deals_at_risk,omitempty"`
	DealsCritical          int            `json:"deals_critical,omitempty"`
	TotalStorageRewards    uint64         `json:"total_storage_rewards,omitempty"`
	TotalRetrievalRewards  uint64         `json:"total_retrieval_rewards,omitempty"`
	TotalRepairRewards     uint64         `json:"total_repair_rewards,omitempty"`
	TotalSlashed           uint64         `json:"total_slashed,omitempty"`
	TotalSupply            uint64         `json:"total_supply,omitempty"`
	StoragePoolRemaining   uint64         `json:"storage_pool_remaining,omitempty"`
	RetrievalPoolRemaining uint64         `json:"retrieval_pool_remaining,omitempty"`
	ValidatorPoolRemaining uint64         `json:"validator_pool_remaining,omitempty"`
	RepairPoolRemaining    uint64         `json:"repair_pool_remaining,omitempty"`
	TokensReleased         uint64         `json:"tokens_released,omitempty"`
	RetrievalReceipts      int            `json:"retrieval_receipts"`
	RetrievalBytes         uint64         `json:"retrieval_bytes"`
	FeeMarket              FeeMarket      `json:"fee_market"`
	StoragePricing         StoragePricing `json:"storage_pricing"`
	PeerCount              int            `json:"peer_count,omitempty"`
	Peers                  []string       `json:"peers,omitempty"`
	LibP2PEnabled          bool           `json:"libp2p_enabled,omitempty"`
	LibP2PID               string         `json:"libp2p_id,omitempty"`
	LibP2PAddrs            []string       `json:"libp2p_addrs,omitempty"`
}

type StorageNodeStatusResponse struct {
	Status                 string                    `json:"status"`
	Address                string                    `json:"address"`
	PublicKey              string                    `json:"public_key"`
	ShardCount             int                       `json:"shard_count"`
	StoredBytes            uint64                    `json:"stored_bytes"`
	DataDir                string                    `json:"data_dir,omitempty"`
	PeerID                 string                    `json:"peer_id,omitempty"`
	PeerAddrs              []string                  `json:"peer_addrs,omitempty"`
	TransportStats         StorageTransportStats     `json:"transport_stats"`
	RecentProviderMemories []ProviderTransportMemory `json:"recent_provider_memories,omitempty"`
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
	FileSize int64         `json:"file_size"`
	Erasure  ErasurePolicy `json:"erasure"`
	Policy   StoragePolicy `json:"policy"`
}

type StorageQuoteResponse struct {
	Pricing               StoragePricing `json:"pricing"`
	FileSize              int64          `json:"file_size"`
	RedundantBytes        uint64         `json:"redundant_bytes"`
	Duration              int64          `json:"duration"`
	BillableGiBMonths     uint64         `json:"billable_gib_months"`
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
	ExpiresAtUnix    int64  `json:"expires_at_unix"`
	MinerEndpoint    string `json:"miner_endpoint,omitempty"`
	Signature        string `json:"signature"`
}

type BatchCommitRequest struct {
	IntentID string         `json:"intent_id"`
	User     string         `json:"user"`
	Receipts []MinerReceipt `json:"receipts"`
}

type BatchCommitResponse struct {
	IntentID          string `json:"intent_id"`
	Status            string `json:"status"`
	CommittedSegments int    `json:"committed_segments"`
	UploadedSize      int64  `json:"uploaded_size"`
}

type FinalizeRequest struct {
	IntentID     string `json:"intent_id"`
	User         string `json:"user"`
	ManifestRoot string `json:"manifest_root"`
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
	Assignments             []StorageAssignment `json:"assignments,omitempty"`
	Erasure                 ErasurePolicy       `json:"erasure"`
	Encryption              *EncryptionMetadata `json:"encryption,omitempty"`
	Policy                  StoragePolicy       `json:"policy"`
	LockedFee               uint64              `json:"locked_fee"`
	PaidFee                 uint64              `json:"paid_fee,omitempty"`
	RefundedFee             uint64              `json:"refunded_fee,omitempty"`
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
	IntentID string `json:"intent_id"`
	User     string `json:"user,omitempty"`
}

type SettleIntentResponse struct {
	IntentID    string `json:"intent_id"`
	Status      string `json:"status"`
	RefundedFee uint64 `json:"refunded_fee"`
	PaidFee     uint64 `json:"paid_fee"`
}

type RenewDealRequest struct {
	IntentID string `json:"intent_id"`
	User     string `json:"user"`
	Duration int64  `json:"duration"`
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
	IntentID string `json:"intent_id"`
	User     string `json:"user"`
	Reason   string `json:"reason,omitempty"`
}

type TerminateDealResponse struct {
	IntentID         string       `json:"intent_id"`
	Status           string       `json:"status"`
	StorageStatus    string       `json:"storage_status"`
	AccessStatus     string       `json:"access_status"`
	RefundedFee      uint64       `json:"refunded_fee"`
	DeleteTasks      []DeleteTask `json:"delete_tasks,omitempty"`
	TerminatedAtUnix int64        `json:"terminated_at_unix"`
}

type SetAccessPolicyRequest struct {
	IntentID     string `json:"intent_id"`
	User         string `json:"user"`
	AccessStatus string `json:"access_status"`
	ReasonHash   string `json:"reason_hash,omitempty"`
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
	Permissions   []string `json:"permissions"`
	Enabled       bool     `json:"enabled"`
	CreatedAtUnix int64    `json:"created_at_unix,omitempty"`
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
	Assignments       []StorageAssignment `json:"assignments,omitempty"`
	Erasure           ErasurePolicy       `json:"erasure"`
	Encryption        *EncryptionMetadata `json:"encryption,omitempty"`
	Policy            StoragePolicy       `json:"policy"`
	LockedFee         uint64              `json:"locked_fee"`
	Receipts          []MinerReceipt      `json:"receipts"`
	CommittedSegments []int               `json:"committed_segments"`
	CommittedShards   []ShardRef          `json:"committed_shards,omitempty"`
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
	AvailableShards     int               `json:"available_shards"`
	RequiredShards      int               `json:"required_shards"`
	TargetShards        int               `json:"target_shards"`
	MissingShardIndexes []int             `json:"missing_shard_indexes"`
	Assignment          StorageAssignment `json:"assignment,omitempty"`
	SourceReceipts      []MinerReceipt    `json:"source_receipts,omitempty"`
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
	MinerAddress       string          `json:"miner_address"`
	PublicKey          string          `json:"public_key"`
	Endpoint           string          `json:"endpoint,omitempty"`
	PeerID             string          `json:"peer_id,omitempty"`
	PeerAddrs          []string        `json:"peer_addrs,omitempty"`
	CapacityBytes      uint64          `json:"capacity_bytes,omitempty"`
	StoredBytes        uint64          `json:"stored_bytes,omitempty"`
	ShardCount         int             `json:"shard_count,omitempty"`
	ShardHashes        []string        `json:"shard_hashes,omitempty"`
	Shards             []ProviderShard `json:"shards,omitempty"`
	LastSeenUnix       int64           `json:"last_seen_unix"`
	ExpiresAtUnix      int64           `json:"expires_at_unix"`
	HealthScoreBPS     uint64          `json:"health_score_bps,omitempty"`
	ProofSuccess       uint64          `json:"proof_success,omitempty"`
	ProofFailure       uint64          `json:"proof_failure,omitempty"`
	ProviderSource     string          `json:"provider_source,omitempty"`
	ProviderRecordLive bool            `json:"provider_record_live,omitempty"`
	Signature          string          `json:"signature"`
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
	MinerAddress       string   `json:"miner_address"`
	ShardHash          string   `json:"shard_hash,omitempty"`
	ShardCID           string   `json:"shard_cid,omitempty"`
	Transport          string   `json:"transport"`
	Endpoint           string   `json:"endpoint,omitempty"`
	PeerID             string   `json:"peer_id,omitempty"`
	PeerAddrs          []string `json:"peer_addrs,omitempty"`
	HealthScoreBPS     uint64   `json:"health_score_bps,omitempty"`
	ProviderRecordLive bool     `json:"provider_record_live,omitempty"`
	ProviderSource     string   `json:"provider_source,omitempty"`
	Priority           int      `json:"priority"`
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
	ProofType        string      `json:"proof_type,omitempty"`
	IntentID         string      `json:"intent_id"`
	DealID           string      `json:"deal_id"`
	SegmentID        int         `json:"segment_id"`
	SegmentRoot      string      `json:"segment_root"`
	ShardIndex       int         `json:"shard_index"`
	ShardHash        string      `json:"shard_hash"`
	ShardSize        int64       `json:"shard_size"`
	SectorCommitment string      `json:"sector_commitment"`
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
	ChallengeID  string `json:"challenge_id"`
	MinerAddress string `json:"miner_address"`
	Status       string `json:"status"`
	Reward       uint64 `json:"reward"`
}

type StartEpochRequest struct {
	IntentID            string `json:"intent_id,omitempty"`
	ChallengesPerDeal   int    `json:"challenges_per_deal"`
	DurationSeconds     int64  `json:"duration_seconds"`
	RewardPerProof      uint64 `json:"reward_per_proof"`
	SlashPerMissedProof uint64 `json:"slash_per_missed_proof"`
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

type PendingRetrievalReward struct {
	ReceiptID     string `json:"receipt_id"`
	MinerAddress  string `json:"miner_address"`
	IntentID      string `json:"intent_id"`
	Reward        uint64 `json:"reward"`
	EpochRound    uint64 `json:"epoch_round"`
	HeldSinceUnix int64  `json:"held_since_unix"`
	ReleaseAtUnix int64  `json:"release_at_unix"`
}

type EpochRewardsResponse struct {
	EpochID              string `json:"epoch_id"`
	EpochRound           uint64 `json:"epoch_round,omitempty"`
	StorageRewardsPaid   uint64 `json:"storage_rewards_paid,omitempty"`
	RetrievalRewardsPaid uint64 `json:"retrieval_rewards_paid,omitempty"`
	RepairRewardsPaid    uint64 `json:"repair_rewards_paid,omitempty"`
	StorageSlashed       uint64 `json:"storage_slashed,omitempty"`
	PendingRetrieval     int    `json:"pending_retrieval_count,omitempty"`
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
	TotalTokenSupply      uint64 `json:"total_token_supply"`
	PendingRetrieval      int    `json:"pending_retrieval_count"`
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
	TokenTotalSupply          uint64 = 10_000_000_000
	TokenMiningSupply         uint64 = 9_000_000_000
	TokenStoragePoolInitial   uint64 = 6_300_000_000
	TokenRetrievalPoolInitial uint64 = 1_200_000_000
	TokenValidatorPoolInitial uint64 = 1_000_000_000
	TokenRepairPoolInitial    uint64 = 500_000_000
)

type RewardPools struct {
	StoragePoolRemaining   uint64 `json:"storage_pool_remaining"`
	RetrievalPoolRemaining uint64 `json:"retrieval_pool_remaining"`
	ValidatorPoolRemaining uint64 `json:"validator_pool_remaining"`
	RepairPoolRemaining    uint64 `json:"repair_pool_remaining"`
	TokensReleased         uint64 `json:"tokens_released"`
}

type MinerStats struct {
	MinerAddress        string `json:"miner_address"`
	PublicKey           string `json:"public_key"`
	Endpoint            string `json:"endpoint"`
	CapacityBytes       uint64 `json:"capacity_bytes"`
	UsedBytes           uint64 `json:"used_bytes"`
	ReservedBytes       uint64 `json:"reserved_bytes,omitempty"`
	Stake               uint64 `json:"stake"`
	Status              string `json:"status"`
	RegisteredAtUnix    int64  `json:"registered_at_unix"`
	ProofSuccess        uint64 `json:"proof_success"`
	ProofFailure        uint64 `json:"proof_failure"`
	ConsecutiveFailures uint64 `json:"consecutive_failures,omitempty"`
	Rewards             uint64 `json:"rewards"`
	StorageRewards      uint64 `json:"storage_rewards,omitempty"`
	RetrievalSuccess    uint64 `json:"retrieval_success,omitempty"`
	RetrievalBytes      uint64 `json:"retrieval_bytes,omitempty"`
	RetrievalRewards    uint64 `json:"retrieval_rewards,omitempty"`
	RepairRewards       uint64 `json:"repair_rewards,omitempty"`
	Slashed             uint64 `json:"slashed"`
	ExitedAtUnix        int64  `json:"exited_at_unix,omitempty"`
	EffectiveWeight     uint64 `json:"effective_weight,omitempty"`
	DelegatorCount      int    `json:"delegator_count,omitempty"`
	SpeedScore          uint64 `json:"speed_score,omitempty"`
	AntiSpamScore       uint64 `json:"anti_spam_score,omitempty"`
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
	Signature     string `json:"signature"`
}

type RegisterMinerResponse struct {
	Miner MinerStats `json:"miner"`
}

type Account struct {
	Address       string `json:"address"`
	PublicKey     string `json:"public_key,omitempty"`
	Balance       uint64 `json:"balance"`
	Nonce         uint64 `json:"nonce"`
	LockedStake   uint64 `json:"locked_stake"`
	LockedStorage uint64 `json:"locked_storage"`
}

type FaucetRequest struct {
	Address string `json:"address"`
	Amount  uint64 `json:"amount"`
}

type FaucetResponse struct {
	Account Account `json:"account"`
}

type TransferRequest struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    uint64 `json:"amount"`
	Nonce     uint64 `json:"nonce,omitempty"`
	Fee       uint64 `json:"fee,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
	Signature string `json:"signature,omitempty"`
	RawTx     string `json:"raw_tx,omitempty"`
}

type TransferResponse struct {
	From Account `json:"from"`
	To   Account `json:"to"`
}

type RawTransactionRequest struct {
	RawTx string `json:"raw_tx"`
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

type FeeMarket struct {
	BaseFee         uint64 `json:"base_fee"`
	TargetBlockTxs  int    `json:"target_block_txs"`
	LastBlockTxs    int    `json:"last_block_txs"`
	UpdatedAtHeight uint64 `json:"updated_at_height"`
}

type MempoolResponse struct {
	Pending   []Transaction `json:"pending"`
	FeeMarket FeeMarket     `json:"fee_market"`
}

type ValidatorInfo struct {
	Address           string `json:"address"`
	PublicKey         string `json:"public_key"`
	Endpoint          string `json:"endpoint,omitempty"`
	Stake             uint64 `json:"stake"`
	DelegatedStake    uint64 `json:"delegated_stake,omitempty"`
	SelfStake         uint64 `json:"self_stake,omitempty"`
	Status            string `json:"status"`
	Consensus         bool   `json:"consensus"`
	RegisteredAtUnix  int64  `json:"registered_at_unix"`
	ProducedBlocks    uint64 `json:"produced_blocks"`
	Slashed           uint64 `json:"slashed"`
	EvidenceCount     uint64 `json:"evidence_count"`
	DelegatorCount    int    `json:"delegator_count,omitempty"`
	Rewards           uint64 `json:"rewards,omitempty"`
	DelegationRewards uint64 `json:"delegation_rewards,omitempty"`
}

type StakeDelegation struct {
	Delegator string `json:"delegator"`
	Validator string `json:"validator"`
	Amount    uint64 `json:"amount"`
	SinceUnix int64  `json:"since_unix"`
}

type DelegateStakeRequest struct {
	Delegator string `json:"delegator"`
	Validator string `json:"validator"`
	Amount    uint64 `json:"amount"`
}

type DelegateStakeResponse struct {
	Delegator      string `json:"delegator"`
	Validator      string `json:"validator"`
	Amount         uint64 `json:"amount"`
	DelegatedStake uint64 `json:"delegated_stake"`
}

type UndelegateStakeRequest struct {
	Delegator string `json:"delegator"`
	Validator string `json:"validator"`
	Amount    uint64 `json:"amount"`
}

type UndelegateStakeResponse struct {
	Delegator      string `json:"delegator"`
	Validator      string `json:"validator"`
	Released       uint64 `json:"released"`
	DelegatedStake uint64 `json:"delegated_stake"`
}

type RegisterValidatorRequest struct {
	Address   string `json:"address"`
	PublicKey string `json:"public_key"`
	Endpoint  string `json:"endpoint,omitempty"`
	Stake     uint64 `json:"stake"`
	Signature string `json:"signature"`
}

type RegisterValidatorResponse struct {
	Validator ValidatorInfo `json:"validator"`
}

type ListValidatorsResponse struct {
	Validators []ValidatorInfo `json:"validators"`
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
	IntentID         string             `json:"intent_id"`
	Owner            string             `json:"owner"`
	Recipient        string             `json:"recipient"`
	RecipientType    string             `json:"recipient_type"`
	Algorithm        string             `json:"algorithm"`
	EncryptedDataKey string             `json:"encrypted_data_key"`
	Nonce            string             `json:"nonce,omitempty"`
	KDF              *PasscodeKDFParams `json:"kdf,omitempty"`
	ExpiresAtUnix    int64              `json:"expires_at_unix,omitempty"`
}

type CreateKeyEnvelopeResponse struct {
	Envelope KeyEnvelope `json:"envelope"`
}

type CreateAddressShareRequest struct {
	IntentID         string `json:"intent_id"`
	Owner            string `json:"owner"`
	Recipient        string `json:"recipient"`
	Algorithm        string `json:"algorithm"`
	EncryptedDataKey string `json:"encrypted_data_key"`
	Nonce            string `json:"nonce,omitempty"`
	ExpiresAtUnix    int64  `json:"expires_at_unix,omitempty"`
}

type CreatePasscodeShareRequest struct {
	IntentID         string             `json:"intent_id"`
	Owner            string             `json:"owner"`
	Mode             string             `json:"mode,omitempty"`
	Algorithm        string             `json:"algorithm"`
	EncryptedDataKey string             `json:"encrypted_data_key"`
	Nonce            string             `json:"nonce,omitempty"`
	KDF              *PasscodeKDFParams `json:"kdf"`
	ExpiresAtUnix    int64              `json:"expires_at_unix,omitempty"`
}

type CreateShareResponse struct {
	Share    ShareRecord `json:"share"`
	Envelope KeyEnvelope `json:"envelope"`
}

type RevokeShareRequest struct {
	ShareID string `json:"share_id"`
	Owner   string `json:"owner"`
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
	Master      string   `json:"master"`
	Name        string   `json:"name"`
	AgentPub    string   `json:"agent_pub"`
	Permissions []string `json:"permissions"`
	DailyLimit  uint64   `json:"daily_limit"`
	TotalLimit  uint64   `json:"total_limit"`
	ExpiresAt   int64    `json:"expires_at"`
	Signature   string   `json:"signature"`
}

type RegisterAgentKeyResponse struct {
	Key AgentKey `json:"key"`
}

type RevokeAgentKeyRequest struct {
	KeyID     string `json:"key_id"`
	Master    string `json:"master"`
	Nonce     uint64 `json:"nonce"`
	Signature string `json:"signature"`
}

type ListAgentKeysResponse struct {
	Keys []AgentKey `json:"keys"`
}
