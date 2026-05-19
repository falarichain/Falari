package chain

import (
	"sort"
	"time"

	"chain/internal/wire"
)

const maxConsensusValidators = 21

type validatorRotationTxPayload struct {
	EpochRound    uint64   `json:"epoch_round"`
	Added         []string `json:"added"`
	Removed       []string `json:"removed"`
	TotalActive   int      `json:"total_active"`
	RotatedAtUnix int64    `json:"rotated_at_unix"`
}

func (s *Store) rotateValidatorsLocked(epochRound uint64) validatorRotationTxPayload {
	previous := s.consensusValidatorAddressesLocked()
	previousSet := map[string]bool{}
	for _, address := range previous {
		previousSet[address] = true
	}

	candidates := s.activeValidatorCandidatesLocked()
	if len(candidates) == 0 {
		return validatorRotationTxPayload{EpochRound: epochRound, TotalActive: 0}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		leftPower := validatorPower(candidates[i])
		rightPower := validatorPower(candidates[j])
		if leftPower != rightPower {
			return leftPower > rightPower
		}
		if candidates[i].ProducedBlocks != candidates[j].ProducedBlocks {
			return candidates[i].ProducedBlocks > candidates[j].ProducedBlocks
		}
		return candidates[i].Address < candidates[j].Address
	})

	newSet := map[string]bool{}
	added := make([]string, 0)
	for i, candidate := range candidates {
		if i >= maxConsensusValidators {
			break
		}
		newSet[candidate.Address] = true
		if !previousSet[candidate.Address] {
			added = append(added, candidate.Address)
		}
	}

	removed := make([]string, 0)
	for _, address := range previous {
		if !newSet[address] {
			removed = append(removed, address)
		}
	}

	s.data.ConsensusValidators = newSet
	payload := validatorRotationTxPayload{
		EpochRound:    epochRound,
		Added:         added,
		Removed:       removed,
		TotalActive:   len(candidates),
		RotatedAtUnix: time.Now().Unix(),
	}
	s.recordTxLocked("validator_rotation", "", payload)
	return payload
}

func (s *Store) activeValidatorCandidatesLocked() []wire.ValidatorInfo {
	candidates := make([]wire.ValidatorInfo, 0, len(s.data.Validators))
	for _, validator := range s.data.Validators {
		if validator.Status != wire.ValidatorStatusActive {
			continue
		}
		candidates = append(candidates, validator)
	}
	return candidates
}
