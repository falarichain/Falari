package indexer

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"chain/internal/client"
	"chain/internal/wire"
)

type BlockEntry struct {
	Height   uint64   `json:"height"`
	Hash     string   `json:"hash"`
	TimeUnix int64    `json:"time_unix"`
	TxIDs    []string `json:"tx_ids"`
}

type DealIndex struct {
	IntentID    string   `json:"intent_id"`
	FileName    string   `json:"file_name"`
	User        string   `json:"user"`
	Status      string   `json:"status"`
	FileSize    int64    `json:"file_size"`
	CIDSegments []string `json:"cid_segments,omitempty"`
	Miners      []string `json:"miners,omitempty"`
	BlockHeight uint64   `json:"block_height"`
}

type CIDIndex struct {
	CID         string `json:"cid"`
	IntentID    string `json:"intent_id"`
	MinerAddrs  []string `json:"miner_addrs"`
	LastSeenAt  int64  `json:"last_seen_at_unix"`
}

type Indexer struct {
	mu      sync.RWMutex
	chainURL string

	blocks        []BlockEntry
	blocksByHash  map[string]uint64
	dealsByIntent map[string]*DealIndex
	cids          map[string]*CIDIndex
	minerHealth   map[string]*wire.MinerStats

	latestHeight  uint64
	lastSyncAt    int64
}

func New(chainURL string) *Indexer {
	return &Indexer{
		chainURL:      chainURL,
		blocksByHash:  map[string]uint64{},
		dealsByIntent: map[string]*DealIndex{},
		cids:          map[string]*CIDIndex{},
		minerHealth:   map[string]*wire.MinerStats{},
	}
}

func (ix *Indexer) Start(interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if err := ix.sync(); err != nil {
				log.Printf("indexer sync error: %v", err)
			}
		}
	}()
}

func (ix *Indexer) sync() error {
	status, err := ix.fetchStatus()
	if err != nil {
		return err
	}
	now := time.Now().Unix()

	ix.mu.Lock()
	defer ix.mu.Unlock()

	currentHeight := ix.latestHeight
	for h := currentHeight + 1; h <= status.Height && h <= currentHeight+100; h++ {
		block, err := ix.fetchBlock(h)
		if err != nil {
			log.Printf("indexer fetch block %d error: %v", h, err)
			break
		}
		ix.blocks = append(ix.blocks, BlockEntry{
			Height:   block.Height,
			Hash:     block.Hash,
			TimeUnix: block.TimeUnix,
			TxIDs:    txIDs(block.Transactions),
		})
		ix.blocksByHash[block.Hash] = block.Height
		ix.latestHeight = block.Height
	}

	ix.indexDealsFromChain()
	ix.indexProvidersFromChain()
	ix.indexMinerHealthFromChain()
	ix.lastSyncAt = now
	return nil
}

func (ix *Indexer) fetchStatus() (wire.ChainStatusResponse, error) {
	var status wire.ChainStatusResponse
	if err := client.NewHTTP(ix.chainURL).Get("/status", &status); err != nil {
		return wire.ChainStatusResponse{}, err
	}
	return status, nil
}

func (ix *Indexer) fetchBlock(height uint64) (wire.Block, error) {
	var block wire.Block
	if err := client.NewHTTP(ix.chainURL).Get("/blocks/"+u64toa(height), &block); err != nil {
		return wire.Block{}, err
	}
	return block, nil
}

func (ix *Indexer) indexDealsFromChain() {
	var resp struct {
		Intents []wire.IntentView `json:"intents"`
	}
	if err := client.NewHTTP(ix.chainURL).Get("/intents", &resp); err != nil {
		log.Printf("indexer fetch intents error: %v", err)
		return
	}
	for _, intent := range resp.Intents {
		existing, ok := ix.dealsByIntent[intent.IntentID]
		if ok && existing.Status == intent.Status {
			continue
		}
		cids := make([]string, 0)
		for _, seg := range intent.Segments {
			cids = append(cids, seg.ShardCIDs...)
		}
		miners := make([]string, 0)
		for _, a := range intent.Assignments {
			miners = append(miners, a.MinerAddress)
		}
		ix.dealsByIntent[intent.IntentID] = &DealIndex{
			IntentID:    intent.IntentID,
			FileName:    intent.FileName,
			User:        intent.User,
			Status:      intent.Status,
			FileSize:    intent.FileSize,
			CIDSegments: cids,
			Miners:      miners,
		}
		for _, cid := range cids {
			if _, ok := ix.cids[cid]; !ok {
				ix.cids[cid] = &CIDIndex{CID: cid, IntentID: intent.IntentID, LastSeenAt: time.Now().Unix()}
			}
			ix.cids[cid].LastSeenAt = time.Now().Unix()
		}
	}
}

func (ix *Indexer) indexProvidersFromChain() {
	var resp struct {
		Providers []wire.StorageProviderRecord `json:"providers"`
	}
	if err := client.NewHTTP(ix.chainURL).Get("/storage/providers", &resp); err != nil {
		log.Printf("indexer fetch providers error: %v", err)
		return
	}
	for _, provider := range resp.Providers {
		for _, shard := range provider.Shards {
			if shard.ShardCID == "" {
				continue
			}
			if entry, ok := ix.cids[shard.ShardCID]; ok {
				found := false
				for _, addr := range entry.MinerAddrs {
					if addr == provider.MinerAddress {
						found = true
						break
					}
				}
				if !found {
					entry.MinerAddrs = append(entry.MinerAddrs, provider.MinerAddress)
				}
			}
		}
	}
}

func (ix *Indexer) indexMinerHealthFromChain() {
}

func (ix *Indexer) SearchDeals(query string) []DealIndex {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	results := make([]DealIndex, 0)
	lower := strings.ToLower(query)
	for _, deal := range ix.dealsByIntent {
		if strings.Contains(strings.ToLower(deal.IntentID), lower) ||
			strings.Contains(strings.ToLower(deal.FileName), lower) ||
			strings.Contains(strings.ToLower(deal.User), lower) {
			results = append(results, *deal)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].IntentID < results[j].IntentID
	})
	if len(results) > 100 {
		results = results[:100]
	}
	return results
}

func (ix *Indexer) SearchCIDs(query string) []CIDIndex {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	results := make([]CIDIndex, 0)
	for _, cid := range ix.cids {
		if strings.Contains(cid.CID, query) || strings.Contains(cid.IntentID, query) {
			results = append(results, *cid)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].CID < results[j].CID
	})
	if len(results) > 100 {
		results = results[:100]
	}
	return results
}

func (ix *Indexer) Status() map[string]any {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return map[string]any{
		"latest_height":   ix.latestHeight,
		"blocks_indexed":  len(ix.blocks),
		"deals_indexed":   len(ix.dealsByIntent),
		"cids_indexed":    len(ix.cids),
		"last_sync_at":    ix.lastSyncAt,
	}
}

func (ix *Indexer) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, ix.Status())
	})
	mux.HandleFunc("GET /search/deals", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		writeJSON(w, http.StatusOK, ix.SearchDeals(q))
	})
	mux.HandleFunc("GET /search/cids", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		writeJSON(w, http.StatusOK, ix.SearchCIDs(q))
	})
	return mux
}

func txIDs(txs []wire.Transaction) []string {
	ids := make([]string, len(txs))
	for i, tx := range txs {
		ids[i] = tx.TxID
	}
	return ids
}

func u64toa(v uint64) string {
	if v == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	return string(buf)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
