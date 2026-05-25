package dht

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"chain/internal/wire"
)

// PublishShard announces a single shard to the DHT using the provider mechanism.
// It converts the shard hash to a CID and calls Provide to register this node
// as a provider for that content.
func (s *Service) PublishShard(record wire.DHTProviderRecord) error {
	if s == nil || s.dht == nil {
		return nil
	}
	c, err := shardHashToCID(record.ShardHash)
	if err != nil {
		return err
	}
	// Store the signed record as a DHT value for rich metadata lookup.
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	ctx, cancel := contextWithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	if err := s.dht.PutValue(ctx, "/falari/shard/"+record.ShardHash, data); err != nil {
		log.Printf("dht: put value for shard %s failed: %v", record.ShardHash, err)
	}
	// Also register as a provider for the shard CID (standard DHT provider mechanism).
	if err := s.dht.Provide(ctx, c, true); err != nil {
		log.Printf("dht: provide shard %s failed: %v", record.ShardHash, err)
		return err
	}
	s.AddShard(record.ShardHash)
	return nil
}

// PublishAllShards re-publishes all shards held by this node.
// Called periodically (default every 60s) to keep records fresh.
func (s *Service) PublishAllShards(builder func(shardHash string) (wire.DHTProviderRecord, error)) {
	s.mu.RLock()
	hashes := make([]string, 0, len(s.shardIndex))
	for h := range s.shardIndex {
		hashes = append(hashes, h)
	}
	s.mu.RUnlock()

	published := 0
	for _, hash := range hashes {
		record, err := builder(hash)
		if err != nil {
			log.Printf("dht: build record for shard %s failed: %v", hash, err)
			continue
		}
		if err := s.PublishShard(record); err != nil {
			log.Printf("dht: publish shard %s failed: %v", hash, err)
			continue
		}
		published++
	}
	if published > 0 {
		log.Printf("dht: republished %d/%d shard records", published, len(hashes))
	}
}

// StartPublishLoop starts a background goroutine that periodically republishes all shards.
func (s *Service) StartPublishLoop(builder func(shardHash string) (wire.DHTProviderRecord, error)) {
	go func() {
		ticker := time.NewTicker(s.cfg.RepublishInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.PublishAllShards(builder)
			}
		}
	}()
}

func contextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeout)
}
