package chain

import (
	"errors"
	"log"

	"chain/internal/wire"
)

func (s *Store) SubmitLocalConsensusVotesForBlock(block wire.Block) {
	resp, err := s.submitLocalConsensusVote(block, wire.ConsensusVotePrevote)
	if err != nil {
		if !errors.Is(err, errLocalConsensusVoteUnavailable) {
			log.Printf("local consensus prevote failed height=%d hash=%s: %v", block.Height, block.Hash, err)
		}
		return
	}
	if resp.Prevotes.Finalized {
		s.MaybeSubmitLocalConsensusPrecommit(block)
	}
}

func (s *Store) MaybeSubmitLocalConsensusPrecommit(block wire.Block) {
	s.mu.Lock()
	if block.Height == 0 || block.Height > uint64(len(s.data.Blocks)) {
		s.mu.Unlock()
		return
	}
	localBlock := s.data.Blocks[block.Height-1]
	if localBlock.Hash != block.Hash {
		s.mu.Unlock()
		return
	}
	prevotes := s.consensusVoteFinalityLocked(localBlock, block.Round, wire.ConsensusVotePrevote)
	s.mu.Unlock()
	if !prevotes.Finalized {
		return
	}
	if _, err := s.submitLocalConsensusVote(block, wire.ConsensusVotePrecommit); err != nil && !errors.Is(err, errLocalConsensusVoteUnavailable) {
		log.Printf("local consensus precommit failed height=%d hash=%s: %v", block.Height, block.Hash, err)
	}
}

var errLocalConsensusVoteUnavailable = errors.New("local consensus vote unavailable")

func (s *Store) submitLocalConsensusVote(block wire.Block, voteType string) (wire.SubmitConsensusVoteResponse, error) {
	vote, err := s.signLocalConsensusVote(block, voteType)
	if err != nil {
		return wire.SubmitConsensusVoteResponse{}, err
	}
	return s.SubmitConsensusVote(wire.SubmitConsensusVoteRequest{Vote: vote})
}

func (s *Store) signLocalConsensusVote(block wire.Block, voteType string) (wire.ConsensusVote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.operatorIdentity == nil {
		return wire.ConsensusVote{}, errLocalConsensusVoteUnavailable
	}
	if block.Height == 0 || block.Height > uint64(len(s.data.Blocks)) {
		return wire.ConsensusVote{}, errLocalConsensusVoteUnavailable
	}
	localBlock := s.data.Blocks[block.Height-1]
	if localBlock.Hash != block.Hash {
		return wire.ConsensusVote{}, errLocalConsensusVoteUnavailable
	}
	if !s.data.ConsensusValidators[s.operatorIdentity.OwnerAddress] {
		return wire.ConsensusVote{}, errLocalConsensusVoteUnavailable
	}
	power := s.validatorPowerLocked(s.operatorIdentity.OwnerAddress)
	if power == 0 {
		return wire.ConsensusVote{}, errLocalConsensusVoteUnavailable
	}
	vote := wire.ConsensusVote{
		Height:             block.Height,
		Round:              block.Round,
		Type:               voteType,
		BlockHash:          block.Hash,
		ValidatorAddress:   s.operatorIdentity.OperatorAddress,
		ValidatorPublicKey: s.operatorIdentity.OperatorPublicKeyHex(),
		Power:              power,
	}
	if err := wire.SignConsensusVote(&vote, s.operatorIdentity.OperatorPrivateKey); err != nil {
		return wire.ConsensusVote{}, err
	}
	return vote, nil
}
