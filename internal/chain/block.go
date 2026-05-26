package chain

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"chain/internal/consensus"
	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

const (
	defaultTargetBlockBytes  = 256 * 1024
	defaultMaxBlockBytes     = 1 * 1024 * 1024
	defaultMaxBlockTxs       = 200
	defaultMaxTxBytes        = 16 * 1024
	defaultMaxStorageTxBytes = 128 * 1024
	defaultBlockSizeHeadroom = 8 * 1024
	maxFutureBlockTimeSkew   = 30 * time.Second
)

func (s *Store) recordTxLocked(txType, from string, payload any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte("{}")
	}
	payloadHash := chaincrypto.HashBytes(raw)
	txID := chaincrypto.HashBytes([]byte(txType + ":" + payloadHash))
	tx := wire.Transaction{
		TxID:          txID,
		Type:          txType,
		From:          wire.NormalizeAddress(from),
		PayloadHash:   payloadHash,
		Payload:       append([]byte(nil), raw...),
		CreatedAtUnix: time.Now().Unix(),
	}
	enrichTransactionMetadata(&tx)
	accepted, err := s.enqueuePendingTxLocked(tx)
	if err != nil || !accepted {
		return txID
	}
	s.data.AppliedTxs[tx.TxID] = true
	broadcaster := s.txBroadcaster
	if broadcaster != nil {
		go broadcaster.BroadcastTransaction(tx)
	}
	return txID
}

func (s *Store) removePendingTxLocked(txID string) {
	for i, tx := range s.data.PendingTxs {
		if tx.TxID == txID {
			s.data.PendingTxs = append(s.data.PendingTxs[:i], s.data.PendingTxs[i+1:]...)
			delete(s.data.AppliedTxs, txID)
			return
		}
	}
}

func (s *Store) AcceptTransaction(tx wire.Transaction) (bool, error) {
	if err := validateTransactionShapeWithLimits(tx, ^uint64(0), ^uint64(0)); err != nil {
		return false, err
	}

	if tx.AgentKeyID != "" || transactionRequiresSignature(tx.Type) {
		if err := wire.VerifyTransactionSignature(tx, s.data.ChainID); err != nil {
			return false, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	limits := s.blockLimitsLocked()
	if err := validateTransactionShapeWithLimits(tx, limits.maxTxBytes, limits.maxStorageTxBytes); err != nil {
		return false, err
	}

	if tx.Type == "transfer" {
		if err := s.verifyTransferTxLocked(tx); err != nil {
			return false, err
		}
	}

	accepted, err := s.enqueuePendingTxLocked(tx)
	if err != nil || !accepted {
		return accepted, err
	}
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ProduceBlock() (wire.ProduceBlockResponse, error) {
	s.mu.Lock()

	if s.operatorIdentity == nil {
		s.mu.Unlock()
		return wire.ProduceBlockResponse{}, errors.New("no operator identity configured")
	}
	validator, ok := s.data.Validators[s.operatorIdentity.OwnerAddress]
	if !ok || validator.Status != wire.ValidatorStatusActive {
		s.mu.Unlock()
		return wire.ProduceBlockResponse{}, errors.New("operator's owner is not an active validator")
	}
	if validator.OperatorPublicKey != s.operatorIdentity.OperatorPublicKeyHex() {
		s.mu.Unlock()
		return wire.ProduceBlockResponse{}, errors.New("operator public key mismatch")
	}
	if err := s.validateLocalProducerTurnLocked(); err != nil {
		s.mu.Unlock()
		return wire.ProduceBlockResponse{}, err
	}
	block, produced, err := s.produceBlockLocked()
	if err != nil {
		s.mu.Unlock()
		return wire.ProduceBlockResponse{}, err
	}
	if !produced {
		s.mu.Unlock()
		return wire.ProduceBlockResponse{Produced: false}, nil
	}
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return wire.ProduceBlockResponse{}, err
	}
	s.mu.Unlock()
	s.broadcastBlock(block)
	s.SubmitLocalConsensusVotesForBlock(block)
	return wire.ProduceBlockResponse{Produced: true, Block: block}, nil
}

func (s *Store) AcceptBlock(block wire.Block) (bool, error) {
	if err := validateBlockShape(block); err != nil {
		return false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateBlockLimitsLocked(block); err != nil {
		return false, err
	}

	if block.Height <= uint64(len(s.data.Blocks)) {
		existing := s.data.Blocks[block.Height-1]
		if existing.Hash == block.Hash {
			return false, nil
		}
		return false, errors.New("forked block at existing height")
	}
	if block.Height != uint64(len(s.data.Blocks)+1) {
		return false, errors.New("block height is not the next height")
	}
	prevHash := ""
	if len(s.data.Blocks) > 0 {
		prevHash = s.data.Blocks[len(s.data.Blocks)-1].Hash
	}
	if block.PrevHash != prevHash {
		return false, errors.New("block prev hash mismatch")
	}
	if err := s.validateBlockTimeLocked(block); err != nil {
		return false, err
	}
	if err := s.validateBlockProducerTurnLocked(block); err != nil {
		return false, err
	}
	if err := s.validateBlockFinalityLocked(block); err != nil {
		return false, err
	}

	// Resolve producer address (operator) to owner via OperatorMap.
	ownerAddr := s.resolveOperatorToOwner(block.ProducerAddress)
	validator, ok := s.data.Validators[ownerAddr]
	if ok && validator.OperatorPublicKey != block.ProducerPublicKey {
		return false, errors.New("block producer operator public key mismatch")
	}
	if !ok {
		validator = wire.ValidatorInfo{
			OwnerAddress:      ownerAddr,
			OperatorAddress:   block.ProducerAddress,
			OperatorPublicKey: block.ProducerPublicKey,
			Status:            wire.ValidatorStatusActive,
		}
	}
	if err := s.applyBlockTransactionsLocked(block); err != nil {
		return false, err
	}
	if block.StateRoot != "" && block.StateRoot != s.stateRootLocked() {
		return false, errors.New("block state root mismatch")
	}
	s.prepareReceiptsForBlockLocked(&block)
	if block.ReceiptsRoot != "" && block.ReceiptsRoot != s.receiptsRootForBlockLocked(block) {
		return false, errors.New("block receipts root mismatch")
	}
	validator = s.validatorLocked(ownerAddr)
	validator.Status = wire.ValidatorStatusActive
	validator.ProducedBlocks++
	s.data.Validators[ownerAddr] = validator
	s.data.Blocks = append(s.data.Blocks, block)
	if block.Finality.Finalized {
		s.finalizeConsensusForBlockLocked(block)
	} else {
		s.data.ConsensusHeight = block.Height
		s.data.ConsensusRound = block.Round
		s.data.ConsensusPhase = consensus.PhasePrevote
		s.data.ConsensusProposer = ownerAddr
	}
	s.adjustFeeMarketAfterBlockLocked(block)
	s.recordProposerTurnLocked(ownerAddr, true)
	s.releaseValidatorPerBlockLocked(block.TimeUnix, ownerAddr)
	s.removePendingTxsLocked(block.Transactions)
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) produceBlockLocked() (wire.Block, bool, error) {
	if s.operatorIdentity == nil {
		return wire.Block{}, false, nil
	}
	validator, ok := s.data.Validators[s.operatorIdentity.OwnerAddress]
	if !ok || validator.Status != wire.ValidatorStatusActive {
		return wire.Block{}, false, nil
	}
	if validator.OperatorPublicKey != s.operatorIdentity.OperatorPublicKeyHex() {
		return wire.Block{}, false, nil
	}
	// Process matured unbonding entries each block.
	s.processMaturedUnbondingEntriesLocked()
	txs := s.selectPendingTxsForBlockLocked()
	appliedTxs, _ := s.applyPendingTransactionsForBlockLocked(txs, s.operatorIdentity.OwnerAddress)
	txLeaves := make([]string, 0, len(appliedTxs))
	for _, tx := range appliedTxs {
		txLeaves = append(txLeaves, txLeaf(tx))
	}
	prevHash := ""
	if len(s.data.Blocks) > 0 {
		prevHash = s.data.Blocks[len(s.data.Blocks)-1].Hash
	}
	block := wire.Block{
		Height:            uint64(len(s.data.Blocks) + 1),
		Round:             s.data.ConsensusRound,
		TimeUnix:          time.Now().Unix(),
		PrevHash:          prevHash,
		TxRoot:            chaincrypto.MerkleRoot(txLeaves),
		StateRoot:         s.stateRootLocked(),
		Transactions:      appliedTxs,
		ProducerAddress:   s.operatorIdentity.OperatorAddress,
		ProducerPublicKey: s.operatorIdentity.OperatorPublicKeyHex(),
	}
	s.prepareReceiptsForBlockLocked(&block)
	block.ReceiptsRoot = s.receiptsRootForBlockLocked(block)
	block.Hash = blockHash(block)
	s.prepareReceiptsForBlockLocked(&block)
	if err := wire.SignBlock(&block, s.operatorIdentity.OperatorPrivateKey); err != nil {
		return wire.Block{}, false, err
	}
	vote, err := s.signLocalBlockVoteLocked(block)
	if err != nil {
		return wire.Block{}, false, err
	}
	block.Finality = s.blockFinalityLocked(block, []wire.BlockVote{vote})
	limits := s.blockLimitsLocked()
	if uint64(blockEncodedSize(block)) > limits.maxBlockBytes {
		return wire.Block{}, false, errors.New("block exceeds maximum size")
	}
	s.data.Blocks = append(s.data.Blocks, block)
	if block.Finality.Finalized {
		s.finalizeConsensusForBlockLocked(block)
	} else {
		s.data.ConsensusHeight = block.Height
		s.data.ConsensusRound = block.Round
		s.data.ConsensusPhase = consensus.PhasePrevote
		s.data.ConsensusProposer = s.operatorIdentity.OwnerAddress
	}
	s.adjustFeeMarketAfterBlockLocked(block)
	s.recordProposerTurnLocked(s.operatorIdentity.OwnerAddress, true)
	s.releaseValidatorPerBlockLocked(block.TimeUnix, s.operatorIdentity.OwnerAddress)
	s.removePendingTxsLocked(appliedTxs)
	for _, tx := range appliedTxs {
		s.data.ConfirmedTxs[tx.TxID] = true
	}
	s.markConsensusValidatorsFromTxsLocked(appliedTxs)
	validator.ProducedBlocks++
	s.data.Validators[s.operatorIdentity.OwnerAddress] = validator
	return block, true, nil
}

func (s *Store) LatestBlock() (wire.Block, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.data.Blocks) == 0 {
		return wire.Block{}, errors.New("no blocks produced yet")
	}
	return s.data.Blocks[len(s.data.Blocks)-1], nil
}

func (s *Store) GetBlock(height uint64) (wire.Block, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if height == 0 || height > uint64(len(s.data.Blocks)) {
		return wire.Block{}, errors.New("block not found")
	}
	return s.data.Blocks[height-1], nil
}

func (s *Store) Height() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return uint64(len(s.data.Blocks))
}

func (s *Store) Mempool() wire.MempoolResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	return wire.MempoolResponse{Pending: s.orderedPendingTxsLocked(), FeeMarket: s.data.FeeMarket}
}

func (s *Store) AcceptBlockVote(vote wire.BlockVote) (wire.BlockVoteResponse, error) {
	if err := wire.VerifyBlockVote(vote); err != nil {
		return wire.BlockVoteResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if vote.Height == 0 || vote.Height > uint64(len(s.data.Blocks)) {
		return wire.BlockVoteResponse{}, errors.New("vote block height not found")
	}
	block := s.data.Blocks[vote.Height-1]
	if vote.BlockHash != block.Hash {
		return wire.BlockVoteResponse{}, errors.New("vote block hash mismatch")
	}
	if err := s.validateBlockVoteLocked(block, vote); err != nil {
		return wire.BlockVoteResponse{}, err
	}
	for _, existing := range block.Finality.Votes {
		if existing.ValidatorAddress == vote.ValidatorAddress {
			if existing.Signature == vote.Signature {
				return wire.BlockVoteResponse{Accepted: false, Block: block}, nil
			}
			return wire.BlockVoteResponse{}, errors.New("validator already voted for this block")
		}
	}
	votes := append(append([]wire.BlockVote(nil), block.Finality.Votes...), vote)
	block.Finality = s.blockFinalityLocked(block, votes)
	s.data.Blocks[vote.Height-1] = block
	if err := s.saveLocked(); err != nil {
		return wire.BlockVoteResponse{}, err
	}
	return wire.BlockVoteResponse{Accepted: true, Block: block}, nil
}

func (s *Store) SubmitConsensusVote(req wire.SubmitConsensusVoteRequest) (wire.SubmitConsensusVoteResponse, error) {
	vote := req.Vote
	if err := wire.VerifyConsensusVote(vote); err != nil {
		return wire.SubmitConsensusVoteResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	block, err := s.blockForConsensusVoteLocked(vote)
	if err != nil {
		return wire.SubmitConsensusVoteResponse{}, err
	}
	if err := s.validateConsensusVoteLocked(block, vote); err != nil {
		return wire.SubmitConsensusVoteResponse{}, err
	}
	key := consensusVoteKey(vote)
	if existing, ok := s.data.ConsensusVotes[key]; ok {
		if existing.BlockHash != vote.BlockHash || existing.Signature != vote.Signature {
			return wire.SubmitConsensusVoteResponse{}, errors.New("validator already submitted a conflicting consensus vote")
		}
		prevotes := s.consensusVoteFinalityLocked(block, vote.Round, wire.ConsensusVotePrevote)
		precommits := s.consensusVoteFinalityLocked(block, vote.Round, wire.ConsensusVotePrecommit)
		return wire.SubmitConsensusVoteResponse{
			Accepted:   false,
			Finalized:  block.Finality.Finalized,
			Vote:       existing,
			Block:      block,
			Prevotes:   prevotes,
			Precommits: precommits,
		}, nil
	}

	s.data.ConsensusVotes[key] = vote
	prevotes := s.consensusVoteFinalityLocked(block, vote.Round, wire.ConsensusVotePrevote)
	precommits := s.consensusVoteFinalityLocked(block, vote.Round, wire.ConsensusVotePrecommit)
	if prevotes.Finalized && s.data.ConsensusHeight == block.Height && s.data.ConsensusRound == vote.Round {
		s.data.ConsensusPhase = consensus.PhasePrecommit
	}
	if vote.Type == wire.ConsensusVotePrecommit && precommits.Finalized {
		block.Finality = precommits
		s.data.Blocks[block.Height-1] = block
		s.data.ConsensusPhase = consensus.PhaseCommit
		s.finalizeConsensusForBlockLocked(block)
	}
	if err := s.saveLocked(); err != nil {
		return wire.SubmitConsensusVoteResponse{}, err
	}
	broadcaster := s.voteBroadcaster
	if broadcaster != nil {
		go broadcaster.BroadcastConsensusVote(vote)
	}
	if vote.Type == wire.ConsensusVotePrevote && prevotes.Finalized {
		go s.MaybeSubmitLocalConsensusPrecommit(block)
	}
	return wire.SubmitConsensusVoteResponse{
		Accepted:   true,
		Finalized:  block.Finality.Finalized,
		Vote:       vote,
		Block:      block,
		Prevotes:   prevotes,
		Precommits: precommits,
	}, nil
}

func (s *Store) ConsensusVotes(height uint64, round uint64, voteType string) wire.ConsensusVotesResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	votes := make([]wire.ConsensusVote, 0)
	for _, vote := range s.data.ConsensusVotes {
		if height > 0 && vote.Height != height {
			continue
		}
		if round > 0 && vote.Round != round {
			continue
		}
		if voteType != "" && vote.Type != voteType {
			continue
		}
		votes = append(votes, vote)
	}
	sort.SliceStable(votes, func(i, j int) bool {
		if votes[i].Height != votes[j].Height {
			return votes[i].Height < votes[j].Height
		}
		if votes[i].Round != votes[j].Round {
			return votes[i].Round < votes[j].Round
		}
		if votes[i].Type != votes[j].Type {
			return votes[i].Type < votes[j].Type
		}
		return votes[i].ValidatorAddress < votes[j].ValidatorAddress
	})
	return wire.ConsensusVotesResponse{Height: height, Round: round, Type: voteType, Votes: votes}
}

func validateBlockShape(block wire.Block) error {
	if block.Height == 0 {
		return errors.New("block height must be positive")
	}
	if block.ProducerAddress == "" || block.ProducerPublicKey == "" {
		return errors.New("block producer is required")
	}
	if block.Hash != blockHash(block) {
		return errors.New("block hash mismatch")
	}
	txLeaves := make([]string, 0, len(block.Transactions))
	seen := map[string]bool{}
	for _, tx := range block.Transactions {
		if err := validateTransactionShapeWithLimits(tx, ^uint64(0), ^uint64(0)); err != nil {
			return err
		}
		if seen[tx.TxID] {
			return errors.New("block contains duplicate transaction")
		}
		seen[tx.TxID] = true
		txLeaves = append(txLeaves, txLeaf(tx))
	}
	if block.TxRoot != chaincrypto.MerkleRoot(txLeaves) {
		return errors.New("block tx root mismatch")
	}
	if err := wire.VerifyBlockSignature(block); err != nil {
		return err
	}
	return nil
}

func (s *Store) validateBlockFinalityLocked(block wire.Block) error {
	if len(block.Finality.Votes) == 0 {
		return nil
	}
	finality := s.blockFinalityLocked(block, block.Finality.Votes)
	if finality.VotingPower != block.Finality.VotingPower ||
		finality.TotalPower != block.Finality.TotalPower ||
		finality.ThresholdPower != block.Finality.ThresholdPower ||
		finality.Finalized != block.Finality.Finalized {
		return errors.New("block finality certificate mismatch")
	}
	if len(finality.Votes) != len(block.Finality.Votes) {
		return errors.New("block finality votes are not canonical")
	}
	for i := range finality.Votes {
		if finality.Votes[i].ValidatorAddress != block.Finality.Votes[i].ValidatorAddress ||
			finality.Votes[i].ValidatorPublicKey != block.Finality.Votes[i].ValidatorPublicKey ||
			finality.Votes[i].BlockHash != block.Finality.Votes[i].BlockHash ||
			finality.Votes[i].Height != block.Finality.Votes[i].Height ||
			finality.Votes[i].Power != block.Finality.Votes[i].Power ||
			finality.Votes[i].Signature != block.Finality.Votes[i].Signature {
			return errors.New("block finality votes are not canonical")
		}
	}
	return nil
}

func (s *Store) validateBlockTimeLocked(block wire.Block) error {
	now := time.Now().Add(maxFutureBlockTimeSkew).Unix()
	if block.TimeUnix > now {
		return errors.New("block time is too far in the future")
	}
	if len(s.data.Blocks) == 0 {
		return nil
	}
	prev := s.data.Blocks[len(s.data.Blocks)-1]
	if block.TimeUnix < prev.TimeUnix {
		return errors.New("block time moves backwards")
	}
	return nil
}

func transactionRequiresSignature(txType string) bool {
	switch txType {
	case "create_intent", "batch_commit", "finalize_deal", "settle_intent",
		"permanent_fund_topup", "renew_deal", "terminate_deal",
		"set_access_policy", "delegate_stake", "undelegate_stake":
		return true
	}
	return false
}

func validateTransactionShape(tx wire.Transaction) error {
	return validateTransactionShapeWithLimits(tx, defaultMaxTxBytes, defaultMaxStorageTxBytes)
}

func validateTransactionShapeWithLimits(tx wire.Transaction, maxTxBytes uint64, maxStorageTxBytes uint64) error {
	if tx.TxID == "" {
		return errors.New("transaction id is required")
	}
	if tx.Type == "" {
		return errors.New("transaction type is required")
	}
	if uint64(transactionEncodedSize(tx)) > maxTransactionBytes(tx.Type, maxTxBytes, maxStorageTxBytes) {
		return errors.New("transaction exceeds maximum size")
	}
	if chaincrypto.HashBytes(tx.Payload) != tx.PayloadHash {
		return errors.New("transaction payload hash mismatch")
	}
	expectedTxID := chaincrypto.HashBytes([]byte(tx.Type + ":" + tx.PayloadHash))
	if tx.TxID != expectedTxID {
		return errors.New("transaction id does not match content hash")
	}
	normalized := tx
	enrichTransactionMetadata(&normalized)
	if !transactionMetadataMatches(tx, normalized) {
		return errors.New("transaction metadata does not match signed payload")
	}
	return nil
}

func (s *Store) validateBlockLimitsLocked(block wire.Block) error {
	limits := s.blockLimitsLocked()
	if uint64(len(block.Transactions)) > limits.maxBlockTxs {
		return errors.New("block contains too many transactions")
	}
	if uint64(blockEncodedSize(block)) > limits.maxBlockBytes {
		return errors.New("block exceeds maximum size")
	}
	for _, tx := range block.Transactions {
		if err := validateTransactionShapeWithLimits(tx, limits.maxTxBytes, limits.maxStorageTxBytes); err != nil {
			return err
		}
	}
	return nil
}

func txLeaf(tx wire.Transaction) string {
	return wire.TransactionLeaf(tx)
}

func transactionMetadataMatches(tx wire.Transaction, normalized wire.Transaction) bool {
	switch tx.Type {
	case "transfer", "multisig_exec", "create_intent", "batch_commit", "finalize_deal",
		"settle_intent", "permanent_fund_topup", "renew_deal", "terminate_deal",
		"set_access_policy", "delegate_stake", "undelegate_stake",
		"create_collection", "append_record", "create_key_envelope",
		"create_share", "revoke_share":
		return wire.NormalizeAddress(tx.From) == normalized.From &&
			tx.Fee == normalized.Fee &&
			tx.Nonce == normalized.Nonce &&
			tx.NonceProtected == normalized.NonceProtected &&
			tx.AgentKeyID == normalized.AgentKeyID &&
			tx.AgentNonce == normalized.AgentNonce
	default:
		return true
	}
}

func (s *Store) validateLocalProducerTurnLocked() error {
	if s.operatorIdentity == nil {
		return errors.New("no operator identity configured")
	}
	nextHeight := uint64(len(s.data.Blocks) + 1)
	if s.data.UpgradePlan.HaltHeight > 0 && nextHeight >= s.data.UpgradePlan.HaltHeight {
		return errors.New("chain is halted for upgrade")
	}
	if s.data.ConsensusValidators[s.operatorIdentity.OwnerAddress] {
		return s.validateConsensusProducerTurnLocked(nextHeight, s.operatorIdentity.OwnerAddress)
	}
	if hasValidatorRegistrationTx(s.data.PendingTxs, s.operatorIdentity.OwnerAddress, s.operatorIdentity.OperatorPublicKeyHex()) {
		return nil
	}
	return errors.New("operator's owner is not yet in the consensus validator set")
}

func (s *Store) validateBlockProducerTurnLocked(block wire.Block) error {
	ownerAddr := s.resolveOperatorToOwner(block.ProducerAddress)
	if s.data.ConsensusValidators[ownerAddr] {
		expected, err := s.selectProposerLocked(block.Height, block.Round)
		if err != nil {
			return err
		}
		if ownerAddr != expected {
			return errors.New("not proposer turn for this height and round")
		}
		return nil
	}
	if hasValidatorRegistrationTx(block.Transactions, ownerAddr, block.ProducerPublicKey) {
		return nil
	}
	return errors.New("block producer is not in the consensus validator set")
}

func (s *Store) validateConsensusProducerTurnLocked(height uint64, producer string) error {
	validators := s.consensusValidatorAddressesLocked()
	if len(validators) == 0 {
		return errors.New("no consensus validators available")
	}
	round := s.data.ConsensusRound
	expected, err := s.selectProposerLocked(height, round)
	if err != nil {
		return err
	}
	if producer != expected {
		return errors.New("not proposer turn")
	}
	return nil
}

func (s *Store) signLocalBlockVoteLocked(block wire.Block) (wire.BlockVote, error) {
	if s.operatorIdentity == nil {
		return wire.BlockVote{}, errors.New("no operator identity configured")
	}
	vote := wire.BlockVote{
		Height:             block.Height,
		BlockHash:          block.Hash,
		ValidatorAddress:   s.operatorIdentity.OperatorAddress,
		ValidatorPublicKey: s.operatorIdentity.OperatorPublicKeyHex(),
		Power:              s.validatorPowerLocked(s.operatorIdentity.OwnerAddress),
	}
	if vote.Power == 0 && hasValidatorRegistrationTx(block.Transactions, s.operatorIdentity.OwnerAddress, s.operatorIdentity.OperatorPublicKeyHex()) {
		vote.Power = 1
	}
	if err := wire.SignBlockVote(&vote, s.operatorIdentity.OperatorPrivateKey); err != nil {
		return wire.BlockVote{}, err
	}
	return vote, nil
}

func (s *Store) blockFinalityLocked(block wire.Block, votes []wire.BlockVote) wire.BlockFinality {
	totalPower := s.totalVotingPowerLocked(block)
	thresholdPower := bftThreshold(totalPower)
	seen := map[string]bool{}
	acceptedVotes := make([]wire.BlockVote, 0, len(votes))
	var votingPower uint64
	for _, vote := range votes {
		if seen[vote.ValidatorAddress] {
			continue
		}
		if s.validateBlockVoteLocked(block, vote) != nil {
			continue
		}
		seen[vote.ValidatorAddress] = true
		power := s.validatorPowerForBlockLocked(block, vote.ValidatorAddress, vote.ValidatorPublicKey)
		normalized := vote
		normalized.Power = power
		acceptedVotes = append(acceptedVotes, normalized)
		votingPower += power
	}
	sort.SliceStable(acceptedVotes, func(i, j int) bool {
		return acceptedVotes[i].ValidatorAddress < acceptedVotes[j].ValidatorAddress
	})
	return wire.BlockFinality{
		Round:          block.Round,
		VotingPower:    votingPower,
		TotalPower:     totalPower,
		ThresholdPower: thresholdPower,
		Finalized:      totalPower > 0 && votingPower >= thresholdPower,
		Votes:          acceptedVotes,
	}
}

func (s *Store) validateBlockVoteLocked(block wire.Block, vote wire.BlockVote) error {
	if vote.Height != block.Height || vote.BlockHash != block.Hash {
		return errors.New("vote does not match block")
	}
	if err := wire.VerifyBlockVote(vote); err != nil {
		return err
	}
	power := s.validatorPowerForBlockLocked(block, vote.ValidatorAddress, vote.ValidatorPublicKey)
	if power == 0 {
		return errors.New("vote is not from an active consensus validator")
	}
	if vote.Power != power {
		return errors.New("vote power mismatch")
	}
	return nil
}

func (s *Store) blockForConsensusVoteLocked(vote wire.ConsensusVote) (wire.Block, error) {
	if vote.Height == 0 || vote.Height > uint64(len(s.data.Blocks)) {
		return wire.Block{}, errors.New("vote block height not found")
	}
	block := s.data.Blocks[vote.Height-1]
	if vote.BlockHash != block.Hash {
		return wire.Block{}, errors.New("vote block hash mismatch")
	}
	return block, nil
}

func (s *Store) validateConsensusVoteLocked(block wire.Block, vote wire.ConsensusVote) error {
	if vote.Type != wire.ConsensusVotePrevote && vote.Type != wire.ConsensusVotePrecommit {
		return errors.New("unsupported consensus vote type")
	}
	if vote.Height != block.Height || vote.BlockHash != block.Hash {
		return errors.New("vote does not match block")
	}
	if vote.Round != block.Round {
		return errors.New("vote round does not match block round")
	}
	if err := wire.VerifyConsensusVote(vote); err != nil {
		return err
	}
	power := s.validatorPowerForBlockLocked(block, vote.ValidatorAddress, vote.ValidatorPublicKey)
	if power == 0 {
		return errors.New("vote is not from an active consensus validator")
	}
	if vote.Power != power {
		return errors.New("vote power mismatch")
	}
	return nil
}

func (s *Store) consensusVoteFinalityLocked(block wire.Block, round uint64, voteType string) wire.BlockFinality {
	totalPower := s.totalVotingPowerLocked(block)
	thresholdPower := bftThreshold(totalPower)
	var votingPower uint64
	for _, vote := range s.data.ConsensusVotes {
		if vote.Height != block.Height || vote.Round != round || vote.Type != voteType || vote.BlockHash != block.Hash {
			continue
		}
		if s.validateConsensusVoteLocked(block, vote) != nil {
			continue
		}
		votingPower += s.validatorPowerForBlockLocked(block, vote.ValidatorAddress, vote.ValidatorPublicKey)
	}
	return wire.BlockFinality{
		Round:          round,
		VotingPower:    votingPower,
		TotalPower:     totalPower,
		ThresholdPower: thresholdPower,
		Finalized:      totalPower > 0 && votingPower >= thresholdPower,
	}
}

func consensusVoteKey(vote wire.ConsensusVote) string {
	return strconv.FormatUint(vote.Height, 10) + ":" +
		strconv.FormatUint(vote.Round, 10) + ":" +
		vote.Type + ":" +
		vote.ValidatorAddress
}

func (s *Store) totalVotingPowerLocked(block wire.Block) uint64 {
	var total uint64
	for _, address := range s.consensusValidatorAddressesLocked() {
		total += s.validatorPowerLocked(address)
	}
	if total == 0 {
		return validatorRegistrationPowerFromTx(block.Transactions, block.ProducerAddress, block.ProducerPublicKey)
	}
	return total
}

func (s *Store) validatorPowerForBlockLocked(block wire.Block, address string, publicKey string) uint64 {
	validator, ok := s.data.Validators[address]
	if ok && validator.Status == wire.ValidatorStatusActive && validator.OperatorPublicKey == publicKey && s.data.ConsensusValidators[address] {
		return validatorPower(validator)
	}
	return validatorRegistrationPowerFromTx(block.Transactions, address, publicKey)
}

func (s *Store) validatorPowerLocked(address string) uint64 {
	validator, ok := s.data.Validators[address]
	if !ok || validator.Status != wire.ValidatorStatusActive {
		return 0
	}
	return validatorPower(validator)
}

func validatorPower(validator wire.ValidatorInfo) uint64 {
	selfStake := validator.SelfStake
	if selfStake == 0 {
		selfStake = validator.Stake
	}
	total := selfStake + validator.DelegatedStake
	if total > 0 {
		return total
	}
	return 1
}

func bftThreshold(totalPower uint64) uint64 {
	if totalPower == 0 {
		return 0
	}
	return (totalPower*2)/3 + 1
}

func (s *Store) consensusValidatorAddressesLocked() []string {
	validators := make([]string, 0, len(s.data.ConsensusValidators))
	for address := range s.data.ConsensusValidators {
		stats, ok := s.data.Validators[address]
		if ok && stats.Status == wire.ValidatorStatusActive {
			validators = append(validators, address)
		}
	}
	sort.Strings(validators)
	return validators
}

func hasValidatorRegistrationTx(txs []wire.Transaction, address string, publicKey string) bool {
	return validatorRegistrationPowerFromTx(txs, address, publicKey) > 0
}

func validatorRegistrationPowerFromTx(txs []wire.Transaction, address string, publicKey string) uint64 {
	for _, tx := range txs {
		if tx.Type != "register_validator" {
			continue
		}
		var req wire.RegisterValidatorRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			continue
		}
		if req.OwnerAddress != address || req.OperatorPublicKey != publicKey {
			continue
		}
		if req.Stake < MinValidatorStake {
			continue
		}
		if wire.VerifyValidatorRegistration(req) == nil {
			return req.Stake
		}
	}
	return 0
}

func (s *Store) markConsensusValidatorsFromTxsLocked(txs []wire.Transaction) {
	for _, tx := range txs {
		if tx.Type != "register_validator" {
			continue
		}
		var req wire.RegisterValidatorRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			continue
		}
		if req.Stake >= MinValidatorStake && wire.VerifyValidatorRegistration(req) == nil {
			s.data.ConsensusValidators[req.OwnerAddress] = true
		}
	}
}

func (s *Store) removePendingTxsLocked(txs []wire.Transaction) {
	if len(s.data.PendingTxs) == 0 || len(txs) == 0 {
		return
	}
	inBlock := map[string]bool{}
	for _, tx := range txs {
		inBlock[tx.TxID] = true
	}
	kept := s.data.PendingTxs[:0]
	for _, tx := range s.data.PendingTxs {
		if !inBlock[tx.TxID] {
			kept = append(kept, tx)
		}
	}
	s.data.PendingTxs = kept
}

func (s *Store) enqueuePendingTxLocked(tx wire.Transaction) (bool, error) {
	if s.data.ConfirmedTxs[tx.TxID] {
		return false, nil
	}
	enrichTransactionMetadata(&tx)
	if err := s.validateTransactionFeeLocked(tx); err != nil {
		return false, err
	}
	for i, pending := range s.data.PendingTxs {
		if pending.TxID == tx.TxID {
			return false, nil
		}
		if !tx.NonceProtected || !pending.NonceProtected {
			continue
		}
		if !sameAddress(pending.From, tx.From) || pending.Nonce != tx.Nonce {
			continue
		}
		if s.data.AppliedTxs[pending.TxID] {
			return false, errors.New("cannot replace already applied pending transaction")
		}
		if tx.Fee <= pending.Fee {
			return false, errors.New("replacement transaction fee is not higher")
		}
		s.data.PendingTxs[i] = tx
		return true, nil
	}
	s.data.PendingTxs = append(s.data.PendingTxs, tx)
	return true, nil
}

func (s *Store) orderedPendingTxsLocked() []wire.Transaction {
	txs := append([]wire.Transaction(nil), s.data.PendingTxs...)
	sortPendingTxs(txs)
	return txs
}

func (s *Store) selectPendingTxsForBlockLocked() []wire.Transaction {
	pending := append([]wire.Transaction(nil), s.data.PendingTxs...)
	sortPendingTxs(pending)
	limits := s.blockLimitsLocked()

	selected := make([]wire.Transaction, 0, len(pending))
	selectedIDs := map[string]bool{}
	nextNonce := map[string]uint64{}
	selectedBytes := 0
	addTx := func(tx wire.Transaction, limitBytes uint64) bool {
		if uint64(len(selected)) >= limits.maxBlockTxs {
			return false
		}
		txBytes := uint64(transactionEncodedSize(tx))
		if txBytes > maxTransactionBytes(tx.Type, limits.maxTxBytes, limits.maxStorageTxBytes) {
			return false
		}
		if uint64(selectedBytes)+txBytes > limitBytes {
			return false
		}
		selected = append(selected, tx)
		selectedIDs[tx.TxID] = true
		selectedBytes += int(txBytes)
		return true
	}

	for _, tx := range pending {
		if tx.From == "" {
			continue
		}
		address := wire.NormalizeAddress(tx.From)
		if _, ok := nextNonce[address]; !ok {
			nextNonce[address] = s.accountLocked(address).Nonce
		}
	}
	for _, tx := range pending {
		if !s.data.AppliedTxs[tx.TxID] {
			continue
		}
		if !addTx(tx, limitWithHeadroom(limits.maxBlockBytes)) {
			return selected
		}
	}

	targetBytes := limitWithHeadroom(limits.targetBlockBytes)
	for {
		nextIndex := -1
		for i, tx := range pending {
			if selectedIDs[tx.TxID] {
				continue
			}
			address := wire.NormalizeAddress(tx.From)
			if tx.Nonce == nextNonce[address] {
				nextIndex = i
				break
			}
		}
		if nextIndex == -1 {
			break
		}
		tx := pending[nextIndex]
		if !addTx(tx, targetBytes) {
			break
		}
		address := wire.NormalizeAddress(tx.From)
		nextNonce[address]++
	}
	return selected
}

func sortPendingTxs(txs []wire.Transaction) {
	sort.SliceStable(txs, func(i, j int) bool {
		if txs[i].Fee != txs[j].Fee {
			return txs[i].Fee > txs[j].Fee
		}
		if !sameAddress(txs[i].From, txs[j].From) {
			return wire.NormalizeAddress(txs[i].From) < wire.NormalizeAddress(txs[j].From)
		}
		if txs[i].Nonce != txs[j].Nonce {
			return txs[i].Nonce < txs[j].Nonce
		}
		if txs[i].CreatedAtUnix != txs[j].CreatedAtUnix {
			return txs[i].CreatedAtUnix < txs[j].CreatedAtUnix
		}
		return txs[i].TxID < txs[j].TxID
	})
}

func enrichTransactionMetadata(tx *wire.Transaction) {
	tx.From = wire.NormalizeAddress(tx.From)
	switch tx.Type {
	case "transfer":
		var req wire.TransferRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			return
		}
		if req.From != "" {
			tx.From = wire.NormalizeAddress(req.From)
		}
		tx.Fee = req.Fee
		tx.NonceProtected = true
		tx.Nonce = req.Nonce
	case "multisig_exec":
		var payload multisigExecTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		tx.From = wire.NormalizeAddress(payload.Request.Wallet)
		tx.Fee = payload.Request.Fee
		tx.NonceProtected = true
		tx.Nonce = payload.Request.Nonce
	case "create_intent":
		var payload createIntentTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountOrAgentMetadata(tx, payload.Request.User, payload.Request.Nonce, payload.Request.LockedFee, payload.Request.AgentKeyID, payload.Request.AgentNonce)
	case "batch_commit":
		var payload batchCommitTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountOrAgentMetadata(tx, payload.Request.User, payload.Request.Nonce, 0, payload.Request.AgentKeyID, payload.Request.AgentNonce)
	case "finalize_deal":
		var payload finalizeDealTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountOrAgentMetadata(tx, payload.Request.User, payload.Request.Nonce, 0, payload.Request.AgentKeyID, payload.Request.AgentNonce)
	case "settle_intent":
		var payload settleIntentTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountMetadata(tx, payload.Request.User, payload.Request.Nonce, 0)
	case "permanent_fund_topup":
		var payload permanentFundTopUpTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountMetadata(tx, payload.Request.User, payload.Request.Nonce, 0)
	case "renew_deal":
		var payload renewDealTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountMetadata(tx, payload.Request.User, payload.Request.Nonce, 0)
	case "terminate_deal":
		var payload terminateDealTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountMetadata(tx, payload.Request.User, payload.Request.Nonce, 0)
	case "set_access_policy":
		var payload setAccessPolicyTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountMetadata(tx, payload.Request.User, payload.Request.Nonce, 0)
	case "delegate_stake":
		var payload delegateStakeTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountMetadata(tx, payload.Request.Delegator, payload.Request.Nonce, 0)
	case "undelegate_stake":
		var payload undelegateStakeTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountMetadata(tx, payload.Request.Delegator, payload.Request.Nonce, 0)
	case "create_collection":
		var payload createCollectionTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountMetadata(tx, payload.Request.User, payload.Request.Nonce, 0)
	case "append_record":
		var payload appendRecordTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountMetadata(tx, payload.Request.User, payload.Request.Nonce, 0)
	case "create_key_envelope", "create_share":
		var payload createShareTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			var envelopePayload createKeyEnvelopeTxPayload
			if err := json.Unmarshal(tx.Payload, &envelopePayload); err != nil {
				return
			}
			enrichAccountMetadata(tx, envelopePayload.Request.Owner, envelopePayload.Request.AccountNonce, 0)
			return
		}
		enrichAccountMetadata(tx, payload.Request.Owner, payload.Request.AccountNonce, 0)
	case "revoke_share":
		var payload revokeShareTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			return
		}
		enrichAccountMetadata(tx, payload.Request.Owner, payload.Request.AccountNonce, 0)
	}
}

func enrichAccountMetadata(tx *wire.Transaction, from string, nonce uint64, fee uint64) {
	tx.From = wire.NormalizeAddress(from)
	tx.Nonce = nonce
	tx.Fee = fee
	tx.NonceProtected = true
}

func enrichAccountOrAgentMetadata(tx *wire.Transaction, from string, nonce uint64, fee uint64, agentKeyID string, agentNonce uint64) {
	tx.From = wire.NormalizeAddress(from)
	tx.Fee = fee
	tx.AgentKeyID = agentKeyID
	tx.AgentNonce = agentNonce
	if agentKeyID != "" {
		tx.Nonce = 0
		tx.NonceProtected = false
		return
	}
	tx.Nonce = nonce
	tx.NonceProtected = true
}

func sameAddress(a string, b string) bool {
	return wire.NormalizeAddress(a) == wire.NormalizeAddress(b)
}

func transactionEncodedSize(tx wire.Transaction) int {
	raw, err := json.Marshal(tx)
	if err != nil {
		return defaultMaxBlockBytes + 1
	}
	return len(raw)
}

func blockEncodedSize(block wire.Block) int {
	raw, err := json.Marshal(block)
	if err != nil {
		return defaultMaxBlockBytes + 1
	}
	return len(raw)
}

type blockLimits struct {
	targetBlockBytes  uint64
	maxBlockBytes     uint64
	maxBlockTxs       uint64
	maxTxBytes        uint64
	maxStorageTxBytes uint64
}

func (s *Store) blockLimitsLocked() blockLimits {
	params := s.miningParamsLocked()
	limits := blockLimits{
		targetBlockBytes:  params.TargetBlockBytes,
		maxBlockBytes:     params.MaxBlockBytes,
		maxBlockTxs:       params.MaxBlockTxs,
		maxTxBytes:        params.MaxTxBytes,
		maxStorageTxBytes: params.MaxStorageTxBytes,
	}
	if limits.targetBlockBytes == 0 {
		limits.targetBlockBytes = defaultTargetBlockBytes
	}
	if limits.maxBlockBytes == 0 {
		limits.maxBlockBytes = defaultMaxBlockBytes
	}
	if limits.maxBlockBytes > defaultMaxBlockBytes {
		limits.maxBlockBytes = defaultMaxBlockBytes
	}
	if limits.maxBlockTxs == 0 {
		limits.maxBlockTxs = defaultMaxBlockTxs
	}
	if limits.maxTxBytes == 0 {
		limits.maxTxBytes = defaultMaxTxBytes
	}
	if limits.maxStorageTxBytes == 0 {
		limits.maxStorageTxBytes = defaultMaxStorageTxBytes
	}
	if limits.targetBlockBytes > limits.maxBlockBytes {
		limits.targetBlockBytes = limits.maxBlockBytes
	}
	return limits
}

func limitWithHeadroom(limit uint64) uint64 {
	if limit <= defaultBlockSizeHeadroom {
		return 0
	}
	return limit - defaultBlockSizeHeadroom
}

func maxTransactionBytes(txType string, maxTxBytes uint64, maxStorageTxBytes uint64) uint64 {
	if isStorageMetadataTransaction(txType) {
		return maxStorageTxBytes
	}
	return maxTxBytes
}

func isStorageMetadataTransaction(txType string) bool {
	switch txType {
	case "create_intent", "batch_commit", "finalize_deal", "settle_intent",
		"renew_deal", "permanent_fund_topup", "terminate_deal",
		"set_access_policy", "submit_delete_receipt", "submit_retrieval_receipt",
		"generate_challenges", "create_repair_tasks", "start_epoch",
		"submit_proof", "finalize_epoch", "create_collection", "append_record",
		"create_key_envelope", "create_share", "revoke_share":
		return true
	default:
		return false
	}
}

func blockHash(block wire.Block) string {
	raw, _ := json.Marshal(struct {
		Height       uint64 `json:"height"`
		Round        uint64 `json:"round,omitempty"`
		TimeUnix     int64  `json:"time_unix"`
		PrevHash     string `json:"prev_hash"`
		TxRoot       string `json:"tx_root"`
		StateRoot    string `json:"state_root,omitempty"`
		ReceiptsRoot string `json:"receipts_root,omitempty"`
		Producer     string `json:"producer"`
		TxCount      int    `json:"tx_count"`
	}{
		Height:       block.Height,
		Round:        block.Round,
		TimeUnix:     block.TimeUnix,
		PrevHash:     block.PrevHash,
		TxRoot:       block.TxRoot,
		StateRoot:    block.StateRoot,
		ReceiptsRoot: block.ReceiptsRoot,
		Producer:     block.ProducerAddress,
		TxCount:      len(block.Transactions),
	})
	return chaincrypto.HashBytes(raw)
}
