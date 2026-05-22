package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"chain/internal/client"
	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

func (n *Node) StartAutoProver(chainURL string, interval time.Duration) {
	if chainURL == "" || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if err := n.provePendingChallenges(chainURL); err != nil {
				log.Printf("auto prover error: %v", err)
			}
		}
	}()
}

func (n *Node) StartAutoRepairer(chainURL string, interval time.Duration) {
	if chainURL == "" || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if err := n.repairPendingTasks(chainURL); err != nil {
				log.Printf("auto repair error: %v", err)
			}
		}
	}()
}

func (n *Node) StartAutoDeleter(chainURL string, interval time.Duration) {
	if chainURL == "" || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if err := n.deletePendingTasks(chainURL); err != nil {
				log.Printf("auto delete error: %v", err)
			}
		}
	}()
}

func (n *Node) StartProviderReporter(chainURL string, endpoint string, capacityBytes uint64, interval time.Duration, peerInfo func() (string, []string)) {
	if chainURL == "" || interval <= 0 {
		return
	}
	go func() {
		report := func() {
			peerID := ""
			var peerAddrs []string
			if peerInfo != nil {
				peerID, peerAddrs = peerInfo()
			}
			record, err := n.ProviderRecord(endpoint, capacityBytes, peerID, peerAddrs, 2*interval)
			if err != nil {
				log.Printf("provider report build failed: %v", err)
				return
			}
			if err := postJSON(chainURL, "/storage/providers", wire.StorageProviderAnnouncement{Provider: record}); err != nil {
				log.Printf("provider report submit failed: %v", err)
				return
			}
			log.Printf("provider report submitted shards=%d", record.ShardCount)
		}
		report()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			report()
		}
	}()
}

func (n *Node) provePendingChallenges(chainURL string) error {
	endpoint := strings.TrimRight(chainURL, "/") + "/challenges?pending=true&miner=" + url.QueryEscape(n.address)
	resp, err := http.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var list wire.ListChallengesResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return err
	}
	for _, challenge := range list.Challenges {
		proof, err := n.Prove(challenge)
		if err != nil {
			log.Printf("auto proof failed locally: challenge=%s error=%v", challenge.ChallengeID, err)
			continue
		}
		if err := postJSON(chainURL, "/proofs", wire.SubmitProofRequest{Proof: proof}); err != nil {
			log.Printf("auto proof submit failed: challenge=%s error=%v", challenge.ChallengeID, err)
			continue
		}
		log.Printf("auto proof submitted challenge=%s", challenge.ChallengeID)
	}
	return nil
}

func (n *Node) repairPendingTasks(chainURL string) error {
	endpoint := strings.TrimRight(chainURL, "/") + "/repairs?miner=" + url.QueryEscape(n.address)
	resp, err := http.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var list wire.RepairPlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return err
	}
	for _, task := range list.Tasks {
		if task.Assignment.MinerAddress != n.address {
			continue
		}
		receipt, err := n.repairTask(chainURL, task)
		if err != nil {
			log.Printf("auto repair failed locally: repair=%s error=%v", task.RepairID, err)
			continue
		}
		if err := postJSON(chainURL, "/batch-commits", wire.BatchCommitRequest{
			IntentID: task.IntentID,
			User:     receipt.User,
			Receipts: []wire.MinerReceipt{receipt},
		}); err != nil {
			log.Printf("auto repair commit failed: repair=%s error=%v", task.RepairID, err)
			continue
		}
		log.Printf("auto repair committed repair=%s intent=%s segment=%d shard=%d", task.RepairID, task.IntentID, task.SegmentID, task.ShardIndex)
	}
	return nil
}

func (n *Node) deletePendingTasks(chainURL string) error {
	query := url.Values{}
	query.Set("miner", n.address)
	query.Set("status", "pending")
	endpoint := strings.TrimRight(chainURL, "/") + "/intents/delete-tasks?" + query.Encode()
	resp, err := http.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var list wire.DeleteTaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return err
	}
	for _, task := range list.Tasks {
		if task.MinerAddress != n.address {
			continue
		}
		if task.RetainPhysical {
			log.Printf("auto delete retained shared shard task=%s intent=%s shard=%s refs=%d", task.TaskID, task.IntentID, task.ShardHash, task.ActiveReferences)
		} else {
			if err := n.DeleteShard(task.ShardHash); err != nil {
				log.Printf("auto delete failed locally: task=%s shard=%s error=%v", task.TaskID, task.ShardHash, err)
				continue
			}
		}
		receipt := wire.DeleteReceipt{
			IntentID:       task.IntentID,
			ShardHash:      task.ShardHash,
			MinerAddress:   n.address,
			MinerPublicKey: n.PublicKeyBase64(),
			DeletedAtUnix:  time.Now().Unix(),
		}
		if err := wire.SignDeleteReceipt(&receipt, n.privateKey); err != nil {
			log.Printf("auto delete receipt signing failed: task=%s error=%v", task.TaskID, err)
			continue
		}
		if err := postJSON(chainURL, "/delete-receipts", wire.SubmitDeleteReceiptRequest{Receipt: receipt}); err != nil {
			log.Printf("auto delete receipt submit failed: task=%s error=%v", task.TaskID, err)
			continue
		}
		log.Printf("auto delete completed task=%s intent=%s shard=%s", task.TaskID, task.IntentID, task.ShardHash)
	}
	return nil
}

func (n *Node) repairTask(chainURL string, task wire.RepairTask) (wire.MinerReceipt, error) {
	if task.Assignment.MinerAddress != n.address {
		return wire.MinerReceipt{}, errors.New("repair task is for a different miner")
	}
	if len(task.SourceReceipts) < task.RequiredShards {
		return wire.MinerReceipt{}, errors.New("not enough source receipts for repair")
	}
	totalShards := task.TargetShards
	if totalShards <= 0 {
		return wire.MinerReceipt{}, errors.New("repair task target shard count is required")
	}
	shards := make([][]byte, totalShards)
	available := 0
	user := ""
	fileRoot := ""
	segmentRoot := ""
	for _, receipt := range task.SourceReceipts {
		if receipt.ShardIndex < 0 || receipt.ShardIndex >= totalShards {
			continue
		}
		if user == "" {
			user = receipt.User
		}
		if fileRoot == "" {
			fileRoot = receipt.FileRoot
		}
		if segmentRoot == "" {
			segmentRoot = receipt.SegmentRoot
		}
		data, err := downloadSourceShard(n, chainURL, receipt)
		if err != nil {
			continue
		}
		if chaincrypto.HashBytes(data) != receipt.ShardHash {
			return wire.MinerReceipt{}, fmt.Errorf("source shard hash mismatch segment=%d shard=%d", receipt.SegmentID, receipt.ShardIndex)
		}
		shards[receipt.ShardIndex] = data
		available++
	}
	if available < task.RequiredShards {
		return wire.MinerReceipt{}, errors.New("not enough available source shards")
	}
	segmentSize := int(task.Assignment.ShardSize) * task.RequiredShards
	segmentData, err := client.DecodeShards(shards, task.RequiredShards, totalShards-task.RequiredShards, segmentSize)
	if err != nil {
		return wire.MinerReceipt{}, err
	}
	rebuilt, err := client.EncodeShards(segmentData, task.RequiredShards, totalShards-task.RequiredShards)
	if err != nil {
		return wire.MinerReceipt{}, err
	}
	if task.ShardIndex < 0 || task.ShardIndex >= len(rebuilt) {
		return wire.MinerReceipt{}, errors.New("repair shard index out of range")
	}
	shard := rebuilt[task.ShardIndex]
	shardHash := chaincrypto.HashBytes(shard)
	if task.Assignment.ShardHash != "" && shardHash != task.Assignment.ShardHash {
		return wire.MinerReceipt{}, errors.New("rebuilt shard hash mismatch")
	}
	receipt, err := n.Store(wire.UploadRequest{
		IntentID:    task.IntentID,
		User:        user,
		FileRoot:    fileRoot,
		SegmentID:   task.SegmentID,
		SegmentRoot: segmentRoot,
		ShardIndex:  task.ShardIndex,
		ShardID:     fmt.Sprintf("%s:%d:%d:auto-repair:%s", task.IntentID, task.SegmentID, task.ShardIndex, task.RepairID),
		ShardHash:   shardHash,
		ShardCID:    task.Assignment.ShardCID,
		ShardSize:   int64(len(shard)),
		DataBase64:  base64.StdEncoding.EncodeToString(shard),
	})
	if err != nil {
		return wire.MinerReceipt{}, err
	}
	return receipt, nil
}

func downloadSourceShard(node *Node, chainURL string, receipt wire.MinerReceipt) ([]byte, error) {
	if routes := discoverRepairRoutes(chainURL, receipt.ShardHash, receipt.ShardCID); len(routes) > 0 {
		if data, err := downloadSourceShardViaRoutes(node, receipt, routes); err == nil {
			return data, nil
		}
	}
	providers := RankProvidersForBlockFetch(discoverRepairProviders(chainURL, receipt.ShardHash, receipt.ShardCID))
	var lastErr error
	if receipt.ShardCID != "" {
		for _, provider := range providers {
			if !providerSupportsP2P(provider) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			data, err := FetchBlockViaLibP2P(ctx, receipt.ShardCID, provider.PeerID, provider.PeerAddrs)
			cancel()
			if err == nil {
				node.recordLibP2PFetchSuccess()
				RememberProviderFetchSuccess(provider, "libp2p")
				log.Printf("repair source fetch transport=libp2p miner=%s cid=%s", provider.MinerAddress, receipt.ShardCID)
				return data, nil
			}
			node.recordLibP2PFetchError()
			node.recordHTTPFallback()
			RememberProviderFetchFailure(provider, "libp2p", err)
			log.Printf("repair source fetch transport=libp2p miner=%s cid=%s fallback=http error=%v", provider.MinerAddress, receipt.ShardCID, err)
			lastErr = err
		}
	}
	endpoints := PreferredProviderEndpoints(receipt.MinerEndpoint, providers)
	for _, endpoint := range endpoints {
		provider := ResolveProviderRecordForEndpoint(endpoint, receipt.MinerAddress, providers)
		httpClient := client.NewHTTP(endpoint)
		if receipt.ShardCID != "" {
			data, err := httpClient.GetBytes("/blocks/" + receipt.ShardCID)
			if err == nil {
				node.recordHTTPBlockFetchHit()
				RememberProviderFetchSuccess(provider, "http-block")
				log.Printf("repair source fetch transport=http-block endpoint=%s cid=%s", endpoint, receipt.ShardCID)
				return data, nil
			}
			RememberProviderFetchFailure(provider, "http-block", err)
			lastErr = err
		}
		data, err := httpClient.GetBytes("/shards/" + receipt.ShardHash + ".bin")
		if err == nil {
			node.recordHTTPShardFetchHit()
			RememberProviderFetchSuccess(provider, "http-shard")
			log.Printf("repair source fetch transport=http-shard endpoint=%s hash=%s", endpoint, receipt.ShardHash)
			return data, nil
		}
		RememberProviderFetchFailure(provider, "http-shard", err)
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("source receipt has no reachable provider endpoint")
	}
	return nil, lastErr
}

func downloadSourceShardViaRoutes(node *Node, receipt wire.MinerReceipt, routes []wire.StorageRoute) ([]byte, error) {
	var lastErr error
	for _, route := range routes {
		provider := providerRecordFromRoute(route)
		switch route.Transport {
		case "libp2p":
			if route.ShardCID == "" || route.PeerID == "" || len(route.PeerAddrs) == 0 {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			data, err := FetchBlockViaLibP2P(ctx, route.ShardCID, route.PeerID, route.PeerAddrs)
			cancel()
			if err == nil {
				node.recordLibP2PFetchSuccess()
				RememberProviderFetchSuccess(provider, "libp2p")
				log.Printf("repair source fetch route=libp2p miner=%s cid=%s", route.MinerAddress, route.ShardCID)
				return data, nil
			}
			node.recordLibP2PFetchError()
			node.recordHTTPFallback()
			RememberProviderFetchFailure(provider, "libp2p", err)
			lastErr = err
		case "http-block":
			if route.Endpoint == "" || route.ShardCID == "" {
				continue
			}
			data, err := client.NewHTTP(route.Endpoint).GetBytes("/blocks/" + route.ShardCID)
			if err == nil {
				node.recordHTTPBlockFetchHit()
				RememberProviderFetchSuccess(provider, "http-block")
				log.Printf("repair source fetch route=http-block endpoint=%s cid=%s", route.Endpoint, route.ShardCID)
				return data, nil
			}
			RememberProviderFetchFailure(provider, "http-block", err)
			lastErr = err
		case "http-shard":
			shardHash := route.ShardHash
			if shardHash == "" {
				shardHash = receipt.ShardHash
			}
			if route.Endpoint == "" || shardHash == "" {
				continue
			}
			data, err := client.NewHTTP(route.Endpoint).GetBytes("/shards/" + shardHash + ".bin")
			if err == nil {
				node.recordHTTPShardFetchHit()
				RememberProviderFetchSuccess(provider, "http-shard")
				log.Printf("repair source fetch route=http-shard endpoint=%s hash=%s", route.Endpoint, shardHash)
				return data, nil
			}
			RememberProviderFetchFailure(provider, "http-shard", err)
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("source receipt has no reachable provider route")
	}
	return nil, lastErr
}

func discoverRepairRoutes(chainURL string, shardHash string, shardCID string) []wire.StorageRoute {
	if chainURL == "" || (shardHash == "" && shardCID == "") {
		return nil
	}
	query := url.Values{}
	if shardHash != "" {
		query.Set("shard_hash", shardHash)
	}
	if shardCID != "" {
		query.Set("shard_cid", shardCID)
	}
	var resp wire.StorageRoutesResponse
	if err := client.NewHTTP(chainURL).Get("/storage/routes?"+query.Encode(), &resp); err != nil {
		return nil
	}
	return resp.Routes
}

func providerRecordFromRoute(route wire.StorageRoute) wire.StorageProviderRecord {
	return wire.StorageProviderRecord{
		MinerAddress:       route.MinerAddress,
		Endpoint:           route.Endpoint,
		PeerID:             route.PeerID,
		PeerAddrs:          append([]string(nil), route.PeerAddrs...),
		ShardHashes:        []string{route.ShardHash},
		HealthScoreBPS:     route.HealthScoreBPS,
		ProviderRecordLive: route.ProviderRecordLive,
		ProviderSource:     route.ProviderSource,
	}
}

func discoverRepairProviders(chainURL string, shardHash string, shardCID string) []wire.StorageProviderRecord {
	if chainURL == "" || (shardHash == "" && shardCID == "") {
		return nil
	}
	query := url.Values{}
	if shardHash != "" {
		query.Set("shard_hash", shardHash)
	}
	if shardCID != "" {
		query.Set("shard_cid", shardCID)
	}
	var resp wire.StorageProvidersResponse
	if err := client.NewHTTP(chainURL).Get("/storage/providers?"+query.Encode(), &resp); err != nil {
		return nil
	}
	return resp.Providers
}
