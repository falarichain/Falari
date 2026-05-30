package chain

import (
	"errors"
	"sort"
	"strconv"
	"time"

	"chain/internal/wire"
)

const doubleVoteEvidenceType = "double_vote"

type validatorEvidenceTxPayload struct {
	Evidence wire.ValidatorEvidence `json:"evidence"`
}

func (s *Store) SubmitValidatorEvidence(req wire.SubmitValidatorEvidenceRequest) (wire.SubmitValidatorEvidenceResponse, error) {
	evidence, err := s.buildDoubleVoteEvidence(req)
	if err != nil {
		return wire.SubmitValidatorEvidenceResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, exists := s.data.ValidatorEvidence[evidence.EvidenceID]; exists {
		return wire.SubmitValidatorEvidenceResponse{Accepted: false, Evidence: existing}, nil
	}
	applied, err := s.applyValidatorEvidenceLocked(evidence)
	if err != nil {
		return wire.SubmitValidatorEvidenceResponse{}, err
	}
	if applied {
		s.recordTxLocked("validator_evidence", evidence.ValidatorAddress, validatorEvidenceTxPayload{Evidence: evidence})
	}
	if err := s.saveLocked(); err != nil {
		return wire.SubmitValidatorEvidenceResponse{}, err
	}
	return wire.SubmitValidatorEvidenceResponse{Accepted: applied, Evidence: evidence}, nil
}

func (s *Store) applyValidatorEvidenceLocked(evidence wire.ValidatorEvidence) (bool, error) {
	if existing, exists := s.data.ValidatorEvidence[evidence.EvidenceID]; exists {
		if !sameValidatorEvidence(existing, evidence) {
			return false, errors.New("conflicting validator evidence id")
		}
		return false, nil
	}
	if err := s.validateValidatorEvidenceLocked(evidence); err != nil {
		return false, err
	}
	// Evidence.ValidatorAddress is the operator address; resolve to owner for slashing.
	ownerAddr := s.resolveOperatorToOwner(evidence.ValidatorAddress)
	account := s.accountLocked(ownerAddr)
	slash := evidence.Slashed
	if slash > account.LockedStake {
		return false, errors.New("validator evidence slash exceeds locked stake")
	}
	account.LockedStake -= slash
	s.data.Accounts[account.Address] = account
	s.addSlashedToPermanentFundLocked(slash)

	validator := s.validatorLocked(ownerAddr)
	validator.Stake = account.LockedStake
	validator.Slashed += slash
	validator.EvidenceCount++
	if validator.Stake == 0 {
		validator.Status = wire.ValidatorStatusSlashed
		delete(s.data.ConsensusValidators, ownerAddr)
	}
	s.data.Validators[ownerAddr] = validator
	s.data.ValidatorEvidence[evidence.EvidenceID] = evidence
	return true, nil
}

func (s *Store) validateValidatorEvidenceLocked(evidence wire.ValidatorEvidence) error {
	if evidence.Type != doubleVoteEvidenceType {
		return errors.New("unsupported validator evidence type")
	}
	if evidence.EvidenceID == "" {
		return errors.New("validator evidence id is required")
	}
	if evidence.Height == 0 {
		return errors.New("validator evidence height is required")
	}
	if evidence.ValidatorAddress == "" || evidence.ValidatorPublicKey == "" {
		return errors.New("validator evidence identity is required")
	}
	if evidence.FirstBlockHash == "" || evidence.SecondBlockHash == "" {
		return errors.New("validator evidence block hashes are required")
	}
	if evidence.FirstBlockHash == evidence.SecondBlockHash {
		return errors.New("validator evidence requires conflicting block hashes")
	}
	voteA, voteB := evidenceVotes(evidence)
	if err := wire.VerifyBlockVote(voteA); err != nil {
		return err
	}
	if err := wire.VerifyBlockVote(voteB); err != nil {
		return err
	}
	if voteA.ValidatorAddress != voteB.ValidatorAddress || voteA.ValidatorPublicKey != voteB.ValidatorPublicKey {
		return errors.New("validator evidence votes must use one validator identity")
	}
	if voteA.Height != voteB.Height || voteA.Height != evidence.Height {
		return errors.New("validator evidence votes must use one height")
	}
	if voteA.Power != voteB.Power || voteA.Power != evidence.Power {
		return errors.New("validator evidence votes must use one power")
	}
	if voteA.BlockHash == voteB.BlockHash {
		return errors.New("validator evidence votes must conflict")
	}
	expectedID := validatorEvidenceID(voteA, voteB)
	if evidence.EvidenceID != expectedID {
		return errors.New("validator evidence id mismatch")
	}
	// Resolve operator address to owner.
	ownerAddr := s.resolveOperatorToOwner(evidence.ValidatorAddress)
	validator, ok := s.data.Validators[ownerAddr]
	if !ok {
		return errors.New("validator evidence target is not registered")
	}
	if validator.OperatorPublicKey != evidence.ValidatorPublicKey {
		return errors.New("validator evidence public key mismatch")
	}
	if validator.Status != wire.ValidatorStatusActive {
		return errors.New("validator evidence target is not active")
	}
	if validatorPower(validator) != evidence.Power {
		return errors.New("validator evidence power mismatch")
	}
	if evidence.Slashed != validatorDoubleVoteSlashLocked(s.accountLocked(ownerAddr)) {
		return errors.New("validator evidence slash mismatch")
	}
	return nil
}

func sameValidatorEvidence(a wire.ValidatorEvidence, b wire.ValidatorEvidence) bool {
	return a.EvidenceID == b.EvidenceID &&
		a.Type == b.Type &&
		a.Height == b.Height &&
		a.ValidatorAddress == b.ValidatorAddress &&
		a.ValidatorPublicKey == b.ValidatorPublicKey &&
		a.Power == b.Power &&
		a.FirstBlockHash == b.FirstBlockHash &&
		a.SecondBlockHash == b.SecondBlockHash &&
		a.FirstSignature == b.FirstSignature &&
		a.SecondSignature == b.SecondSignature &&
		a.Slashed == b.Slashed
}

func (s *Store) buildDoubleVoteEvidence(req wire.SubmitValidatorEvidenceRequest) (wire.ValidatorEvidence, error) {
	voteA := req.VoteA
	voteB := req.VoteB
	if err := wire.VerifyBlockVote(voteA); err != nil {
		return wire.ValidatorEvidence{}, err
	}
	if err := wire.VerifyBlockVote(voteB); err != nil {
		return wire.ValidatorEvidence{}, err
	}
	if voteA.Height == 0 || voteA.Height != voteB.Height {
		return wire.ValidatorEvidence{}, errors.New("double vote evidence requires one non-zero height")
	}
	if voteA.ValidatorAddress == "" || voteA.ValidatorAddress != voteB.ValidatorAddress {
		return wire.ValidatorEvidence{}, errors.New("double vote evidence requires one validator address")
	}
	if voteA.ValidatorPublicKey == "" || voteA.ValidatorPublicKey != voteB.ValidatorPublicKey {
		return wire.ValidatorEvidence{}, errors.New("double vote evidence requires one validator public key")
	}
	if voteA.Power == 0 || voteA.Power != voteB.Power {
		return wire.ValidatorEvidence{}, errors.New("double vote evidence requires one voting power")
	}
	if voteA.BlockHash == voteB.BlockHash {
		return wire.ValidatorEvidence{}, errors.New("double vote evidence requires two block hashes")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	evidenceID := validatorEvidenceID(voteA, voteB)
	if existing, exists := s.data.ValidatorEvidence[evidenceID]; exists {
		return existing, nil
	}
	// Resolve operator address to owner.
	ownerAddr := s.resolveOperatorToOwner(voteA.ValidatorAddress)
	validator, ok := s.data.Validators[ownerAddr]
	if !ok || validator.Status != wire.ValidatorStatusActive {
		return wire.ValidatorEvidence{}, errors.New("double vote evidence target is not an active validator")
	}
	if validator.OperatorPublicKey != voteA.ValidatorPublicKey {
		return wire.ValidatorEvidence{}, errors.New("double vote evidence public key mismatch")
	}
	if validatorPower(validator) != voteA.Power {
		return wire.ValidatorEvidence{}, errors.New("double vote evidence power mismatch")
	}
	evidence := wire.ValidatorEvidence{
		EvidenceID:         evidenceID,
		Type:               doubleVoteEvidenceType,
		Height:             voteA.Height,
		ValidatorAddress:   voteA.ValidatorAddress,
		ValidatorPublicKey: voteA.ValidatorPublicKey,
		Power:              voteA.Power,
		FirstBlockHash:     voteA.BlockHash,
		SecondBlockHash:    voteB.BlockHash,
		FirstSignature:     voteA.Signature,
		SecondSignature:    voteB.Signature,
		Slashed:            validatorDoubleVoteSlashLocked(s.accountLocked(ownerAddr)),
		CreatedAtUnix:      time.Now().Unix(),
	}
	return evidence, nil
}

func evidenceVotes(evidence wire.ValidatorEvidence) (wire.BlockVote, wire.BlockVote) {
	common := wire.BlockVote{
		Height:             evidence.Height,
		ValidatorAddress:   evidence.ValidatorAddress,
		ValidatorPublicKey: evidence.ValidatorPublicKey,
		Power:              evidence.Power,
	}
	voteA := common
	voteA.BlockHash = evidence.FirstBlockHash
	voteA.Signature = evidence.FirstSignature
	voteB := common
	voteB.BlockHash = evidence.SecondBlockHash
	voteB.Signature = evidence.SecondSignature
	return voteA, voteB
}

func validatorEvidenceID(voteA wire.BlockVote, voteB wire.BlockVote) string {
	hashes := []string{voteA.BlockHash, voteB.BlockHash}
	sort.Strings(hashes)
	return hashString(doubleVoteEvidenceType + ":" + voteA.ValidatorAddress + ":" + strconv.FormatUint(voteA.Height, 10) + ":" + hashes[0] + ":" + hashes[1])
}

func validatorDoubleVoteSlashLocked(account wire.Account) uint64 {
	if account.LockedStake <= 1 {
		return account.LockedStake
	}
	slash := account.LockedStake / 2
	if slash == 0 {
		return 1
	}
	return slash
}
