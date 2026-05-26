package chain

import (
	"log"
	"time"
)

func (s *Store) StartBlockProducer(interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			resp, err := s.ProduceBlock()
			if err != nil {
				log.Printf("produce block failed: %v", err)
				continue
			}
			if resp.Produced {
				log.Printf("produced block height=%d txs=%d hash=%s", resp.Block.Height, len(resp.Block.Transactions), resp.Block.Hash)
			}
		}
	}()
}

// StartBlockTimeoutChecker periodically checks whether the current block
// proposer has timed out and advances the consensus round if needed.
func (s *Store) StartBlockTimeoutChecker(interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			s.mu.Lock()
			timeout, _ := s.checkBlockTimeoutLocked(time.Now().Unix())
			if timeout {
				if err := s.saveLocked(); err != nil {
					log.Printf("save after block timeout failed: %v", err)
				}
			}
			s.mu.Unlock()
		}
	}()
}
