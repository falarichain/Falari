package chain

import "chain/internal/wire"

// recordProposerTurnLocked records whether a validator successfully produced
// its assigned block turn. Uses a ring buffer bounded by AvailabilityWindowSize.
// Caller must hold s.mu.
func (s *Store) recordProposerTurnLocked(address string, produced bool) {
	params := s.miningParamsLocked()
	windowSize := params.AvailabilityWindowSize
	if windowSize == 0 {
		windowSize = 7200
	}

	if s.data.ProposerTurns == nil {
		s.data.ProposerTurns = map[string]*wire.ValidatorTurnWindow{}
	}

	window, ok := s.data.ProposerTurns[address]
	if !ok {
		window = &wire.ValidatorTurnWindow{}
		s.data.ProposerTurns[address] = window
	}

	if uint64(len(window.Turns)) < windowSize {
		// Ring buffer not yet full — append.
		window.Turns = append(window.Turns, produced)
	} else {
		// Ring buffer full — overwrite oldest entry at Head.
		old := window.Turns[window.Head]
		if old {
			if window.Successes > 0 {
				window.Successes--
			}
		} else {
			if window.Misses > 0 {
				window.Misses--
			}
		}
		window.Turns[window.Head] = produced
	}

	if produced {
		window.Successes++
	} else {
		window.Misses++
	}
	window.Head = (window.Head + 1) % int(windowSize)
}

// availabilityScoreLocked returns the availability score for a validator
// in basis points (0–10000). New validators with no turn data get 10000 (100%).
// Caller must hold s.mu.
func (s *Store) availabilityScoreLocked(address string) uint64 {
	if s.data.ProposerTurns == nil {
		return 10000
	}
	window, ok := s.data.ProposerTurns[address]
	if !ok {
		return 10000
	}
	total := window.Successes + window.Misses
	if total == 0 {
		return 10000
	}
	return window.Successes * 10000 / total
}

// effectivePowerLocked returns the validator's effective power for proposer
// selection, which is stakePower × availabilityScore.
// Caller must hold s.mu.
func (s *Store) effectivePowerLocked(address string) uint64 {
	rawPower := s.validatorPowerLocked(address)
	if rawPower == 0 {
		return 0
	}
	score := s.availabilityScoreLocked(address)
	return rawPower * score / 10000
}
