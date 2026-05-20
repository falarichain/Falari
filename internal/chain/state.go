package chain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

const defaultStorageProofSamples = 8
const defaultBaseFee uint64 = 1
const defaultTargetBlockTxs = 10
const defaultStorageBasePricePerGiBMonth uint64 = 1
const defaultStorageMinimumFee uint64 = 1
const defaultPermanentStorageDuration = int64(10 * 365 * 24 * 60 * 60)
const defaultRetrievalRewardPerMiB uint64 = 1
const defaultRetrievalRateWindow = int64(3600)
const defaultRetrievalRateDecayDivisor uint64 = 2
const defaultMinerDegradeThreshold uint64 = 3
const defaultRepairRewardPerShard uint64 = 1
const defaultRetrievalRewardHoldDuration = int64(3600)
const defaultMaxRetrievalRewardPerWindow uint64 = 100
const defaultRetrievalSpeedSampleWindow = int64(60)
const defaultRetrievalAbuseSpeedMultiplier uint64 = 10
const defaultMinerStoredBytesWeightBPS uint64 = 4000
const defaultMinerProofScoreWeightBPS uint64 = 3500
const defaultMinerAvailabilityWeightBPS uint64 = 1500
const defaultMinerDecentralizationWeightBPS uint64 = 1000

type Intent struct {
	wire.IntentView
	DeadlineUnix int64                             `json:"deadline_unix"`
	Receipts     map[int]map[int]wire.MinerReceipt `json:"receipts"`
	CreatedAt    int64                             `json:"created_at"`
	UpdatedAt    int64                             `json:"updated_at"`
}

type State struct {
	Intents                 map[string]*Intent                     `json:"intents"`
	Deals                   map[string]string                      `json:"deals"`
	Challenges              map[string]wire.StorageChallenge       `json:"challenges"`
	Proofs                  map[string]wire.StorageProof           `json:"proofs"`
	Epochs                  map[string]wire.ProofEpoch             `json:"epochs"`
	Miners                  map[string]wire.MinerStats             `json:"miners"`
	Accounts                map[string]wire.Account                `json:"accounts"`
	Blocks                  []wire.Block                           `json:"blocks"`
	PendingTxs              []wire.Transaction                     `json:"pending_txs"`
	Receipts                map[string]wire.TransactionReceipt     `json:"receipts"`
	Validators              map[string]wire.ValidatorInfo          `json:"validators"`
	ConsensusValidators     map[string]bool                        `json:"consensus_validators"`
	ValidatorEvidence       map[string]wire.ValidatorEvidence      `json:"validator_evidence"`
	ConsensusVotes          map[string]wire.ConsensusVote          `json:"consensus_votes"`
	FeeMarket               wire.FeeMarket                         `json:"fee_market"`
	FeeChargedTxs           map[string]bool                        `json:"fee_charged_txs"`
	StoragePricing          wire.StoragePricing                    `json:"storage_pricing"`
	RepairTasks             map[string]wire.RepairTask             `json:"repair_tasks"`
	ProviderRecords         map[string]wire.StorageProviderRecord  `json:"provider_records"`
	DeleteTasks             map[string]wire.DeleteTask             `json:"delete_tasks"`
	GovernanceAudits        []wire.GovernanceAuditRecord           `json:"governance_audits"`
	DeleteReceipts          map[string]wire.DeleteReceipt          `json:"delete_receipts"`
	RetrievalReceipts       map[string]wire.RetrievalReceipt       `json:"retrieval_receipts"`
	RetrievalWindows        map[string]wire.RetrievalRateWindow    `json:"retrieval_windows"`
	PendingRetrievalRewards map[string]wire.PendingRetrievalReward `json:"pending_retrieval_rewards"`
	StakeDelegations        map[string]wire.StakeDelegation        `json:"stake_delegations"`
	DealHealths             map[string]wire.DealHealth             `json:"deal_healths"`
	RewardPools             *reward.Pools                          `json:"reward_pools"`
	Collections             map[string]wire.DataCollection         `json:"collections"`
	DataRecords             map[string]wire.DataRecord             `json:"data_records"`
	CollectionRecords       map[string][]string                    `json:"collection_records"`
	AppliedTxs              map[string]bool                        `json:"applied_txs"`
	ConfirmedTxs            map[string]bool                        `json:"confirmed_txs"`
	EpochRound              uint64                                 `json:"epoch_round"`
	ConsensusHeight         uint64                                 `json:"consensus_height"`
	ConsensusRound          uint64                                 `json:"consensus_round"`
	ConsensusPhase          string                                 `json:"consensus_phase"`
	ConsensusProposer       string                                 `json:"consensus_proposer"`
	UpgradePlan             consensus.UpgradePlan                  `json:"upgrade_plan"`
	AgentKeys               map[string]*wire.AgentKey              `json:"agent_keys"`
}

type Store struct {
	mu              sync.Mutex
	path            string
	db              *leveldb.DB
	data            State
	blockProducer   *ValidatorIdentity
	broadcaster     BlockBroadcaster
	txBroadcaster   TransactionBroadcaster
	voteBroadcaster ConsensusVoteBroadcaster
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

	raw, rawErr := readStateFile(path)
	hasGenesis := genesisPath != ""

	if !hasExistingStateFile(path) && hasGenesis {
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

func readStateFile(path string) ([]byte, error) {
	if isLevelDBPath(path) {
		db, err := leveldb.OpenFile(strings.TrimPrefix(path, "leveldb://"), nil)
		if err != nil {
			return nil, err
		}
		defer db.Close()
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

func hasExistingStateFile(path string) bool {
	if path == "" {
		return false
	}
	if isLevelDBPath(path) {
		db, err := leveldb.OpenFile(strings.TrimPrefix(path, "leveldb://"), nil)
		if err != nil {
			return false
		}
		defer db.Close()
		_, err = db.Get(levelDBStateKey, nil)
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
	return newStateFromGenesis(doc), nil
}

func newStateFromGenesis(doc wire.GenesisDoc) State {
	state := newState()
	for _, acc := range doc.Accounts {
		address := wire.NormalizeAddress(acc.Address)
		state.Accounts[address] = wire.Account{
			Address:      address,
			Balance:      acc.Balance,
			LockedStake:  0,
			LockedStorage: 0,
		}
	}
	for _, v := range doc.Validators {
		addr := wire.NormalizeAddress(v.Address)
		state.Validators[addr] = wire.ValidatorInfo{
			Address:          addr,
			PublicKey:        v.PublicKey,
			Endpoint:         v.Endpoint,
			Stake:            v.Stake,
			SelfStake:        v.Stake,
			Status:           "active",
			Consensus:        true,
			RegisteredAtUnix: doc.GenesisTime,
		}
		state.ConsensusValidators[addr] = true
	}
	if doc.RewardPools != nil {
		state.RewardPools = &reward.Pools{
			StorageRemaining:   doc.RewardPools.StoragePoolRemaining,
			RetrievalRemaining: doc.RewardPools.RetrievalPoolRemaining,
			ValidatorRemaining: doc.RewardPools.ValidatorPoolRemaining,
			RepairRemaining:    doc.RewardPools.RepairPoolRemaining,
		}
	}
	return state
}

func newState() State {
	return State{
		Intents:             map[string]*Intent{},
		Deals:               map[string]string{},
		Challenges:          map[string]wire.StorageChallenge{},
		Proofs:              map[string]wire.StorageProof{},
		Epochs:              map[string]wire.ProofEpoch{},
		Miners:              map[string]wire.MinerStats{},
		Accounts:            map[string]wire.Account{},
		Blocks:              []wire.Block{},
		PendingTxs:          []wire.Transaction{},
		Receipts:            map[string]wire.TransactionReceipt{},
		Validators:          map[string]wire.ValidatorInfo{},
		ConsensusValidators: map[string]bool{},
		ValidatorEvidence:   map[string]wire.ValidatorEvidence{},
		ConsensusVotes:      map[string]wire.ConsensusVote{},
		FeeMarket:           defaultFeeMarket(),
		FeeChargedTxs:       map[string]bool{},
		StoragePricing:      defaultStoragePricing(),
		RepairTasks:         map[string]wire.RepairTask{},
		ProviderRecords:     map[string]wire.StorageProviderRecord{},
		DeleteTasks:         map[string]wire.DeleteTask{},
		GovernanceAudits:    []wire.GovernanceAuditRecord{},
		DeleteReceipts:      map[string]wire.DeleteReceipt{},
		RetrievalReceipts:   map[string]wire.RetrievalReceipt{},
		Collections:         map[string]wire.DataCollection{},
		DataRecords:         map[string]wire.DataRecord{},
		CollectionRecords:   map[string][]string{},
		AppliedTxs:          map[string]bool{},
		ConfirmedTxs:        map[string]bool{},
		AgentKeys:           map[string]*wire.AgentKey{},
	}
}

func normalizeState(state *State) {
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
	if state.FeeChargedTxs == nil {
		state.FeeChargedTxs = map[string]bool{}
	}
	if state.StoragePricing.BasePricePerGiBMonth == 0 {
		state.StoragePricing = defaultStoragePricing()
	}
	if state.StoragePricing.MinimumFee == 0 {
		state.StoragePricing.MinimumFee = defaultStorageMinimumFee
	}
	if state.StoragePricing.PermanentDuration == 0 {
		state.StoragePricing.PermanentDuration = defaultPermanentStorageDuration
	}
	if state.RepairTasks == nil {
		state.RepairTasks = map[string]wire.RepairTask{}
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
	if state.DeleteReceipts == nil {
		state.DeleteReceipts = map[string]wire.DeleteReceipt{}
	}
	if state.RetrievalReceipts == nil {
		state.RetrievalReceipts = map[string]wire.RetrievalReceipt{}
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
	if state.AppliedTxs == nil {
		state.AppliedTxs = map[string]bool{}
	}
	if state.ConfirmedTxs == nil {
		state.ConfirmedTxs = map[string]bool{}
	}
	if state.AgentKeys == nil {
		state.AgentKeys = map[string]*wire.AgentKey{}
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
	for _, intent := range state.Intents {
		if intent.Receipts == nil {
			intent.Receipts = map[int]map[int]wire.MinerReceipt{}
		}
		normalizeIntentLifecycle(intent)
	}
}

func defaultFeeMarket() wire.FeeMarket {
	return wire.FeeMarket{
		BaseFee:        defaultBaseFee,
		TargetBlockTxs: defaultTargetBlockTxs,
	}
}

func defaultStoragePricing() wire.StoragePricing {
	return wire.StoragePricing{
		BasePricePerGiBMonth: defaultStorageBasePricePerGiBMonth,
		MinimumFee:           defaultStorageMinimumFee,
		PermanentDuration:    defaultPermanentStorageDuration,
	}
}

func (s *Store) CreateIntent(req wire.CreateIntentRequest) (wire.CreateIntentResponse, error) {
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
	userAccount.Balance -= req.LockedFee
	userAccount.LockedStorage += req.LockedFee
	s.data.Accounts[userAccount.Address] = userAccount

	intentID, err := randomID("intent")
	if err != nil {
		return wire.CreateIntentResponse{}, err
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
		CreatedAt:    now,
		UpdatedAt:    now,
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
	return wire.CreateIntentResponse{IntentID: intentID, Status: wire.StatusUploading, RequiredFee: quote.RequiredFee, LockedFee: req.LockedFee, Assignments: assignments}, nil
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

	for _, receipt := range req.Receipts {
		if err := validateReceipt(intent, receipt); err != nil {
			return wire.BatchCommitResponse{}, err
		}
		if err := s.validateReceiptAssignmentLocked(intent, receipt); err != nil {
			return wire.BatchCommitResponse{}, err
		}
		miner, err := s.registeredMinerLocked(receipt.MinerAddress, receipt.MinerPublicKey)
		if err != nil {
			return wire.BatchCommitResponse{}, err
		}
		if miner.UsedBytes+uint64(receipt.ShardSize) > miner.CapacityBytes {
			return wire.BatchCommitResponse{}, errors.New("miner capacity exceeded")
		}
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
				s.completeRepairTaskLocked(repairTask)
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
				s.completeRepairTaskLocked(repairTask)
				miner = s.minerStatsLocked(receipt.MinerAddress)
			}
			miner.UsedBytes += uint64(receipt.ShardSize)
		}
		s.data.Miners[receipt.MinerAddress] = miner
	}

	intent.CommittedSegments = committedSegments(intent)
	intent.UploadedSize = committedSize(intent)
	intent.Status = wire.StatusPartial
	intent.UpdatedAt = time.Now().Unix()
	s.recordTxLocked("batch_commit", req.User, batchCommitTxPayload{
		Request:           req,
		CommittedSegments: intent.CommittedSegments,
		UploadedSize:      intent.UploadedSize,
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

	dealID, err := randomID("deal")
	if err != nil {
		return wire.FinalizeResponse{}, err
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
	s.data.Deals[dealID] = intent.IntentID
	s.recordTxLocked("finalize_deal", req.User, map[string]any{
		"intent_id":     intent.IntentID,
		"deal_id":       dealID,
		"user":          req.User,
		"manifest_root": req.ManifestRoot,
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
	resp, err := s.settleIntentLocked(intent, time.Now().Unix())
	if err != nil {
		return wire.SettleIntentResponse{}, err
	}
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
		req.RewardPerProof = 1
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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
	resp := s.finalizeEpochLocked(epoch)
	if err := s.saveLocked(); err != nil {
		return wire.FinalizeEpochResponse{}, err
	}
	return resp, nil
}

func (s *Store) finalizeEpochLocked(epoch wire.ProofEpoch) wire.FinalizeEpochResponse {
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
	s.recordTxLocked("finalize_epoch", "", finalizeEpochTxPayload{Response: resp, RepairTasks: repairTasks})
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
		resp := s.finalizeEpochLocked(epoch)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	stats, ok := s.data.Miners[minerAddress]
	if !ok {
		return wire.MinerStats{MinerAddress: minerAddress}, nil
	}
	return stats, nil
}

func (s *Store) Account(address string) (wire.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accountLocked(wire.NormalizeAddress(address)), nil
}

func (s *Store) Faucet(req wire.FaucetRequest) (wire.FaucetResponse, error) {
	req.Address = wire.NormalizeAddress(req.Address)
	if req.Address == "" {
		return wire.FaucetResponse{}, errors.New("address is required")
	}
	if req.Amount == 0 {
		return wire.FaucetResponse{}, errors.New("amount must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	account := s.accountLocked(req.Address)
	account.Balance += req.Amount
	s.data.Accounts[req.Address] = account
	s.recordTxLocked("faucet", req.Address, map[string]any{
		"address": req.Address,
		"amount":  req.Amount,
	})
	if err := s.saveLocked(); err != nil {
		return wire.FaucetResponse{}, err
	}
	return wire.FaucetResponse{Account: account}, nil
}

func (s *Store) Transfer(req wire.TransferRequest) (wire.TransferResponse, error) {
	if req.From == "" || req.To == "" {
		return wire.TransferResponse{}, errors.New("from and to are required")
	}
	if req.Amount == 0 {
		return wire.TransferResponse{}, errors.New("amount must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	from, to, err := s.applyTransferLocked(req)
	if err != nil {
		return wire.TransferResponse{}, err
	}
	s.recordTxLocked("transfer", req.From, req)
	if err := s.saveLocked(); err != nil {
		return wire.TransferResponse{}, err
	}
	return wire.TransferResponse{From: from, To: to}, nil
}

func (s *Store) SubmitRawTransaction(req wire.RawTransactionRequest) (wire.TransferResponse, error) {
	return wire.TransferResponse{}, errors.New("raw EVM transactions are no longer supported; use POST /transfers for native transfers")
}

func (s *Store) RegisterMiner(req wire.RegisterMinerRequest) (wire.RegisterMinerResponse, error) {
	if err := wire.VerifyMinerRegistration(req); err != nil {
		return wire.RegisterMinerResponse{}, err
	}
	if req.MinerAddress == "" || req.PublicKey == "" || req.Endpoint == "" {
		return wire.RegisterMinerResponse{}, errors.New("miner address, public key, and endpoint are required")
	}
	if req.CapacityBytes == 0 {
		return wire.RegisterMinerResponse{}, errors.New("capacity must be positive")
	}
	if req.Stake == 0 {
		return wire.RegisterMinerResponse{}, errors.New("stake must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.minerStatsLocked(req.MinerAddress)
	account := s.accountLocked(req.MinerAddress)
	if req.Stake > existing.Stake {
		additionalStake := req.Stake - existing.Stake
		if account.Balance < additionalStake {
			return wire.RegisterMinerResponse{}, errors.New("insufficient balance for miner stake")
		}
		account.Balance -= additionalStake
		account.LockedStake += additionalStake
	}
	existing.MinerAddress = req.MinerAddress
	existing.PublicKey = req.PublicKey
	existing.Endpoint = req.Endpoint
	existing.CapacityBytes = req.CapacityBytes
	existing.Stake = req.Stake
	existing.Status = wire.MinerStatusActive
	if existing.RegisteredAtUnix == 0 {
		existing.RegisteredAtUnix = time.Now().Unix()
	}
	s.data.Accounts[req.MinerAddress] = account
	s.data.Miners[req.MinerAddress] = existing
	s.recordTxLocked("register_miner", req.MinerAddress, map[string]any{
		"miner_address":  req.MinerAddress,
		"public_key":     req.PublicKey,
		"endpoint":       req.Endpoint,
		"capacity_bytes": req.CapacityBytes,
		"stake":          req.Stake,
		"signature":      req.Signature,
	})
	if err := s.saveLocked(); err != nil {
		return wire.RegisterMinerResponse{}, err
	}
	return wire.RegisterMinerResponse{Miner: existing}, nil
}

func (s *Store) DeregisterMiner(minerAddress string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats := s.minerStatsLocked(minerAddress)
	if stats.Status == wire.MinerStatusExited {
		return errors.New("miner already exited")
	}
	if stats.Status != wire.MinerStatusExiting {
		stats.Status = wire.MinerStatusExiting
		stats.ExitedAtUnix = time.Now().Add(7 * 24 * time.Hour).Unix()
	}
	s.data.Miners[minerAddress] = stats
	s.recordTxLocked("deregister_miner", minerAddress, map[string]any{
		"miner_address": minerAddress,
		"exited_at":     stats.ExitedAtUnix,
	})
	return s.saveLocked()
}

func (s *Store) SetBlockProducer(identity *ValidatorIdentity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockProducer = identity
}

func (s *Store) RegisterValidator(req wire.RegisterValidatorRequest) (wire.RegisterValidatorResponse, error) {
	if err := wire.VerifyValidatorRegistration(req); err != nil {
		return wire.RegisterValidatorResponse{}, err
	}
	if req.Address == "" || req.PublicKey == "" {
		return wire.RegisterValidatorResponse{}, errors.New("validator address and public key are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.validatorLocked(req.Address)
	account := s.accountLocked(req.Address)
	if req.Stake > existing.Stake {
		additionalStake := req.Stake - existing.Stake
		if account.Balance < additionalStake {
			return wire.RegisterValidatorResponse{}, errors.New("insufficient balance for validator stake")
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
	s.recordTxLocked("register_validator", req.Address, map[string]any{
		"address":    req.Address,
		"public_key": req.PublicKey,
		"endpoint":   req.Endpoint,
		"stake":      req.Stake,
		"signature":  req.Signature,
	})
	if err := s.saveLocked(); err != nil {
		return wire.RegisterValidatorResponse{}, err
	}
	return wire.RegisterValidatorResponse{Validator: existing}, nil
}

func (s *Store) DeregisterValidator(validatorAddress string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	validator, ok := s.data.Validators[validatorAddress]
	if !ok {
		return errors.New("validator not found")
	}
	if validator.Status == wire.ValidatorStatusExited {
		return errors.New("validator already exited")
	}
	if validator.Status != wire.ValidatorStatusExiting {
		validator.Status = wire.ValidatorStatusExiting
	}
	delete(s.data.ConsensusValidators, validatorAddress)
	s.data.Validators[validatorAddress] = validator
	s.recordTxLocked("deregister_validator", validatorAddress, map[string]any{
		"validator_address": validatorAddress,
		"status":            validator.Status,
	})
	return s.saveLocked()
}

func (s *Store) finalizeExitingValidatorsLocked() {
	for address, validator := range s.data.Validators {
		switch validator.Status {
		case wire.ValidatorStatusSlashed:
			validator.Status = wire.ValidatorStatusExiting
			s.data.Validators[address] = validator
		case wire.ValidatorStatusExiting:
			validator.Status = wire.ValidatorStatusExited
			s.data.Validators[address] = validator
		}
	}
}

func (s *Store) Validators() wire.ListValidatorsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.finalizeExitingValidatorsLocked()
	validators := make([]wire.ValidatorInfo, 0, len(s.data.Validators))
	for _, validator := range s.data.Validators {
		validator.Consensus = s.data.ConsensusValidators[validator.Address]
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

	_, alreadyRewarded := s.data.Proofs[req.Proof.ChallengeID]
	s.data.Proofs[req.Proof.ChallengeID] = req.Proof
	stats := s.minerStatsLocked(req.Proof.MinerAddress)
	reward := uint64(0)
	if !alreadyRewarded {
		reward = s.payableStorageRewardLocked(challenge)
		stats.ProofSuccess++
		stats.ConsecutiveFailures = 0
		stats.StorageRewards = saturatingAdd(stats.StorageRewards, reward)
		stats.Rewards += reward
		if stats.Status == wire.MinerStatusDegraded {
			stats.Status = wire.MinerStatusActive
		}
		s.data.Miners[req.Proof.MinerAddress] = stats
		s.payStorageRewardLocked(challenge, req.Proof.MinerAddress, reward)
		if challenge.EpochID != "" {
			if epoch, ok := s.data.Epochs[challenge.EpochID]; ok {
				epoch.StorageRewardsPaid += reward
				s.data.Epochs[challenge.EpochID] = epoch
			}
		}
	}
	s.recordTxLocked("submit_proof", req.Proof.MinerAddress, submitProofTxPayload{
		Request:         req,
		Reward:          reward,
		AlreadyRewarded: alreadyRewarded,
		SubmittedAtUnix: time.Now().Unix(),
	})
	if err := s.saveLocked(); err != nil {
		return wire.SubmitProofResponse{}, err
	}
	return wire.SubmitProofResponse{
		ChallengeID:  req.Proof.ChallengeID,
		MinerAddress: req.Proof.MinerAddress,
		Status:       "accepted",
		Reward:       reward,
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
	if receipt.SegmentID < 0 || receipt.SegmentID >= len(intent.SegmentRoots) {
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
	if time.Now().Unix() > receipt.ExpiresAtUnix {
		return errors.New("receipt expired")
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
		sampleCount := storageProofSampleCount(receipt.ShardSize, chaincrypto.DefaultLeafSize)
		leafIndices := challengeLeafIndices(nonce, receipt.ShardHash, receipt.ShardSize, chaincrypto.DefaultLeafSize, sampleCount)
		leafRanges := challengeLeafRanges(receipt.ShardSize, chaincrypto.DefaultLeafSize, leafIndices)
		challengeSeed := hashString(intent.IntentID + ":" + receipt.ShardHash + ":" + nonce)
		challenge := wire.StorageChallenge{
			ChallengeID:      challengeID,
			EpochID:          epochID,
			ProofType:        proofTypeMerklePORV1,
			IntentID:         intent.IntentID,
			DealID:           intent.DealID,
			SegmentID:        receipt.SegmentID,
			SegmentRoot:      receipt.SegmentRoot,
			ShardIndex:       receipt.ShardIndex,
			ShardHash:        receipt.ShardHash,
			ShardSize:        receipt.ShardSize,
			SectorCommitment: receipt.SectorCommitment,
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
		s.data.Challenges[challengeID] = challenge
		challenges = append(challenges, challenge)
	}
	return challenges, nil
}

func (s *Store) minerStatsLocked(minerAddress string) wire.MinerStats {
	stats, ok := s.data.Miners[minerAddress]
	if !ok {
		return wire.MinerStats{MinerAddress: minerAddress}
	}
	return stats
}

func (s *Store) validatorLocked(address string) wire.ValidatorInfo {
	validator, ok := s.data.Validators[address]
	if !ok {
		return wire.ValidatorInfo{Address: address}
	}
	return validator
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
	if !ok || (miner.Status != wire.MinerStatusActive && miner.Status != wire.MinerStatusDegraded && miner.Status != wire.MinerStatusJailed) {
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
	return hashString(challenge.ChallengeID + ":" + challenge.Nonce + ":" + challenge.ChallengeHash + ":" + strings.Join(proofLeafHashes(proof), ",") + ":" + strings.Join(proofLeafPayloads(proof), ",") + ":" + challenge.SectorCommitment)
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

func challengeLeafIndices(nonce string, shardHash string, shardSize int64, leafSize int, samples int) []int {
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
		sum := sha256.Sum256([]byte(nonce + ":" + shardHash + ":" + strconv.Itoa(round)))
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

func storageProofSampleCount(shardSize int64, leafSize int) int {
	leafCount := chaincrypto.LeafCount(shardSize, leafSize)
	if leafCount <= 1 {
		return 1
	}
	samples := defaultStorageProofSamples
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
	return hashString(proofTypeMerklePORV1 + ":" + challenge.ChallengeID + ":" + challenge.IntentID + ":" + challenge.DealID + ":" + challenge.ShardHash + ":" + challenge.SectorCommitment + ":" + challenge.Nonce + ":" + challenge.ChallengeSeed + ":" + strings.Join(intsToStrings(indices), ","))
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
	return os.WriteFile(s.path, raw, 0o644)
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
