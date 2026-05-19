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

func (s *Store) recordTxLocked(txType, from string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte("{}")
	}
	payloadHash := chaincrypto.HashBytes(raw)
	txID, err := randomID("tx")
	if err != nil {
		txID = chaincrypto.HashBytes([]byte(txType + from + payloadHash + strconv.FormatInt(time.Now().UnixNano(), 10)))
	}
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
		return
	}
	s.data.AppliedTxs[tx.TxID] = true
	broadcaster := s.txBroadcaster
	if broadcaster != nil {
		go broadcaster.BroadcastTransaction(tx)
	}
}

func (s *Store) AcceptTransaction(tx wire.Transaction) (bool, error) {
	if err := validateTransactionShape(tx); err != nil {
		return false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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

	if len(s.data.PendingTxs) > 0 {
		if s.blockProducer == nil {
			s.mu.Unlock()
			return wire.ProduceBlockResponse{}, errors.New("no block producer configured")
		}
		validator, ok := s.data.Validators[s.blockProducer.Address]
		if !ok || validator.Status != wire.ValidatorStatusActive {
			s.mu.Unlock()
			return wire.ProduceBlockResponse{}, errors.New("block producer is not an active validator")
		}
		if validator.PublicKey != s.blockProducer.PublicKeyBase64() {
			s.mu.Unlock()
			return wire.ProduceBlockResponse{}, errors.New("block producer public key mismatch")
		}
		if err := s.validateLocalProducerTurnLocked(); err != nil {
			s.mu.Unlock()
			return wire.ProduceBlockResponse{}, err
		}
	}
	block, produced := s.produceBlockLocked()
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
	if err := s.validateBlockProducerTurnLocked(block); err != nil {
		return false, err
	}
	if err := s.validateBlockFinalityLocked(block); err != nil {
		return false, err
	}

	validator, ok := s.data.Validators[block.ProducerAddress]
	if ok && validator.PublicKey != block.ProducerPublicKey {
		return false, errors.New("block producer public key mismatch")
	}
	if !ok {
		validator = wire.ValidatorInfo{
			Address:   block.ProducerAddress,
			PublicKey: block.ProducerPublicKey,
			Status:    wire.ValidatorStatusActive,
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
	validator = s.validatorLocked(block.ProducerAddress)
	validator.Address = block.ProducerAddress
	validator.PublicKey = block.ProducerPublicKey
	validator.Status = wire.ValidatorStatusActive
	validator.ProducedBlocks++
	s.data.Validators[block.ProducerAddress] = validator
	s.data.Blocks = append(s.data.Blocks, block)
	if block.Finality.Finalized {
		s.finalizeConsensusForBlockLocked(block)
	} else {
		s.data.ConsensusHeight = block.Height
		s.data.ConsensusRound = block.Round
		s.data.ConsensusPhase = consensus.PhasePrevote
		s.data.ConsensusProposer = block.ProducerAddress
	}
	s.adjustFeeMarketAfterBlockLocked(block)
	s.removePendingTxsLocked(block.Transactions)
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) produceBlockLocked() (wire.Block, bool) {
	if len(s.data.PendingTxs) == 0 {
		return wire.Block{}, false
	}
	if s.blockProducer == nil {
		return wire.Block{}, false
	}
	validator, ok := s.data.Validators[s.blockProducer.Address]
	if !ok || validator.Status != wire.ValidatorStatusActive {
		return wire.Block{}, false
	}
	if validator.PublicKey != s.blockProducer.PublicKeyBase64() {
		return wire.Block{}, false
	}
	txs := s.selectPendingTxsForBlockLocked()
	if len(txs) == 0 {
		return wire.Block{}, false
	}
	if err := s.applyPendingTransactionsForBlockLocked(txs, s.blockProducer.Address); err != nil {
		return wire.Block{}, false
	}
	txLeaves := make([]string, 0, len(txs))
	for _, tx := range txs {
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
		Transactions:      txs,
		ProducerAddress:   s.blockProducer.Address,
		ProducerPublicKey: s.blockProducer.PublicKeyBase64(),
	}
	s.prepareReceiptsForBlockLocked(&block)
	block.ReceiptsRoot = s.receiptsRootForBlockLocked(block)
	block.Hash = blockHash(block)
	s.prepareReceiptsForBlockLocked(&block)
	if err := wire.SignBlock(&block, s.blockProducer.PrivateKey); err != nil {
		return wire.Block{}, false
	}
	vote, err := s.signLocalBlockVoteLocked(block)
	if err != nil {
		return wire.Block{}, false
	}
	block.Finality = s.blockFinalityLocked(block, []wire.BlockVote{vote})
	s.data.Blocks = append(s.data.Blocks, block)
	if block.Finality.Finalized {
		s.finalizeConsensusForBlockLocked(block)
	} else {
		s.data.ConsensusHeight = block.Height
		s.data.ConsensusRound = block.Round
		s.data.ConsensusPhase = consensus.PhasePrevote
		s.data.ConsensusProposer = block.ProducerAddress
	}
	s.adjustFeeMarketAfterBlockLocked(block)
	s.removePendingTxsLocked(txs)
	for _, tx := range txs {
		s.data.ConfirmedTxs[tx.TxID] = true
	}
	s.markConsensusValidatorsFromTxsLocked(txs)
	validator.ProducedBlocks++
	s.data.Validators[validator.Address] = validator
	return block, true
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
		if err := validateTransactionShape(tx); err != nil {
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

func validateTransactionShape(tx wire.Transaction) error {
	if tx.TxID == "" {
		return errors.New("transaction id is required")
	}
	if tx.Type == "" {
		return errors.New("transaction type is required")
	}
	if chaincrypto.HashBytes(tx.Payload) != tx.PayloadHash {
		return errors.New("transaction payload hash mismatch")
	}
	enrichTransactionMetadata(&tx)
	return nil
}

func txLeaf(tx wire.Transaction) string {
	return chaincrypto.HashBytes([]byte(tx.TxID + ":" + tx.PayloadHash))
}

func (s *Store) validateLocalProducerTurnLocked() error {
	if s.blockProducer == nil {
		return errors.New("no block producer configured")
	}
	nextHeight := uint64(len(s.data.Blocks) + 1)
	if s.data.UpgradePlan.HaltHeight > 0 && nextHeight >= s.data.UpgradePlan.HaltHeight {
		return errors.New("chain is halted for upgrade")
	}
	if s.data.ConsensusValidators[s.blockProducer.Address] {
		return s.validateConsensusProducerTurnLocked(nextHeight, s.blockProducer.Address)
	}
	if hasValidatorRegistrationTx(s.data.PendingTxs, s.blockProducer.Address, s.blockProducer.PublicKeyBase64()) {
		return nil
	}
	return errors.New("block producer is not yet in the consensus validator set")
}

func (s *Store) validateBlockProducerTurnLocked(block wire.Block) error {
	if s.data.ConsensusValidators[block.ProducerAddress] {
		expected, err := s.selectProposerLocked(block.Height, block.Round)
		if err != nil {
			return err
		}
		if block.ProducerAddress != expected {
			return errors.New("not proposer turn for this height and round")
		}
		return nil
	}
	if hasValidatorRegistrationTx(block.Transactions, block.ProducerAddress, block.ProducerPublicKey) {
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
	if s.blockProducer == nil {
		return wire.BlockVote{}, errors.New("no block producer configured")
	}
	vote := wire.BlockVote{
		Height:             block.Height,
		BlockHash:          block.Hash,
		ValidatorAddress:   s.blockProducer.Address,
		ValidatorPublicKey: s.blockProducer.PublicKeyBase64(),
		Power:              s.validatorPowerLocked(s.blockProducer.Address),
	}
	if vote.Power == 0 && hasValidatorRegistrationTx(block.Transactions, s.blockProducer.Address, s.blockProducer.PublicKeyBase64()) {
		vote.Power = 1
	}
	if err := wire.SignBlockVote(&vote, s.blockProducer.PrivateKey); err != nil {
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
	if total == 0 && hasValidatorRegistrationTx(block.Transactions, block.ProducerAddress, block.ProducerPublicKey) {
		return 1
	}
	return total
}

func (s *Store) validatorPowerForBlockLocked(block wire.Block, address string, publicKey string) uint64 {
	validator, ok := s.data.Validators[address]
	if ok && validator.Status == wire.ValidatorStatusActive && validator.PublicKey == publicKey && s.data.ConsensusValidators[address] {
		return validatorPower(validator)
	}
	if hasValidatorRegistrationTx(block.Transactions, address, publicKey) {
		return 1
	}
	return 0
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
	for _, tx := range txs {
		if tx.Type != "register_validator" {
			continue
		}
		var req wire.RegisterValidatorRequest
		if err := json.Unmarshal(tx.Payload, &req); err != nil {
			continue
		}
		if req.Address != address || req.PublicKey != publicKey {
			continue
		}
		if wire.VerifyValidatorRegistration(req) == nil {
			return true
		}
	}
	return false
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
		if wire.VerifyValidatorRegistration(req) == nil {
			s.data.ConsensusValidators[req.Address] = true
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

	selected := make([]wire.Transaction, 0, len(pending))
	selectedIDs := map[string]bool{}
	nextNonce := map[string]uint64{}
	for _, tx := range pending {
		if !tx.NonceProtected || tx.From == "" {
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
		selected = append(selected, tx)
		selectedIDs[tx.TxID] = true
	}

	for {
		nextIndex := -1
		for i, tx := range pending {
			if selectedIDs[tx.TxID] {
				continue
			}
			if !tx.NonceProtected {
				nextIndex = i
				break
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
		selected = append(selected, tx)
		selectedIDs[tx.TxID] = true
		if tx.NonceProtected {
			address := wire.NormalizeAddress(tx.From)
			nextNonce[address]++
		}
	}
	return selected
}

func sortPendingTxs(txs []wire.Transaction) {
	sort.SliceStable(txs, func(i, j int) bool {
		if txs[i].Fee != txs[j].Fee {
			return txs[i].Fee > txs[j].Fee
		}
		if txs[i].NonceProtected != txs[j].NonceProtected {
			return txs[i].NonceProtected
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
		if wire.IsSignedTransfer(req) {
			tx.NonceProtected = true
			tx.Nonce = req.Nonce
		}
	}
}

func sameAddress(a string, b string) bool {
	return wire.NormalizeAddress(a) == wire.NormalizeAddress(b)
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
