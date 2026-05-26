package chain

import (
	"sort"
	"time"

	"chain/internal/wire"
)

const defaultMaxConsensusValidators = 21

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

	params := s.miningParamsLocked()
	maxValidators := params.MaxConsensusValidators
	if maxValidators == 0 {
		maxValidators = defaultMaxConsensusValidators
	}
	minValidators := params.MinConsensusValidators
	if minValidators == 0 {
		minValidators = 2
	}
	availThreshold := params.AvailabilityThresholdBPS
	if availThreshold == 0 {
		availThreshold = 6000
	}

	candidates := s.activeValidatorCandidatesLocked()
	if len(candidates) == 0 {
		return validatorRotationTxPayload{EpochRound: epochRound, TotalActive: 0}
	}

	// Sort by effective power (stake × availability), then ProducedBlocks, then address.
	sort.SliceStable(candidates, func(i, j int) bool {
		leftPower := s.effectivePowerLocked(candidates[i].OwnerAddress)
		rightPower := s.effectivePowerLocked(candidates[j].OwnerAddress)
		if leftPower != rightPower {
			return leftPower > rightPower
		}
		if candidates[i].ProducedBlocks != candidates[j].ProducedBlocks {
			return candidates[i].ProducedBlocks > candidates[j].ProducedBlocks
		}
		return candidates[i].OwnerAddress < candidates[j].OwnerAddress
	})

	// Select top candidates, filtering by availability threshold.
	newSet := map[string]bool{}
	added := make([]string, 0)
	for _, candidate := range candidates {
		if uint64(len(newSet)) >= maxValidators {
			break
		}
		score := s.availabilityScoreLocked(candidate.OwnerAddress)
		if score < availThreshold && uint64(len(newSet)) >= minValidators {
			continue
		}
		newSet[candidate.OwnerAddress] = true
		if !previousSet[candidate.OwnerAddress] {
			added = append(added, candidate.OwnerAddress)
		}
	}

	// Safety: ensure minimum validators even if all are below threshold.
	if uint64(len(newSet)) < minValidators {
		for _, candidate := range candidates {
			if uint64(len(newSet)) >= minValidators {
				break
			}
			if !newSet[candidate.OwnerAddress] {
				newSet[candidate.OwnerAddress] = true
				if !previousSet[candidate.OwnerAddress] {
					added = append(added, candidate.OwnerAddress)
				}
			}
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
