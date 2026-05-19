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
