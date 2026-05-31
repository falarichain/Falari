package chain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"chain/internal/consensus"
	chaincrypto "chain/internal/crypto"
	"chain/internal/reward"
	"chain/internal/wire"

	"github.com/syndtr/goleveldb/leveldb"
)

var levelDBStateKey = []byte("state:snapshot")

const defaultBaseFee uint64 = 100_000_000
const defaultTargetBlockTxs = 10
const defaultStorageBasePrice uint64 = 10_000_000 // 0.01 Token/MiB/30天 = 0.12 Token/MiB/年
const defaultStorageMinimumFee uint64 = 1_000_000 // 0.01 Token
const defaultStorageBurnBPS uint64 = 300
const defaultStorageRetrievalBPS uint64 = 300
const defaultStorageFoundationBPS uint64 = 300
const defaultPermanentStorageDuration = int64(50 * 365 * 24 * 60 * 60) // 50年

const defaultRetrievalAbuseSpeedMultiplier uint64 = 10
const miningRewardVestingDays = int64(90)
const miningRewardVestingDaySeconds = int64(24 * 60 * 60)
const defaultPermanentFundAnnualSpendBPS uint64 = 200

type Intent struct {
	wire.IntentView
	DeadlineUnix int64                             `json:"deadline_unix"`
	Receipts     map[int]map[int]wire.MinerReceipt `json:"receipts"`
	CreatedAt    int64                             `json:"created_at"`
	UpdatedAt    int64                             `json:"updated_at"`
}

type State struct {
	ChainID                    string                                    `json:"chain_id"`
	Intents                    map[string]*Intent                        `json:"intents"`
	Deals                      map[string]string                         `json:"deals"`
	Challenges                 map[string]wire.StorageChallenge          `json:"challenges"`
	Proofs                     map[string]wire.StorageProof              `json:"proofs"`
	Epochs                     map[string]wire.ProofEpoch                `json:"epochs"`
	Miners                     map[string]wire.MinerStats                `json:"miners"`
	Accounts                   map[string]wire.Account                   `json:"accounts"`
	Blocks                     []wire.Block                              `json:"blocks"`
	PendingTxs                 []wire.Transaction                        `json:"pending_txs"`
	Receipts                   map[string]wire.TransactionReceipt        `json:"receipts"`
	Validators                 map[string]wire.ValidatorInfo             `json:"validators"`
	ConsensusValidators        map[string]bool                           `json:"consensus_validators"`
	ValidatorEvidence          map[string]wire.ValidatorEvidence         `json:"validator_evidence"`
	ConsensusVotes             map[string]wire.ConsensusVote             `json:"consensus_votes"`
	FeeMarket                  wire.FeeMarket                            `json:"fee_market"`
	FeeChargedTxs              map[string]bool                           `json:"fee_charged_txs"`
	StoragePricing             wire.StoragePricing                       `json:"storage_pricing"`
	StorageFeePool             wire.StorageFeePool                       `json:"storage_fee_pool"`
	DealEscrows                map[string]wire.DealEscrow                `json:"deal_escrows"`
	PermanentStorageFunds      map[string]wire.PermanentStorageFund      `json:"permanent_storage_funds"`
	RepairTasks                map[string]wire.RepairTask                `json:"repair_tasks"`
	ProviderRecords            map[string]wire.StorageProviderRecord     `json:"provider_records"`
	DeleteTasks                map[string]wire.DeleteTask                `json:"delete_tasks"`
	GovernanceAudits           []wire.GovernanceAuditRecord              `json:"governance_audits"`
	GovernanceOperators        map[string]wire.GovernanceOperator        `json:"governance_operators"`
	OperatorNonces             map[string]uint64                         `json:"operator_nonces,omitempty"`
	DeleteReceipts             map[string]wire.DeleteReceipt             `json:"delete_receipts"`
	RetrievalReceipts          map[string]wire.RetrievalReceipt          `json:"retrieval_receipts"`
	RetrievalWindows           map[string]wire.RetrievalRateWindow       `json:"retrieval_windows"`
	MiningRewardVestings       map[string]wire.MiningRewardVestingBucket `json:"mining_reward_vestings"`
	StakeDelegations           map[string]wire.StakeDelegation           `json:"stake_delegations"`
	DealHealths                map[string]wire.DealHealth                `json:"deal_healths"`
	RewardPools                *reward.Pools                             `json:"reward_pools"`
	MiningParams               *MiningParams                             `json:"mining_params"`
	Collections                map[string]wire.DataCollection            `json:"collections"`
	DataRecords                map[string]wire.DataRecord                `json:"data_records"`
	CollectionRecords          map[string][]string                       `json:"collection_records"`
	KeyEnvelopes               map[string]wire.KeyEnvelope               `json:"key_envelopes"`
	ShareRecords               map[string]wire.ShareRecord               `json:"share_records"`
	AppliedTxs                 map[string]bool                           `json:"applied_txs"`
	ConfirmedTxs               map[string]bool                           `json:"confirmed_txs"`
	EpochRound                 uint64                                    `json:"epoch_round"`
	BonusGrantedCount          uint64                                    `json:"bonus_granted_count,omitempty"`
	NextMinerID                uint64                                    `json:"next_miner_id,omitempty"`
	ConsensusHeight            uint64                                    `json:"consensus_height"`
	ConsensusRound             uint64                                    `json:"consensus_round"`
	ConsensusPhase             string                                    `json:"consensus_phase"`
	ConsensusProposer          string                                    `json:"consensus_proposer"`
	UpgradePlan                consensus.UpgradePlan                     `json:"upgrade_plan"`
	AgentKeys                  map[string]*wire.AgentKey                 `json:"agent_keys"`
	BlacklistedShards          map[string]wire.BlacklistEntry            `json:"blacklisted_shards"`
	GovernanceProposals        map[string]wire.GovernanceProposal        `json:"governance_proposals"`
	GovernanceVotes            map[string][]wire.GovernanceVote          `json:"governance_votes"`
	MultisigWallets            map[string]*wire.MultisigWallet           `json:"multisig_wallets"`
	DirectActionRecords        map[string]wire.DirectActionRecord        `json:"direct_action_records,omitempty"`
	DirectActionReviewVotes    map[string][]wire.DirectActionReviewVote  `json:"direct_action_review_votes,omitempty"`
	DirectActionReviewWindowSeconds int64                                `json:"direct_action_review_window_seconds,omitempty"`
	FoundationAddress          string                                    `json:"foundation_address,omitempty"`
	RetrievalAddress           string                                    `json:"retrieval_address,omitempty"`
	LastReleaseAtUnix          int64                                     `json:"last_release_at_unix,omitempty"`
	LastValidatorReleaseAtUnix int64                                     `json:"last_validator_release_at_unix,omitempty"`
	StorageRewardIndex         string                                    `json:"storage_reward_index,omitempty"`
	StorageRewardRemainder     string                                    `json:"storage_reward_remainder,omitempty"`

	// Validator availability scoring — per-validator ring buffer of proposer turn results.
	ProposerTurns map[string]*wire.ValidatorTurnWindow `json:"proposer_turns,omitempty"`
	// Timestamp when the current consensus round started (for timeout detection).
	ConsensusRoundStartedAtUnix int64 `json:"consensus_round_started_at_unix,omitempty"`

	// Governance threshold configuration (numerator/denominator).
	// DataModerationThreshold applies to freeze/block/legal_hold/appeal.
	// OperatorChangeThreshold applies to add/remove/update_operator and update_config.
	DataModerationThresholdNum int `json:"data_moderation_threshold_num"`
	DataModerationThresholdDen int `json:"data_moderation_threshold_den"`
	OperatorChangeThresholdNum int `json:"operator_change_threshold_num"`
	OperatorChangeThresholdDen int `json:"operator_change_threshold_den"`

	// Operator-to-owner mapping: operatorAddress -> ownerAddress.
	OperatorMap map[string]string `json:"operator_map,omitempty"`
	// Unbonding entries for staking withdrawal delays.
	UnbondingEntries map[string]wire.UnbondingEntry `json:"unbonding_entries,omitempty"`

	// Bridge state.
	BridgeConfig           *wire.BridgeConfig                     `json:"bridge_config,omitempty"`
	BridgeOutbounds        map[uint64]*wire.BridgeOutbound        `json:"bridge_outbounds,omitempty"`
	BridgeInbounds         map[string]*wire.BridgeInbound         `json:"bridge_inbounds,omitempty"`
	BridgeConsumedMessages map[string]*wire.BridgeConsumedMessage `json:"bridge_consumed_messages,omitempty"`
	BridgeOutboundNonce    uint64                                 `json:"bridge_outbound_nonce,omitempty"`

	// Pending shard repairs: tracks missed proofs that haven't yet reached the
	// repair delay threshold. Key format: intentID:segmentID:shardIndex:minerAddress.
	PendingShardRepairs map[string]wire.PendingShardRepair `json:"pending_shard_repairs,omitempty"`

	// Miner NFT template: shared badge image for all miners.
	// Generated as SVG placeholder when miner #1 registers;
	// can be replaced by miner #1 uploading a custom design.
	MinerNFTTemplate     string `json:"miner_nft_template,omitempty"`
	MinerNFTContentType  string `json:"miner_nft_content_type,omitempty"`
	MinerNFTTemplateHash string `json:"miner_nft_template_hash,omitempty"`
}

type Store struct {
	mu               sync.Mutex
	path             string
	db               *leveldb.DB
	data             State
	operatorIdentity *OperatorIdentity
	broadcaster      BlockBroadcaster
	txBroadcaster    TransactionBroadcaster
	voteBroadcaster  ConsensusVoteBroadcaster
	blockInterval    time.Duration
}

// SetBlockInterval configures the block production interval for per-block reward calculations.
func (s *Store) SetBlockInterval(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockInterval = d
}

func OpenStore(path string) (*Store, error) {
	return OpenStoreWithGenesis(path, "")
}

func OpenStoreWithGenesis(path string, genesisPath string) (*Store, error) {
	store := &Store{
		path: path,
		data: newState(),
	}
	if path == "" {
		return store, nil
	}

	if isLevelDBPath(path) {
		db, err := leveldb.OpenFile(strings.TrimPrefix(path, "leveldb://"), nil)
		if err != nil {
			return nil, err
		}
		store.db = db
	}

	raw, rawErr := readStateFile(path, store.db)
	hasGenesis := genesisPath != ""

	if !hasExistingStateFile(path, store.db) && hasGenesis {
		data, err := newStateFromGenesisFile(genesisPath)
		if err != nil {
			return nil, err
		}
		store.data = data
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
		return store, nil
	}

	if rawErr != nil {
		return nil, rawErr
	}
	if len(raw) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(raw, &store.data); err != nil {
		return nil, err
	}
	normalizeState(&store.data)
	return store, nil
}

func readStateFile(path string, db *leveldb.DB) ([]byte, error) {
	if db != nil {
		raw, err := db.Get(levelDBStateKey, nil)
		if errors.Is(err, leveldb.ErrNotFound) {
			return nil, nil
		}
		return raw, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}

func hasExistingStateFile(path string, db *leveldb.DB) bool {
	if path == "" {
		return false
	}
	if db != nil {
		_, err := db.Get(levelDBStateKey, nil)
		return err == nil
	}
	_, err := os.Stat(path)
	return err == nil
}

func newStateFromGenesisFile(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var doc wire.GenesisDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return State{}, err
	}
	return newStateFromGenesis(doc)
}

func newStateFromGenesis(doc wire.GenesisDoc) (State, error) {
	state := newState()
	// 1. Load accounts (balances are untouched by validator staking).
	for _, acc := range doc.Accounts {
		address := wire.NormalizeAddress(acc.Address)
		state.Accounts[address] = wire.Account{
			Address:       address,
			Balance:       acc.Balance,
			LockedStake:   0,
			LockedStorage: 0,
		}
	}
	// 2. Load reward pools before validators so we can deduct stakes from foundation pool.
	if doc.RewardPools != nil {
		state.RewardPools = &reward.Pools{
			StorageRemaining:    doc.RewardPools.StoragePoolRemaining,
			RetrievalRemaining:  doc.RewardPools.RetrievalPoolRemaining,
			ValidatorRemaining:  doc.RewardPools.ValidatorPoolRemaining,
			PermanentFundRemaining: doc.RewardPools.PermanentFundRemaining,
			FoundationRemaining: doc.RewardPools.FoundationPoolRemaining,
		}
	}
	// 3. Process validators: deduct stake from foundation pool, lock in account.
	var totalStake uint64
	for _, v := range doc.Validators {
		totalStake += v.Stake
	}
	if totalStake > 0 {
		if state.RewardPools == nil {
			return State{}, fmt.Errorf("genesis has validators but no reward_pools to deduct foundation stake from")
		}
		if state.RewardPools.FoundationRemaining < totalStake {
			return State{}, fmt.Errorf("foundation pool %d < total validator stake %d", state.RewardPools.FoundationRemaining, totalStake)
		}
		state.RewardPools.FoundationRemaining -= totalStake
	}
	for _, v := range doc.Validators {
		ownerAddr := wire.NormalizeAddress(v.OwnerAddress)
		operatorAddr := wire.NormalizeAddress(v.OperatorAddress)
		account, exists := state.Accounts[ownerAddr]
		if !exists {
			return State{}, fmt.Errorf("genesis validator %s has no account entry in genesis", ownerAddr)
		}
		if v.Stake < MinValidatorStake {
			return State{}, fmt.Errorf("genesis validator %s stake %d below minimum %d", ownerAddr, v.Stake, MinValidatorStake)
		}
		// Lock stake (deducted from foundation pool, not from account balance).
		account.LockedStake += v.Stake
		state.Accounts[ownerAddr] = account
		state.Validators[ownerAddr] = wire.ValidatorInfo{
			OwnerAddress:      ownerAddr,
			OperatorAddress:   operatorAddr,
			OperatorPublicKey: v.OperatorPublicKey,
			Endpoint:          v.Endpoint,
			Stake:             v.Stake,
			SelfStake:         v.Stake,
			Status:            "active",
			Consensus:         true,
			RegisteredAtUnix:  doc.GenesisTime,
		}
		state.ConsensusValidators[ownerAddr] = true
		if state.OperatorMap == nil {
			state.OperatorMap = map[string]string{}
		}
		state.OperatorMap[operatorAddr] = ownerAddr
	}
	if doc.FoundationAddress != "" {
		state.FoundationAddress = wire.NormalizeAddress(doc.FoundationAddress)
	}
	if doc.RetrievalAddress != "" {
		state.RetrievalAddress = wire.NormalizeAddress(doc.RetrievalAddress)
	}
	for _, operator := range doc.GovernanceOperators {
		key := ""
		// Derive operator address from ECDSA public key when available.
		if operator.PublicKey != "" {
			key = wire.GovernanceOperatorAddress(operator.PublicKey)
		}
		if key == "" {
			key = normalizeGovernanceOperator(operator.Operator)
		}
		if key == "" {
			continue
		}
		enabled := true
		if operator.Enabled != nil {
			enabled = *operator.Enabled
		}
		state.GovernanceOperators[key] = wire.GovernanceOperator{
			Operator:      key,
			PublicKey:     operator.PublicKey,
			Permissions:   append([]string(nil), operator.Permissions...),
			Enabled:       enabled,
			CreatedAtUnix: doc.GenesisTime,
		}
	}
	// Load governance threshold configuration from genesis.
	state.DataModerationThresholdNum = doc.DataModerationThresholdNum
	state.DataModerationThresholdDen = doc.DataModerationThresholdDen
	state.OperatorChangeThresholdNum = doc.OperatorChangeThresholdNum
	state.OperatorChangeThresholdDen = doc.OperatorChangeThresholdDen
	state.ChainID = doc.ChainID
	return state, nil
}

func newState() State {
	return State{
		ChainID:                "falari-dev",
		Intents:                map[string]*Intent{},
		Deals:                  map[string]string{},
		Challenges:             map[string]wire.StorageChallenge{},
		Proofs:                 map[string]wire.StorageProof{},
		Epochs:                 map[string]wire.ProofEpoch{},
		Miners:                 map[string]wire.MinerStats{},
		Accounts:               map[string]wire.Account{},
		Blocks:                 []wire.Block{},
		PendingTxs:             []wire.Transaction{},
		Receipts:               map[string]wire.TransactionReceipt{},
		Validators:             map[string]wire.ValidatorInfo{},
		ConsensusValidators:    map[string]bool{},
		ValidatorEvidence:      map[string]wire.ValidatorEvidence{},
		ConsensusVotes:         map[string]wire.ConsensusVote{},
		FeeMarket:              defaultFeeMarket(),
		FeeChargedTxs:          map[string]bool{},
		StoragePricing:         defaultStoragePricing(),
		DealEscrows:            map[string]wire.DealEscrow{},
		PermanentStorageFunds:  map[string]wire.PermanentStorageFund{},
		RepairTasks:            map[string]wire.RepairTask{},
		ProviderRecords:        map[string]wire.StorageProviderRecord{},
		DeleteTasks:            map[string]wire.DeleteTask{},
		GovernanceAudits:       []wire.GovernanceAuditRecord{},
		GovernanceOperators:    map[string]wire.GovernanceOperator{},
		OperatorNonces:         map[string]uint64{},
		DeleteReceipts:         map[string]wire.DeleteReceipt{},
		RetrievalReceipts:      map[string]wire.RetrievalReceipt{},
		RetrievalWindows:       map[string]wire.RetrievalRateWindow{},
		MiningRewardVestings:   map[string]wire.MiningRewardVestingBucket{},
		StakeDelegations:       map[string]wire.StakeDelegation{},
		DealHealths:            map[string]wire.DealHealth{},
		ProposerTurns:          map[string]*wire.ValidatorTurnWindow{},
		Collections:            map[string]wire.DataCollection{},
		DataRecords:            map[string]wire.DataRecord{},
		CollectionRecords:      map[string][]string{},
		KeyEnvelopes:           map[string]wire.KeyEnvelope{},
		ShareRecords:           map[string]wire.ShareRecord{},
		AppliedTxs:             map[string]bool{},
		ConfirmedTxs:           map[string]bool{},
		AgentKeys:              map[string]*wire.AgentKey{},
		GovernanceProposals:    map[string]wire.GovernanceProposal{},
		GovernanceVotes:        map[string][]wire.GovernanceVote{},
		MultisigWallets:        map[string]*wire.MultisigWallet{},
		DirectActionRecords:    map[string]wire.DirectActionRecord{},
		DirectActionReviewVotes: map[string][]wire.DirectActionReviewVote{},
		OperatorMap:            map[string]string{},
		UnbondingEntries:       map[string]wire.UnbondingEntry{},
		BridgeOutbounds:        map[uint64]*wire.BridgeOutbound{},
		BridgeInbounds:         map[string]*wire.BridgeInbound{},
		BridgeConsumedMessages: map[string]*wire.BridgeConsumedMessage{},
		PendingShardRepairs:    map[string]wire.PendingShardRepair{},
		StorageRewardIndex:     "0",
		StorageRewardRemainder: "0",
		// Governance threshold defaults: data moderation = 1/3, operator changes = 2/3.
		DataModerationThresholdNum: 1,
		DataModerationThresholdDen: 3,
		OperatorChangeThresholdNum: 2,
		OperatorChangeThresholdDen: 3,
		// Direct action review window default: 72 hours.
		DirectActionReviewWindowSeconds: wire.DirectActionReviewWindowSeconds,
	}
}

func normalizeState(state *State) {
	if state.ChainID == "" {
		state.ChainID = "falari-dev"
	}
	if state.Intents == nil {
		state.Intents = map[string]*Intent{}
	}
	if state.Deals == nil {
		state.Deals = map[string]string{}
	}
	if state.Challenges == nil {
		state.Challenges = map[string]wire.StorageChallenge{}
	}
	if state.Proofs == nil {
		state.Proofs = map[string]wire.StorageProof{}
	}
	if state.Epochs == nil {
		state.Epochs = map[string]wire.ProofEpoch{}
	}
	if state.Miners == nil {
		state.Miners = map[string]wire.MinerStats{}
	}
	if state.Accounts == nil {
		state.Accounts = map[string]wire.Account{}
	}
	if state.Blocks == nil {
		state.Blocks = []wire.Block{}
	}
	if state.PendingTxs == nil {
		state.PendingTxs = []wire.Transaction{}
	}
	if state.Receipts == nil {
		state.Receipts = map[string]wire.TransactionReceipt{}
	}
	if state.Validators == nil {
		state.Validators = map[string]wire.ValidatorInfo{}
	}
	if state.ConsensusValidators == nil {
		state.ConsensusValidators = map[string]bool{}
	}
	if state.ValidatorEvidence == nil {
		state.ValidatorEvidence = map[string]wire.ValidatorEvidence{}
	}
	if state.ConsensusVotes == nil {
		state.ConsensusVotes = map[string]wire.ConsensusVote{}
	}
	if state.FeeMarket.BaseFee == 0 {
		state.FeeMarket = defaultFeeMarket()
	}
	if state.FeeMarket.TargetBlockTxs <= 0 {
		state.FeeMarket.TargetBlockTxs = defaultTargetBlockTxs
	}
	if state.FeeMarket.Multipliers == (wire.FeeMultipliers{}) {
		state.FeeMarket.Multipliers = defaultFeeMultipliers()
	}
	if state.FeeChargedTxs == nil {
		state.FeeChargedTxs = map[string]bool{}
	}
	if state.StoragePricing.BasePrice == 0 {
		state.StoragePricing = defaultStoragePricing()
	}
	if state.StoragePricing.MinimumFee == 0 {
		state.StoragePricing.MinimumFee = defaultStorageMinimumFee
	}
	if state.StoragePricing.PermanentDuration == 0 {
		state.StoragePricing.PermanentDuration = defaultPermanentStorageDuration
	}
	if state.DealEscrows == nil {
		state.DealEscrows = map[string]wire.DealEscrow{}
	}
	if state.PermanentStorageFunds == nil {
		state.PermanentStorageFunds = map[string]wire.PermanentStorageFund{}
	}
	if state.RepairTasks == nil {
		state.RepairTasks = map[string]wire.RepairTask{}
	}
	if state.PendingShardRepairs == nil {
		state.PendingShardRepairs = map[string]wire.PendingShardRepair{}
	}
	if state.ProviderRecords == nil {
		state.ProviderRecords = map[string]wire.StorageProviderRecord{}
	}
	if state.DeleteTasks == nil {
		state.DeleteTasks = map[string]wire.DeleteTask{}
	}
	if state.GovernanceAudits == nil {
		state.GovernanceAudits = []wire.GovernanceAuditRecord{}
	}
	if state.GovernanceOperators == nil {
		state.GovernanceOperators = map[string]wire.GovernanceOperator{}
	}
	if state.OperatorNonces == nil {
		state.OperatorNonces = map[string]uint64{}
	}
	if state.DeleteReceipts == nil {
		state.DeleteReceipts = map[string]wire.DeleteReceipt{}
	}
	if state.RetrievalReceipts == nil {
		state.RetrievalReceipts = map[string]wire.RetrievalReceipt{}
	}
	if state.RetrievalWindows == nil {
		state.RetrievalWindows = map[string]wire.RetrievalRateWindow{}
	}
	if state.MiningRewardVestings == nil {
		state.MiningRewardVestings = map[string]wire.MiningRewardVestingBucket{}
	}
	if state.StakeDelegations == nil {
		state.StakeDelegations = map[string]wire.StakeDelegation{}
	}
	if state.DealHealths == nil {
		state.DealHealths = map[string]wire.DealHealth{}
	}
	if state.ProposerTurns == nil {
		state.ProposerTurns = map[string]*wire.ValidatorTurnWindow{}
	}
	if state.Collections == nil {
		state.Collections = map[string]wire.DataCollection{}
	}
	if state.DataRecords == nil {
		state.DataRecords = map[string]wire.DataRecord{}
	}
	if state.CollectionRecords == nil {
		state.CollectionRecords = map[string][]string{}
	}
	if state.KeyEnvelopes == nil {
		state.KeyEnvelopes = map[string]wire.KeyEnvelope{}
	}
	if state.ShareRecords == nil {
		state.ShareRecords = map[string]wire.ShareRecord{}
	}
	if state.AppliedTxs == nil {
		state.AppliedTxs = map[string]bool{}
	}
	if state.ConfirmedTxs == nil {
		state.ConfirmedTxs = map[string]bool{}
	}
	if state.AgentKeys == nil {
		state.AgentKeys = map[string]*wire.AgentKey{}
	}
	if state.GovernanceProposals == nil {
		state.GovernanceProposals = map[string]wire.GovernanceProposal{}
	}
	if state.GovernanceVotes == nil {
		state.GovernanceVotes = map[string][]wire.GovernanceVote{}
	}
	if state.MultisigWallets == nil {
		state.MultisigWallets = map[string]*wire.MultisigWallet{}
	}
	if state.DirectActionRecords == nil {
		state.DirectActionRecords = map[string]wire.DirectActionRecord{}
	}
	if state.DirectActionReviewVotes == nil {
		state.DirectActionReviewVotes = map[string][]wire.DirectActionReviewVote{}
	}
	if state.DirectActionReviewWindowSeconds == 0 {
		state.DirectActionReviewWindowSeconds = wire.DirectActionReviewWindowSeconds
	}
	// Governance threshold defaults: data moderation = 1/3, operator changes = 2/3.
	if state.DataModerationThresholdNum == 0 || state.DataModerationThresholdDen == 0 {
		state.DataModerationThresholdNum = 1
		state.DataModerationThresholdDen = 3
	}
	if state.OperatorChangeThresholdNum == 0 || state.OperatorChangeThresholdDen == 0 {
		state.OperatorChangeThresholdNum = 2
		state.OperatorChangeThresholdDen = 3
	}
	if state.MiningParams == nil {
		defaults := DefaultMiningParams()
		state.MiningParams = &defaults
	}
	if state.StorageRewardIndex == "" {
		state.StorageRewardIndex = "0"
	}
	if state.StorageRewardRemainder == "" {
		state.StorageRewardRemainder = "0"
	}
	for address, miner := range state.Miners {
		if miner.StorageRewardIndex == "" {
			miner.StorageRewardIndex = state.StorageRewardIndex
		}
		if miner.Status == wire.MinerStatusActive || miner.Status == wire.MinerStatusDegraded || miner.Status == wire.MinerStatusJailed {
			miner.AccessServiceRequired = true
			if !miner.UploadServiceEnabled && !miner.DownloadServiceEnabled {
				miner.UploadServiceEnabled = true
				miner.DownloadServiceEnabled = true
			}
			state.Miners[address] = miner
		}
	}
	for address, account := range state.Accounts {
		normalized := wire.NormalizeAddress(address)
		account.Address = wire.NormalizeAddress(account.Address)
		if account.Address == "" {
			account.Address = normalized
		}
		if normalized != address {
			delete(state.Accounts, address)
		}
		state.Accounts[normalized] = account
	}
	for key, bucket := range state.MiningRewardVestings {
		normalized := wire.NormalizeAddress(bucket.Address)
		if normalized == "" {
			delete(state.MiningRewardVestings, key)
			continue
		}
		if bucket.Sources == nil {
			bucket.Sources = map[string]uint64{}
		}
		bucket.Address = normalized
		state.MiningRewardVestings[key] = bucket
	}
	pendingMiningByAddress := map[string]uint64{}
	for _, bucket := range state.MiningRewardVestings {
		if bucket.Total <= bucket.Released {
			continue
		}
		pendingMiningByAddress[bucket.Address] = saturatingAdd(pendingMiningByAddress[bucket.Address], bucket.Total-bucket.Released)
	}
	for address, pending := range pendingMiningByAddress {
		account := state.Accounts[address]
		account.Address = address
		account.PendingMiningRewards = pending
		state.Accounts[address] = account
	}
	for address, account := range state.Accounts {
		if _, ok := pendingMiningByAddress[address]; !ok {
			account.PendingMiningRewards = 0
			state.Accounts[address] = account
		}
	}
	for _, intent := range state.Intents {
		if intent.Receipts == nil {
			intent.Receipts = map[int]map[int]wire.MinerReceipt{}
		}
		if intent.LockedFee > 0 {
			ensureDealEscrowForState(state, intent)
		}
		if fund, ok := state.PermanentStorageFunds[intent.IntentID]; ok {
			if fund.SustainableDailyRate == 0 && fund.Balance > 0 {
				fund.SustainableDailyRate = permanentFundDailyRate(fund.Balance)
				state.PermanentStorageFunds[intent.IntentID] = fund
			}
			intent.PermanentFundBalance = fund.Balance
			intent.PermanentFundPaid = fund.Paid
		}
		normalizeIntentLifecycle(intent)
	}
	rebuildStorageFeePoolForState(state)

	// Bridge state initialization.
	if state.BridgeOutbounds == nil {
		state.BridgeOutbounds = map[uint64]*wire.BridgeOutbound{}
	}
	if state.BridgeInbounds == nil {
		state.BridgeInbounds = map[string]*wire.BridgeInbound{}
	}
	if state.BridgeConsumedMessages == nil {
		state.BridgeConsumedMessages = map[string]*wire.BridgeConsumedMessage{}
	}
}

func defaultFeeMultipliers() wire.FeeMultipliers {
	return wire.FeeMultipliers{
		BridgeOut:         20000, // 2.0x
		CreateIntent:      15000, // 1.5x
		UploadNFTTemplate: 15000, // 1.5x
		RegisterValidator: 15000, // 1.5x
		BatchCommit:       15000, // 1.5x
	}
}

func defaultFeeMarket() wire.FeeMarket {
	return wire.FeeMarket{
		BaseFee:        defaultBaseFee,
		TargetBlockTxs: defaultTargetBlockTxs,
		Multipliers:    defaultFeeMultipliers(),
	}
}

func defaultStoragePricing() wire.StoragePricing {
	return wire.StoragePricing{
		BasePrice:         defaultStorageBasePrice,
		MinimumFee:        defaultStorageMinimumFee,
		PermanentDuration: defaultPermanentStorageDuration,
	}
}

func (s *Store) CreateIntent(req wire.CreateIntentRequest) (wire.CreateIntentResponse, error) {
	req.User = wire.NormalizeAddress(req.User)
	if req.User == "" {
		return wire.CreateIntentResponse{}, errors.New("user is required")
	}
	if req.FileRoot == "" || len(req.SegmentRoots) == 0 {
		return wire.CreateIntentResponse{}, errors.New("file root and segment roots are required")
	}
	if req.FileSize <= 0 || req.SegmentSize <= 0 {
		return wire.CreateIntentResponse{}, errors.New("file size and segment size must be positive")
	}
	if len(req.Segments) != len(req.SegmentRoots) {
		return wire.CreateIntentResponse{}, errors.New("segments must match segment roots")
	}
	if req.Erasure.DataShards <= 0 || req.Erasure.ParityShards < 0 {
		return wire.CreateIntentResponse{}, errors.New("invalid erasure policy")
	}
	if req.DeadlineUnix == 0 {
		req.DeadlineUnix = time.Now().Add(24 * time.Hour).Unix()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	quote, err := s.storageQuoteForIntentLocked(req)
	if err != nil {
		return wire.CreateIntentResponse{}, err
	}
	if req.LockedFee == 0 {
		req.LockedFee = quote.RequiredFee
	}
	if req.LockedFee < quote.RequiredFee {
		return wire.CreateIntentResponse{}, errors.New("locked storage fee below current quote")
	}
	assignments, err := s.buildStorageAssignmentsLocked(req)
	if err != nil {
		return wire.CreateIntentResponse{}, err
	}
	userAccount := s.accountLocked(req.User)
	if userAccount.Balance < req.LockedFee {
		return wire.CreateIntentResponse{}, errors.New("insufficient balance for storage fee")
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, req.User, "create_intent", req.LockedFee, func(agentPub string) error {
			return wire.VerifyCreateIntentAgent(req, agentPub)
		}); err != nil {
			return wire.CreateIntentResponse{}, err
		}
	} else {
		if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
			return wire.VerifyCreateIntent(req)
		}); err != nil {
			return wire.CreateIntentResponse{}, err
		}
		s.consumeAccountNonceLocked(req.User)
	}
	userAccount = s.accountLocked(req.User)
	burnAmount := req.LockedFee * defaultStorageBurnBPS / 10_000
	retrievalAmount := req.LockedFee * defaultStorageRetrievalBPS / 10_000
	foundationAmount := req.LockedFee * defaultStorageFoundationBPS / 10_000
	minerPortion := req.LockedFee - burnAmount - retrievalAmount - foundationAmount
	intentID, err := randomID("intent")
	if err != nil {
		return wire.CreateIntentResponse{}, err
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, req.LockedFee); err != nil {
			return wire.CreateIntentResponse{}, err
		}
	}
	userAccount.Balance -= req.LockedFee
	userAccount.LockedStorage += minerPortion
	s.data.Accounts[userAccount.Address] = userAccount

	// Transfer retrieval and foundation shares to their addresses (or burn if unconfigured).
	actualRetrieval := uint64(0)
	actualFoundation := uint64(0)
	if retrievalAmount > 0 && s.data.RetrievalAddress != "" {
		retAcc := s.accountLocked(s.data.RetrievalAddress)
		retAcc.Balance += retrievalAmount
		s.data.Accounts[retAcc.Address] = retAcc
		actualRetrieval = retrievalAmount
		s.data.StorageFeePool.TotalToRetrieval = saturatingAdd(s.data.StorageFeePool.TotalToRetrieval, retrievalAmount)
	} else {
		burnAmount += retrievalAmount
	}
	if foundationAmount > 0 && s.data.FoundationAddress != "" {
		fndAcc := s.accountLocked(s.data.FoundationAddress)
		fndAcc.Balance += foundationAmount
		s.data.Accounts[fndAcc.Address] = fndAcc
		actualFoundation = foundationAmount
		s.data.StorageFeePool.TotalToFoundation = saturatingAdd(s.data.StorageFeePool.TotalToFoundation, foundationAmount)
	} else {
		burnAmount += foundationAmount
	}

	now := time.Now().Unix()
	s.data.Intents[intentID] = &Intent{
		IntentView: wire.IntentView{
			IntentID:         intentID,
			User:             req.User,
			FileName:         req.FileName,
			FileSize:         req.FileSize,
			SegmentSize:      req.SegmentSize,
			FileRoot:         req.FileRoot,
			SegmentRoots:     req.SegmentRoots,
			Segments:         req.Segments,
			RepairPools:      req.RepairPools,
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
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.createPermanentFundLocked(s.data.Intents[intentID], now)
	s.createDealEscrowLocked(s.data.Intents[intentID], now)
	if burnAmount > 0 {
		s.data.Intents[intentID].BurnedFee = burnAmount
		escrow := s.dealEscrowLocked(s.data.Intents[intentID])
		escrow.BurnedFee = burnAmount
		s.data.DealEscrows[intentID] = escrow
		s.data.StorageFeePool.TotalBurned = saturatingAdd(s.data.StorageFeePool.TotalBurned, burnAmount)
	}
	s.reserveStorageAssignmentsLocked(assignments)
	s.recordTxLocked("create_intent", req.User, createIntentTxPayload{
		IntentID:      intentID,
		Request:       req,
		CreatedAtUnix: now,
	})
	if err := s.saveLocked(); err != nil {
		return wire.CreateIntentResponse{}, err
	}
	return wire.CreateIntentResponse{IntentID: intentID, Status: wire.StatusUploading, RequiredFee: quote.RequiredFee, LockedFee: req.LockedFee, BurnedFee: burnAmount, RetrievalFee: actualRetrieval, FoundationFee: actualFoundation, Assignments: assignments}, nil
}

func (s *Store) BatchCommit(req wire.BatchCommitRequest) (wire.BatchCommitResponse, error) {
	if len(req.Receipts) == 0 {
		return wire.BatchCommitResponse{}, errors.New("at least one receipt is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.BatchCommitResponse{}, errors.New("intent not found")
	}
	if intent.User != req.User {
		return wire.BatchCommitResponse{}, errors.New("intent user mismatch")
	}
	if intent.Status == wire.StatusFinalized {
		return wire.BatchCommitResponse{}, errors.New("intent already finalized")
	}
	if time.Now().Unix() > intent.DeadlineUnix {
		intent.Status = wire.StatusExpired
		_ = s.saveLocked()
		return wire.BatchCommitResponse{}, errors.New("intent expired")
	}

	if err := s.validateBatchCommitCapacityLocked(intent, req.Receipts); err != nil {
		return wire.BatchCommitResponse{}, err
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, req.User, "batch_commit", 0, func(agentPub string) error {
			return wire.VerifyBatchCommitAgent(req, agentPub)
		}); err != nil {
			return wire.BatchCommitResponse{}, err
		}
	} else {
		if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
			return wire.VerifyBatchCommit(req)
		}); err != nil {
			return wire.BatchCommitResponse{}, err
		}
		s.consumeAccountNonceLocked(req.User)
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, 0); err != nil {
			return wire.BatchCommitResponse{}, err
		}
	}

	committedAt := time.Now().Unix()
	repairChallenges := make([]wire.StorageChallenge, 0)
	for _, receipt := range req.Receipts {
		s.accrueStorageRewardForMinerLocked(receipt.MinerAddress)
		miner := s.minerStatsLocked(receipt.MinerAddress)
		if intent.Receipts[receipt.SegmentID] == nil {
			intent.Receipts[receipt.SegmentID] = map[int]wire.MinerReceipt{}
		}
		oldReceipt, existingReceipt := intent.Receipts[receipt.SegmentID][receipt.ShardIndex]
		repairTask, hasRepairTask := s.pendingRepairTaskForShardLocked(intent.IntentID, receipt.SegmentID, receipt.ShardIndex, receipt.MinerAddress)
		intent.Receipts[receipt.SegmentID][receipt.ShardIndex] = receipt
		switch {
		case existingReceipt && oldReceipt.MinerAddress != receipt.MinerAddress:
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
				challenge, err := s.requireRepairProofLocked(repairTask, receipt, committedAt)
				if err != nil {
					return wire.BatchCommitResponse{}, err
				}
				repairChallenges = append(repairChallenges, challenge)
				miner = s.minerStatsLocked(receipt.MinerAddress)
			}
			miner.UsedBytes += uint64(receipt.ShardSize)
		case !existingReceipt:
			if assignment, ok := assignmentForShard(intent.Assignments, receipt.SegmentID, receipt.ShardIndex); ok {
				s.releaseStorageReservationLocked(assignment)
				miner = s.minerStatsLocked(receipt.MinerAddress)
			}
			if hasRepairTask {
				s.releaseStorageReservationLocked(repairTask.Assignment)
				intent.Assignments = setAssignmentForShard(intent.Assignments, repairTask.Assignment)
				challenge, err := s.requireRepairProofLocked(repairTask, receipt, committedAt)
				if err != nil {
					return wire.BatchCommitResponse{}, err
				}
				repairChallenges = append(repairChallenges, challenge)
				miner = s.minerStatsLocked(receipt.MinerAddress)
			}
			miner.UsedBytes += uint64(receipt.ShardSize)
		}
		s.data.Miners[receipt.MinerAddress] = miner
	}

	intent.CommittedSegments = committedSegments(intent)
	intent.UploadedSize = committedSize(intent)
	intent.Status = wire.StatusPartial
	intent.UpdatedAt = committedAt
	s.recordTxLocked("batch_commit", req.User, batchCommitTxPayload{
		Request:           req,
		CommittedSegments: intent.CommittedSegments,
		UploadedSize:      intent.UploadedSize,
		CommittedAtUnix:   committedAt,
		RepairChallenges:  repairChallenges,
	})

	if err := s.saveLocked(); err != nil {
		return wire.BatchCommitResponse{}, err
	}
	return wire.BatchCommitResponse{
		IntentID:          intent.IntentID,
		Status:            intent.Status,
		CommittedSegments: intent.CommittedSegments,
		UploadedSize:      intent.UploadedSize,
	}, nil
}

func (s *Store) Finalize(req wire.FinalizeRequest) (wire.FinalizeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.FinalizeResponse{}, errors.New("intent not found")
	}
	if intent.User != req.User {
		return wire.FinalizeResponse{}, errors.New("intent user mismatch")
	}
	if intent.Status == wire.StatusFinalized {
		return wire.FinalizeResponse{IntentID: intent.IntentID, DealID: intent.DealID, Status: intent.Status}, nil
	}
	if committedSegments(intent) != len(intent.SegmentRoots) {
		return wire.FinalizeResponse{}, errors.New("not all segments are committed")
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, req.User, "finalize", 0, func(agentPub string) error {
			return wire.VerifyFinalizeAgent(req, agentPub)
		}); err != nil {
			return wire.FinalizeResponse{}, err
		}
	} else {
		if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
			return wire.VerifyFinalize(req)
		}); err != nil {
			return wire.FinalizeResponse{}, err
		}
		s.consumeAccountNonceLocked(req.User)
	}

	dealID, err := randomID("deal")
	if err != nil {
		return wire.FinalizeResponse{}, err
	}
	if requestUsesAgent(req.AgentKeyID) {
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, 0); err != nil {
			return wire.FinalizeResponse{}, err
		}
	}
	intent.DealID = dealID
	intent.Status = wire.StatusFinalized
	now := time.Now().Unix()
	intent.StorageStatus = wire.StorageStatusActive
	intent.AccessStatus = defaultAccessStatus(intent.IntentView)
	intent.ModerationStatus = wire.ModerationStatusNone
	if intent.Policy.Duration > 0 {
		intent.ExpiresAtUnix = now + intent.Policy.Duration
	}
	intent.UpdatedAt = now
	s.activateDealEscrowLocked(intent, now)
	s.data.Deals[dealID] = intent.IntentID
	s.recordTxLocked("finalize_deal", req.User, finalizeDealTxPayload{
		Request:         req,
		IntentID:        intent.IntentID,
		DealID:          dealID,
		User:            req.User,
		ManifestRoot:    req.ManifestRoot,
		FinalizedAtUnix: now,
	})

	if err := s.saveLocked(); err != nil {
		return wire.FinalizeResponse{}, err
	}
	return wire.FinalizeResponse{IntentID: intent.IntentID, DealID: dealID, Status: intent.Status}, nil
}

func (s *Store) SettleIntent(req wire.SettleIntentRequest) (wire.SettleIntentResponse, error) {
	if req.IntentID == "" {
		return wire.SettleIntentResponse{}, errors.New("intent is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.SettleIntentResponse{}, errors.New("intent not found")
	}
	if req.User != "" && req.User != intent.User {
		return wire.SettleIntentResponse{}, errors.New("intent user mismatch")
	}
	if req.User == "" {
		req.User = intent.User
	}
	if err := s.verifyAccountRequestLocked(req.ChainID, req.User, req.Nonce, func() error {
		return wire.VerifySettleIntent(req)
	}); err != nil {
		return wire.SettleIntentResponse{}, err
	}
	resp, err := s.settleIntentLocked(intent, time.Now().Unix())
	if err != nil {
		return wire.SettleIntentResponse{}, err
	}
	s.consumeAccountNonceLocked(req.User)
	s.recordTxLocked("settle_intent", intent.User, settleIntentTxPayload{
		Request:       req,
		Response:      resp,
		SettledAtUnix: time.Now().Unix(),
	})
	if err := s.saveLocked(); err != nil {
		return wire.SettleIntentResponse{}, err
	}
	return resp, nil
}

func (s *Store) settleIntentLocked(intent *Intent, now int64) (wire.SettleIntentResponse, error) {
	switch intent.Status {
	case wire.StatusUploading, wire.StatusPartial:
		if now <= intent.DeadlineUnix {
			return wire.SettleIntentResponse{}, errors.New("intent deadline has not passed")
		}
		intent.Status = wire.StatusExpired
		intent.StorageStatus = wire.StorageStatusExpired
		intent.AccessStatus = wire.AccessStatusSuspended
	case wire.StatusFinalized:
		if intent.Policy.Duration <= 0 {
			return wire.SettleIntentResponse{}, errors.New("storage duration is permanent")
		}
		if intent.ExpiresAtUnix == 0 {
			intent.ExpiresAtUnix = intent.UpdatedAt + intent.Policy.Duration
		}
		if now < intent.ExpiresAtUnix {
			return wire.SettleIntentResponse{}, errors.New("storage duration has not ended")
		}
		if now < intent.ExpiresAtUnix+defaultGracePeriod {
			return wire.SettleIntentResponse{}, errors.New("grace period has not ended")
		}
		if intent.Policy.DeletionPolicy == wire.DeletionPolicyRetain {
			intent.AccessStatus = wire.AccessStatusSuspended
			intent.UpdatedAt = now
			return wire.SettleIntentResponse{
				IntentID: intent.IntentID,
				Status:   intent.Status,
				PaidFee:  intent.PaidFee,
			}, nil
		}
		intent.Status = wire.StatusExpired
		intent.StorageStatus = wire.StorageStatusExpired
		intent.AccessStatus = wire.AccessStatusSuspended
	case wire.StatusExpired:
	default:
		return wire.SettleIntentResponse{}, errors.New("intent cannot be settled")
	}
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
		s.recordStorageFeeRefundLocked(intent, refund)
		s.data.Accounts[user.Address] = user
	}
	intent.UpdatedAt = now
	return wire.SettleIntentResponse{
		IntentID:    intent.IntentID,
		Status:      intent.Status,
		RefundedFee: refund,
		PaidFee:     intent.PaidFee,
	}, nil
}

func (s *Store) SettleExpiredIntents() ([]wire.SettleIntentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	responses := []wire.SettleIntentResponse{}
	for _, intent := range s.data.Intents {
		if !intentSettlementDue(intent, now) {
			continue
		}
		resp, err := s.settleIntentLocked(intent, now)
		if err != nil {
			continue
		}
		s.recordTxLocked("settle_intent", intent.User, settleIntentTxPayload{
			Request:       wire.SettleIntentRequest{IntentID: intent.IntentID, User: intent.User},
			Response:      resp,
			SettledAtUnix: now,
		})
		responses = append(responses, resp)
	}
	if len(responses) > 0 {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return responses, nil
}

func intentSettlementDue(intent *Intent, now int64) bool {
	if intent == nil {
		return false
	}
	if remainingIntentEscrow(intent) == 0 {
		return false
	}
	switch intent.Status {
	case wire.StatusUploading, wire.StatusPartial:
		return intent.DeadlineUnix > 0 && now > intent.DeadlineUnix
	case wire.StatusFinalized:
		expiresAt := intent.ExpiresAtUnix
		if expiresAt == 0 {
			expiresAt = intent.UpdatedAt + intent.Policy.Duration
		}
		if intent.Policy.Duration <= 0 || now < expiresAt {
			return false
		}
		if intent.Policy.DeletionPolicy == wire.DeletionPolicyImmediate {
			return true
		}
		return now >= expiresAt+defaultGracePeriod
	default:
		return false
	}
}

func (s *Store) GetIntent(intentID string) (wire.IntentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.data.Intents[intentID]
	if !ok {
		return wire.IntentView{}, errors.New("intent not found")
	}
	normalizeIntentLifecycle(intent)
	return intent.IntentView, nil
}

func (s *Store) GenerateChallenges(req wire.GenerateChallengeRequest) (wire.GenerateChallengeResponse, error) {
	if req.Count <= 0 {
		req.Count = 1
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.GenerateChallengeResponse{}, errors.New("intent not found")
	}
	if !intentAllowsStorageProof(intent) {
		return wire.GenerateChallengeResponse{}, errors.New("intent is not active for storage proofs")
	}

	challenges, err := s.generateChallengesLocked(intent, "", req.Count, time.Now().Add(10*time.Minute).Unix(), 0)
	if err != nil {
		return wire.GenerateChallengeResponse{}, err
	}
	s.recordTxLocked("generate_challenges", intent.User, generateChallengesTxPayload{
		Request:    req,
		Challenges: challenges,
	})

	if err := s.saveLocked(); err != nil {
		return wire.GenerateChallengeResponse{}, err
	}
	return wire.GenerateChallengeResponse{Challenges: challenges}, nil
}

func (s *Store) ListChallenges(minerAddress string, pendingOnly bool) (wire.ListChallengesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	var challenges []wire.StorageChallenge
	for _, challenge := range s.data.Challenges {
		if minerAddress != "" && challenge.MinerAddress != minerAddress {
			continue
		}
		if pendingOnly {
			if _, ok := s.data.Proofs[challenge.ChallengeID]; ok {
				continue
			}
			if challenge.ExpiresAtUnix < now {
				continue
			}
			if challenge.EpochID != "" {
				epoch, ok := s.data.Epochs[challenge.EpochID]
				if ok && epoch.Status == "finalized" {
					continue
				}
			}
		}
		challenges = append(challenges, challenge)
	}
	return wire.ListChallengesResponse{Challenges: challenges}, nil
}

func (s *Store) RepairPlan(intentID string) (wire.RepairPlanResponse, error) {
	if intentID == "" {
		return wire.RepairPlanResponse{}, errors.New("intent is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.data.Intents[intentID]
	if !ok {
		return wire.RepairPlanResponse{}, errors.New("intent not found")
	}
	totalShards := intent.Erasure.DataShards + intent.Erasure.ParityShards
	tasks := make([]wire.RepairTask, 0)
	seen := map[string]bool{}
	for _, task := range s.data.RepairTasks {
		if task.IntentID != intentID || task.Status != repairStatusPending {
			continue
		}
		tasks = append(tasks, task)
		seen[repairShardKey(task.SegmentID, task.ShardIndex)] = true
	}
	for segmentID := range intent.SegmentRoots {
		receipts := intent.Receipts[segmentID]
		if len(receipts) >= totalShards {
			continue
		}
		missing := make([]int, 0)
		for shardIndex := 0; shardIndex < totalShards; shardIndex++ {
			if seen[repairShardKey(segmentID, shardIndex)] {
				continue
			}
			if _, ok := receipts[shardIndex]; !ok {
				missing = append(missing, shardIndex)
			}
		}
		if len(missing) == 0 {
			continue
		}
		tasks = append(tasks, wire.RepairTask{
			IntentID:            intentID,
			SegmentID:           segmentID,
			AvailableShards:     len(receipts),
			RequiredShards:      intent.Erasure.DataShards,
			TargetShards:        totalShards,
			MissingShardIndexes: missing,
		})
	}
	return wire.RepairPlanResponse{IntentID: intentID, Tasks: tasks}, nil
}

func (s *Store) PendingRepairTasks(minerAddress string) (wire.RepairPlanResponse, error) {
	if minerAddress == "" {
		return wire.RepairPlanResponse{}, errors.New("miner is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tasks := make([]wire.RepairTask, 0)
	for _, task := range s.data.RepairTasks {
		if task.Status != repairStatusPending {
			continue
		}
		if task.Assignment.MinerAddress != minerAddress {
			continue
		}
		tasks = append(tasks, task)
	}
	return wire.RepairPlanResponse{Tasks: tasks}, nil
}

func (s *Store) StartEpoch(req wire.StartEpochRequest) (wire.StartEpochResponse, error) {
	if req.ChallengesPerDeal <= 0 {
		req.ChallengesPerDeal = 1
	}
	if req.DurationSeconds <= 0 {
		req.DurationSeconds = 10 * 60
	}
	if req.RewardPerProof == 0 {
		req.RewardPerProof = reward.TokenUnit
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Auto-sign for scheduler path when operator identity is available.
	if req.OperatorAddress == "" && s.operatorIdentity != nil {
		operatorAddr := s.operatorIdentity.OperatorAddress
		if _, ok := s.data.GovernanceOperators[normalizeGovernanceOperator(operatorAddr)]; ok {
			req.OperatorAddress = operatorAddr
			req.ChainID = s.data.ChainID
			req.Nonce = s.data.OperatorNonces[normalizeGovernanceOperator(operatorAddr)]
			req.CreatedAtUnix = time.Now().Unix()
			if err := wire.SignStartEpochRequest(&req, s.operatorIdentity.OperatorPrivateKey); err != nil {
				return wire.StartEpochResponse{}, errors.New("failed to sign start epoch: " + err.Error())
			}
		}
	}

	epochID, err := randomID("epoch")
	if err != nil {
		return wire.StartEpochResponse{}, err
	}
	deadline := time.Now().Add(time.Duration(req.DurationSeconds) * time.Second).Unix()
	var challenges []wire.StorageChallenge
	for _, intent := range s.data.Intents {
		if !intentAllowsStorageProof(intent) {
			continue
		}
		if req.IntentID != "" && intent.IntentID != req.IntentID {
			continue
		}
		intentChallenges, err := s.generateChallengesLocked(intent, epochID, req.ChallengesPerDeal, deadline, req.RewardPerProof)
		if err != nil {
			return wire.StartEpochResponse{}, err
		}
		challenges = append(challenges, intentChallenges...)
	}
	if len(challenges) == 0 {
		return wire.StartEpochResponse{}, errors.New("no finalized deals available for epoch")
	}
	challengeIDs := make([]string, 0, len(challenges))
	for _, challenge := range challenges {
		challengeIDs = append(challengeIDs, challenge.ChallengeID)
	}
	s.data.EpochRound++
	epoch := wire.ProofEpoch{
		EpochID:             epochID,
		EpochRound:          s.data.EpochRound,
		IntentID:            req.IntentID,
		ChallengeIDs:        challengeIDs,
		StartedAtUnix:       time.Now().Unix(),
		DeadlineUnix:        deadline,
		Status:              "active",
		RewardPerProof:      req.RewardPerProof,
		SlashPerMissedProof: req.SlashPerMissedProof,
	}
	s.data.Epochs[epochID] = epoch
	s.recordTxLocked("start_epoch", "", startEpochTxPayload{
		Request:    req,
		Epoch:      epoch,
		Challenges: challenges,
	})
	if err := s.saveLocked(); err != nil {
		return wire.StartEpochResponse{}, err
	}
	return wire.StartEpochResponse{Epoch: epoch, Challenges: challenges}, nil
}

func (s *Store) FinalizeEpoch(req wire.FinalizeEpochRequest) (wire.FinalizeEpochResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	epoch, ok := s.data.Epochs[req.EpochID]
	if !ok {
		return wire.FinalizeEpochResponse{}, errors.New("epoch not found")
	}
	if epoch.Status == "finalized" {
		return wire.FinalizeEpochResponse{}, errors.New("epoch already finalized")
	}

	// Auto-sign for scheduler path when operator identity is available.
	if req.OperatorAddress == "" && s.operatorIdentity != nil {
		operatorAddr := s.operatorIdentity.OperatorAddress
		if _, ok := s.data.GovernanceOperators[normalizeGovernanceOperator(operatorAddr)]; ok {
			req.OperatorAddress = operatorAddr
			req.ChainID = s.data.ChainID
			req.Nonce = s.data.OperatorNonces[normalizeGovernanceOperator(operatorAddr)]
			req.CreatedAtUnix = time.Now().Unix()
			if err := wire.SignFinalizeEpochRequest(&req, s.operatorIdentity.OperatorPrivateKey); err != nil {
				return wire.FinalizeEpochResponse{}, errors.New("failed to sign finalize epoch: " + err.Error())
			}
		}
	}

	resp := s.finalizeEpochLocked(epoch, req)
	if err := s.saveLocked(); err != nil {
		return wire.FinalizeEpochResponse{}, err
	}
	return resp, nil
}

func (s *Store) finalizeEpochLocked(epoch wire.ProofEpoch, finalizeReq wire.FinalizeEpochRequest) wire.FinalizeEpochResponse {
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
		if stats.Status == wire.MinerStatusActive && stats.ConsecutiveFailures >= s.miningParamsLocked().MinerDegradeThreshold {
			stats.Status = wire.MinerStatusDegraded
		}
		if stats.Status == wire.MinerStatusDegraded && stats.ConsecutiveFailures >= s.miningParamsLocked().MinerDegradeThreshold*2 {
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
		s.data.Accounts[account.Address] = account

		actualSlash := fromBonus + fromStake
		stats.Stake = account.LockedStake
		stats.Slashed += actualSlash
		totalSlashed += actualSlash
		s.addSlashedToPermanentFundLocked(actualSlash)

		// Auto-exit: bonus and stake both depleted.
		if account.LockedBonus == 0 && account.LockedStake == 0 && actualSlash > 0 {
			stats.Status = wire.MinerStatusExiting
			stats.ExitedAtUnix = time.Now().Add(7 * 24 * time.Hour).Unix()
		}
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
	resp := wire.FinalizeEpochResponse{
		EpochID:              epoch.EpochID,
		Status:               epoch.Status,
		AcceptedProofs:       accepted,
		MissedProofs:         missed,
		RepairTasks:          repairTasks,
		StorageRewardsPaid:   epoch.StorageRewardsPaid,
		RetrievalRewardsPaid: retrievalTotal,
		RepairRewardsPaid:    repairTotal,
		StorageSlashed:       totalSlashed,
		RepairTasksCreated:   len(repairTasks),
	}
	s.recordTxLocked("finalize_epoch", "", finalizeEpochTxPayload{Request: finalizeReq, Response: resp, RepairTasks: repairTasks})
	s.rotateValidatorsLocked(epoch.EpochRound)
	return resp
}

func (s *Store) repairTaskForMissedChallengeLocked(challenge wire.StorageChallenge) (wire.RepairTask, bool) {
	if challenge.IntentID == "" || challenge.MinerAddress == "" {
		return wire.RepairTask{}, false
	}
	if _, exists := s.pendingRepairTaskForShardLocked(challenge.IntentID, challenge.SegmentID, challenge.ShardIndex, ""); exists {
		return wire.RepairTask{}, false
	}
	intent, ok := s.data.Intents[challenge.IntentID]
	if !ok || intent.Status == wire.StatusExpired {
		return wire.RepairTask{}, false
	}
	unavailable := map[string]bool{challenge.MinerAddress: true}

	// Cross-parity shard itself lost → rebuild from both segments.
	if challenge.SegmentID < 0 {
		poolID := -(challenge.SegmentID + 1)
		for _, pool := range intent.RepairPools {
			if pool.PoolID == poolID {
				task, err := s.buildCrossParityRebuildTaskLocked(intent, &pool, challenge.SegmentID, challenge.ShardIndex, challenge.MinerAddress, "missed_proof", unavailable)
				if err == nil {
					return task, true
				}
				break
			}
		}
		return wire.RepairTask{}, false
	}

	// Regular segment shard lost → try cross-parity repair first.
	if pool, posInPool := findPoolForSegment(intent.RepairPools, challenge.SegmentID); pool != nil {
		peerSegID := pool.SegmentIDs[1-posInPool]
		paritySegID := -(pool.PoolID + 1)
		if crossParityReceiptsAvailable(intent, peerSegID, paritySegID, challenge.ShardIndex, unavailable) {
			task, err := s.buildCrossParityRepairTaskLocked(intent, pool, challenge.SegmentID, peerSegID, paritySegID, challenge.ShardIndex, challenge.MinerAddress, "missed_proof")
			if err == nil {
				return task, true
			}
		}
	}

	// Fallback to RS repair.
	task, err := s.buildRepairTaskForShardLocked(intent, challenge.SegmentID, challenge.ShardIndex, challenge.MinerAddress, "missed_proof", unavailable)
	if err != nil {
		return wire.RepairTask{}, false
	}
	return task, true
}

func (s *Store) FinalizeExpiredEpochs() ([]wire.FinalizeEpochResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	var responses []wire.FinalizeEpochResponse
	for _, epoch := range s.data.Epochs {
		if epoch.Status == "finalized" || epoch.DeadlineUnix > now {
			continue
		}
		// Build and auto-sign request for scheduler path.
		req := wire.FinalizeEpochRequest{EpochID: epoch.EpochID}
		if s.operatorIdentity != nil {
			operatorAddr := s.operatorIdentity.OperatorAddress
			if _, ok := s.data.GovernanceOperators[normalizeGovernanceOperator(operatorAddr)]; ok {
				req.OperatorAddress = operatorAddr
				req.ChainID = s.data.ChainID
				req.Nonce = s.data.OperatorNonces[normalizeGovernanceOperator(operatorAddr)]
				req.CreatedAtUnix = now
				_ = wire.SignFinalizeEpochRequest(&req, s.operatorIdentity.OperatorPrivateKey)
			}
		}
		resp := s.finalizeEpochLocked(epoch, req)
		responses = append(responses, resp)
	}
	if len(responses) > 0 {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return responses, nil
}

func (s *Store) MinerStats(minerAddress string) (wire.MinerStats, error) {
	minerAddress = wire.NormalizeAddress(minerAddress)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireInactiveMinersLocked()
	s.expireMinerBonusesLocked()
	s.finalizeExitingMinersLocked()
	stats, ok := s.data.Miners[minerAddress]
	if !ok {
		return wire.MinerStats{MinerAddress: minerAddress}, nil
	}
	pending, vesting, claimable := s.miningRewardVestingSummaryLocked(minerAddress, time.Now().Unix())
	stats.PendingMiningRewards = pending
	stats.VestingMiningRewards = vesting
	stats.ClaimableMiningRewards = claimable
	stats = s.attachEstimatedStorageRewardsLocked(stats)
	// Attach locked bonus from account.
	account := s.accountLocked(minerAddress)
	stats.LockedBonus = account.LockedBonus
	return stats, nil
}

// MinerShards returns all shard hashes currently assigned to a miner
// across all committed intent receipts.
func (s *Store) MinerShards(minerAddress string) wire.MinerShardsResponse {
	minerAddress = wire.NormalizeAddress(minerAddress)
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]bool)
	for _, intent := range s.data.Intents {
		for _, segmentReceipts := range intent.Receipts {
			for _, receipt := range segmentReceipts {
				if receipt.MinerAddress == minerAddress && receipt.ShardHash != "" {
					seen[receipt.ShardHash] = true
				}
			}
		}
	}
	hashes := make([]string, 0, len(seen))
	for h := range seen {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	return wire.MinerShardsResponse{
		MinerAddress: minerAddress,
		ShardHashes:  hashes,
		ShardCount:   len(hashes),
	}
}

func (s *Store) Account(address string) (wire.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accountLocked(wire.NormalizeAddress(address)), nil
}

// CreditBalance credits tokens to an account. This is an internal method only
// used for testing and genesis initialization. It is NOT exposed via HTTP.
func (s *Store) CreditBalance(address string, amount uint64) error {
	address = wire.NormalizeAddress(address)
	if address == "" {
		return errors.New("address is required")
	}
	if amount == 0 {
		return errors.New("amount must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	account := s.accountLocked(address)
	account.Balance += amount
	s.data.Accounts[address] = account
	s.recordTxLocked("genesis_credit", address, map[string]any{
		"address": address,
		"amount":  amount,
	})
	if err := s.saveLocked(); err != nil {
		return err
	}
	return nil
}

func (s *Store) Transfer(req wire.TransferRequest) (wire.TransferResponse, error) {
	if req.From == "" || req.To == "" {
		return wire.TransferResponse{}, errors.New("from and to are required")
	}
	if req.Amount == 0 {
		return wire.TransferResponse{}, errors.New("amount must be positive")
	}
	if !wire.IsSignedTransfer(req) {
		return wire.TransferResponse{}, errors.New("unsigned transfers are not supported; signature and public key are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	txID := s.recordTxLocked("transfer", req.From, req)
	from, to, err := s.applyTransferLocked(req)
	if err != nil {
		s.removePendingTxLocked(txID)
		return wire.TransferResponse{}, err
	}
	if err := s.saveLocked(); err != nil {
		return wire.TransferResponse{}, err
	}
	return wire.TransferResponse{From: from, To: to}, nil
}

func (s *Store) RegisterMiner(req wire.RegisterMinerRequest) (wire.RegisterMinerResponse, error) {
	req.MinerAddress = wire.NormalizeAddress(req.MinerAddress)
	if req.MinerAddress == "" || req.PublicKey == "" || req.Endpoint == "" {
		return wire.RegisterMinerResponse{}, errors.New("miner address, public key, and endpoint are required")
	}
	if req.CapacityBytes == 0 {
		return wire.RegisterMinerResponse{}, errors.New("capacity must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	params := s.miningParamsLocked()
	if params.MinCapacityBytes > 0 && req.CapacityBytes < params.MinCapacityBytes {
		return wire.RegisterMinerResponse{}, errors.New("capacity below minimum")
	}

	// Verify chain_id, nonce, and signature — prevents replay of old registration signatures.
	if err := s.verifyAccountRequestLocked(req.ChainID, req.MinerAddress, req.Nonce, func() error {
		return wire.VerifyMinerRegistration(req)
	}); err != nil {
		return wire.RegisterMinerResponse{}, err
	}

	// Strict one-time registration: reject if this address has ever registered.
	if _, exists := s.data.Miners[req.MinerAddress]; exists {
		existing := s.data.Miners[req.MinerAddress]
		return wire.RegisterMinerResponse{}, fmt.Errorf("address %s is already registered as miner #%d; use adjust-capacity to change capacity", req.MinerAddress, existing.MinerID)
	}

	account := s.accountLocked(req.MinerAddress)
	// Lock user-supplied stake from balance.
	if req.Stake > 0 {
		if account.Balance < req.Stake {
			return wire.RegisterMinerResponse{}, errors.New("insufficient balance for miner stake")
		}
		account.Balance -= req.Stake
		account.LockedStake += req.Stake
	}
	// One-time registration bonus (with cap + pool accounting).
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
	// Stake requirement: LockedBonus + LockedStake must cover capacity-based stake.
	if params.StakePerTiB > 0 {
		requiredStake := RequiredStakeForCapacity(req.CapacityBytes, params.StakePerTiB)
		totalLocked := account.LockedBonus + account.LockedStake
		if totalLocked < requiredStake {
			return wire.RegisterMinerResponse{}, errors.New("insufficient stake for declared capacity")
		}
	}
	// Assign unique miner ID.
	s.data.NextMinerID++
	minerID := s.data.NextMinerID

	// Generate default NFT template on first miner registration.
	if s.data.MinerNFTTemplate == "" {
		template := GenerateDefaultNFTTemplate()
		s.data.MinerNFTTemplate = template
		s.data.MinerNFTContentType = "image/svg+xml"
		hash := sha256.Sum256([]byte(template))
		s.data.MinerNFTTemplateHash = hex.EncodeToString(hash[:])
	}

	now := time.Now().Unix()
	stats := wire.MinerStats{
		MinerAddress:           req.MinerAddress,
		MinerID:                minerID,
		PublicKey:              req.PublicKey,
		Endpoint:               req.Endpoint,
		CapacityBytes:          req.CapacityBytes,
		Stake:                  req.Stake,
		Status:                 wire.MinerStatusActive,
		RegisteredAtUnix:       now,
		AccessServiceRequired:  true,
		UploadServiceEnabled:   true,
		DownloadServiceEnabled: true,
		RetrievalObligMet:      true,
		StorageRewardIndex:     s.data.StorageRewardIndex,
	}
	s.consumeAccountNonceLocked(req.MinerAddress)
	s.data.Accounts[req.MinerAddress] = account
	s.data.Miners[req.MinerAddress] = stats
	s.recordTxLocked("register_miner", req.MinerAddress, req)
	if err := s.saveLocked(); err != nil {
		return wire.RegisterMinerResponse{}, err
	}
	return wire.RegisterMinerResponse{Miner: stats}, nil
}

func (s *Store) DeregisterMiner(req wire.DeregisterMinerRequest) error {
	req.MinerAddress = wire.NormalizeAddress(req.MinerAddress)

	s.mu.Lock()
	defer s.mu.Unlock()

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
		return errors.New("miner already exited")
	}
	exitedAt := time.Now().Add(7 * 24 * time.Hour).Unix()
	if stats.Status != wire.MinerStatusExiting {
		stats.Status = wire.MinerStatusExiting
		stats.ExitedAtUnix = exitedAt
	} else {
		exitedAt = stats.ExitedAtUnix
	}
	s.consumeAccountNonceLocked(req.MinerAddress)
	s.data.Miners[req.MinerAddress] = stats
	s.recordTxLocked("deregister_miner", req.MinerAddress, deregisterMinerTxPayload{
		Request:      req,
		ExitedAtUnix: exitedAt,
	})
	return s.saveLocked()
}

// maxNFTTemplateBytes is the maximum allowed size for an uploaded NFT template (512 KB).
const maxNFTTemplateBytes = 512 * 1024

func (s *Store) UploadNFTTemplate(req wire.UploadNFTTemplateRequest) error {
	req.MinerAddress = wire.NormalizeAddress(req.MinerAddress)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.verifyAccountRequestLocked(req.ChainID, req.MinerAddress, req.Nonce, func() error {
		return wire.VerifyUploadNFTTemplate(req)
	}); err != nil {
		return err
	}

	// Only miner #1 is allowed to upload the NFT template.
	var minerOneAddress string
	for addr, m := range s.data.Miners {
		if m.MinerID == 1 {
			minerOneAddress = addr
			break
		}
	}
	if minerOneAddress == "" || req.MinerAddress != minerOneAddress {
		return errors.New("only miner #1 can upload the NFT template")
	}

	// Validate content type.
	if req.ContentType != "image/svg+xml" && req.ContentType != "image/png" {
		return errors.New("content type must be image/svg+xml or image/png")
	}

	// Decode base64 content.
	raw, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		return fmt.Errorf("invalid base64 content: %w", err)
	}
	if len(raw) == 0 {
		return errors.New("empty content")
	}
	if len(raw) > maxNFTTemplateBytes {
		return fmt.Errorf("content too large: %d bytes (max %d)", len(raw), maxNFTTemplateBytes)
	}

	// SVG must contain placeholders.
	if req.ContentType == "image/svg+xml" {
		svgStr := string(raw)
		if !strings.Contains(svgStr, "{{MINER_ID}}") || !strings.Contains(svgStr, "{{MINER_ADDR}}") {
			return errors.New("SVG template must contain {{MINER_ID}} and {{MINER_ADDR}} placeholders")
		}
	}

	hash := sha256.Sum256(raw)
	s.data.MinerNFTTemplate = req.Content
	s.data.MinerNFTContentType = req.ContentType
	s.data.MinerNFTTemplateHash = hex.EncodeToString(hash[:])

	s.consumeAccountNonceLocked(req.MinerAddress)
	s.recordTxLocked("upload_nft_template", req.MinerAddress, req)
	return s.saveLocked()
}

// GetNFTTemplate returns the current NFT template and its content type.
func (s *Store) GetNFTTemplate() (template string, contentType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.MinerNFTTemplate, s.data.MinerNFTContentType
}

func (s *Store) AdjustCapacity(req wire.AdjustCapacityRequest) (wire.AdjustCapacityResponse, error) {
	req.MinerAddress = wire.NormalizeAddress(req.MinerAddress)
	if req.MinerAddress == "" {
		return wire.AdjustCapacityResponse{}, errors.New("miner address is required")
	}
	if req.NewCapacityBytes == 0 {
		return wire.AdjustCapacityResponse{}, errors.New("new capacity must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	params := s.miningParamsLocked()
	if params.MinCapacityBytes > 0 && req.NewCapacityBytes < params.MinCapacityBytes {
		return wire.AdjustCapacityResponse{}, errors.New("new capacity below minimum")
	}

	// Verify signature.
	if err := s.verifyAccountRequestLocked(req.ChainID, req.MinerAddress, req.Nonce, func() error {
		return wire.VerifyAdjustCapacity(req)
	}); err != nil {
		return wire.AdjustCapacityResponse{}, err
	}

	stats := s.minerStatsLocked(req.MinerAddress)
	if stats.MinerAddress == "" {
		return wire.AdjustCapacityResponse{}, errors.New("miner not registered")
	}
	if stats.Status != wire.MinerStatusActive && stats.Status != wire.MinerStatusDegraded {
		return wire.AdjustCapacityResponse{}, errors.New("capacity can only be adjusted while miner is active or degraded")
	}

	// Cooldown check.
	cooldown := params.CapacityAdjustCooldownSeconds
	if cooldown == 0 {
		cooldown = 7 * 24 * 60 * 60
	}
	now := time.Now().Unix()
	if stats.LastCapacityAdjustUnix > 0 && uint64(now-stats.LastCapacityAdjustUnix) < cooldown {
		remaining := cooldown - uint64(now-stats.LastCapacityAdjustUnix)
		return wire.AdjustCapacityResponse{}, fmt.Errorf("capacity adjustment on cooldown; try again in %d seconds", remaining)
	}

	oldCapacity := stats.CapacityBytes
	newCapacity := req.NewCapacityBytes
	account := s.accountLocked(req.MinerAddress)
	var refundUnbonding uint64

	if params.StakePerTiB > 0 {
		oldRequired := RequiredStakeForCapacity(oldCapacity, params.StakePerTiB)
		newRequired := RequiredStakeForCapacity(newCapacity, params.StakePerTiB)

		if newRequired > oldRequired {
			// Increasing capacity: require immediate additional stake from balance.
			totalLocked := account.LockedBonus + account.LockedStake
			if totalLocked < newRequired {
				shortfall := newRequired - totalLocked
				if account.Balance < shortfall {
					return wire.AdjustCapacityResponse{}, fmt.Errorf("insufficient balance for additional stake; need %d more", shortfall)
				}
				account.Balance -= shortfall
				account.LockedStake += shortfall
			}
		} else if newRequired < oldRequired {
			// Decreasing capacity: refund excess LockedStake via 7-day unbonding.
			// Only refund from LockedStake (user's own funds), never from LockedBonus.
			excess := oldRequired - newRequired
			refundFromLockedStake := excess
			if refundFromLockedStake > account.LockedStake {
				refundFromLockedStake = account.LockedStake
			}
			// But never refund more than what's actually "excess" beyond newRequired.
			afterRefund := account.LockedBonus + account.LockedStake - refundFromLockedStake
			if afterRefund < newRequired {
				refundFromLockedStake = account.LockedBonus + account.LockedStake - newRequired
				if refundFromLockedStake > account.LockedStake {
					refundFromLockedStake = account.LockedStake
				}
			}
			if refundFromLockedStake > 0 {
				account.LockedStake -= refundFromLockedStake
				account.UnbondingBalance += refundFromLockedStake
				refundUnbonding = refundFromLockedStake

				if s.data.UnbondingEntries == nil {
					s.data.UnbondingEntries = map[string]wire.UnbondingEntry{}
				}
				unbondingID := req.MinerAddress + ":capacity_adjust:" + strconv.FormatInt(now, 10)
				s.data.UnbondingEntries[unbondingID] = wire.UnbondingEntry{
					ID:            unbondingID,
					Delegator:     req.MinerAddress,
					Validator:     req.MinerAddress,
					Amount:        refundFromLockedStake,
					CreatedAtUnix: now,
					MaturesAtUnix: now + wire.UnbondingPeriodSeconds,
				}
			}
		}
	}

	stats.CapacityBytes = newCapacity
	stats.LastCapacityAdjustUnix = now
	s.consumeAccountNonceLocked(req.MinerAddress)
	s.data.Accounts[req.MinerAddress] = account
	s.data.Miners[req.MinerAddress] = stats
	s.recordTxLocked("adjust_capacity", req.MinerAddress, req)
	if err := s.saveLocked(); err != nil {
		return wire.AdjustCapacityResponse{}, err
	}
	return wire.AdjustCapacityResponse{Miner: stats, RefundUnbonding: refundUnbonding}, nil
}

func (s *Store) ClaimMiningRewards(req wire.ClaimMiningRewardsRequest) (wire.ClaimMiningRewardsResponse, error) {
	req.MinerAddress = wire.NormalizeAddress(req.MinerAddress)
	if req.MinerAddress == "" {
		return wire.ClaimMiningRewardsResponse{}, errors.New("miner address is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.verifyAccountRequestLocked(req.ChainID, req.MinerAddress, req.Nonce, func() error {
		return wire.VerifyClaimMiningRewards(req)
	}); err != nil {
		return wire.ClaimMiningRewardsResponse{}, err
	}
	txID := s.recordTxLocked("claim_mining_rewards", req.MinerAddress, req)
	claimed := s.applyClaimMiningRewardsLocked(req.MinerAddress, time.Now().Unix())
	s.consumeAccountNonceLocked(req.MinerAddress)
	if err := s.saveLocked(); err != nil {
		s.removePendingTxLocked(txID)
		return wire.ClaimMiningRewardsResponse{}, err
	}
	account := s.accountLocked(req.MinerAddress)
	pending, vesting, claimable := s.miningRewardVestingSummaryLocked(req.MinerAddress, time.Now().Unix())
	return wire.ClaimMiningRewardsResponse{
		MinerAddress:           req.MinerAddress,
		Claimed:                claimed,
		Balance:                account.Balance,
		PendingMiningRewards:   pending,
		VestingMiningRewards:   vesting,
		ClaimableMiningRewards: claimable,
	}, nil
}

func (s *Store) SetOperatorIdentity(identity *OperatorIdentity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operatorIdentity = identity
}

// ChainID returns the chain identifier. Safe for concurrent use.
func (s *Store) ChainID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.ChainID
}

// AccountNonce returns the current nonce for the given address. Safe for concurrent use.
func (s *Store) AccountNonce(address string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accountLocked(wire.NormalizeAddress(address)).Nonce
}

func (s *Store) RegisterValidator(req wire.RegisterValidatorRequest) (wire.RegisterValidatorResponse, error) {
	if req.OwnerAddress == "" || req.OperatorAddress == "" || req.OperatorPublicKey == "" {
		return wire.RegisterValidatorResponse{}, errors.New("owner address, operator address and operator public key are required")
	}
	req.OwnerAddress = wire.NormalizeAddress(req.OwnerAddress)
	req.OperatorAddress = wire.NormalizeAddress(req.OperatorAddress)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify chain_id, nonce, and both owner + operator signatures.
	if err := s.verifyAccountRequestLocked(req.ChainID, req.OwnerAddress, req.Nonce, func() error {
		return wire.VerifyValidatorRegistration(req)
	}); err != nil {
		return wire.RegisterValidatorResponse{}, err
	}

	// Check operator uniqueness: operator must not already be mapped.
	if s.data.OperatorMap == nil {
		s.data.OperatorMap = map[string]string{}
	}
	if existingOwner, mapped := s.data.OperatorMap[req.OperatorAddress]; mapped {
		return wire.RegisterValidatorResponse{}, errors.New("operator address already registered for owner " + existingOwner)
	}
	// Check owner uniqueness and state guard against replay.
	existing := s.validatorLocked(req.OwnerAddress)
	if existing.OwnerAddress != "" {
		switch existing.Status {
		case wire.ValidatorStatusActive:
			return wire.RegisterValidatorResponse{}, errors.New("owner already has an active validator")
		case wire.ValidatorStatusExiting:
			return wire.RegisterValidatorResponse{}, errors.New("validator is currently exiting; wait for exit to complete")
		case wire.ValidatorStatusExited:
			return wire.RegisterValidatorResponse{}, errors.New("validator has exited; registration replay is not allowed")
		case wire.ValidatorStatusSlashed:
			return wire.RegisterValidatorResponse{}, errors.New("validator has been slashed; must go through governance recovery")
		case wire.ValidatorStatusJailed:
			return wire.RegisterValidatorResponse{}, errors.New("validator is jailed; must go through unjailing process")
		}
	}

	account := s.accountLocked(req.OwnerAddress)
	if req.Stake < MinValidatorStake {
		return wire.RegisterValidatorResponse{}, errors.New("validator stake below minimum required")
	}
	if req.Stake > existing.Stake {
		additionalStake := req.Stake - existing.Stake
		if account.Balance < additionalStake {
			return wire.RegisterValidatorResponse{}, errors.New("insufficient balance for validator stake")
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
	existing.CommissionRateBPS = req.CommissionRateBPS
	existing.Status = wire.ValidatorStatusActive
	if existing.RegisteredAtUnix == 0 {
		existing.RegisteredAtUnix = time.Now().Unix()
	}
	s.consumeAccountNonceLocked(req.OwnerAddress)
	s.data.Accounts[account.Address] = account
	s.data.Validators[req.OwnerAddress] = existing
	s.data.ConsensusValidators[req.OwnerAddress] = true
	s.data.OperatorMap[req.OperatorAddress] = req.OwnerAddress
	s.recordTxLocked("register_validator", req.OwnerAddress, req)
	if err := s.saveLocked(); err != nil {
		return wire.RegisterValidatorResponse{}, err
	}
	return wire.RegisterValidatorResponse{Validator: existing}, nil
}

func (s *Store) DeregisterValidator(req wire.DeregisterValidatorRequest) error {
	req.ValidatorAddress = wire.NormalizeAddress(req.ValidatorAddress)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.verifyAccountRequestLocked(req.ChainID, req.ValidatorAddress, req.Nonce, func() error {
		return wire.VerifyDeregisterValidator(req)
	}); err != nil {
		return err
	}

	validator, ok := s.data.Validators[req.ValidatorAddress]
	if !ok {
		return errors.New("validator not found")
	}
	if validator.Status == wire.ValidatorStatusExited {
		return errors.New("validator already exited")
	}
	if validator.Status != wire.ValidatorStatusExiting {
		validator.Status = wire.ValidatorStatusExiting
	}
	s.consumeAccountNonceLocked(req.ValidatorAddress)
	delete(s.data.ConsensusValidators, req.ValidatorAddress)
	// Remove operator mapping.
	if validator.OperatorAddress != "" {
		delete(s.data.OperatorMap, validator.OperatorAddress)
	}
	s.data.Validators[req.ValidatorAddress] = validator
	s.recordTxLocked("deregister_validator", req.ValidatorAddress, map[string]any{
		"validator_address": req.ValidatorAddress,
		"status":            validator.Status,
	})
	return s.saveLocked()
}

func (s *Store) finalizeExitingValidatorsLocked() {
	now := time.Now().Unix()
	for address, validator := range s.data.Validators {
		switch validator.Status {
		case wire.ValidatorStatusSlashed:
			validator.Status = wire.ValidatorStatusExiting
			s.data.Validators[address] = validator
		case wire.ValidatorStatusExiting:
			validator.Status = wire.ValidatorStatusExited
			s.data.Validators[address] = validator

			// Return operator's self-stake via 7-day unbonding.
			if validator.SelfStake > 0 {
				account := s.accountLocked(address)
				release := validator.SelfStake
				if release > account.LockedStake {
					release = account.LockedStake
				}
				if release > 0 {
					account.LockedStake -= release
					account.UnbondingBalance += release
					s.data.Accounts[address] = account

					if s.data.UnbondingEntries == nil {
						s.data.UnbondingEntries = map[string]wire.UnbondingEntry{}
					}
					unbondingID := address + ":self:" + strconv.FormatInt(now, 10)
					s.data.UnbondingEntries[unbondingID] = wire.UnbondingEntry{
						ID:            unbondingID,
						Delegator:     address,
						Validator:     address,
						Amount:        release,
						CreatedAtUnix: now,
						MaturesAtUnix: now + wire.UnbondingPeriodSeconds,
					}
				}
				validator.SelfStake = 0
				s.data.Validators[address] = validator
			}
		}
	}
}

// finalizeExitingMinersLocked transitions miners from exiting to exited once
// their ExitedAtUnix deadline has passed. Called alongside
// finalizeExitingValidatorsLocked in the epoch scheduler and on miner queries.
func (s *Store) finalizeExitingMinersLocked() {
	now := time.Now().Unix()
	for address, stats := range s.data.Miners {
		if stats.Status == wire.MinerStatusExiting && stats.ExitedAtUnix > 0 && now >= stats.ExitedAtUnix {
			stats.Status = wire.MinerStatusExited
			s.data.Miners[address] = stats

			// Return miner stake via 7-day unbonding.
			if stats.Stake > 0 {
				account := s.accountLocked(address)
				release := stats.Stake
				if release > account.LockedStake {
					release = account.LockedStake
				}
				if release > 0 {
					account.LockedStake -= release
					account.UnbondingBalance += release
					s.data.Accounts[address] = account

					if s.data.UnbondingEntries == nil {
						s.data.UnbondingEntries = map[string]wire.UnbondingEntry{}
					}
					unbondingID := address + ":miner:" + strconv.FormatInt(now, 10)
					s.data.UnbondingEntries[unbondingID] = wire.UnbondingEntry{
						ID:            unbondingID,
						Delegator:     address,
						Validator:     address,
						Amount:        release,
						CreatedAtUnix: now,
						MaturesAtUnix: now + wire.UnbondingPeriodSeconds,
					}
				}
				stats.Stake = 0
				s.data.Miners[address] = stats
			}

			// Return remaining LockedBonus to Storage Pool (was reserved at grant time)
			// and release the bonus slot for new registrations.
			account := s.accountLocked(address)
			if account.LockedBonus > 0 {
				s.initRewardPoolsLocked()
				s.data.RewardPools.StorageRemaining = saturatingAdd(s.data.RewardPools.StorageRemaining, account.LockedBonus)
				account.LockedBonus = 0
				if s.data.BonusGrantedCount > 0 {
					s.data.BonusGrantedCount--
				}
				s.data.Accounts[address] = account
			}
		}
	}
}

// expireInactiveMinersLocked cancels registration bonuses and initiates exit
// for miners who failed to submit any valid storage proof within the activation
// window. Called from the scheduler, Status, and MinerStats alongside the
// other cleanup functions.
func (s *Store) expireInactiveMinersLocked() {
	params := s.miningParamsLocked()
	if params.ActivationWindowSeconds == 0 {
		return
	}
	now := time.Now().Unix()
	for address, stats := range s.data.Miners {
		// Only target active or degraded miners.
		if stats.Status != wire.MinerStatusActive && stats.Status != wire.MinerStatusDegraded {
			continue
		}
		// Miner has submitted at least one proof — activated.
		if stats.ProofSuccess > 0 {
			continue
		}
		// Already processed.
		if stats.BonusExpired || stats.BonusReleased {
			continue
		}
		if stats.RegisteredAtUnix <= 0 {
			continue
		}
		// Activation window not yet passed.
		if (now - stats.RegisteredAtUnix) <= int64(params.ActivationWindowSeconds) {
			continue
		}
		// Cancel bonus and return to pool.
		account := s.accountLocked(address)
		if account.LockedBonus > 0 {
			s.initRewardPoolsLocked()
			s.data.RewardPools.StorageRemaining = saturatingAdd(
				s.data.RewardPools.StorageRemaining, account.LockedBonus)
			account.LockedBonus = 0
			if s.data.BonusGrantedCount > 0 {
				s.data.BonusGrantedCount--
			}
		}
		stats.BonusExpired = true
		stats.Status = wire.MinerStatusExiting
		stats.ExitedAtUnix = now + wire.UnbondingPeriodSeconds
		s.data.Accounts[address] = account
		s.data.Miners[address] = stats
	}
}

// expireMinerBonusesLocked batch-expires registration bonuses for miners whose
// 90-day deadline has passed. This catches miners who stopped submitting proofs
// before meeting the release conditions. Called from the scheduler, Status, and
// MinerStats alongside finalizeExitingMinersLocked.
func (s *Store) expireMinerBonusesLocked() {
	params := s.miningParamsLocked()
	if params.BonusDeadlineSeconds == 0 {
		return
	}
	now := time.Now().Unix()
	for address, stats := range s.data.Miners {
		if stats.BonusReleased || stats.BonusExpired {
			continue
		}
		if stats.RegisteredAtUnix <= 0 {
			continue
		}
		account := s.accountLocked(address)
		if account.LockedBonus == 0 {
			continue
		}
		if (now - stats.RegisteredAtUnix) > int64(params.BonusDeadlineSeconds) {
			stats.BonusExpired = true
			s.initRewardPoolsLocked()
			s.data.RewardPools.StorageRemaining = saturatingAdd(s.data.RewardPools.StorageRemaining, account.LockedBonus)
			account.LockedBonus = 0
			if s.data.BonusGrantedCount > 0 {
				s.data.BonusGrantedCount--
			}
			s.data.Accounts[address] = account
			s.data.Miners[address] = stats
		}
	}
}

func (s *Store) Validators() wire.ListValidatorsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.finalizeExitingValidatorsLocked()
	validators := make([]wire.ValidatorInfo, 0, len(s.data.Validators))
	for _, validator := range s.data.Validators {
		validator.Consensus = s.data.ConsensusValidators[validator.OwnerAddress]
		validator.AvailabilityScoreBPS = s.availabilityScoreLocked(validator.OwnerAddress)
		validators = append(validators, validator)
	}
	return wire.ListValidatorsResponse{Validators: validators}
}

func (s *Store) SubmitProof(req wire.SubmitProofRequest) (wire.SubmitProofResponse, error) {
	if err := wire.VerifyProof(req.Proof); err != nil {
		return wire.SubmitProofResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	challenge, ok := s.data.Challenges[req.Proof.ChallengeID]
	if !ok {
		return wire.SubmitProofResponse{}, errors.New("challenge not found")
	}
	if time.Now().Unix() > challenge.ExpiresAtUnix {
		return wire.SubmitProofResponse{}, errors.New("challenge expired")
	}
	if challenge.EpochID != "" {
		epoch, ok := s.data.Epochs[challenge.EpochID]
		if ok && epoch.Status == "finalized" {
			return wire.SubmitProofResponse{}, errors.New("epoch already finalized")
		}
	}
	if req.Proof.MinerAddress != challenge.MinerAddress {
		return wire.SubmitProofResponse{}, errors.New("proof miner mismatch")
	}
	if req.Proof.MinerPublicKey != challenge.MinerPublicKey {
		return wire.SubmitProofResponse{}, errors.New("proof public key mismatch")
	}
	if _, err := s.registeredMinerLocked(req.Proof.MinerAddress, req.Proof.MinerPublicKey); err != nil {
		return wire.SubmitProofResponse{}, err
	}
	if err := validateStorageProof(challenge, req.Proof); err != nil {
		return wire.SubmitProofResponse{}, err
	}

	existingProof, alreadyRewarded := s.data.Proofs[req.Proof.ChallengeID]
	if alreadyRewarded {
		// Do not overwrite existing proof data — preserve audit trail.
		_ = existingProof
	} else {
		s.data.Proofs[req.Proof.ChallengeID] = req.Proof
	}
	stats := s.minerStatsLocked(req.Proof.MinerAddress)
	reward := uint64(0)
	settledStoragePoolReward := uint64(0)
	bonusReleased := false
	bonusExpired := false
	if !alreadyRewarded {
		settledStoragePoolReward = s.settleStorageRewardForMinerLocked(req.Proof.MinerAddress, time.Now().Unix())
		stats = s.minerStatsLocked(req.Proof.MinerAddress)
		reward = s.payableStorageRewardLocked(challenge)
		stats.ProofSuccess++
		stats.ConsecutiveFailures = 0
		s.clearPendingShardRepairsForMinerLocked(req.Proof.MinerAddress)
		stats.StorageRewards = saturatingAdd(stats.StorageRewards, reward)
		stats.Rewards = saturatingAdd(stats.Rewards, reward)
		if stats.Status == wire.MinerStatusDegraded {
			stats.Status = wire.MinerStatusActive
		}
		s.data.Miners[req.Proof.MinerAddress] = stats

		// Check bonus expiry and release.
		account := s.accountLocked(req.Proof.MinerAddress)
		if !stats.BonusReleased && !stats.BonusExpired && account.LockedBonus > 0 {
			params := s.miningParamsLocked()

			// ① Check deadline first — expire if past 90 days.
			if params.BonusDeadlineSeconds > 0 && stats.RegisteredAtUnix > 0 &&
				(time.Now().Unix()-stats.RegisteredAtUnix) > int64(params.BonusDeadlineSeconds) {
				stats.BonusExpired = true
				bonusExpired = true
				s.initRewardPoolsLocked()
				s.data.RewardPools.StorageRemaining = saturatingAdd(s.data.RewardPools.StorageRemaining, account.LockedBonus)
				account.LockedBonus = 0
				if s.data.BonusGrantedCount > 0 {
					s.data.BonusGrantedCount--
				}
				s.data.Accounts[req.Proof.MinerAddress] = account
				s.data.Miners[req.Proof.MinerAddress] = stats
			} else if params.MinBonusProofCount > 0 {
				// ② Check release conditions.
				total := stats.ProofSuccess + stats.ProofFailure
				if stats.ProofSuccess >= params.MinBonusProofCount &&
					total > 0 &&
					stats.ProofSuccess*10000/total >= params.MinBonusSuccessRateBPS &&
					stats.RetrievalObligMet &&
					(params.MinBonusRetrievalCount == 0 || stats.RetrievalSuccess >= params.MinBonusRetrievalCount) {
					stats.BonusReleased = true
					bonusReleased = true
					account.Balance += account.LockedBonus
					// No pool deduction — already reserved at grant time.
					account.LockedBonus = 0
					s.data.Accounts[req.Proof.MinerAddress] = account
					s.data.Miners[req.Proof.MinerAddress] = stats
				}
			}
		}

		s.payStorageRewardLocked(challenge, req.Proof.MinerAddress, reward)
		if challenge.EpochID != "" {
			if epoch, ok := s.data.Epochs[challenge.EpochID]; ok {
				epoch.StorageRewardsPaid += reward
				s.data.Epochs[challenge.EpochID] = epoch
			}
		}
	}
	if challenge.RepairID != "" {
		s.completeRepairTaskAfterProofLocked(challenge.RepairID, challenge.ChallengeID)
	}
	s.recordTxLocked("submit_proof", req.Proof.MinerAddress, submitProofTxPayload{
		Request:                  req,
		Reward:                   reward,
		SettledStoragePoolReward: settledStoragePoolReward,
		AlreadyRewarded:          alreadyRewarded,
		BonusReleased:            bonusReleased,
		BonusExpired:             bonusExpired,
		SubmittedAtUnix:          time.Now().Unix(),
	})
	if err := s.saveLocked(); err != nil {
		return wire.SubmitProofResponse{}, err
	}
	return wire.SubmitProofResponse{
		ChallengeID:              req.Proof.ChallengeID,
		MinerAddress:             req.Proof.MinerAddress,
		Status:                   "accepted",
		Reward:                   reward,
		SettledStoragePoolReward: settledStoragePoolReward,
	}, nil
}

func validateReceipt(intent *Intent, receipt wire.MinerReceipt) error {
	if err := wire.VerifyReceipt(receipt); err != nil {
		return err
	}
	if receipt.IntentID != intent.IntentID {
		return errors.New("receipt intent mismatch")
	}
	if receipt.User != intent.User {
		return errors.New("receipt user mismatch")
	}
	if receipt.FileRoot != intent.FileRoot {
		return errors.New("receipt file root mismatch")
	}
	if receipt.SegmentID < 0 {
		// Cross-parity receipt: validate against RepairPool.
		poolID := -(receipt.SegmentID + 1)
		var pool *wire.RepairPool
		for i := range intent.RepairPools {
			if intent.RepairPools[i].PoolID == poolID {
				pool = &intent.RepairPools[i]
				break
			}
		}
		if pool == nil {
			return errors.New("receipt segment out of range")
		}
		if receipt.ShardIndex < 0 || receipt.ShardIndex >= len(pool.CrossParity.ShardHashes) {
			return errors.New("receipt shard index out of range")
		}
		if receipt.ShardHash != pool.CrossParity.ShardHashes[receipt.ShardIndex] {
			return errors.New("receipt shard hash does not match cross-parity plan")
		}
	} else {
		if receipt.SegmentID >= len(intent.SegmentRoots) {
			return errors.New("receipt segment out of range")
		}
		if receipt.SegmentRoot != intent.SegmentRoots[receipt.SegmentID] {
			return errors.New("receipt segment root mismatch")
		}
		if receipt.ShardIndex < 0 || receipt.ShardIndex >= len(intent.Segments[receipt.SegmentID].ShardHashes) {
			return errors.New("receipt shard index out of range")
		}
		if receipt.ShardHash != intent.Segments[receipt.SegmentID].ShardHashes[receipt.ShardIndex] {
			return errors.New("receipt shard hash does not match segment plan")
		}
	}
	if time.Now().Unix() > receipt.ExpiresAtUnix {
		return errors.New("receipt expired")
	}
	return nil
}

func (s *Store) validateBatchCommitCapacityLocked(intent *Intent, receipts []wire.MinerReceipt) error {
	projectedUsed := map[string]uint64{}
	for _, receipt := range receipts {
		if err := validateReceipt(intent, receipt); err != nil {
			return err
		}
		if err := s.validateReceiptAssignmentLocked(intent, receipt); err != nil {
			return err
		}
		miner, err := s.registeredMinerLocked(receipt.MinerAddress, receipt.MinerPublicKey)
		if err != nil {
			return err
		}
		used, ok := projectedUsed[receipt.MinerAddress]
		if !ok {
			used = miner.UsedBytes
		}
		if oldReceipt, exists := intent.Receipts[receipt.SegmentID][receipt.ShardIndex]; exists {
			if oldReceipt.MinerAddress == receipt.MinerAddress {
				continue
			}
			oldUsed, ok := projectedUsed[oldReceipt.MinerAddress]
			if !ok {
				oldUsed = s.minerStatsLocked(oldReceipt.MinerAddress).UsedBytes
			}
			oldSize := uint64(oldReceipt.ShardSize)
			if oldUsed < oldSize {
				projectedUsed[oldReceipt.MinerAddress] = 0
			} else {
				projectedUsed[oldReceipt.MinerAddress] = oldUsed - oldSize
			}
		}
		size := uint64(receipt.ShardSize)
		if used > miner.CapacityBytes || size > miner.CapacityBytes-used {
			return errors.New("miner capacity exceeded")
		}
		projectedUsed[receipt.MinerAddress] = used + size
	}
	return nil
}

func committedSize(intent *Intent) int64 {
	var size int64
	last := len(intent.SegmentRoots) - 1
	for segmentID, receipts := range intent.Receipts {
		if len(receipts) < intent.Erasure.DataShards {
			continue
		}
		if segmentID == last {
			remaining := intent.FileSize - int64(last)*intent.SegmentSize
			if remaining > 0 {
				size += remaining
				continue
			}
		}
		size += intent.SegmentSize
	}
	return size
}

func committedSegments(intent *Intent) int {
	count := 0
	for _, receipts := range intent.Receipts {
		if len(receipts) >= intent.Erasure.DataShards {
			count++
		}
	}
	return count
}

func flattenReceipts(intent *Intent) []wire.MinerReceipt {
	var receipts []wire.MinerReceipt
	for _, byShard := range intent.Receipts {
		for _, receipt := range byShard {
			receipts = append(receipts, receipt)
		}
	}
	return receipts
}

func (s *Store) generateChallengesLocked(intent *Intent, epochID string, count int, deadline int64, reward uint64) ([]wire.StorageChallenge, error) {
	receipts := flattenReceipts(intent)
	if len(receipts) == 0 {
		return nil, errors.New("intent has no receipts")
	}
	if count > len(receipts) {
		count = len(receipts)
	}

	challenges := make([]wire.StorageChallenge, 0, count)
	start := int(time.Now().UnixNano() % int64(len(receipts)))
	for i := 0; i < count; i++ {
		receipt := receipts[(start+i)%len(receipts)]
		challengeID, err := randomID("challenge")
		if err != nil {
			return nil, err
		}
		nonce, err := randomID("nonce")
		if err != nil {
			return nil, err
		}
		challenge := s.storageChallengeForReceiptLocked(intent, receipt, epochID, "", challengeID, nonce, deadline, reward)
		s.data.Challenges[challengeID] = challenge
		challenges = append(challenges, challenge)
	}
	return challenges, nil
}

func (s *Store) storageChallengeForReceiptLocked(intent *Intent, receipt wire.MinerReceipt, epochID string, repairID string, challengeID string, nonce string, deadline int64, reward uint64) wire.StorageChallenge {
	sampleCount := storageProofSampleCount(receipt.ShardSize, chaincrypto.DefaultLeafSize, s.miningParamsLocked().StorageProofSamples)
	minerSeal := computeMinerSeal(receipt.SectorCommitment, receipt.MinerAddress)
	leafIndices := challengeLeafIndices(nonce, receipt.ShardHash, minerSeal, receipt.ShardSize, chaincrypto.DefaultLeafSize, sampleCount)
	leafRanges := challengeLeafRanges(receipt.ShardSize, chaincrypto.DefaultLeafSize, leafIndices)
	intentID := ""
	dealID := ""
	if intent != nil {
		intentID = intent.IntentID
		dealID = intent.DealID
	}
	challengeSeed := hashString(intentID + ":" + receipt.ShardHash + ":" + nonce)
	challenge := wire.StorageChallenge{
		ChallengeID:      challengeID,
		EpochID:          epochID,
		RepairID:         repairID,
		ProofType:        proofTypeMerklePORV1,
		IntentID:         intentID,
		DealID:           dealID,
		SegmentID:        receipt.SegmentID,
		SegmentRoot:      receipt.SegmentRoot,
		ShardIndex:       receipt.ShardIndex,
		ShardHash:        receipt.ShardHash,
		ShardSize:        receipt.ShardSize,
		SectorCommitment: receipt.SectorCommitment,
		MinerSeal:        minerSeal,
		LeafSize:         chaincrypto.DefaultLeafSize,
		LeafIndex:        leafIndices[0],
		LeafIndices:      leafIndices,
		LeafRanges:       leafRanges,
		Difficulty:       uint64(len(leafIndices)),
		PayloadBytes:     challengePayloadBytes(leafRanges),
		MinerAddress:     receipt.MinerAddress,
		MinerPublicKey:   receipt.MinerPublicKey,
		MinerEndpoint:    receipt.MinerEndpoint,
		Nonce:            nonce,
		ChallengeSeed:    challengeSeed,
		ExpiresAtUnix:    deadline,
		Reward:           reward,
	}
	challenge.SampleCount = len(leafIndices)
	challenge.ChallengeHash = storageChallengeHash(challenge)
	return challenge
}

func (s *Store) minerStatsLocked(minerAddress string) wire.MinerStats {
	stats, ok := s.data.Miners[minerAddress]
	if !ok {
		return wire.MinerStats{MinerAddress: minerAddress}
	}
	return stats
}

func (s *Store) validatorLocked(address string) wire.ValidatorInfo {
	address = wire.NormalizeAddress(address)
	validator, ok := s.data.Validators[address]
	if !ok {
		return wire.ValidatorInfo{OwnerAddress: address}
	}
	return validator
}

// resolveOperatorToOwner resolves an operator address to its owner address.
// If the address is not found in OperatorMap, it is returned as-is (assumed to be owner).
func (s *Store) resolveOperatorToOwner(operatorAddress string) string {
	operatorAddress = wire.NormalizeAddress(operatorAddress)
	if owner, ok := s.data.OperatorMap[operatorAddress]; ok {
		return owner
	}
	return operatorAddress
}

func (s *Store) accountLocked(address string) wire.Account {
	address = wire.NormalizeAddress(address)
	account, ok := s.data.Accounts[address]
	if !ok {
		return wire.Account{Address: address}
	}
	if account.Address == "" {
		account.Address = address
	}
	return account
}

func (s *Store) registeredMinerLocked(minerAddress string, publicKey string) (wire.MinerStats, error) {
	miner, ok := s.data.Miners[minerAddress]
	if !ok || (miner.Status != wire.MinerStatusActive && miner.Status != wire.MinerStatusDegraded) {
		return wire.MinerStats{}, errors.New("miner is not registered")
	}
	if miner.PublicKey != publicKey {
		return wire.MinerStats{}, errors.New("miner public key mismatch")
	}
	if miner.Stake == 0 {
		return wire.MinerStats{}, errors.New("miner has no effective stake")
	}
	return miner, nil
}

func expectedProofHash(challenge wire.StorageChallenge, proof wire.StorageProof) string {
	base := challenge.ChallengeID + ":" + challenge.Nonce + ":" + challenge.ChallengeHash + ":" + strings.Join(proofLeafHashes(proof), ",") + ":" + strings.Join(proofLeafPayloads(proof), ",") + ":" + challenge.SectorCommitment
	if challenge.MinerSeal != "" {
		base += ":" + challenge.MinerSeal
	}
	return hashString(base)
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func challengeLeafIndex(nonce string, shardSize int64, leafSize int) int {
	count := chaincrypto.LeafCount(shardSize, leafSize)
	if count <= 1 {
		return 0
	}
	sum := sha256.Sum256([]byte(nonce))
	var n uint64
	for _, b := range sum[:8] {
		n = (n << 8) | uint64(b)
	}
	return int(n % uint64(count))
}

func challengeLeafIndices(nonce string, shardHash string, minerSeal string, shardSize int64, leafSize int, samples int) []int {
	leafCount := chaincrypto.LeafCount(shardSize, leafSize)
	if leafCount <= 1 {
		return []int{0}
	}
	if samples <= 0 || samples > leafCount {
		samples = leafCount
	}
	indices := make([]int, 0, samples)
	seen := map[int]bool{}
	for round := 0; len(indices) < samples; round++ {
		seed := nonce + ":" + shardHash + ":" + minerSeal + ":" + strconv.Itoa(round)
		sum := sha256.Sum256([]byte(seed))
		var n uint64
		for _, b := range sum[:8] {
			n = (n << 8) | uint64(b)
		}
		index := int(n % uint64(leafCount))
		if seen[index] {
			continue
		}
		seen[index] = true
		indices = append(indices, index)
	}
	return indices
}

func storageProofSampleCount(shardSize int64, leafSize int, baseSamples int) int {
	leafCount := chaincrypto.LeafCount(shardSize, leafSize)
	if leafCount <= 1 {
		return 1
	}
	samples := baseSamples
	for n := leafCount; n > 1; n >>= 1 {
		samples += 2
	}
	if samples > 64 {
		samples = 64
	}
	if samples > leafCount {
		samples = leafCount
	}
	return samples
}

func challengeLeafRanges(shardSize int64, leafSize int, indices []int) []wire.LeafRange {
	ranges := make([]wire.LeafRange, 0, len(indices))
	for _, index := range indices {
		ranges = append(ranges, challengeLeafRange(shardSize, leafSize, index))
	}
	return ranges
}

func challengePayloadBytes(ranges []wire.LeafRange) int64 {
	var total int64
	for _, leafRange := range ranges {
		total += int64(leafRange.Length)
	}
	return total
}

func challengeLeafRange(shardSize int64, leafSize int, index int) wire.LeafRange {
	if leafSize <= 0 {
		leafSize = chaincrypto.DefaultLeafSize
	}
	offset := int64(index) * int64(leafSize)
	length := leafSize
	if offset >= shardSize {
		return wire.LeafRange{LeafIndex: index, Offset: offset, Length: 0}
	}
	if remaining := shardSize - offset; remaining < int64(length) {
		length = int(remaining)
	}
	return wire.LeafRange{LeafIndex: index, Offset: offset, Length: length}
}

func storageChallengeHash(challenge wire.StorageChallenge) string {
	indices := challengeLeafIndicesForValidation(challenge)
	base := proofTypeMerklePORV1 + ":" + challenge.ChallengeID + ":" + challenge.EpochID + ":" + challenge.RepairID + ":" + challenge.IntentID + ":" + challenge.DealID + ":" + challenge.ShardHash + ":" + challenge.SectorCommitment + ":" + challenge.Nonce + ":" + challenge.ChallengeSeed + ":" + strings.Join(intsToStrings(indices), ",")
	if challenge.MinerSeal != "" {
		base += ":" + challenge.MinerSeal
	}
	return hashString(base)
}

func intsToStrings(values []int) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strconv.Itoa(value))
	}
	return out
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if s.db != nil {
		return s.db.Put(levelDBStateKey, raw, nil)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	// Atomic write: write to temp file then rename to prevent corruption on crash.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func isLevelDBPath(path string) bool {
	return strings.HasPrefix(path, "leveldb://") ||
		strings.HasSuffix(path, ".ldb") ||
		strings.HasSuffix(path, ".leveldb")
}

func randomID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}
