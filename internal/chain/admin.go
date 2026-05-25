package chain

// GetMiningParams returns the current mining parameters.
func (s *Store) GetMiningParams() MiningParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.MiningParams == nil {
		return DefaultMiningParams()
	}
	return *s.data.MiningParams
}

func applyIfNonZero(target *uint64, source uint64) {
	if source != 0 {
		*target = source
	}
}
