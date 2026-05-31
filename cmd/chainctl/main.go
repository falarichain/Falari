package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	chainpkg "chain/internal/chain"
	"chain/internal/client"
	"chain/internal/consensus"
	chaincrypto "chain/internal/crypto"
	falaridht "chain/internal/dht"
	"chain/internal/reward"
	"chain/internal/storage"
	"chain/internal/wire"

	libp2p "github.com/libp2p/go-libp2p"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "create-intent":
		createIntent(os.Args[2:])
	case "status":
		showStatus(os.Args[2:])
	case "manifest":
		exportManifest(os.Args[2:])
	case "collection-create":
		createCollection(os.Args[2:])
	case "collection-append":
		appendCollectionRecord(os.Args[2:])
	case "collection-records":
		listCollectionRecords(os.Args[2:])
	case "collection-download":
		downloadCollection(os.Args[2:])
	case "record":
		showRecord(os.Args[2:])
	case "upload":
		upload(os.Args[2:])
	case "finalize":
		finalize(os.Args[2:])
	case "settle-intent":
		settleIntent(os.Args[2:])
	case "renew":
		renewDeal(os.Args[2:])
	case "permanent-fund":
		topUpPermanentFund(os.Args[2:])
	case "terminate-deal":
		terminateDeal(os.Args[2:])
	case "delete-tasks":
		listDeleteTasks(os.Args[2:])
	case "access-policy":
		setAccessPolicy(os.Args[2:])
	case "governance-keygen":
		governanceKeygen(os.Args[2:])
	case "governance-propose":
		governancePropose(os.Args[2:])
	case "governance-vote":
		governanceVote(os.Args[2:])
	case "governance-execute":
		governanceExecute(os.Args[2:])
	case "governance-proposals":
		governanceProposals(os.Args[2:])
	case "governance-operators":
		governanceOperatorsList(os.Args[2:])
	case "governance-audit":
		listGovernanceAudit(os.Args[2:])
	case "consensus":
		consensusState(os.Args[2:])
	case "consensus-votes":
		consensusVotes(os.Args[2:])
	case "set-upgrade":
		setUpgrade(os.Args[2:])
	case "retrieval-receipt":
		submitRetrievalReceipt(os.Args[2:])
	case "download":
		download(os.Args[2:])
	case "repair":
		repair(os.Args[2:])
	case "prove":
		prove(os.Args[2:])
	case "epoch":
		runEpoch(os.Args[2:])
	case "finalize-epoch":
		finalizeEpoch(os.Args[2:])
	case "miner":
		minerStats(os.Args[2:])
	case "balance":
		balance(os.Args[2:])
	case "transfer":
		transfer(os.Args[2:])
	case "account-new":
		accountNew(os.Args[2:])
	case "intent":
		showIntent(os.Args[2:])
	case "block":
		showBlock(os.Args[2:])
	case "mempool":
		showMempool(os.Args[2:])
	case "produce-block":
		produceBlock(os.Args[2:])
	case "vote-block":
		voteBlock(os.Args[2:])
	case "agent-key":
		agentKeyCommand(os.Args[2:])
	case "validators":
		listValidators(os.Args[2:])
	case "peers":
		listPeers(os.Args[2:])
	case "storage-providers":
		storageProviders(os.Args[2:])
	case "mine":
		mine(os.Args[2:])
	case "gc":
		garbageCollect(os.Args[2:])
	case "genesis":
		genesisCommand(os.Args[2:])
	case "fee-market":
		feeMarketCommand(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func createIntent(args []string) {
	fs := flag.NewFlagSet("create-intent", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	filePath := fs.String("file", "", "file to store")
	outPath := fs.String("out", "./upload-plan.json", "upload plan path")
	user := fs.String("user", "user_demo", "user address")
	accountKeyPath := fs.String("account-key", "", "account key file used to sign the intent")
	segmentSize := fs.Int64("segment-size", 4*1024*1024, "segment size in bytes")
	dataShards := fs.Int("data-shards", 4, "erasure data shard count")
	parityShards := fs.Int("parity-shards", 2, "erasure parity shard count")
	storageClass := fs.String("class", "permanent", "storage class")
	lockedFee := fs.Uint64("fee", 0, "locked storage fee")
	encrypt := fs.Bool("encrypt", false, "encrypt file locally before erasure coding and upload")
	encryptionKey := fs.String("key", "", "encryption key file or raw base64/hex key")
	encryptionKeyOut := fs.String("key-out", "./storage.key", "path to write generated encryption key")
	fs.Parse(args)

	if *filePath == "" {
		log.Fatal("-file is required")
	}
	if *accountKeyPath == "" {
		log.Fatal("-account-key is required")
	}
	accountKey, err := loadAccountKey(*accountKeyPath)
	if err != nil {
		log.Fatal(err)
	}
	if *user == "" || *user == "user_demo" {
		*user = accountKey.Address
	} else if !strings.EqualFold(*user, accountKey.Address) {
		log.Fatalf("-user %s does not match -account-key address %s", *user, accountKey.Address)
	}
	var size int64
	var planSegmentSize int64
	var segments []wire.SegmentPlan
	var roots []string
	var fileRoot string
	var encryption *wire.EncryptionMetadata
	var generatedKey []byte
	var encKey []byte
	if *encrypt {
		key := []byte(nil)
		if *encryptionKey != "" {
			key, err = readEncryptionKey(*encryptionKey)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			key, err = client.GenerateEncryptionKey()
			if err != nil {
				log.Fatal(err)
			}
			generatedKey = key
		}
		encKey = key
		nonce, err := client.GenerateEncryptionNonce()
		if err != nil {
			log.Fatal(err)
		}
		size, planSegmentSize, encryption, segments, roots, fileRoot, err = client.ComputeEncryptedErasurePlan(*filePath, *segmentSize, *dataShards, *parityShards, key, nonce)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		size, segments, roots, fileRoot, err = client.ComputeErasurePlan(*filePath, *segmentSize, *dataShards, *parityShards)
		if err != nil {
			log.Fatal(err)
		}
		planSegmentSize = *segmentSize
	}
	fee := *lockedFee
	erasure := wire.ErasurePolicy{
		DataShards:   *dataShards,
		ParityShards: *parityShards,
	}
	// Compute cross-parity repair pools.
	var repairPools []wire.RepairPool
	if len(segments) >= 2 {
		if *encrypt {
			repairPools, err = client.ComputeRepairPoolsEncrypted(*filePath, *segmentSize, encKey, *encryption, segments, *dataShards, *parityShards)
		} else {
			repairPools, err = client.ComputeRepairPools(*filePath, planSegmentSize, segments, *dataShards, *parityShards)
		}
		if err != nil {
			log.Fatal(err)
		}
	}
	policy := wire.StoragePolicy{
		Class:      *storageClass,
		Redundancy: fmt.Sprintf("reed-solomon-%d-%d", *dataShards, *parityShards),
		Renewable:  true,
	}
	if fee == 0 {
		var quote wire.StorageQuoteResponse
		err := client.NewHTTP(*chainURL).Post("/storage/quote", wire.StorageQuoteRequest{
			FileSize: size,
			Erasure: wire.ErasurePolicy{
				DataShards:   *dataShards,
				ParityShards: *parityShards,
			},
			Policy: policy,
		}, &quote)
		if err != nil {
			fee = estimateFee(size)
		} else {
			fee = quote.RequiredFee
		}
	}
	req := wire.CreateIntentRequest{
		ChainID:      chainID(*chainURL),
		User:         *user,
		FileName:     filepath.Base(*filePath),
		FileSize:     size,
		SegmentSize:  planSegmentSize,
		FileRoot:     fileRoot,
		SegmentRoots: roots,
		Segments:     segments,
		RepairPools:  repairPools,
		Erasure:      erasure,
		Encryption:   encryption,
		Policy:       policy,
		LockedFee:    fee,
		DeadlineUnix: time.Now().Add(24 * time.Hour).Unix(),
		Nonce:        accountNonce(*chainURL, accountKey.Address),
	}
	if err := wire.SignCreateIntent(&req, accountKey.PrivateKey); err != nil {
		log.Fatal(err)
	}

	var resp wire.CreateIntentResponse
	if err := client.NewHTTP(*chainURL).Post("/intents", req, &resp); err != nil {
		log.Fatal(err)
	}

	plan := wire.UploadPlan{
		IntentID:     resp.IntentID,
		User:         *user,
		FileName:     filepath.Base(*filePath),
		FileSize:     size,
		SegmentSize:  planSegmentSize,
		FileRoot:     fileRoot,
		SegmentRoots: roots,
		Segments:     segments,
		RepairPools:  repairPools,
		Assignments:  resp.Assignments,
		Erasure:      erasure,
		Encryption:   encryption,
		Policy:       policy,
		LockedFee:    resp.LockedFee,
	}
	if err := client.SavePlan(*outPath, plan); err != nil {
		log.Fatal(err)
	}
	if len(generatedKey) > 0 {
		if err := os.WriteFile(*encryptionKeyOut, []byte(client.FormatEncryptionKey(generatedKey)+"\n"), 0o600); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("encryption key saved to %s\n", *encryptionKeyOut)
	}
	fmt.Printf("created intent %s with %d segments, file root %s, locked_fee=%s required_fee=%s\n",
		resp.IntentID, len(roots), fileRoot, formatGF(resp.LockedFee), formatGF(resp.RequiredFee))
}

func showStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	storageURLs := fs.String("storage", "", "optional comma-separated storage node URLs")
	fs.Parse(args)

	var status wire.ChainStatusResponse
	if err := client.NewHTTP(*chainURL).Get("/status", &status); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("chain status=%s height=%d finalized=%d pending_txs=%d base_fee=%s\n",
		status.Status, status.Height, status.LatestFinalizedHeight, status.PendingTransactions, formatGF(status.FeeMarket.BaseFee))
	fmt.Printf("storage active_miners=%d/%d capacity=%s used=%s reserved=%s pending_repairs=%d pending_challenges=%d\n",
		status.ActiveMiners, status.Miners, formatBytes(status.CapacityBytes), formatBytes(status.UsedBytes),
		formatBytes(status.ReservedBytes), status.PendingRepairTasks, status.PendingChallenges)
	fmt.Printf("deals at_risk=%d critical=%d\n", status.DealsAtRisk, status.DealsCritical)
	fmt.Printf("rewards storage=%s retrieval=%s repair=%s slashed=%s\n",
		formatGF(status.TotalStorageRewards), formatGF(status.TotalRetrievalRewards), formatGF(status.TotalRepairRewards), formatGF(status.TotalSlashed))
	fmt.Printf("pools storage=%s retrieval=%s validator=%s permanent_fund=%s released=%s supply=%s\n",
		formatGF(status.StoragePoolRemaining), formatGF(status.RetrievalPoolRemaining), formatGF(status.ValidatorPoolRemaining),
		formatGF(status.PermanentFundRemaining), formatGF(status.TokensReleased), formatGF(status.TotalSupply))
	fmt.Printf("retrieval receipts=%d bytes=%s\n", status.RetrievalReceipts, formatBytes(status.RetrievalBytes))
	fmt.Printf("intents total=%d uploading=%d partial=%d finalized=%d expired=%d deals=%d\n",
		status.Intents, status.UploadingIntents, status.PartialIntents, status.FinalizedIntents, status.ExpiredIntents, status.Deals)
	fmt.Printf("collections total=%d records=%d\n", status.Collections, status.DataRecords)
	fmt.Printf("validators total=%d consensus=%d epoch_round=%d epochs_finalized=%d peers=%d libp2p=%t\n",
		status.Validators, status.ConsensusValidators, status.EpochRound, status.EpochsFinalized, status.PeerCount, status.LibP2PEnabled)
	if status.LatestBlockHash != "" {
		fmt.Printf("latest_block hash=%s time=%d\n", status.LatestBlockHash, status.LatestBlockTimeUnix)
	}

	for _, endpoint := range parseEndpoints(*storageURLs) {
		var storageStatus wire.StorageNodeStatusResponse
		if err := client.NewHTTP(endpoint).Get("/status", &storageStatus); err != nil {
			fmt.Printf("storage_node endpoint=%s error=%v\n", endpoint, err)
			continue
		}
		fmt.Printf("storage_node endpoint=%s address=%s shards=%d stored=%s\n",
			endpoint, storageStatus.Address, storageStatus.ShardCount, formatBytes(storageStatus.StoredBytes))
		stats := storageStatus.TransportStats
		fmt.Printf("storage_transport endpoint=%s fetch_p2p=%d fetch_p2p_errors=%d http_fallbacks=%d http_block=%d http_shard=%d serve_p2p=%d serve_http_block=%d serve_http_shard=%d\n",
			endpoint, stats.LibP2PFetchSuccess, stats.LibP2PFetchErrors, stats.HTTPFallbacks,
			stats.HTTPBlockFetchHits, stats.HTTPShardFetchHits, stats.LibP2PServeHits,
			stats.HTTPBlockServeHits, stats.HTTPShardServeHits)
		for _, memory := range storageStatus.RecentProviderMemories {
			fmt.Printf("storage_provider_memory endpoint=%s provider=%s outcome=%s transport=%s failures=%d cooldown_until=%d updated=%d peer=%s miner=%s error=%s\n",
				endpoint, memory.ProviderKey, memory.LastOutcome, memory.LastTransport,
				memory.ConsecutiveFailures, memory.CooldownUntilUnix, memory.UpdatedAtUnix,
				memory.PeerID, memory.MinerAddress, memory.LastError)
		}
	}
}

func exportManifest(args []string) {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("intent", "", "intent id")
	outPath := fs.String("out", "./download-plan.json", "path to write manifest upload plan")
	noCache := fs.Bool("no-cache", false, "skip populating provider cache (faster export)")
	fs.Parse(args)

	if *intentID == "" {
		log.Fatal("-intent is required")
	}
	manifest, err := fetchManifest(*chainURL, *intentID)
	if err != nil {
		log.Fatal(err)
	}
	if !*noCache {
		// Collect unique shard hashes from receipts.
		shardHashes := make(map[string]bool)
		for _, receipt := range manifest.Plan.Receipts {
			shardHashes[receipt.ShardHash] = true
		}
		// Query providers for each shard to build the offline cache.
		var providerCache []wire.StorageProviderRecord
		seen := make(map[string]bool)
		for shardHash := range shardHashes {
			providers := discoverStorageProviders(*chainURL, shardHash, "")
			for _, p := range providers {
				key := p.MinerAddress + "|" + p.PeerID + "|" + shardHash
				if !seen[key] {
					seen[key] = true
					providerCache = append(providerCache, p)
				}
			}
		}
		manifest.Plan.ProviderCache = providerCache
		if len(providerCache) > 0 {
			log.Printf("provider cache populated with %d entries for %d shards", len(providerCache), len(shardHashes))
		}
	}
	if err := client.SavePlan(*outPath, manifest.Plan); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("manifest saved intent=%s status=%s complete=%t receipts=%d out=%s\n",
		manifest.IntentID, manifest.Status, manifest.Complete, manifest.ReceiptCount, *outPath)
}

func fetchManifest(chainURL string, intentID string) (wire.StorageManifestResponse, error) {
	var manifest wire.StorageManifestResponse
	err := client.NewHTTP(chainURL).Get("/manifests/"+intentID, &manifest)
	return manifest, err
}

func createCollection(args []string) {
	fs := flag.NewFlagSet("collection-create", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	user := fs.String("user", "user_demo", "collection owner")
	name := fs.String("name", "", "collection name")
	description := fs.String("description", "", "optional collection description")
	metadataRaw := fs.String("metadata", "", "optional JSON object metadata")
	keyPath := fs.String("key", "", "optional account key file for signed collection creation")
	fs.Parse(args)

	if *name == "" {
		log.Fatal("-name is required")
	}
	req := wire.CreateCollectionRequest{
		User:        *user,
		Name:        *name,
		Description: *description,
		Metadata:    parseMetadata(*metadataRaw),
	}
	if *keyPath != "" {
		key, err := loadAccountKey(*keyPath)
		if err != nil {
			log.Fatal(err)
		}
		if req.User == "" || req.User == "user_demo" {
			req.User = key.Address
		}
		if !strings.EqualFold(req.User, key.Address) {
			log.Fatal("-user does not match account key")
		}
		req.ChainID = chainID(*chainURL)
		req.Nonce = accountNonce(*chainURL, key.Address)
		if err := wire.SignCreateCollection(&req, key.PrivateKey); err != nil {
			log.Fatal(err)
		}
	}
	var resp wire.CreateCollectionResponse
	if err := client.NewHTTP(*chainURL).Post("/collections", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("collection %s user=%s name=%s\n", resp.Collection.CollectionID, resp.Collection.User, resp.Collection.Name)
}

func appendCollectionRecord(args []string) {
	fs := flag.NewFlagSet("collection-append", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	collectionID := fs.String("collection", "", "collection id")
	intentID := fs.String("intent", "", "finalized intent id")
	user := fs.String("user", "", "optional collection owner")
	parent := fs.String("parent", "", "optional parent record id")
	kind := fs.String("kind", "memory", "record kind")
	key := fs.String("key", "", "optional record key")
	manifestRoot := fs.String("manifest-root", "", "optional manifest root")
	metadataRaw := fs.String("metadata", "", "optional JSON object metadata")
	keyPath := fs.String("account-key", "", "optional account key file for signed record append")
	fs.Parse(args)

	if *collectionID == "" || *intentID == "" {
		log.Fatal("-collection and -intent are required")
	}
	req := wire.AppendRecordRequest{
		CollectionID: *collectionID,
		User:         *user,
		IntentID:     *intentID,
		ParentRecord: *parent,
		Kind:         *kind,
		Key:          *key,
		ManifestRoot: *manifestRoot,
		Metadata:     parseMetadata(*metadataRaw),
	}
	if *keyPath != "" {
		key, err := loadAccountKey(*keyPath)
		if err != nil {
			log.Fatal(err)
		}
		if req.User == "" {
			req.User = key.Address
		}
		if !strings.EqualFold(req.User, key.Address) {
			log.Fatal("-user does not match account key")
		}
		req.ChainID = chainID(*chainURL)
		req.Nonce = accountNonce(*chainURL, key.Address)
		if err := wire.SignAppendRecord(&req, key.PrivateKey); err != nil {
			log.Fatal(err)
		}
	}
	var resp wire.AppendRecordResponse
	if err := client.NewHTTP(*chainURL).Post("/records", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("record %s collection=%s intent=%s parent=%s kind=%s key=%s\n",
		resp.Record.RecordID, resp.Record.CollectionID, resp.Record.IntentID,
		resp.Record.ParentRecord, resp.Record.Kind, resp.Record.Key)
}

func listCollectionRecords(args []string) {
	fs := flag.NewFlagSet("collection-records", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	collectionID := fs.String("collection", "", "collection id")
	kind := fs.String("kind", "", "optional record kind filter")
	key := fs.String("key", "", "optional record key filter")
	parent := fs.String("parent", "", "optional parent record filter")
	limit := fs.Int("limit", 0, "optional maximum records to return")
	latest := fs.Bool("latest", false, "return newest records first")
	fs.Parse(args)

	if *collectionID == "" {
		log.Fatal("-collection is required")
	}
	query := url.Values{}
	if *kind != "" {
		query.Set("kind", *kind)
	}
	if *key != "" {
		query.Set("key", *key)
	}
	if *parent != "" {
		query.Set("parent", *parent)
	}
	if *limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", *limit))
	}
	if *latest {
		query.Set("latest", "true")
	}
	path := "/collections/" + *collectionID + "/records"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp wire.CollectionRecordsResponse
	if err := client.NewHTTP(*chainURL).Get(path, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("collection %s name=%s records=%d\n", resp.Collection.CollectionID, resp.Collection.Name, len(resp.Records))
	for _, record := range resp.Records {
		fmt.Printf("record %s intent=%s deal=%s parent=%s kind=%s key=%s file_root=%s\n",
			record.RecordID, record.IntentID, record.DealID, record.ParentRecord,
			record.Kind, record.Key, record.FileRoot)
	}
}

func downloadCollection(args []string) {
	fs := flag.NewFlagSet("collection-download", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "http://localhost:9090", "gateway URL")
	collectionID := fs.String("collection", "", "collection id to download")
	outDir := fs.String("out", "./collection-download", "output directory")
	fs.Parse(args)

	if *collectionID == "" {
		log.Fatal("-collection is required")
	}

	type collectionFileInfo struct {
		IntentID  string `json:"intent_id"`
		RecordID  string `json:"record_id"`
		FileName  string `json:"file_name"`
		FileSize  int64  `json:"file_size"`
		Encrypted bool   `json:"encrypted"`
		Kind      string `json:"kind,omitempty"`
		Key       string `json:"key,omitempty"`
	}
	type collectionFilesResponse struct {
		CollectionID string               `json:"collection_id"`
		Name         string               `json:"name"`
		Files        []collectionFileInfo `json:"files"`
	}

	var resp collectionFilesResponse
	if err := client.NewHTTP(*gatewayURL).Get("/collection/"+*collectionID+"/files", &resp); err != nil {
		log.Fatalf("fetch collection files: %v", err)
	}

	if len(resp.Files) == 0 {
		log.Printf("collection %s (%s): no files found", resp.CollectionID, resp.Name)
		return
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("create output directory: %v", err)
	}

	log.Printf("collection %s (%s): %d files", resp.CollectionID, resp.Name, len(resp.Files))

	gwClient := client.NewHTTP(*gatewayURL)
	for i, file := range resp.Files {
		outPath := filepath.Join(*outDir, file.FileName)
		log.Printf("[%d/%d] downloading %s (%d bytes)...", i+1, len(resp.Files), file.FileName, file.FileSize)

		data, err := gwClient.GetBytes("/download/" + file.IntentID)
		if err != nil {
			log.Printf("  ERROR: %v", err)
			continue
		}
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			log.Printf("  ERROR writing %s: %v", outPath, err)
			continue
		}
		log.Printf("  saved %s (%d bytes)", outPath, len(data))
	}
	log.Printf("done. %d files saved to %s", len(resp.Files), *outDir)
}

func showRecord(args []string) {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	recordID := fs.String("id", "", "record id")
	fs.Parse(args)

	if *recordID == "" {
		log.Fatal("-id is required")
	}
	resp, err := fetchRecord(*chainURL, *recordID)
	if err != nil {
		log.Fatal(err)
	}
	record := resp.Record
	fmt.Printf("record %s collection=%s user=%s intent=%s deal=%s parent=%s kind=%s key=%s file_root=%s\n",
		record.RecordID, record.CollectionID, record.User, record.IntentID, record.DealID,
		record.ParentRecord, record.Kind, record.Key, record.FileRoot)
}

func fetchRecord(chainURL string, recordID string) (wire.DataRecordResponse, error) {
	var resp wire.DataRecordResponse
	err := client.NewHTTP(chainURL).Get("/records/"+recordID, &resp)
	return resp, err
}

func fetchRecordManifest(chainURL string, recordID string) (wire.DataRecordManifestResponse, error) {
	var resp wire.DataRecordManifestResponse
	err := client.NewHTTP(chainURL).Get("/records/"+recordID+"/manifest", &resp)
	return resp, err
}

func upload(args []string) {
	fs := flag.NewFlagSet("upload", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	storageURLs := fs.String("storage", "http://localhost:9090", "comma-separated storage node URLs")
	planPath := fs.String("plan", "./upload-plan.json", "upload plan path")
	filePath := fs.String("file", "", "file to upload")
	encryptionKey := fs.String("key", "", "encryption key file or raw base64/hex key for encrypted plans")
	accountKeyPath := fs.String("account-key", "", "account key file used to sign batch commits")
	batchSize := fs.Int("batch", 64, "receipts per batch commit")
	fs.Parse(args)

	if *filePath == "" {
		log.Fatal("-file is required")
	}
	if *accountKeyPath == "" {
		log.Fatal("-account-key is required")
	}
	accountKey, err := loadAccountKey(*accountKeyPath)
	if err != nil {
		log.Fatal(err)
	}
	plan, err := client.LoadPlan(*planPath)
	if err != nil {
		log.Fatal(err)
	}
	if !strings.EqualFold(plan.User, accountKey.Address) {
		log.Fatalf("plan user %s does not match -account-key address %s", plan.User, accountKey.Address)
	}
	endpoints := parseEndpoints(*storageURLs)
	if len(endpoints) == 0 {
		log.Fatal("at least one storage endpoint is required")
	}
	key, err := encryptionKeyForPlan(plan, *encryptionKey)
	if err != nil {
		log.Fatal(err)
	}

	file, err := os.Open(*filePath)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	chainClient := client.NewHTTP(*chainURL)
	uploaded := client.ReceiptShardSet(plan.Receipts)
	pending := client.PendingCommitReceipts(plan)
	if *batchSize <= 0 {
		*batchSize = 64
	}

	for segmentID := 0; segmentID < len(plan.SegmentRoots); segmentID++ {
		if len(pending) >= *batchSize {
			commitBatch(chainClient, *chainURL, plan, pending, accountKey)
			client.MarkCommitted(&plan, pending)
			pending = nil
			if err := client.SavePlan(*planPath, plan); err != nil {
				log.Fatal(err)
			}
		}
		if client.HasAllShardReceipts(plan, segmentID) {
			continue
		}

		shards, cleanup, err := encodeUploadSegment(file, plan, segmentID, key)
		if err != nil {
			log.Fatal(err)
		}
		if len(shards) != len(plan.Segments[segmentID].ShardHashes) {
			cleanup()
			log.Fatalf("segment %d shard count mismatch", segmentID)
		}

		for shardIndex, shard := range shards {
			ref := wire.ShardRef{SegmentID: segmentID, ShardIndex: shardIndex}
			if uploaded[ref] {
				continue
			}
			shardHash := shard.Hash
			if shardHash != plan.Segments[segmentID].ShardHashes[shardIndex] {
				cleanup()
				log.Fatalf("segment %d shard %d hash mismatch with upload plan", segmentID, shardIndex)
			}
			shardData, err := os.ReadFile(shard.Path)
			if err != nil {
				cleanup()
				log.Fatal(err)
			}
			endpoint := endpoints[(segmentID+shardIndex)%len(endpoints)]
			if assignment, ok := uploadAssignment(plan, segmentID, shardIndex); ok && assignment.Endpoint != "" {
				endpoint = assignment.Endpoint
			}
			req := wire.UploadRequest{
				IntentID:    plan.IntentID,
				User:        plan.User,
				FileRoot:    plan.FileRoot,
				SegmentID:   segmentID,
				SegmentRoot: plan.SegmentRoots[segmentID],
				ShardIndex:  shardIndex,
				ShardID:     fmt.Sprintf("%s:%d:%d", plan.IntentID, segmentID, shardIndex),
				ShardHash:   shardHash,
				ShardCID:    shardCIDForPlan(plan, segmentID, shardIndex),
				ShardSize:   shard.Size,
				DataBase64:  base64.StdEncoding.EncodeToString(shardData),
			}

			var receipt wire.MinerReceipt
			if err := client.NewHTTP(endpoint).Post("/upload", req, &receipt); err != nil {
				cleanup()
				log.Fatal(err)
			}
			receipt.MinerEndpoint = endpoint
			plan.Receipts = append(plan.Receipts, receipt)
			uploaded[ref] = true
			pending = append(pending, receipt)
			if err := client.SavePlan(*planPath, plan); err != nil {
				cleanup()
				log.Fatal(err)
			}
			if len(pending) >= *batchSize {
				commitBatch(chainClient, *chainURL, plan, pending, accountKey)
				client.MarkCommitted(&plan, pending)
				pending = nil
				if err := client.SavePlan(*planPath, plan); err != nil {
					cleanup()
					log.Fatal(err)
				}
			}
		}
		cleanup()
	}
	if len(pending) > 0 {
		commitBatch(chainClient, *chainURL, plan, pending, accountKey)
		client.MarkCommitted(&plan, pending)
	}
	if err := client.SavePlan(*planPath, plan); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("uploaded %d receipts for intent %s\n", len(plan.Receipts), plan.IntentID)
}

func finalize(args []string) {
	fs := flag.NewFlagSet("finalize", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	planPath := fs.String("plan", "./upload-plan.json", "upload plan path")
	accountKeyPath := fs.String("account-key", "", "account key file used to sign finalize")
	fs.Parse(args)
	if *accountKeyPath == "" {
		log.Fatal("-account-key is required")
	}
	accountKey, err := loadAccountKey(*accountKeyPath)
	if err != nil {
		log.Fatal(err)
	}

	plan, err := client.LoadPlan(*planPath)
	if err != nil {
		log.Fatal(err)
	}
	if !strings.EqualFold(plan.User, accountKey.Address) {
		log.Fatalf("plan user %s does not match -account-key address %s", plan.User, accountKey.Address)
	}
	req := wire.FinalizeRequest{
		ChainID:      chainID(*chainURL),
		IntentID:     plan.IntentID,
		User:         plan.User,
		ManifestRoot: chaincrypto.HashBytes([]byte(plan.FileRoot + ":" + plan.FileName)),
		Nonce:        accountNonce(*chainURL, accountKey.Address),
	}
	if err := wire.SignFinalize(&req, accountKey.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp wire.FinalizeResponse
	if err := client.NewHTTP(*chainURL).Post("/finalize", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("finalized deal %s for intent %s\n", resp.DealID, resp.IntentID)
}

func mine(args []string) {
	fs := flag.NewFlagSet("mine", flag.ExitOnError)
	addr := fs.String("addr", ":9092", "HTTP listen address for upload/download service")
	data := fs.String("data", "./data/miner1", "storage data directory")
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL for miner registration")
	endpoint := fs.String("endpoint", "", "public endpoint registered on chain")
	capacity := fs.Uint64("capacity", 1<<40, "declared storage capacity in bytes")
	stake := fs.Uint64("stake", 1000, "declared miner stake")
	autoProve := fs.Bool("auto-prove", true, "poll chain node and automatically submit storage proofs")
	proveInterval := fs.Duration("prove-interval", 2*time.Second, "automatic proof polling interval")
	autoRepair := fs.Bool("auto-repair", true, "poll chain node and automatically execute repair tasks")
	repairInterval := fs.Duration("repair-interval", 10*time.Second, "automatic repair polling interval")
	autoDelete := fs.Bool("auto-delete", true, "poll chain node and automatically execute delete tasks")
	deleteInterval := fs.Duration("delete-interval", 10*time.Second, "automatic delete polling interval")
	p2pListen := fs.String("p2p-listen", "", "comma-separated libp2p provider listen multiaddrs")
	p2pPeers := fs.String("p2p-peers", "", "comma-separated libp2p provider peer multiaddrs")
	p2pTopic := fs.String("p2p-topic", "storage-chain/providers/devnet", "libp2p provider discovery topic")
	fs.Parse(args)

	minerKey := os.Getenv("MINER_PRIVATE_KEY")
	if minerKey == "" {
		log.Fatal("MINER_PRIVATE_KEY environment variable is not set; generate a key with: genkey")
	}
	node, err := storage.OpenNode(*data, minerKey)
	if err != nil {
		log.Fatalf("open mining node: %v", err)
	}
	registeredEndpoint := *endpoint
	if registeredEndpoint == "" {
		registeredEndpoint = "http://localhost" + *addr
	}
	if *chainURL == "" {
		log.Fatal("-chain is required to enable mining")
	}
	node.ConfigureChain(*chainURL, registeredEndpoint)
	if err := node.Register(*chainURL, registeredEndpoint, *capacity, *stake); err != nil {
		log.Fatalf("register miner: %v", err)
	}
	fmt.Printf("mining enabled address=%s endpoint=%s capacity=%d stake=%s\n", node.Address(), registeredEndpoint, *capacity, formatGF(*stake))
	fmt.Println("upload/download access service is mandatory while mining is enabled")

	if *autoProve {
		node.StartAutoProver(*chainURL, *proveInterval)
	}
	if *autoRepair {
		node.StartAutoRepairer(*chainURL, *repairInterval)
	}
	if *autoDelete {
		node.StartAutoDeleter(*chainURL, *deleteInterval)
	}

	var providerNetwork *storage.ProviderNetwork
	if *p2pListen != "" || *p2pPeers != "" {
		providerNetwork, err = storage.StartProviderNetwork(node, *p2pListen, *p2pPeers, *p2pTopic, registeredEndpoint, *capacity)
		if err != nil {
			log.Fatalf("start provider discovery: %v", err)
		}
		defer providerNetwork.Close()
		fmt.Printf("provider discovery enabled peer_id=%s addrs=%v\n", providerNetwork.PeerID(), providerNetwork.Addrs())
	}
	node.StartProviderReporter(*chainURL, registeredEndpoint, *capacity, 30*time.Second, func() (string, []string) {
		if providerNetwork == nil {
			return "", nil
		}
		return providerNetwork.PeerID(), providerNetwork.Addrs()
	})

	server := storage.NewServerWithProviderNetwork(node, providerNetwork)
	httpServer := &http.Server{
		Addr:    *addr,
		Handler: server.Routes(),
	}
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("mining access service error: %v", err)
		}
	}()
	fmt.Printf("mining access service listening on %s\n", *addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

func settleIntent(args []string) {
	fs := flag.NewFlagSet("settle-intent", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("intent", "", "intent id")
	user := fs.String("user", "", "optional user address")
	planPath := fs.String("plan", "", "optional upload plan path")
	accountKeyPath := fs.String("account-key", "", "account key file used to sign settlement")
	fs.Parse(args)

	if *planPath != "" {
		plan, err := client.LoadPlan(*planPath)
		if err != nil {
			log.Fatal(err)
		}
		if *intentID == "" {
			*intentID = plan.IntentID
		}
		if *user == "" {
			*user = plan.User
		}
	}
	if *intentID == "" {
		log.Fatal("-intent or -plan is required")
	}
	if *accountKeyPath == "" {
		log.Fatal("-account-key is required")
	}
	accountKey, err := loadAccountKey(*accountKeyPath)
	if err != nil {
		log.Fatal(err)
	}
	if *user == "" {
		*user = accountKey.Address
	} else if !strings.EqualFold(*user, accountKey.Address) {
		log.Fatalf("-user %s does not match -account-key address %s", *user, accountKey.Address)
	}
	req := wire.SettleIntentRequest{ChainID: chainID(*chainURL), IntentID: *intentID, User: *user, Nonce: accountNonce(*chainURL, accountKey.Address)}
	if err := wire.SignSettleIntent(&req, accountKey.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp wire.SettleIntentResponse
	if err := client.NewHTTP(*chainURL).Post("/intents/settle", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("settled intent %s status=%s refunded=%s paid=%s\n", resp.IntentID, resp.Status, formatGF(resp.RefundedFee), formatGF(resp.PaidFee))
}

func renewDeal(args []string) {
	fs := flag.NewFlagSet("renew", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("intent", "", "intent id")
	user := fs.String("user", "", "optional user address")
	duration := fs.Int64("duration", 0, "renewal duration in seconds")
	planPath := fs.String("plan", "", "optional upload plan path")
	accountKeyPath := fs.String("account-key", "", "account key file used to sign renewal")
	fs.Parse(args)

	if *planPath != "" {
		plan, err := client.LoadPlan(*planPath)
		if err != nil {
			log.Fatal(err)
		}
		if *intentID == "" {
			*intentID = plan.IntentID
		}
		if *user == "" {
			*user = plan.User
		}
	}
	if *intentID == "" {
		log.Fatal("-intent or -plan is required")
	}
	if *duration <= 0 {
		log.Fatal("-duration is required (seconds)")
	}
	if *accountKeyPath == "" {
		log.Fatal("-account-key is required")
	}
	accountKey, err := loadAccountKey(*accountKeyPath)
	if err != nil {
		log.Fatal(err)
	}
	if *user == "" {
		*user = accountKey.Address
	} else if !strings.EqualFold(*user, accountKey.Address) {
		log.Fatalf("-user %s does not match -account-key address %s", *user, accountKey.Address)
	}
	req := wire.RenewDealRequest{ChainID: chainID(*chainURL), IntentID: *intentID, User: *user, Duration: *duration, Nonce: accountNonce(*chainURL, accountKey.Address)}
	if err := wire.SignRenewDeal(&req, accountKey.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp wire.RenewDealResponse
	if err := client.NewHTTP(*chainURL).Post("/intents/"+*intentID+"/renew", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("renewed intent %s status=%s expires_at=%d paid=%s locked=%s grace=%t\n",
		resp.IntentID, resp.Status, resp.ExpiresAtUnix, formatGF(resp.PaidAmount), formatGF(resp.NewLockedFee), resp.GraceUsed)
}

func topUpPermanentFund(args []string) {
	fs := flag.NewFlagSet("permanent-fund", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("intent", "", "intent id")
	user := fs.String("user", "", "optional user address")
	amount := fs.Uint64("amount", 0, "amount to add to the permanent storage fund")
	planPath := fs.String("plan", "", "optional upload plan path")
	accountKeyPath := fs.String("account-key", "", "account key file used to sign top-up")
	fs.Parse(args)

	if *planPath != "" {
		plan, err := client.LoadPlan(*planPath)
		if err != nil {
			log.Fatal(err)
		}
		if *intentID == "" {
			*intentID = plan.IntentID
		}
		if *user == "" {
			*user = plan.User
		}
	}
	if *intentID == "" {
		log.Fatal("-intent or -plan is required")
	}
	if *amount == 0 {
		log.Fatal("-amount is required")
	}
	if *accountKeyPath == "" {
		log.Fatal("-account-key is required")
	}
	accountKey, err := loadAccountKey(*accountKeyPath)
	if err != nil {
		log.Fatal(err)
	}
	if *user == "" {
		*user = accountKey.Address
	} else if !strings.EqualFold(*user, accountKey.Address) {
		log.Fatalf("-user %s does not match -account-key address %s", *user, accountKey.Address)
	}
	req := wire.PermanentFundTopUpRequest{
		ChainID:  chainID(*chainURL),
		IntentID: *intentID,
		User:     *user,
		Amount:   *amount,
		Nonce:    accountNonce(*chainURL, accountKey.Address),
	}
	if err := wire.SignPermanentFundTopUp(&req, accountKey.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp wire.PermanentFundTopUpResponse
	if err := client.NewHTTP(*chainURL).Post("/intents/permanent-fund", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("permanent fund intent=%s balance=%s contributed=%s paid=%s closed=%t\n",
		resp.Fund.IntentID, formatGF(resp.Fund.Balance), formatGF(resp.Fund.Contributed), formatGF(resp.Fund.Paid), resp.Fund.Closed)
}

func consensusState(args []string) {
	fs := flag.NewFlagSet("consensus", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	fs.Parse(args)
	var cs consensus.State
	if err := client.NewHTTP(*chainURL).Get("/consensus", &cs); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("consensus height=%d round=%d phase=%s proposer=%s voting_power=%d total_power=%d timeout_ms=%d\n",
		cs.Height, cs.Round, cs.Phase, cs.Proposer, cs.VotingPower, cs.TotalPower, cs.BlockTimeout)
}

func consensusVotes(args []string) {
	fs := flag.NewFlagSet("consensus-votes", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	height := fs.Uint64("height", 0, "optional block height")
	round := fs.Uint64("round", 0, "optional consensus round")
	voteType := fs.String("type", "", "optional vote type: prevote or precommit")
	fs.Parse(args)

	query := url.Values{}
	if *height > 0 {
		query.Set("height", fmt.Sprintf("%d", *height))
	}
	if *round > 0 {
		query.Set("round", fmt.Sprintf("%d", *round))
	}
	if *voteType != "" {
		query.Set("type", *voteType)
	}
	path := "/consensus/votes"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp wire.ConsensusVotesResponse
	if err := client.NewHTTP(*chainURL).Get(path, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("consensus_votes=%d", len(resp.Votes))
	if resp.Height > 0 {
		fmt.Printf(" height=%d", resp.Height)
	}
	if resp.Round > 0 {
		fmt.Printf(" round=%d", resp.Round)
	}
	if resp.Type != "" {
		fmt.Printf(" type=%s", resp.Type)
	}
	fmt.Println()
	for _, vote := range resp.Votes {
		fmt.Printf("vote height=%d round=%d type=%s validator=%s power=%d block=%s\n",
			vote.Height, vote.Round, vote.Type, vote.ValidatorAddress, vote.Power, vote.BlockHash)
	}
}

func setUpgrade(args []string) {
	fs := flag.NewFlagSet("set-upgrade", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyPath := fs.String("key", "", "path to governance operator key file")
	name := fs.String("name", "", "upgrade name")
	haltHeight := fs.Uint64("halt_height", 0, "height to halt chain")
	haltTime := fs.Int64("halt_time", 0, "unix time to halt chain")
	info := fs.String("info", "", "upgrade info")
	fs.Parse(args)
	if *name == "" {
		log.Fatal("-name is required")
	}
	if *keyPath == "" {
		log.Fatal("-key is required")
	}
	var plan consensus.UpgradePlan
	if err := postOperator(*chainURL, "/upgrade", consensus.UpgradePlan{
		Name: *name, HaltHeight: *haltHeight, HaltTime: *haltTime, Info: *info,
	}, &plan, *keyPath); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("upgrade plan set name=%s halt_height=%d halt_time=%d\n", plan.Name, plan.HaltHeight, plan.HaltTime)
}

func terminateDeal(args []string) {
	fs := flag.NewFlagSet("terminate-deal", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("intent", "", "intent id")
	user := fs.String("user", "", "deal owner")
	reason := fs.String("reason", "", "optional termination reason")
	planPath := fs.String("plan", "", "optional upload plan path")
	accountKeyPath := fs.String("account-key", "", "account key file used to sign termination")
	fs.Parse(args)

	if *planPath != "" {
		plan, err := client.LoadPlan(*planPath)
		if err != nil {
			log.Fatal(err)
		}
		if *intentID == "" {
			*intentID = plan.IntentID
		}
		if *user == "" {
			*user = plan.User
		}
	}
	if *intentID == "" {
		log.Fatal("-intent or -plan is required")
	}
	if *accountKeyPath == "" {
		log.Fatal("-account-key is required")
	}
	accountKey, err := loadAccountKey(*accountKeyPath)
	if err != nil {
		log.Fatal(err)
	}
	if *user == "" {
		*user = accountKey.Address
	} else if !strings.EqualFold(*user, accountKey.Address) {
		log.Fatalf("-user %s does not match -account-key address %s", *user, accountKey.Address)
	}
	req := wire.TerminateDealRequest{
		ChainID:  chainID(*chainURL),
		IntentID: *intentID,
		User:     *user,
		Reason:   *reason,
		Nonce:    accountNonce(*chainURL, accountKey.Address),
	}
	if err := wire.SignTerminateDeal(&req, accountKey.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp wire.TerminateDealResponse
	if err := client.NewHTTP(*chainURL).Post("/intents/terminate", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("terminated intent=%s storage=%s access=%s refunded=%s delete_tasks=%d\n",
		resp.IntentID, resp.StorageStatus, resp.AccessStatus, formatGF(resp.RefundedFee), len(resp.DeleteTasks))
}

func setAccessPolicy(args []string) {
	fs := flag.NewFlagSet("access-policy", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("intent", "", "intent id")
	user := fs.String("user", "", "deal owner")
	accessStatus := fs.String("status", wire.AccessStatusPrivate, "access status: public, private, suspended, blocked")
	reasonHash := fs.String("reason-hash", "", "optional reason hash")
	accountKeyPath := fs.String("account-key", "", "account key file used to sign access policy")
	fs.Parse(args)

	if *intentID == "" {
		log.Fatal("-intent is required")
	}
	if *accountKeyPath == "" {
		log.Fatal("-account-key is required")
	}
	accountKey, err := loadAccountKey(*accountKeyPath)
	if err != nil {
		log.Fatal(err)
	}
	if *user == "" {
		*user = accountKey.Address
	} else if !strings.EqualFold(*user, accountKey.Address) {
		log.Fatalf("-user %s does not match -account-key address %s", *user, accountKey.Address)
	}
	req := wire.SetAccessPolicyRequest{
		ChainID:      chainID(*chainURL),
		IntentID:     *intentID,
		User:         *user,
		AccessStatus: *accessStatus,
		ReasonHash:   *reasonHash,
		Nonce:        accountNonce(*chainURL, accountKey.Address),
	}
	if err := wire.SignSetAccessPolicy(&req, accountKey.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp wire.SetAccessPolicyResponse
	if err := client.NewHTTP(*chainURL).Post("/intents/access", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("access updated intent=%s access=%s moderation=%s\n", resp.IntentID, resp.AccessStatus, resp.ModerationStatus)
}

func governanceKeygen(args []string) {
	fs := flag.NewFlagSet("governance-keygen", flag.ExitOnError)
	out := fs.String("out", "", "output key file path")
	fs.Parse(args)

	if *out == "" {
		log.Fatal("-out is required")
	}
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		log.Fatalf("failed to generate key: %v", err)
	}
	keyFile := struct {
		Address    string `json:"address"`
		PublicKey  string `json:"public_key"`
		PrivateKey string `json:"private_key"`
	}{
		Address:    wire.AccountAddress(&privateKey.PublicKey),
		PublicKey:  encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey)),
		PrivateKey: encodeHex(ethcrypto.FromECDSA(privateKey)),
	}
	data, err := json.MarshalIndent(keyFile, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal key: %v", err)
	}
	if err := os.WriteFile(*out, data, 0600); err != nil {
		log.Fatalf("failed to write key file: %v", err)
	}
	fmt.Printf("governance key generated: address=%s public_key=%s\n", keyFile.Address, keyFile.PublicKey)
}

type governanceKeyFile struct {
	Address    string `json:"address"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

func loadGovernanceKey(path string) governanceKeyFile {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("failed to read key file: %v", err)
	}
	var key governanceKeyFile
	if err := json.Unmarshal(data, &key); err != nil {
		log.Fatalf("failed to parse key file: %v", err)
	}
	return key
}

func loadGovernanceECDSAKey(path string) *ecdsa.PrivateKey {
	keyFile := loadGovernanceKey(path)
	privateKey, err := ethcrypto.HexToECDSA(trimHexPrefix(keyFile.PrivateKey))
	if err != nil {
		log.Fatalf("failed to parse ECDSA private key: %v", err)
	}
	return privateKey
}

func governancePropose(args []string) {
	fs := flag.NewFlagSet("governance-propose", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyPath := fs.String("key", "", "path to governance operator key file")
	intentID := fs.String("intent", "", "intent id (required for deal actions)")
	action := fs.String("action", "freeze", "action: freeze, block, legal_hold, appeal, add_operator, remove_operator, update_operator, update_config, update_mining_params")
	reasonHash := fs.String("reason-hash", "", "required reason hash")
	expiresAtUnix := fs.Int64("expires-at-unix", 0, "future unix timestamp for freeze expiration")
	preserveStorage := fs.Bool("preserve-storage", true, "keep storage responsibility active")
	appealDeadlineUnix := fs.Int64("appeal-deadline-unix", 0, "appeal deadline unix timestamp")
	targetOperator := fs.String("target-operator", "", "target operator address (for add/remove/update_operator)")
	targetPublicKey := fs.String("target-public-key", "", "target operator public key (for add/update_operator)")
	targetPermissions := fs.String("target-permissions", "", "comma-separated permissions (for add/update_operator)")
	dataModThresholdNum := fs.Int("data-mod-threshold-num", 0, "data moderation threshold numerator (for update_config)")
	dataModThresholdDen := fs.Int("data-mod-threshold-den", 0, "data moderation threshold denominator (for update_config)")
	opChangeThresholdNum := fs.Int("op-change-threshold-num", 0, "operator change threshold numerator (for update_config)")
	opChangeThresholdDen := fs.Int("op-change-threshold-den", 0, "operator change threshold denominator (for update_config)")
	// Mining params flags.
	storageReleaseRateBPS := fs.Uint64("storage-release-rate-bps", 0, "storage pool release rate BPS (for update_mining_params)")
	storageRewardPerBlock := fs.Uint64("storage-reward-per-block", 0, "storage pool per-block reward in smallest units (for update_mining_params)")
	foundationRewardPerBlock := fs.Uint64("foundation-reward-per-block", 0, "foundation pool per-block reward in smallest units (for update_mining_params)")
	retrievalRewardPerBlock := fs.Uint64("retrieval-reward-per-block", 0, "retrieval pool per-block reward in smallest units (for update_mining_params)")
	retrievalReleaseRateBPS := fs.Uint64("retrieval-release-rate-bps", 0, "retrieval pool release rate BPS (for update_mining_params)")
	storedBytesWeightBPS := fs.Uint64("stored-bytes-weight-bps", 0, "stored bytes weight factor BPS (for update_mining_params)")
	proofScoreWeightBPS := fs.Uint64("proof-score-weight-bps", 0, "proof score weight factor BPS (for update_mining_params)")
	availabilityWeightBPS := fs.Uint64("availability-weight-bps", 0, "availability weight factor BPS (for update_mining_params)")
	retrievalSpeedWeightBPS := fs.Uint64("retrieval-speed-weight-bps", 0, "retrieval speed weight factor BPS (for update_mining_params)")
	ipDispersionWeightBPS := fs.Uint64("ip-dispersion-weight-bps", 0, "IP dispersion weight factor BPS (for update_mining_params)")
	retrievalRewardPerMiB := fs.Uint64("retrieval-reward-per-mib", 0, "retrieval reward per MiB (for update_mining_params)")
	maxRetrievalRewardPerWindow := fs.Uint64("max-retrieval-reward-per-window", 0, "max retrieval reward per window (for update_mining_params)")
	minerDegradeThreshold := fs.Uint64("miner-degrade-threshold", 0, "miner degrade threshold (for update_mining_params)")
	storageProofSamples := fs.Int("storage-proof-samples", 0, "storage proof samples count (for update_mining_params)")
	validatorCommissionBPS := fs.Uint64("validator-commission-bps", 0, "validator commission BPS (for update_mining_params)")
	retrievalWeightBPS := fs.Uint64("retrieval-weight-bps", 0, "retrieval weight BPS (for update_mining_params)")
	targetBlockBytes := fs.Uint64("target-block-bytes", 0, "target block size in bytes (for update_mining_params)")
	maxBlockBytes := fs.Uint64("max-block-bytes", 0, "max block size in bytes (for update_mining_params)")
	maxBlockTxs := fs.Uint64("max-block-txs", 0, "max transactions per block (for update_mining_params)")
	maxTxBytes := fs.Uint64("max-tx-bytes", 0, "max regular transaction size in bytes (for update_mining_params)")
	maxStorageTxBytes := fs.Uint64("max-storage-tx-bytes", 0, "max storage metadata transaction size in bytes (for update_mining_params)")
	registrationBonusAmount := fs.Uint64("registration-bonus-amount", 0, "registration bonus amount in smallest units (for update_mining_params)")
	minBonusProofCount := fs.Uint64("min-bonus-proof-count", 0, "minimum successful proofs to release bonus (for update_mining_params)")
	minBonusSuccessRateBPS := fs.Uint64("min-bonus-success-rate-bps", 0, "minimum success rate BPS for bonus release (for update_mining_params)")
	minBonusRetrievalCount := fs.Uint64("min-bonus-retrieval-count", 0, "minimum successful retrievals for bonus release (for update_mining_params)")
	maxBonusAddresses := fs.Uint64("max-bonus-addresses", 0, "maximum miners who can receive bonus (for update_mining_params)")
	bonusDeadlineSeconds := fs.Uint64("bonus-deadline-seconds", 0, "bonus deadline in seconds (for update_mining_params)")
	activationWindowSeconds := fs.Uint64("activation-window-seconds", 0, "activation window in seconds (for update_mining_params)")
	fs.Parse(args)

	if *keyPath == "" || *reasonHash == "" {
		log.Fatal("-key and -reason-hash are required")
	}

	isOpMgmt := *action == "add_operator" || *action == "remove_operator" || *action == "update_operator"
	isConfig := *action == "update_config"
	isMiningParams := *action == "update_mining_params"
	if !isOpMgmt && !isConfig && !isMiningParams && *intentID == "" {
		log.Fatal("-intent is required for deal actions")
	}

	key := loadGovernanceKey(*keyPath)
	privKey := loadGovernanceECDSAKey(*keyPath)

	var permissions []string
	if *targetPermissions != "" {
		for _, p := range strings.Split(*targetPermissions, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				permissions = append(permissions, p)
			}
		}
	}

	now := time.Now().Unix()
	req := wire.CreateGovernanceProposalRequest{
		Proposer:                          key.Address,
		IntentID:                          *intentID,
		Action:                            *action,
		ReasonHash:                        *reasonHash,
		ExpiresAtUnix:                     *expiresAtUnix,
		PreserveStorage:                   *preserveStorage,
		AppealDeadlineUnix:                *appealDeadlineUnix,
		TargetOperator:                    *targetOperator,
		TargetPublicKey:                   *targetPublicKey,
		TargetPermissions:                 permissions,
		TargetDataModerationThresholdNum:  *dataModThresholdNum,
		TargetDataModerationThresholdDen:  *dataModThresholdDen,
		TargetOperatorChangeThresholdNum:  *opChangeThresholdNum,
		TargetOperatorChangeThresholdDen:  *opChangeThresholdDen,
		TargetStorageReleaseRateBPS:       *storageReleaseRateBPS,
		TargetStorageRewardPerBlock:       *storageRewardPerBlock,
		TargetFoundationRewardPerBlock:    *foundationRewardPerBlock,
		TargetRetrievalRewardPerBlock:     *retrievalRewardPerBlock,
		TargetRetrievalReleaseRateBPS:     *retrievalReleaseRateBPS,
		TargetStoredBytesWeightBPS:        *storedBytesWeightBPS,
		TargetProofScoreWeightBPS:         *proofScoreWeightBPS,
		TargetAvailabilityWeightBPS:       *availabilityWeightBPS,
		TargetRetrievalSpeedWeightBPS:     *retrievalSpeedWeightBPS,
		TargetIPDispersionWeightBPS:       *ipDispersionWeightBPS,
		TargetRetrievalRewardPerMiB:       *retrievalRewardPerMiB,
		TargetMaxRetrievalRewardPerWindow: *maxRetrievalRewardPerWindow,
		TargetMinerDegradeThreshold:       *minerDegradeThreshold,
		TargetStorageProofSamples:         *storageProofSamples,
		TargetValidatorCommissionBPS:      *validatorCommissionBPS,
		TargetRetrievalWeightBPS:          *retrievalWeightBPS,
		TargetBlockBytes:                  *targetBlockBytes,
		TargetMaxBlockBytes:               *maxBlockBytes,
		TargetMaxBlockTxs:                 *maxBlockTxs,
		TargetMaxTxBytes:                  *maxTxBytes,
		TargetMaxStorageTxBytes:           *maxStorageTxBytes,
		TargetRegistrationBonusAmount:     *registrationBonusAmount,
		TargetMinBonusProofCount:          *minBonusProofCount,
		TargetMinBonusSuccessRateBPS:      *minBonusSuccessRateBPS,
		TargetMinBonusRetrievalCount:      *minBonusRetrievalCount,
		TargetMaxBonusAddresses:           *maxBonusAddresses,
		TargetBonusDeadlineSeconds:        *bonusDeadlineSeconds,
		TargetActivationWindowSeconds:     *activationWindowSeconds,
		Nonce:                             fetchOperatorNonce(*chainURL, key.Address),
		CreatedAtUnix:                     now,
	}
	if err := wire.SignGovernanceProposal(&req, privKey); err != nil {
		log.Fatalf("failed to sign proposal: %v", err)
	}
	var resp wire.CreateGovernanceProposalResponse
	if err := client.NewHTTP(*chainURL).Post("/governance/proposals", req, &resp); err != nil {
		log.Fatal(err)
	}
	if isOpMgmt {
		fmt.Printf("proposal created: id=%s action=%s target=%s status=%s\n",
			resp.Proposal.ProposalID, resp.Proposal.Action, resp.Proposal.TargetOperator, resp.Proposal.Status)
	} else if isConfig {
		fmt.Printf("proposal created: id=%s action=%s data_mod=%d/%d op_change=%d/%d status=%s\n",
			resp.Proposal.ProposalID, resp.Proposal.Action,
			resp.Proposal.TargetDataModerationThresholdNum, resp.Proposal.TargetDataModerationThresholdDen,
			resp.Proposal.TargetOperatorChangeThresholdNum, resp.Proposal.TargetOperatorChangeThresholdDen,
			resp.Proposal.Status)
	} else if isMiningParams {
		fmt.Printf("proposal created: id=%s action=%s status=%s\n",
			resp.Proposal.ProposalID, resp.Proposal.Action, resp.Proposal.Status)
	} else {
		fmt.Printf("proposal created: id=%s action=%s intent=%s status=%s\n",
			resp.Proposal.ProposalID, resp.Proposal.Action, resp.Proposal.IntentID, resp.Proposal.Status)
	}
}

func governanceVote(args []string) {
	fs := flag.NewFlagSet("governance-vote", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyPath := fs.String("key", "", "path to governance operator key file")
	proposalID := fs.String("proposal", "", "proposal id")
	approve := fs.Bool("approve", true, "approve or reject the proposal")
	fs.Parse(args)

	if *keyPath == "" || *proposalID == "" {
		log.Fatal("-key and -proposal are required")
	}
	key := loadGovernanceKey(*keyPath)
	privKey := loadGovernanceECDSAKey(*keyPath)

	now := time.Now().Unix()
	req := wire.CastGovernanceVoteRequest{
		ProposalID:    *proposalID,
		Voter:         key.Address,
		Approve:       *approve,
		Nonce:         fetchOperatorNonce(*chainURL, key.Address),
		CreatedAtUnix: now,
	}
	if err := wire.SignGovernanceVote(&req, privKey); err != nil {
		log.Fatalf("failed to sign vote: %v", err)
	}
	var resp wire.CastGovernanceVoteResponse
	if err := client.NewHTTP(*chainURL).Post("/governance/votes", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("vote cast: proposal=%s voter=%s approve=%v approvals=%d rejects=%d threshold=%d executed=%v\n",
		resp.Vote.ProposalID, resp.Vote.Voter, resp.Vote.Approve, resp.ApproveCount, resp.RejectCount, resp.Threshold, resp.Executed)
}

func governanceExecute(args []string) {
	fs := flag.NewFlagSet("governance-execute", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyPath := fs.String("key", "", "path to governance operator key file")
	proposalID := fs.String("proposal", "", "proposal id")
	fs.Parse(args)

	if *keyPath == "" || *proposalID == "" {
		log.Fatal("-key and -proposal are required")
	}
	key := loadGovernanceKey(*keyPath)
	privKey := loadGovernanceECDSAKey(*keyPath)

	now := time.Now().Unix()
	req := wire.ExecuteGovernanceProposalRequest{
		ProposalID:    *proposalID,
		Executor:      key.Address,
		Nonce:         fetchOperatorNonce(*chainURL, key.Address),
		CreatedAtUnix: now,
	}
	if err := wire.SignGovernanceExecute(&req, privKey); err != nil {
		log.Fatalf("failed to sign execute request: %v", err)
	}
	var resp wire.ExecuteGovernanceProposalResponse
	if err := client.NewHTTP(*chainURL).Post("/governance/execute", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("proposal executed: id=%s status=%s action=%s access=%s moderation=%s storage=%s\n",
		resp.Proposal.ProposalID, resp.Proposal.Status, resp.Proposal.Action,
		resp.GovernanceResult.AccessStatus, resp.GovernanceResult.ModerationStatus, resp.GovernanceResult.StorageStatus)
}

func governanceProposals(args []string) {
	fs := flag.NewFlagSet("governance-proposals", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	status := fs.String("status", "", "filter by status: pending, executed, rejected, expired, cancelled")
	intentID := fs.String("intent", "", "filter by intent id")
	fs.Parse(args)

	var resp wire.GovernanceProposalListResponse
	query := url.Values{}
	if *status != "" {
		query.Set("status", *status)
	}
	if *intentID != "" {
		query.Set("intent", *intentID)
	}
	path := "/governance/proposals"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if err := client.NewHTTP(*chainURL).Get(path, &resp); err != nil {
		log.Fatal(err)
	}
	for _, p := range resp.Proposals {
		votes := resp.Votes[p.ProposalID]
		fmt.Printf("proposal=%s proposer=%s action=%s intent=%s status=%s votes=%d created=%d\n",
			p.ProposalID, p.Proposer, p.Action, p.IntentID, p.Status, len(votes), p.CreatedAtUnix)
		for _, v := range votes {
			fmt.Printf("  vote: voter=%s approve=%v at=%d\n", v.Voter, v.Approve, v.CreatedAtUnix)
		}
	}
}

func governanceOperatorsList(args []string) {
	fs := flag.NewFlagSet("governance-operators", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	fs.Parse(args)

	var resp wire.GovernanceOperatorListResponse
	if err := client.NewHTTP(*chainURL).Get("/governance/operators", &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("data moderation threshold: %d/%d (num=%d den=%d)\n",
		resp.DataModerationThreshold, len(resp.Operators), resp.DataModerationThresholdNum, resp.DataModerationThresholdDen)
	fmt.Printf("operator change threshold: %d/%d (num=%d den=%d)\n\n",
		resp.OperatorChangeThreshold, len(resp.Operators), resp.OperatorChangeThresholdNum, resp.OperatorChangeThresholdDen)
	for _, op := range resp.Operators {
		hasKey := "no"
		if op.PublicKey != "" {
			hasKey = "yes"
		}
		fmt.Printf("operator=%s enabled=%v key=%s nonce=%d permissions=%v\n",
			op.Operator, op.Enabled, hasKey, op.Nonce, op.Permissions)
	}
}

// fetchOperatorNonce queries the chain for the current nonce of a governance operator.
func fetchOperatorNonce(chainURL, address string) uint64 {
	var resp wire.GovernanceOperatorListResponse
	if err := client.NewHTTP(chainURL).Get("/governance/operators", &resp); err != nil {
		log.Fatalf("failed to fetch operator nonce: %v", err)
	}
	for _, op := range resp.Operators {
		if strings.EqualFold(op.Operator, address) {
			return op.Nonce
		}
	}
	log.Fatalf("operator %s not found", address)
	return 0
}

func listDeleteTasks(args []string) {
	fs := flag.NewFlagSet("delete-tasks", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("intent", "", "intent id")
	miner := fs.String("miner", "", "miner address")
	status := fs.String("status", "", "task status: pending, completed")
	fs.Parse(args)

	var resp wire.DeleteTaskResponse
	query := url.Values{}
	if *intentID != "" {
		query.Set("intent", *intentID)
	}
	if *miner != "" {
		query.Set("miner", *miner)
	}
	if *status != "" {
		query.Set("status", *status)
	}
	path := "/intents/delete-tasks"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if err := client.NewHTTP(*chainURL).Get(path, &resp); err != nil {
		log.Fatal(err)
	}
	for _, task := range resp.Tasks {
		fmt.Printf("delete task=%s intent=%s miner=%s shard=%s status=%s created=%d completed=%d\n",
			task.TaskID, task.IntentID, task.MinerAddress, task.ShardHash, task.Status, task.CreatedAtUnix, task.CompletedAtUnix)
	}
}

func listGovernanceAudit(args []string) {
	fs := flag.NewFlagSet("governance-audit", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("intent", "", "intent id")
	operator := fs.String("operator", "", "operator identity")
	action := fs.String("action", "", "governance action")
	fs.Parse(args)

	var resp wire.GovernanceAuditResponse
	query := url.Values{}
	if *intentID != "" {
		query.Set("intent", *intentID)
	}
	if *operator != "" {
		query.Set("operator", *operator)
	}
	if *action != "" {
		query.Set("action", *action)
	}
	path := "/intents/governance/audit"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if err := client.NewHTTP(*chainURL).Get(path, &resp); err != nil {
		log.Fatal(err)
	}
	for _, record := range resp.Records {
		fmt.Printf("audit=%s intent=%s operator=%s action=%s type=%s access=%s moderation=%s storage=%s expires=%d appeal=%d at=%d\n",
			record.AuditID, record.IntentID, record.Operator, record.Action, record.GovernanceType, record.AccessStatus, record.ModerationStatus, record.StorageStatus, record.ModerationExpiresAtUnix, record.AppealDeadlineUnix, record.RecordedAtUnix)
	}
}

func submitRetrievalReceipt(args []string) {
	fs := flag.NewFlagSet("retrieval-receipt", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	storageURL := fs.String("storage", "http://localhost:9090", "storage or retrieval node URL")
	intentID := fs.String("intent", "", "intent id")
	shardHash := fs.String("shard", "", "shard hash")
	keyPath := fs.String("key", "", "client account key file")
	requestID := fs.String("request", "", "optional retrieval request id")
	bytesServed := fs.Uint64("bytes", 0, "optional served bytes; defaults to downloaded shard size")
	fs.Parse(args)

	if *intentID == "" || *shardHash == "" || *keyPath == "" {
		log.Fatal("-intent, -shard, and -key are required")
	}
	key, err := loadAccountKey(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	served := *bytesServed
	if served == 0 {
		data, err := fetchShardBytes(*storageURL, *shardHash)
		if err != nil {
			log.Fatal(err)
		}
		served = uint64(len(data))
	}
	if *requestID == "" {
		*requestID = randomCLIID("retrieval_request")
	}
	receipt := wire.RetrievalReceipt{
		ReceiptID:      randomCLIID("retrieval_receipt"),
		RequestID:      *requestID,
		IntentID:       *intentID,
		ShardHash:      *shardHash,
		User:           key.Address,
		ClientAddress:  key.Address,
		BytesServed:    served,
		ServedAtUnix:   time.Now().Unix(),
		MinerAddress:   "",
		MinerPublicKey: "",
	}
	if err := wire.SignRetrievalClientReceipt(&receipt, key.PrivateKey); err != nil {
		log.Fatal(err)
	}
	if err := client.NewHTTP(*storageURL).Post("/retrieval-receipts/sign", receipt, &receipt); err != nil {
		log.Fatal(err)
	}
	var resp wire.SubmitRetrievalReceiptResponse
	if err := client.NewHTTP(*chainURL).Post("/retrieval-receipts", wire.SubmitRetrievalReceiptRequest{Receipt: receipt}, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("retrieval receipt=%s intent=%s miner=%s bytes=%s reward=%s status=%s\n",
		resp.ReceiptID, resp.IntentID, resp.MinerAddress, formatBytes(resp.BytesServed), formatGF(resp.Reward), resp.Status)
}

func fetchShardBytes(storageURL string, shardHash string) ([]byte, error) {
	shardCID := ""
	if shardHash != "" {
		cid, err := wire.RawCIDForHash(shardHash)
		if err == nil {
			shardCID = cid
		}
	}
	return fetchShardBytesByRef(storageURL, shardHash, shardCID)
}

func fetchShardBytesByRef(storageURL string, shardHash string, shardCID string) ([]byte, error) {
	base := strings.TrimRight(storageURL, "/")
	if shardCID != "" {
		resp, err := http.Get(base + "/blocks/" + shardCID)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return io.ReadAll(resp.Body)
			}
		}
	}
	resp, err := http.Get(base + "/shards/" + shardHash)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch shard failed: %s %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return io.ReadAll(resp.Body)
}

func randomCLIID(prefix string) string {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func download(args []string) {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL used with -intent")
	intentID := fs.String("intent", "", "optional intent id to download from on-chain manifest")
	recordID := fs.String("record", "", "optional data record id to download from")
	storageURLs := fs.String("storage", "", "fallback comma-separated storage node URLs")
	planPath := fs.String("plan", "./upload-plan.json", "upload plan path")
	outPath := fs.String("out", "./restored.bin", "output file path")
	encryptionKey := fs.String("key", "", "encryption key file or raw base64/hex key for encrypted manifests")
	dhtEnabled := fs.Bool("dht", false, "enable DHT-based provider discovery as fallback")
	dhtBootstrap := fs.String("dht-bootstrap", "", "comma-separated DHT bootstrap peer multiaddrs")
	fs.Parse(args)

	var dhtService *falaridht.Service
	if *dhtEnabled {
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"))
		if err != nil {
			log.Fatalf("create ephemeral libp2p host: %v", err)
		}
		defer h.Close()
		dhtCfg := falaridht.DefaultConfig()
		if *dhtBootstrap != "" {
			for _, addr := range strings.Split(*dhtBootstrap, ",") {
				addr = strings.TrimSpace(addr)
				if addr != "" {
					dhtCfg.BootstrapPeers = append(dhtCfg.BootstrapPeers, addr)
				}
			}
		}
		dhtService, err = falaridht.New(h, dhtCfg)
		if err != nil {
			log.Fatalf("create DHT service: %v", err)
		}
		if err := dhtService.Start(); err != nil {
			log.Fatalf("start DHT service: %v", err)
		}
		defer dhtService.Close()
		log.Printf("DHT discovery enabled for download")
	}

	var plan wire.UploadPlan
	useProviderDiscovery := false
	if *recordID != "" {
		recordManifest, err := fetchRecordManifest(*chainURL, *recordID)
		if err != nil {
			log.Fatal(err)
		}
		if *intentID != "" && *intentID != recordManifest.Record.IntentID {
			log.Fatal("-intent does not match -record intent")
		}
		if !recordManifest.Manifest.Complete {
			log.Fatalf("manifest for record %s intent %s is incomplete: receipts=%d",
				*recordID, recordManifest.Record.IntentID, recordManifest.Manifest.ReceiptCount)
		}
		plan = recordManifest.Manifest.Plan
		*intentID = ""
		useProviderDiscovery = true
	}
	if *intentID != "" {
		manifest, err := fetchManifest(*chainURL, *intentID)
		if err != nil {
			log.Fatal(err)
		}
		if !manifest.Complete {
			log.Fatalf("manifest for intent %s is incomplete: receipts=%d", manifest.IntentID, manifest.ReceiptCount)
		}
		plan = manifest.Plan
		useProviderDiscovery = true
	}
	if *intentID == "" && *recordID == "" {
		var err error
		plan, err = client.LoadPlan(*planPath)
		if err != nil {
			log.Fatal(err)
		}
	}
	key, err := encryptionKeyForPlan(plan, *encryptionKey)
	if err != nil {
		log.Fatal(err)
	}
	bySegment := map[int]map[int]wire.MinerReceipt{}
	for _, receipt := range plan.Receipts {
		if bySegment[receipt.SegmentID] == nil {
			bySegment[receipt.SegmentID] = map[int]wire.MinerReceipt{}
		}
		bySegment[receipt.SegmentID][receipt.ShardIndex] = receipt
	}
	if len(bySegment) < len(plan.SegmentRoots) {
		log.Fatalf("plan has receipts for %d segments, expected %d", len(bySegment), len(plan.SegmentRoots))
	}
	out, err := os.Create(*outPath)
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	fallbackEndpoints := parseEndpoints(*storageURLs)
	totalShards := plan.Erasure.DataShards + plan.Erasure.ParityShards
	for segmentID := 0; segmentID < len(plan.SegmentRoots); segmentID++ {
		receipts, ok := bySegment[segmentID]
		if !ok {
			log.Fatalf("missing receipt for segment %d", segmentID)
		}
		discoveryURL := ""
		if useProviderDiscovery {
			discoveryURL = *chainURL
		}
		shards, err := downloadSegmentShards(receipts, totalShards, plan.Erasure.DataShards, fallbackEndpoints, discoveryURL, dhtService, plan.ProviderCache)
		if err != nil {
			log.Fatalf("download segment %d: %v", segmentID, err)
		}
		data, err := client.DecodeShards(shards, plan.Erasure.DataShards, plan.Erasure.ParityShards, decodedSegmentSize(plan, segmentID))
		if err != nil {
			log.Fatalf("decode segment %d: %v", segmentID, err)
		}
		if plan.Encryption != nil {
			data, err = client.DecryptSegment(data, key, *plan.Encryption, segmentID)
			if err != nil {
				log.Fatalf("decrypt segment %d: %v", segmentID, err)
			}
		}
		if _, err := out.Write(data); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("downloaded and verified %s\n", *outPath)
}

func repair(args []string) {
	fs := flag.NewFlagSet("repair", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	storageURLs := fs.String("storage", "http://localhost:9090", "comma-separated storage node URLs")
	planPath := fs.String("plan", "./upload-plan.json", "upload plan path")
	unavailableRaw := fs.String("unavailable", "", "comma-separated unavailable miner addresses to replace")
	useChainTasks := fs.Bool("chain-tasks", true, "create and execute on-chain repair tasks before local fallback")
	includeMissing := fs.Bool("include-missing", true, "include missing shards when creating on-chain repair tasks")
	batchSize := fs.Int("batch", 64, "receipts per batch commit")
	accountKeyPath := fs.String("account-key", "", "account key file used to sign repair batch commits")
	dhtEnabled := fs.Bool("dht", false, "enable DHT-based provider discovery as fallback")
	dhtBootstrap := fs.String("dht-bootstrap", "", "comma-separated DHT bootstrap peer multiaddrs")
	fs.Parse(args)
	if *accountKeyPath == "" {
		log.Fatal("-account-key is required")
	}
	accountKey, err := loadAccountKey(*accountKeyPath)
	if err != nil {
		log.Fatal(err)
	}

	var dhtService *falaridht.Service
	if *dhtEnabled {
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"))
		if err != nil {
			log.Fatalf("create ephemeral libp2p host: %v", err)
		}
		defer h.Close()
		dhtCfg := falaridht.DefaultConfig()
		if *dhtBootstrap != "" {
			for _, addr := range strings.Split(*dhtBootstrap, ",") {
				addr = strings.TrimSpace(addr)
				if addr != "" {
					dhtCfg.BootstrapPeers = append(dhtCfg.BootstrapPeers, addr)
				}
			}
		}
		dhtService, err = falaridht.New(h, dhtCfg)
		if err != nil {
			log.Fatalf("create DHT service: %v", err)
		}
		if err := dhtService.Start(); err != nil {
			log.Fatalf("start DHT service: %v", err)
		}
		defer dhtService.Close()
		log.Printf("DHT discovery enabled for repair")
	}

	plan, err := client.LoadPlan(*planPath)
	if err != nil {
		log.Fatal(err)
	}
	if !strings.EqualFold(plan.User, accountKey.Address) {
		log.Fatalf("plan user %s does not match -account-key address %s", plan.User, accountKey.Address)
	}
	endpoints := parseEndpoints(*storageURLs)
	if len(endpoints) == 0 {
		log.Fatal("at least one storage endpoint is required")
	}
	unavailable := stringSet(*unavailableRaw)
	totalShards := plan.Erasure.DataShards + plan.Erasure.ParityShards
	bySegment := receiptsBySegment(plan.Receipts)
	chainClient := client.NewHTTP(*chainURL)
	var pending []wire.MinerReceipt
	repaired := 0
	if *useChainTasks {
		unavailableList := setValues(unavailable)
		if len(unavailableList) > 0 || *includeMissing {
			var createResp wire.CreateRepairResponse
			if err := chainClient.Post("/repairs", wire.CreateRepairRequest{
				IntentID:          plan.IntentID,
				UnavailableMiners: unavailableList,
				IncludeMissing:    *includeMissing,
			}, &createResp); err != nil && len(unavailableList) > 0 {
				log.Fatal(err)
			}
		}
		var repairResp wire.RepairPlanResponse
		if err := chainClient.Get("/repairs?intent="+plan.IntentID, &repairResp); err != nil {
			log.Fatal(err)
		}
		if assignedTasks := assignedRepairTasks(repairResp.Tasks); len(assignedTasks) > 0 {
			repaired = executeAssignedRepairTasks(chainClient, *chainURL, &plan, assignedTasks, endpoints, *batchSize, *planPath, accountKey)
			fmt.Printf("repair completed from chain tasks: repaired_shards=%d intent=%s\n", repaired, plan.IntentID)
			return
		}
	}

	for segmentID := 0; segmentID < len(plan.SegmentRoots); segmentID++ {
		receipts := bySegment[segmentID]
		if !segmentNeedsRepair(receipts, totalShards, unavailable) {
			continue
		}
		shards := make([][]byte, totalShards)
		available := 0
		for shardIndex, receipt := range receipts {
			if unavailable[receipt.MinerAddress] {
				continue
			}
			providers := discoverStorageProviders(*chainURL, receipt.ShardHash, receipt.ShardCID)
			data, err := downloadShard(receipt, providers, endpoints)
			if err != nil && dhtService != nil {
				// DHT fallback for repair shard download.
				ctx, cancel := context.WithTimeout(context.Background(), falaridht.DefaultLookupTimeout)
				dhtRecords, dhtErr := dhtService.FindProviders(ctx, receipt.ShardHash)
				cancel()
				if dhtErr == nil && len(dhtRecords) > 0 {
					dhtProviders := falaridht.ToStorageProviderRecords(dhtRecords)
					dhtEndpoints := providerEndpoints(dhtProviders)
					data, err = downloadShard(receipt, dhtProviders, dhtEndpoints)
				}
			}
			if err != nil {
				continue
			}
			if chaincrypto.HashBytes(data) != receipt.ShardHash {
				log.Fatalf("downloaded shard hash mismatch for segment %d shard %d", receipt.SegmentID, receipt.ShardIndex)
			}
			shards[shardIndex] = data
			available++
		}
		if available < plan.Erasure.DataShards {
			log.Fatalf("segment %d only has %d available shards, need %d", segmentID, available, plan.Erasure.DataShards)
		}
		segmentData, err := client.DecodeShards(shards, plan.Erasure.DataShards, plan.Erasure.ParityShards, decodedSegmentSize(plan, segmentID))
		if err != nil {
			log.Fatalf("repair decode segment %d: %v", segmentID, err)
		}
		rebuilt, err := client.EncodeShards(segmentData, plan.Erasure.DataShards, plan.Erasure.ParityShards)
		if err != nil {
			log.Fatalf("repair encode segment %d: %v", segmentID, err)
		}
		for shardIndex, shard := range rebuilt {
			existing, ok := receipts[shardIndex]
			if ok && !unavailable[existing.MinerAddress] {
				continue
			}
			shardHash := chaincrypto.HashBytes(shard)
			if shardHash != plan.Segments[segmentID].ShardHashes[shardIndex] {
				log.Fatalf("rebuilt shard hash mismatch for segment %d shard %d", segmentID, shardIndex)
			}
			endpoint := endpoints[(segmentID+shardIndex+repaired)%len(endpoints)]
			req := wire.UploadRequest{
				IntentID:    plan.IntentID,
				User:        plan.User,
				FileRoot:    plan.FileRoot,
				SegmentID:   segmentID,
				SegmentRoot: plan.SegmentRoots[segmentID],
				ShardIndex:  shardIndex,
				ShardID:     fmt.Sprintf("%s:%d:%d:repair:%d", plan.IntentID, segmentID, shardIndex, time.Now().UnixNano()),
				ShardHash:   shardHash,
				ShardCID:    shardCIDForPlan(plan, segmentID, shardIndex),
				ShardSize:   int64(len(shard)),
				DataBase64:  base64.StdEncoding.EncodeToString(shard),
			}
			var receipt wire.MinerReceipt
			if err := client.NewHTTP(endpoint).Post("/upload", req, &receipt); err != nil {
				log.Fatal(err)
			}
			receipt.MinerEndpoint = endpoint
			plan.Receipts = replaceReceipt(plan.Receipts, receipt)
			if bySegment[segmentID] == nil {
				bySegment[segmentID] = map[int]wire.MinerReceipt{}
			}
			bySegment[segmentID][shardIndex] = receipt
			pending = append(pending, receipt)
			repaired++
			if err := client.SavePlan(*planPath, plan); err != nil {
				log.Fatal(err)
			}
			if len(pending) >= *batchSize {
				commitBatch(chainClient, *chainURL, plan, pending, accountKey)
				client.MarkCommitted(&plan, pending)
				pending = nil
				if err := client.SavePlan(*planPath, plan); err != nil {
					log.Fatal(err)
				}
			}
		}
	}
	if len(pending) > 0 {
		commitBatch(chainClient, *chainURL, plan, pending, accountKey)
		client.MarkCommitted(&plan, pending)
	}
	if err := client.SavePlan(*planPath, plan); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("repair completed: repaired_shards=%d intent=%s\n", repaired, plan.IntentID)
}

func showIntent(args []string) {
	fs := flag.NewFlagSet("intent", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("id", "", "intent id")
	fs.Parse(args)

	if *intentID == "" {
		log.Fatal("-id is required")
	}
	var view wire.IntentView
	if err := client.NewHTTP(*chainURL).Get("/intents/"+*intentID, &view); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("intent %s status=%s committed=%d/%d uploaded=%d/%d\n",
		view.IntentID, view.Status, view.CommittedSegments, len(view.SegmentRoots), view.UploadedSize, view.FileSize)
}

func prove(args []string) {
	fs := flag.NewFlagSet("prove", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("intent", "", "intent id")
	count := fs.Int("count", 1, "number of challenges to generate")
	storageURLs := fs.String("storage", "", "fallback comma-separated storage node URLs")
	fs.Parse(args)

	if *intentID == "" {
		log.Fatal("-intent is required")
	}
	var challengeResp wire.GenerateChallengeResponse
	chainClient := client.NewHTTP(*chainURL)
	if err := chainClient.Post("/challenges", wire.GenerateChallengeRequest{
		IntentID: *intentID,
		Count:    *count,
	}, &challengeResp); err != nil {
		log.Fatal(err)
	}

	fallbackEndpoints := parseEndpoints(*storageURLs)
	for _, challenge := range challengeResp.Challenges {
		proof, err := requestProof(challenge, fallbackEndpoints, *chainURL)
		if err != nil {
			log.Fatal(err)
		}
		var submitResp wire.SubmitProofResponse
		if err := chainClient.Post("/proofs", wire.SubmitProofRequest{Proof: proof}, &submitResp); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("proof accepted: challenge=%s miner=%s shard=%s\n",
			submitResp.ChallengeID, submitResp.MinerAddress, proof.ShardHash)
	}
}

func runEpoch(args []string) {
	fs := flag.NewFlagSet("epoch", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	intentID := fs.String("intent", "", "optional intent id")
	challengesPerDeal := fs.Int("challenges", 4, "challenges per finalized deal")
	keyPath := fs.String("key", "", "path to governance operator key file")
	rewardPerProof := fs.Uint64("reward", reward.TokenUnit, "reward per accepted proof")
	slash := fs.Uint64("slash", 1, "slash per missed proof")
	duration := fs.Int64("duration", 600, "epoch duration in seconds")
	storageURLs := fs.String("storage", "", "fallback comma-separated storage node URLs")
	fs.Parse(args)
	if *keyPath == "" {
		log.Fatal("-key is required")
	}

	chainClient := client.NewHTTP(*chainURL)
	var epochResp wire.StartEpochResponse
	if err := postOperator(*chainURL, "/epochs", wire.StartEpochRequest{
		IntentID:            *intentID,
		ChallengesPerDeal:   *challengesPerDeal,
		DurationSeconds:     *duration,
		RewardPerProof:      *rewardPerProof,
		SlashPerMissedProof: *slash,
	}, &epochResp, *keyPath); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("started epoch %s with %d challenges\n", epochResp.Epoch.EpochID, len(epochResp.Challenges))

	fallbackEndpoints := parseEndpoints(*storageURLs)
	for _, challenge := range epochResp.Challenges {
		proof, err := requestProof(challenge, fallbackEndpoints, *chainURL)
		if err != nil {
			log.Printf("proof failed locally: challenge=%s miner=%s error=%v", challenge.ChallengeID, challenge.MinerAddress, err)
			continue
		}
		var submitResp wire.SubmitProofResponse
		if err := chainClient.Post("/proofs", wire.SubmitProofRequest{Proof: proof}, &submitResp); err != nil {
			log.Printf("proof rejected: challenge=%s miner=%s error=%v", challenge.ChallengeID, challenge.MinerAddress, err)
			continue
		}
		fmt.Printf("proof accepted: challenge=%s miner=%s reward=%s\n",
			submitResp.ChallengeID, submitResp.MinerAddress, formatGF(submitResp.Reward))
	}
}

func finalizeEpoch(args []string) {
	fs := flag.NewFlagSet("finalize-epoch", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	epochID := fs.String("epoch", "", "epoch id")
	keyPath := fs.String("key", "", "path to governance operator key file")
	fs.Parse(args)

	if *epochID == "" {
		log.Fatal("-epoch is required")
	}
	if *keyPath == "" {
		log.Fatal("-key is required")
	}
	var resp wire.FinalizeEpochResponse
	if err := postOperator(*chainURL, "/epochs/finalize", wire.FinalizeEpochRequest{EpochID: *epochID}, &resp, *keyPath); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("finalized epoch %s accepted=%d missed=%d storage_rewards=%s retrieval_rewards=%s repair_rewards=%s slashed=%s repairs=%d\n",
		resp.EpochID, resp.AcceptedProofs, resp.MissedProofs,
		formatGF(resp.StorageRewardsPaid), formatGF(resp.RetrievalRewardsPaid), formatGF(resp.RepairRewardsPaid),
		formatGF(resp.StorageSlashed), resp.RepairTasksCreated)
}

func minerStats(args []string) {
	fs := flag.NewFlagSet("miner", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	miner := fs.String("address", "", "miner address")
	fs.Parse(args)

	if *miner == "" {
		log.Fatal("-address is required")
	}
	var stats wire.MinerStats
	if err := client.NewHTTP(*chainURL).Get("/miners/"+*miner, &stats); err != nil {
		log.Fatal(err)
	}
	total := stats.ProofSuccess + stats.ProofFailure
	successRate := "N/A"
	if total > 0 {
		rate := float64(stats.ProofSuccess) / float64(total) * 100
		successRate = fmt.Sprintf("%.1f%%", rate)
	}
	fmt.Printf("miner %s status=%s success=%d failure=%d success_rate=%s consecutive=%d rewards=%s storage_rewards=%s retrieval_rewards=%s repair_rewards=%s slashed=%s locked_bonus=%s bonus_released=%v bonus_expired=%v\n",
		stats.MinerAddress, stats.Status, stats.ProofSuccess, stats.ProofFailure, successRate, stats.ConsecutiveFailures,
		formatGF(stats.Rewards), formatGF(stats.StorageRewards), formatGF(stats.RetrievalRewards), formatGF(stats.RepairRewards),
		formatGF(stats.Slashed), formatGF(stats.LockedBonus), stats.BonusReleased, stats.BonusExpired)
}

func balance(args []string) {
	fs := flag.NewFlagSet("balance", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	address := fs.String("address", "", "account address")
	fs.Parse(args)

	if *address == "" {
		log.Fatal("-address is required")
	}
	var account wire.Account
	if err := client.NewHTTP(*chainURL).Get("/accounts/"+*address, &account); err != nil {
		log.Fatal(err)
	}
	printAccount(account)
}

func transfer(args []string) {
	fs := flag.NewFlagSet("transfer", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	to := fs.String("to", "", "recipient account")
	amount := fs.Uint64("amount", 0, "amount to transfer")
	fee := fs.Uint64("fee", 1, "mempool priority fee")
	keyPath := fs.String("key", "", "account key file for signed transfer")
	fs.Parse(args)

	if *to == "" {
		log.Fatal("-to is required")
	}
	if *keyPath == "" {
		log.Fatal("-key is required")
	}
	key, err := loadAccountKey(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	var account wire.Account
	if err := client.NewHTTP(*chainURL).Get("/accounts/"+key.Address, &account); err != nil {
		log.Fatal(err)
	}
	var statusResp wire.ChainStatusResponse
	if err := client.NewHTTP(*chainURL).Get("/status", &statusResp); err != nil {
		log.Fatal(err)
	}
	req := wire.TransferRequest{
		From:   key.Address,
		To:     *to,
		Amount: *amount,
		Fee:    *fee,
		Nonce:  account.Nonce,
	}
	req.PublicKey = encodeHex(ethcrypto.FromECDSAPub(key.PublicKey))
	if err := wire.SignTransfer(&req, key.PrivateKey, statusResp.ChainID); err != nil {
		log.Fatal(err)
	}
	var resp wire.TransferResponse
	if err := client.NewHTTP(*chainURL).Post("/transfer", req, &resp); err != nil {
		log.Fatal(err)
	}
	printAccount(resp.From)
	printAccount(resp.To)
}

func evmDeploy(args []string) {
	log.Println("warning: evm-deploy is a legacy compatibility command and is no longer part of the production mainline")
	fs := flag.NewFlagSet("evm-deploy", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyPath := fs.String("key", "", "account key file")
	bytecode := fs.String("bytecode", "", "contract init bytecode hex")
	bytecodeFile := fs.String("bytecode-file", "", "path containing contract init bytecode hex")
	value := fs.Uint64("value", 0, "native token value")
	gas := fs.Uint64("gas", 3_000_000, "gas limit")
	gasPrice := fs.Uint64("gas-price", 1, "gas price")
	fs.Parse(args)

	if *keyPath == "" {
		log.Fatal("-key is required")
	}
	code, err := readHexInput(*bytecode, *bytecodeFile)
	if err != nil {
		log.Fatal(err)
	}
	key, err := loadAccountKey(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	nonce := accountNonce(*chainURL, key.Address)
	tx := types.NewContractCreation(nonce, new(big.Int).SetUint64(*value), *gas, new(big.Int).SetUint64(*gasPrice), code)
	rawTx, hash := signAndSubmitEVMTransaction(*chainURL, tx, key.PrivateKey)
	contract := ethcrypto.CreateAddress(common.HexToAddress(key.Address), nonce)
	fmt.Printf("evm deploy tx=%s contract=%s raw=%s\n", hash, contract.Hex(), rawTx)
}

func evmSend(args []string) {
	log.Println("warning: evm-send is a legacy compatibility command and is no longer part of the production mainline")
	fs := flag.NewFlagSet("evm-send", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyPath := fs.String("key", "", "account key file")
	to := fs.String("to", "", "contract or account address")
	dataHex := fs.String("data", "0x", "transaction calldata hex")
	value := fs.Uint64("value", 0, "native token value")
	gas := fs.Uint64("gas", 300_000, "gas limit")
	gasPrice := fs.Uint64("gas-price", 1, "gas price")
	fs.Parse(args)

	if *keyPath == "" || *to == "" {
		log.Fatal("-key and -to are required")
	}
	key, err := loadAccountKey(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	if !common.IsHexAddress(*to) {
		log.Fatal("-to must be an Ethereum address")
	}
	nonce := accountNonce(*chainURL, key.Address)
	tx := types.NewTransaction(nonce, common.HexToAddress(*to), new(big.Int).SetUint64(*value), *gas, new(big.Int).SetUint64(*gasPrice), common.FromHex(*dataHex))
	rawTx, hash := signAndSubmitEVMTransaction(*chainURL, tx, key.PrivateKey)
	fmt.Printf("evm send tx=%s raw=%s\n", hash, rawTx)
}

func evmCall(args []string) {
	log.Println("warning: evm-call is a legacy compatibility command and is no longer part of the production mainline")
	fs := flag.NewFlagSet("evm-call", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	from := fs.String("from", "", "optional caller address")
	to := fs.String("to", "", "contract address")
	dataHex := fs.String("data", "0x", "call data hex")
	value := fs.String("value", "0x0", "call value quantity")
	gas := fs.String("gas", "0x0", "call gas quantity")
	fs.Parse(args)

	if *to == "" {
		log.Fatal("-to is required")
	}
	result := evmJSONRPC[string](*chainURL, "eth_call", []any{map[string]any{
		"from":  *from,
		"to":    *to,
		"data":  *dataHex,
		"value": *value,
		"gas":   *gas,
	}, "latest"})
	fmt.Println(result)
}

func evmCollectionCreate(args []string) {
	log.Println("warning: evm-collection-create is a legacy compatibility command and is no longer part of the production mainline")
	fs := flag.NewFlagSet("evm-collection-create", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyPath := fs.String("key", "", "account key file")
	name := fs.String("name", "", "collection name")
	description := fs.String("description", "", "optional collection description")
	metadataRaw := fs.String("metadata", "", "optional JSON object metadata")
	gas := fs.Uint64("gas", 300_000, "gas limit")
	gasPrice := fs.Uint64("gas-price", 1, "gas price")
	fs.Parse(args)
	_ = gas
	_ = gasPrice

	if *keyPath == "" || *name == "" {
		log.Fatal("-key and -name are required")
	}
	key, err := loadAccountKey(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	req := wire.CreateCollectionRequest{
		User:        key.Address,
		Name:        *name,
		Description: *description,
		Metadata:    parseMetadata(*metadataRaw),
		ChainID:     chainID(*chainURL),
		Nonce:       accountNonce(*chainURL, key.Address),
	}
	if err := wire.SignCreateCollection(&req, key.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp wire.CreateCollectionResponse
	if err := client.NewHTTP(*chainURL).Post("/collections", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("collection %s user=%s name=%s\n", resp.Collection.CollectionID, resp.Collection.User, resp.Collection.Name)
}

func evmRecordAppend(args []string) {
	log.Println("warning: evm-record-append is a legacy compatibility command and is no longer part of the production mainline")
	fs := flag.NewFlagSet("evm-record-append", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyPath := fs.String("key", "", "account key file")
	collectionID := fs.String("collection", "", "collection id")
	intentID := fs.String("intent", "", "finalized intent id")
	parent := fs.String("parent", "", "optional parent record id")
	kind := fs.String("kind", "memory", "record kind")
	recordKey := fs.String("record-key", "", "optional record key")
	manifestRoot := fs.String("manifest-root", "", "optional manifest root")
	metadataRaw := fs.String("metadata", "", "optional JSON object metadata")
	gas := fs.Uint64("gas", 300_000, "gas limit")
	gasPrice := fs.Uint64("gas-price", 1, "gas price")
	fs.Parse(args)
	_ = gas
	_ = gasPrice

	if *keyPath == "" || *collectionID == "" || *intentID == "" {
		log.Fatal("-key, -collection, and -intent are required")
	}
	key, err := loadAccountKey(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	req := wire.AppendRecordRequest{
		CollectionID: *collectionID,
		User:         key.Address,
		IntentID:     *intentID,
		ParentRecord: *parent,
		Kind:         *kind,
		Key:          *recordKey,
		ManifestRoot: *manifestRoot,
		Metadata:     parseMetadata(*metadataRaw),
		ChainID:      chainID(*chainURL),
		Nonce:        accountNonce(*chainURL, key.Address),
	}
	if err := wire.SignAppendRecord(&req, key.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp wire.AppendRecordResponse
	if err := client.NewHTTP(*chainURL).Post("/records", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("record %s collection=%s intent=%s parent=%s kind=%s key=%s\n",
		resp.Record.RecordID, resp.Record.CollectionID, resp.Record.IntentID,
		resp.Record.ParentRecord, resp.Record.Kind, resp.Record.Key)
}

func printAccount(account wire.Account) {
	fmt.Printf("account %s balance=%s nonce=%d locked_stake=%s locked_storage=%s locked_bonus=%s pending_mining=%s\n",
		account.Address, formatGF(account.Balance), account.Nonce, formatGF(account.LockedStake), formatGF(account.LockedStorage), formatGF(account.LockedBonus), formatGF(account.PendingMiningRewards))
}

func accountNonce(chainURL string, address string) uint64 {
	var account wire.Account
	if err := client.NewHTTP(chainURL).Get("/accounts/"+address, &account); err != nil {
		log.Fatal(err)
	}
	return account.Nonce
}

func chainID(chainURL string) string {
	var status wire.ChainStatusResponse
	if err := client.NewHTTP(chainURL).Get("/status", &status); err != nil {
		log.Fatal(err)
	}
	if status.ChainID == "" {
		log.Fatal("chain status did not include chain_id")
	}
	return status.ChainID
}

func operatorNonce(chainURL string, address string) uint64 {
	var resp wire.GovernanceOperatorListResponse
	if err := client.NewHTTP(chainURL).Get("/governance/operators", &resp); err != nil {
		log.Fatal(err)
	}
	normalized := wire.NormalizeAddress(address)
	for _, op := range resp.Operators {
		if wire.NormalizeAddress(op.Operator) == normalized {
			return op.Nonce
		}
	}
	log.Fatalf("governance operator %s not found", address)
	return 0
}

func postOperator(chainURL string, path string, in any, out any, keyPath string) error {
	keyFile := loadGovernanceKey(keyPath)
	privateKey := loadGovernanceECDSAKey(keyPath)
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	nonce := operatorNonce(chainURL, keyFile.Address)
	timestamp := time.Now().Unix()
	signature, err := wire.SignOperatorRequest(chainID(chainURL), http.MethodPost, path, raw, nonce, timestamp, privateKey)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(chainURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Operator-Address", keyFile.Address)
	req.Header.Set("X-Operator-Nonce", fmt.Sprintf("%d", nonce))
	req.Header.Set("X-Operator-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-Operator-Signature", signature)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

type evmRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type evmRPCResponse[T any] struct {
	Result T `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func evmJSONRPC[T any](chainURL string, method string, params any) T {
	var resp evmRPCResponse[T]
	if err := client.NewHTTP(chainURL).Post("/", evmRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}, &resp); err != nil {
		log.Fatal(err)
	}
	if resp.Error != nil {
		log.Fatalf("json-rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result
}

func signAndSubmitEVMTransaction(chainURL string, tx *types.Transaction, privateKey *ecdsa.PrivateKey) (string, string) {
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(wire.DefaultEVMChainIDBig()), privateKey)
	if err != nil {
		log.Fatal(err)
	}
	raw, err := signedTx.MarshalBinary()
	if err != nil {
		log.Fatal(err)
	}
	rawTx := "0x" + hex.EncodeToString(raw)
	hash := evmJSONRPC[string](chainURL, "eth_sendRawTransaction", []any{rawTx})
	return rawTx, hash
}

func readHexInput(raw string, path string) ([]byte, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		raw = strings.TrimSpace(string(data))
	}
	if raw == "" {
		return nil, fmt.Errorf("hex input is required")
	}
	return common.FromHex(raw), nil
}

func encryptionKeyForPlan(plan wire.UploadPlan, keyRef string) ([]byte, error) {
	if plan.Encryption == nil {
		return nil, nil
	}
	key, err := readEncryptionKey(keyRef)
	if err != nil {
		return nil, err
	}
	if err := client.ValidateEncryptionKey(plan.Encryption, key); err != nil {
		return nil, err
	}
	return key, nil
}

func readEncryptionKey(keyRef string) ([]byte, error) {
	if keyRef == "" {
		return nil, fmt.Errorf("-key is required for encrypted storage data")
	}
	raw := keyRef
	if data, err := os.ReadFile(keyRef); err == nil {
		raw = strings.TrimSpace(string(data))
	}
	return client.ParseEncryptionKey(strings.TrimSpace(raw))
}

type accountKeyFile struct {
	Address    string `json:"address"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

type agentKeyFile struct {
	AgentKeyID string `json:"agent_key_id"`
	Master     string `json:"master"`
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
}

type accountKey struct {
	Address    string
	PublicKey  *ecdsa.PublicKey
	PrivateKey *ecdsa.PrivateKey
}

func accountNew(args []string) {
	fs := flag.NewFlagSet("account-new", flag.ExitOnError)
	outPath := fs.String("out", "./account.json", "account key file path")
	fs.Parse(args)

	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		log.Fatal(err)
	}
	key := accountKeyFile{
		Address:    wire.AccountAddress(&privateKey.PublicKey),
		PublicKey:  encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey)),
		PrivateKey: encodeHex(ethcrypto.FromECDSA(privateKey)),
	}
	raw, err := json.MarshalIndent(key, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*outPath, raw, 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("created account %s key=%s\n", key.Address, *outPath)
}

func loadAccountKey(path string) (accountKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return accountKey{}, err
	}
	var keyFile accountKeyFile
	if err := json.Unmarshal(raw, &keyFile); err != nil {
		return accountKey{}, err
	}
	privateKey, err := ethcrypto.HexToECDSA(trimHexPrefix(keyFile.PrivateKey))
	if err != nil {
		return accountKey{}, err
	}
	address := wire.AccountAddress(&privateKey.PublicKey)
	if keyFile.Address != "" && !strings.EqualFold(keyFile.Address, address) {
		return accountKey{}, fmt.Errorf("account key address mismatch: file=%s derived=%s", keyFile.Address, address)
	}
	return accountKey{
		Address:    address,
		PublicKey:  &privateKey.PublicKey,
		PrivateKey: privateKey,
	}, nil
}

func encodeHex(raw []byte) string {
	return "0x" + hex.EncodeToString(raw)
}

func trimHexPrefix(raw string) string {
	return strings.TrimPrefix(strings.TrimPrefix(raw, "0x"), "0X")
}

func showBlock(args []string) {
	fs := flag.NewFlagSet("block", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	height := fs.String("height", "latest", "block height or latest")
	fs.Parse(args)

	path := "/blocks/latest"
	if *height != "latest" {
		path = "/blocks/" + *height
	}
	var resp wire.BlockResponse
	if err := client.NewHTTP(*chainURL).Get(path, &resp); err != nil {
		log.Fatal(err)
	}
	printBlock(resp.Block)
}

func showMempool(args []string) {
	fs := flag.NewFlagSet("mempool", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	fs.Parse(args)

	var resp wire.MempoolResponse
	if err := client.NewHTTP(*chainURL).Get("/mempool", &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("mempool pending=%d base_fee=%s target_block_txs=%d last_block_txs=%d\n",
		len(resp.Pending), formatGF(resp.FeeMarket.BaseFee), resp.FeeMarket.TargetBlockTxs, resp.FeeMarket.LastBlockTxs)
	for _, tx := range resp.Pending {
		fmt.Printf("tx %s type=%s from=%s nonce=%d fee=%s nonce_protected=%t payload=%s\n",
			tx.TxID, tx.Type, tx.From, tx.Nonce, formatGF(tx.Fee), tx.NonceProtected, tx.PayloadHash)
	}
}

func produceBlock(args []string) {
	fs := flag.NewFlagSet("produce-block", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyPath := fs.String("key", "", "path to governance operator key file")
	fs.Parse(args)
	if *keyPath == "" {
		log.Fatal("-key is required")
	}

	var resp wire.ProduceBlockResponse
	if err := postOperator(*chainURL, "/blocks/produce", map[string]string{}, &resp, *keyPath); err != nil {
		log.Fatal(err)
	}
	if !resp.Produced {
		fmt.Println("no pending transactions")
		return
	}
	printBlock(resp.Block)
}

func voteBlock(args []string) {
	fs := flag.NewFlagSet("vote-block", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	height := fs.String("height", "latest", "block height or latest")
	_ = fs.String("validator-key", "", "deprecated: use VALIDATOR_PRIVATE_KEY env var")
	fs.Parse(args)

	path := "/blocks/latest"
	if *height != "latest" {
		path = "/blocks/" + *height
	}
	var blockResp wire.BlockResponse
	if err := client.NewHTTP(*chainURL).Get(path, &blockResp); err != nil {
		log.Fatal(err)
	}
	identity, err := chainpkg.LoadOperatorIdentityFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	power := validatorPowerForVote(*chainURL, identity.OperatorAddress)
	vote := wire.BlockVote{
		Height:             blockResp.Block.Height,
		BlockHash:          blockResp.Block.Hash,
		ValidatorAddress:   identity.OperatorAddress,
		ValidatorPublicKey: identity.OperatorPublicKeyHex(),
		Power:              power,
	}
	if err := wire.SignBlockVote(&vote, identity.OperatorPrivateKey); err != nil {
		log.Fatal(err)
	}
	var voteResp wire.BlockVoteResponse
	if err := client.NewHTTP(*chainURL).Post("/blocks/votes", vote, &voteResp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("vote accepted=%t validator=%s power=%d\n", voteResp.Accepted, vote.ValidatorAddress, vote.Power)
	printBlock(voteResp.Block)
}

func printBlock(block wire.Block) {
	fmt.Printf("block height=%d txs=%d producer=%s hash=%s prev=%s tx_root=%s\n",
		block.Height, len(block.Transactions), block.ProducerAddress, block.Hash, block.PrevHash, block.TxRoot)
	fmt.Printf("finality finalized=%t voting_power=%d total_power=%d threshold=%d votes=%d\n",
		block.Finality.Finalized, block.Finality.VotingPower, block.Finality.TotalPower,
		block.Finality.ThresholdPower, len(block.Finality.Votes))
	for _, tx := range block.Transactions {
		fmt.Printf("tx %s type=%s from=%s\n", tx.TxID, tx.Type, tx.From)
	}
}

func validatorPowerForVote(chainURL string, address string) uint64 {
	var resp wire.ListValidatorsResponse
	if err := client.NewHTTP(chainURL).Get("/validators", &resp); err != nil {
		log.Fatal(err)
	}
	for _, validator := range resp.Validators {
		if validator.OperatorAddress != address {
			continue
		}
		if validator.Stake > 0 {
			return validator.Stake
		}
		return 1
	}
	return 1
}

func listValidators(args []string) {
	fs := flag.NewFlagSet("validators", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	fs.Parse(args)

	var resp wire.ListValidatorsResponse
	if err := client.NewHTTP(*chainURL).Get("/validators", &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("validators=%d\n", len(resp.Validators))
	for _, validator := range resp.Validators {
		fmt.Printf("validator %s status=%s consensus=%t stake=%s delegated=%s produced=%d delegators=%d endpoint=%s\n",
			validator.OwnerAddress, validator.Status, validator.Consensus, formatGF(validator.SelfStake), formatGF(validator.DelegatedStake), validator.ProducedBlocks, validator.DelegatorCount, validator.Endpoint)
	}
}

func listPeers(args []string) {
	fs := flag.NewFlagSet("peers", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	fs.Parse(args)

	var resp struct {
		Peers       []string `json:"peers"`
		LibP2PID    string   `json:"libp2p_id"`
		LibP2PAddrs []string `json:"libp2p_addrs"`
	}
	if err := client.NewHTTP(*chainURL).Get("/peers", &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("peers=%d\n", len(resp.Peers))
	for _, peer := range resp.Peers {
		fmt.Println(peer)
	}
	if resp.LibP2PID != "" {
		fmt.Printf("libp2p_id=%s\n", resp.LibP2PID)
	}
	if len(resp.LibP2PAddrs) > 0 {
		fmt.Printf("libp2p_addrs=%d\n", len(resp.LibP2PAddrs))
		for _, addr := range resp.LibP2PAddrs {
			fmt.Println(addr)
		}
	}
}

func storageProviders(args []string) {
	fs := flag.NewFlagSet("storage-providers", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	shardHash := fs.String("shard", "", "optional shard hash")
	shardCID := fs.String("shard-cid", "", "optional shard CID")
	intentID := fs.String("intent", "", "optional intent id")
	fs.Parse(args)

	path := "/storage/providers"
	query := url.Values{}
	if *shardHash != "" {
		query.Set("shard_hash", *shardHash)
	}
	if *shardCID != "" {
		query.Set("shard_cid", *shardCID)
	}
	if *intentID != "" {
		query.Set("intent", *intentID)
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp wire.StorageProvidersResponse
	if err := client.NewHTTP(*chainURL).Get(path, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("providers=%d", len(resp.Providers))
	if resp.ShardHash != "" {
		fmt.Printf(" shard=%s", resp.ShardHash)
	}
	if resp.ShardCID != "" {
		fmt.Printf(" cid=%s", resp.ShardCID)
	}
	if resp.IntentID != "" {
		fmt.Printf(" intent=%s", resp.IntentID)
	}
	fmt.Println()
	for _, provider := range resp.Providers {
		fmt.Printf("provider %s endpoint=%s peer=%s source=%s health=%d shards=%d stored=%s capacity=%s proofs=%d/%d\n",
			provider.MinerAddress, provider.Endpoint, provider.PeerID, provider.ProviderSource,
			provider.HealthScoreBPS, provider.ShardCount, formatBytes(provider.StoredBytes),
			formatBytes(provider.CapacityBytes), provider.ProofSuccess, provider.ProofFailure)
		for _, addr := range provider.PeerAddrs {
			fmt.Printf("  %s\n", addr)
		}
	}
}

func requestProof(challenge wire.StorageChallenge, fallbackEndpoints []string, chainURL string) (wire.StorageProof, error) {
	endpoints := []string{}
	if challenge.MinerEndpoint != "" {
		endpoints = append(endpoints, strings.TrimRight(challenge.MinerEndpoint, "/"))
	}
	endpoints = append(endpoints, discoverStorageEndpoints(chainURL, challenge.ShardHash, "")...)
	endpoints = append(endpoints, fallbackEndpoints...)
	var lastErr error
	for _, endpoint := range endpoints {
		var proof wire.StorageProof
		err := client.NewHTTP(endpoint).Post("/prove", wire.ProveRequest{Challenge: challenge}, &proof)
		if err == nil {
			return proof, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no endpoint available for challenge %s", challenge.ChallengeID)
	}
	return wire.StorageProof{}, lastErr
}

func receiptsBySegment(receipts []wire.MinerReceipt) map[int]map[int]wire.MinerReceipt {
	out := map[int]map[int]wire.MinerReceipt{}
	for _, receipt := range receipts {
		if out[receipt.SegmentID] == nil {
			out[receipt.SegmentID] = map[int]wire.MinerReceipt{}
		}
		out[receipt.SegmentID][receipt.ShardIndex] = receipt
	}
	return out
}

func uploadAssignment(plan wire.UploadPlan, segmentID int, shardIndex int) (wire.StorageAssignment, bool) {
	for _, assignment := range plan.Assignments {
		if assignment.SegmentID == segmentID && assignment.ShardIndex == shardIndex {
			return assignment, true
		}
	}
	return wire.StorageAssignment{}, false
}

func shardCIDForPlan(plan wire.UploadPlan, segmentID int, shardIndex int) string {
	if segmentID >= 0 && segmentID < len(plan.Segments) {
		segment := plan.Segments[segmentID]
		if shardIndex >= 0 && shardIndex < len(segment.ShardCIDs) {
			return segment.ShardCIDs[shardIndex]
		}
		if shardIndex >= 0 && shardIndex < len(segment.ShardHashes) {
			cid, err := wire.RawCIDForHash(segment.ShardHashes[shardIndex])
			if err == nil {
				return cid
			}
		}
	}
	return ""
}

func encodeUploadSegment(file *os.File, plan wire.UploadPlan, segmentID int, encryptionKey []byte) ([]client.ShardFile, func(), error) {
	if plan.Encryption == nil {
		offset := int64(segmentID) * plan.SegmentSize
		segmentBytes := plan.SegmentSize
		if remaining := plan.FileSize - offset; remaining < segmentBytes {
			segmentBytes = remaining
		}
		return client.EncodeSegmentToTempFiles(file, offset, segmentBytes, plan.Erasure.DataShards, plan.Erasure.ParityShards, "")
	}
	plain, err := readPlaintextSegment(file, *plan.Encryption, segmentID)
	if err != nil {
		return nil, nil, err
	}
	ciphertext, err := client.EncryptSegment(plain, encryptionKey, *plan.Encryption, segmentID)
	if err != nil {
		return nil, nil, err
	}
	return client.EncodeBytesToTempFiles(ciphertext, plan.Erasure.DataShards, plan.Erasure.ParityShards, "")
}

func readPlaintextSegment(file *os.File, meta wire.EncryptionMetadata, segmentID int) ([]byte, error) {
	offset := int64(segmentID) * meta.PlaintextSegmentSize
	size := meta.PlaintextSegmentSize
	if remaining := meta.PlaintextSize - offset; remaining < size {
		size = remaining
	}
	if size <= 0 {
		return nil, fmt.Errorf("segment %d is out of encrypted plaintext range", segmentID)
	}
	plain := make([]byte, size)
	if _, err := file.ReadAt(plain, offset); err != nil && err != io.EOF {
		return nil, err
	}
	return plain, nil
}

func decodedSegmentSize(plan wire.UploadPlan, segmentID int) int {
	if plan.Encryption != nil {
		return client.EncryptedSegmentSize(*plan.Encryption, segmentID)
	}
	return segmentDataSize(plan, segmentID)
}

func assignedRepairTasks(tasks []wire.RepairTask) []wire.RepairTask {
	out := make([]wire.RepairTask, 0, len(tasks))
	seen := map[wire.ShardRef]bool{}
	for _, task := range tasks {
		if task.Status != "" && task.Status != "pending" {
			continue
		}
		if task.Assignment.MinerAddress == "" || task.Assignment.Endpoint == "" {
			continue
		}
		ref := wire.ShardRef{SegmentID: task.SegmentID, ShardIndex: task.ShardIndex}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, task)
	}
	return out
}

func executeAssignedRepairTasks(chainClient *client.HTTP, chainURL string, plan *wire.UploadPlan, tasks []wire.RepairTask, fallbackEndpoints []string, batchSize int, planPath string, accountKey accountKey) int {
	totalShards := plan.Erasure.DataShards + plan.Erasure.ParityShards
	bySegment := receiptsBySegment(plan.Receipts)
	var pending []wire.MinerReceipt
	repaired := 0
	for _, task := range tasks {
		if task.SegmentID < 0 || task.SegmentID >= len(plan.SegmentRoots) || task.ShardIndex < 0 || task.ShardIndex >= totalShards {
			log.Fatalf("repair task out of range: %+v", task)
		}
		receipts := bySegment[task.SegmentID]
		shards := make([][]byte, totalShards)
		available := 0
		for shardIndex, receipt := range receipts {
			if shardIndex == task.ShardIndex || receipt.MinerAddress == task.OldMinerAddress {
				continue
			}
			providers := discoverStorageProviders(chainURL, receipt.ShardHash, receipt.ShardCID)
			data, err := downloadShard(receipt, providers, fallbackEndpoints)
			if err != nil {
				continue
			}
			if chaincrypto.HashBytes(data) != receipt.ShardHash {
				log.Fatalf("downloaded shard hash mismatch for segment %d shard %d", receipt.SegmentID, receipt.ShardIndex)
			}
			shards[shardIndex] = data
			available++
		}
		if available < plan.Erasure.DataShards {
			log.Fatalf("repair task %s segment %d only has %d available shards, need %d", task.RepairID, task.SegmentID, available, plan.Erasure.DataShards)
		}
		segmentData, err := client.DecodeShards(shards, plan.Erasure.DataShards, plan.Erasure.ParityShards, decodedSegmentSize(*plan, task.SegmentID))
		if err != nil {
			log.Fatalf("repair decode segment %d: %v", task.SegmentID, err)
		}
		rebuilt, err := client.EncodeShards(segmentData, plan.Erasure.DataShards, plan.Erasure.ParityShards)
		if err != nil {
			log.Fatalf("repair encode segment %d: %v", task.SegmentID, err)
		}
		shard := rebuilt[task.ShardIndex]
		shardHash := chaincrypto.HashBytes(shard)
		expectedHash := plan.Segments[task.SegmentID].ShardHashes[task.ShardIndex]
		if task.Assignment.ShardHash != "" {
			expectedHash = task.Assignment.ShardHash
		}
		if shardHash != expectedHash {
			log.Fatalf("rebuilt shard hash mismatch for repair task %s segment %d shard %d", task.RepairID, task.SegmentID, task.ShardIndex)
		}
		endpoint := task.Assignment.Endpoint
		if endpoint == "" {
			if len(fallbackEndpoints) == 0 {
				log.Fatalf("repair task %s has no endpoint and no fallback storage endpoints", task.RepairID)
			}
			endpoint = fallbackEndpoints[repaired%len(fallbackEndpoints)]
		}
		req := wire.UploadRequest{
			IntentID:    plan.IntentID,
			User:        plan.User,
			FileRoot:    plan.FileRoot,
			SegmentID:   task.SegmentID,
			SegmentRoot: plan.SegmentRoots[task.SegmentID],
			ShardIndex:  task.ShardIndex,
			ShardID:     fmt.Sprintf("%s:%d:%d:repair-task:%s", plan.IntentID, task.SegmentID, task.ShardIndex, task.RepairID),
			ShardHash:   shardHash,
			ShardCID:    shardCIDForPlan(*plan, task.SegmentID, task.ShardIndex),
			ShardSize:   int64(len(shard)),
			DataBase64:  base64.StdEncoding.EncodeToString(shard),
		}
		var receipt wire.MinerReceipt
		if err := client.NewHTTP(endpoint).Post("/upload", req, &receipt); err != nil {
			log.Fatal(err)
		}
		receipt.MinerEndpoint = endpoint
		plan.Receipts = replaceReceipt(plan.Receipts, receipt)
		plan.Assignments = replaceAssignment(plan.Assignments, task.Assignment)
		if bySegment[task.SegmentID] == nil {
			bySegment[task.SegmentID] = map[int]wire.MinerReceipt{}
		}
		bySegment[task.SegmentID][task.ShardIndex] = receipt
		pending = append(pending, receipt)
		repaired++
		if err := client.SavePlan(planPath, *plan); err != nil {
			log.Fatal(err)
		}
		if len(pending) >= batchSize {
			commitBatch(chainClient, chainURL, *plan, pending, accountKey)
			client.MarkCommitted(plan, pending)
			pending = nil
			if err := client.SavePlan(planPath, *plan); err != nil {
				log.Fatal(err)
			}
		}
	}
	if len(pending) > 0 {
		commitBatch(chainClient, chainURL, *plan, pending, accountKey)
		client.MarkCommitted(plan, pending)
	}
	if err := client.SavePlan(planPath, *plan); err != nil {
		log.Fatal(err)
	}
	return repaired
}

func replaceAssignment(assignments []wire.StorageAssignment, replacement wire.StorageAssignment) []wire.StorageAssignment {
	for i, assignment := range assignments {
		if assignment.SegmentID == replacement.SegmentID && assignment.ShardIndex == replacement.ShardIndex {
			assignments[i] = replacement
			return assignments
		}
	}
	return append(assignments, replacement)
}

func segmentDataSize(plan wire.UploadPlan, segmentID int) int {
	segmentSize := int(plan.SegmentSize)
	if segmentID == len(plan.SegmentRoots)-1 {
		remaining := plan.FileSize - int64(segmentID)*plan.SegmentSize
		if remaining > 0 {
			segmentSize = int(remaining)
		}
	}
	return segmentSize
}

func segmentNeedsRepair(receipts map[int]wire.MinerReceipt, totalShards int, unavailable map[string]bool) bool {
	if len(receipts) < totalShards {
		return true
	}
	for _, receipt := range receipts {
		if unavailable[receipt.MinerAddress] {
			return true
		}
	}
	return false
}

func replaceReceipt(receipts []wire.MinerReceipt, replacement wire.MinerReceipt) []wire.MinerReceipt {
	out := receipts[:0]
	for _, receipt := range receipts {
		if receipt.SegmentID == replacement.SegmentID && receipt.ShardIndex == replacement.ShardIndex {
			continue
		}
		out = append(out, receipt)
	}
	return append(out, replacement)
}

func stringSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func setValues(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func commitBatch(chainClient *client.HTTP, chainURL string, plan wire.UploadPlan, receipts []wire.MinerReceipt, accountKey accountKey) {
	req := wire.BatchCommitRequest{
		ChainID:  chainID(chainURL),
		IntentID: plan.IntentID,
		User:     plan.User,
		Receipts: receipts,
		Nonce:    accountNonce(chainURL, accountKey.Address),
	}
	if err := wire.SignBatchCommit(&req, accountKey.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp wire.BatchCommitResponse
	if err := chainClient.Post("/batch-commits", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("batch committed: %d segments, uploaded %d bytes\n", resp.CommittedSegments, resp.UploadedSize)
}

func estimateFee(size int64) uint64 {
	mb := uint64((size + 1024*1024 - 1) / (1024 * 1024))
	if mb == 0 {
		mb = 1
	}
	return mb
}

func parseEndpoints(raw string) []string {
	var endpoints []string
	for _, endpoint := range strings.Split(raw, ",") {
		endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
		if endpoint != "" {
			endpoints = append(endpoints, endpoint)
		}
	}
	return endpoints
}

func parseMetadata(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		log.Fatalf("invalid metadata JSON object: %v", err)
	}
	return metadata
}

func feeMarketCommand(args []string) {
	if len(args) < 1 {
		log.Fatal("fee-market requires a subcommand: set, get")
	}
	switch args[0] {
	case "set":
		setFeeMarketCmd(args[1:])
	case "get":
		getFeeMarketCmd(args[1:])
	default:
		log.Fatalf("unknown fee-market subcommand: %s", args[0])
	}
}

func setFeeMarketCmd(args []string) {
	fs := flag.NewFlagSet("fee-market set", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyPath := fs.String("key", "", "path to governance operator key file (required)")
	baseFee := fs.Uint64("base-fee", 0, "new base fee in base units")
	targetBlockTxs := fs.Int("target-block-txs", 0, "target transactions per block")
	multBridgeOut := fs.Uint64("mult-bridge-out", 0, "bridge_out multiplier (BPS, 10000=1.0x)")
	multCreateIntent := fs.Uint64("mult-create-intent", 0, "create_intent multiplier (BPS)")
	multUploadNFT := fs.Uint64("mult-upload-nft", 0, "upload_nft_template multiplier (BPS)")
	multRegisterVal := fs.Uint64("mult-register-validator", 0, "register_validator multiplier (BPS)")
	multBatchCommit := fs.Uint64("mult-batch-commit", 0, "batch_commit multiplier (BPS)")
	fs.Parse(args)
	if *keyPath == "" {
		log.Fatal("-key is required")
	}
	req := wire.SetFeeMarketRequest{}
	if *baseFee > 0 {
		req.BaseFee = baseFee
	}
	if *targetBlockTxs > 0 {
		req.TargetBlockTxs = targetBlockTxs
	}
	if *multBridgeOut > 0 || *multCreateIntent > 0 || *multUploadNFT > 0 || *multRegisterVal > 0 || *multBatchCommit > 0 {
		req.Multipliers = &wire.FeeMultipliers{
			BridgeOut:         *multBridgeOut,
			CreateIntent:      *multCreateIntent,
			UploadNFTTemplate: *multUploadNFT,
			RegisterValidator: *multRegisterVal,
			BatchCommit:       *multBatchCommit,
		}
	}
	var resp wire.FeeMarket
	if err := postOperator(*chainURL, "/fee-market", req, &resp, *keyPath); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("fee market updated: base_fee=%s target_block_txs=%d\n",
		formatGF(resp.BaseFee), resp.TargetBlockTxs)
}

func getFeeMarketCmd(args []string) {
	fs := flag.NewFlagSet("fee-market get", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	fs.Parse(args)
	var resp wire.FeeMarket
	if err := client.NewHTTP(*chainURL).Get("/fee-market", &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("fee_market:\n")
	fmt.Printf("  base_fee:          %s\n", formatGF(resp.BaseFee))
	fmt.Printf("  target_block_txs:  %d\n", resp.TargetBlockTxs)
	fmt.Printf("  last_block_txs:    %d\n", resp.LastBlockTxs)
	fmt.Printf("  updated_at_height: %d\n", resp.UpdatedAtHeight)
	fmt.Printf("  multipliers:\n")
	fmt.Printf("    bridge_out:          %d (%.1fx)\n", resp.Multipliers.BridgeOut, float64(resp.Multipliers.BridgeOut)/10000)
	fmt.Printf("    create_intent:       %d (%.1fx)\n", resp.Multipliers.CreateIntent, float64(resp.Multipliers.CreateIntent)/10000)
	fmt.Printf("    upload_nft_template: %d (%.1fx)\n", resp.Multipliers.UploadNFTTemplate, float64(resp.Multipliers.UploadNFTTemplate)/10000)
	fmt.Printf("    register_validator:  %d (%.1fx)\n", resp.Multipliers.RegisterValidator, float64(resp.Multipliers.RegisterValidator)/10000)
	fmt.Printf("    batch_commit:        %d (%.1fx)\n", resp.Multipliers.BatchCommit, float64(resp.Multipliers.BatchCommit)/10000)
}

func formatGF(amount uint64) string {
	whole := amount / wire.TokenUnit
	frac := amount % wire.TokenUnit
	if frac == 0 {
		return fmt.Sprintf("%d GF", whole)
	}
	s := fmt.Sprintf("%d.%08d", whole, frac)
	s = strings.TrimRight(s, "0")
	return s + " GF"
}

func formatBytes(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%dB", value)
	}
	div := uint64(unit)
	exp := 0
	for value/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

func downloadShard(receipt wire.MinerReceipt, providers []wire.StorageProviderRecord, fallbackEndpoints []string) ([]byte, error) {
	providers = storage.RankProvidersForBlockFetch(providers)
	var lastErr error
	if receipt.ShardCID != "" {
		for _, provider := range providers {
			if provider.PeerID == "" || len(provider.PeerAddrs) == 0 {
				continue
			}
			data, err := storage.FetchBlockViaLibP2P(context.Background(), receipt.ShardCID, provider.PeerID, provider.PeerAddrs)
			if err == nil {
				storage.RememberProviderFetchSuccess(provider, "libp2p")
				log.Printf("download transport=libp2p miner=%s cid=%s", provider.MinerAddress, receipt.ShardCID)
				return data, nil
			}
			storage.RememberProviderFetchFailure(provider, "libp2p", err)
			log.Printf("download transport=libp2p miner=%s cid=%s fallback=http error=%v", provider.MinerAddress, receipt.ShardCID, err)
			lastErr = err
		}
	}

	endpoints := storage.PreferredProviderEndpoints(receipt.MinerEndpoint, providers)
	seen := map[string]bool{}
	for _, endpoint := range endpoints {
		seen[endpoint] = true
	}
	for _, endpoint := range fallbackEndpoints {
		endpoint = strings.TrimRight(endpoint, "/")
		if endpoint == "" || seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		endpoints = append(endpoints, endpoint)
	}
	for _, endpoint := range endpoints {
		provider := storage.ResolveProviderRecordForEndpoint(endpoint, receipt.MinerAddress, providers)
		httpClient := client.NewHTTP(endpoint)
		var data []byte
		var err error
		if receipt.ShardCID != "" {
			data, err = httpClient.GetBytes("/blocks/" + receipt.ShardCID)
			if err == nil {
				storage.RememberProviderFetchSuccess(provider, "http-block")
				log.Printf("download transport=http-block endpoint=%s cid=%s", endpoint, receipt.ShardCID)
				return data, nil
			}
			storage.RememberProviderFetchFailure(provider, "http-block", err)
			lastErr = err
		}
		data, err = httpClient.GetBytes("/shards/" + receipt.ShardHash + ".bin")
		if err == nil {
			storage.RememberProviderFetchSuccess(provider, "http-shard")
			log.Printf("download transport=http-shard endpoint=%s hash=%s", endpoint, receipt.ShardHash)
			return data, nil
		}
		storage.RememberProviderFetchFailure(provider, "http-shard", err)
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no endpoint available for shard %s", receipt.ShardHash)
	}
	return nil, lastErr
}

type shardDownloadResult struct {
	shardIndex int
	data       []byte
	err        error
}

func downloadSegmentShards(receipts map[int]wire.MinerReceipt, totalShards int, requiredShards int, fallbackEndpoints []string, chainURL string, dhtSvc *falaridht.Service, providerCache []wire.StorageProviderRecord) ([][]byte, error) {
	if requiredShards <= 0 {
		return nil, fmt.Errorf("required shard count must be positive")
	}
	shards := make([][]byte, totalShards)
	resultCh := make(chan shardDownloadResult, len(receipts))
	var wg sync.WaitGroup
	for shardIndex, receipt := range receipts {
		if shardIndex < 0 || shardIndex >= totalShards {
			continue
		}
		shardIndex := shardIndex
		receipt := receipt
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Phase 1: Try routes from chain API.
			routes := discoverStorageRoutes(chainURL, receipt.ShardHash, receipt.ShardCID)
			if data, err := downloadShardViaRoutes(receipt, routes); err == nil {
				if chaincrypto.HashBytes(data) != receipt.ShardHash {
					err = fmt.Errorf("downloaded shard hash mismatch for segment %d shard %d", receipt.SegmentID, receipt.ShardIndex)
				}
				resultCh <- shardDownloadResult{shardIndex: shardIndex, data: data, err: err}
				return
			}
			// Phase 1.5: Try cached providers from plan (offline support).
			if len(providerCache) > 0 {
				cachedProviders := filterProvidersByShard(providerCache, receipt.ShardHash)
				if len(cachedProviders) > 0 {
					cachedEndpoints := providerEndpoints(cachedProviders)
					data, err := downloadShard(receipt, cachedProviders, cachedEndpoints)
					if err == nil && chaincrypto.HashBytes(data) != receipt.ShardHash {
						err = fmt.Errorf("downloaded shard hash mismatch for segment %d shard %d", receipt.SegmentID, receipt.ShardIndex)
					}
					if err == nil {
						resultCh <- shardDownloadResult{shardIndex: shardIndex, data: data}
						return
					}
				}
			}
			// Phase 2: DHT discovery fallback.
			if dhtSvc != nil {
				ctx, cancel := context.WithTimeout(context.Background(), falaridht.DefaultLookupTimeout)
				dhtRecords, err := dhtSvc.FindProviders(ctx, receipt.ShardHash)
				cancel()
				if err == nil && len(dhtRecords) > 0 {
					dhtProviders := falaridht.ToStorageProviderRecords(dhtRecords)
					dhtEndpoints := providerEndpoints(dhtProviders)
					data, err := downloadShard(receipt, dhtProviders, dhtEndpoints)
					if err == nil && chaincrypto.HashBytes(data) != receipt.ShardHash {
						err = fmt.Errorf("downloaded shard hash mismatch for segment %d shard %d", receipt.SegmentID, receipt.ShardIndex)
					}
					if err == nil {
						resultCh <- shardDownloadResult{shardIndex: shardIndex, data: data}
						return
					}
				}
			}
			// Phase 3: Chain API providers.
			providers := discoverStorageProviders(chainURL, receipt.ShardHash, receipt.ShardCID)
			endpoints := providerEndpoints(providers)
			endpoints = append(endpoints, fallbackEndpoints...)
			data, err := downloadShard(receipt, providers, endpoints)
			if err == nil && chaincrypto.HashBytes(data) != receipt.ShardHash {
				err = fmt.Errorf("downloaded shard hash mismatch for segment %d shard %d", receipt.SegmentID, receipt.ShardIndex)
			}
			resultCh <- shardDownloadResult{shardIndex: shardIndex, data: data, err: err}
		}()
	}
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	available := 0
	var lastErr error
	for result := range resultCh {
		if result.err != nil {
			lastErr = result.err
			continue
		}
		if shards[result.shardIndex] == nil {
			shards[result.shardIndex] = result.data
			available++
		}
		if available >= requiredShards {
			return shards, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("not enough shard receipts available")
	}
	return nil, fmt.Errorf("only downloaded %d/%d required shards: %w", available, requiredShards, lastErr)
}

func downloadShardViaRoutes(receipt wire.MinerReceipt, routes []wire.StorageRoute) ([]byte, error) {
	if len(routes) == 0 {
		return nil, fmt.Errorf("no storage routes available")
	}
	var lastErr error
	for _, route := range routes {
		provider := providerFromRoute(route)
		switch route.Transport {
		case "libp2p":
			if route.ShardCID == "" || route.PeerID == "" || len(route.PeerAddrs) == 0 {
				continue
			}
			data, err := storage.FetchBlockViaLibP2P(context.Background(), route.ShardCID, route.PeerID, route.PeerAddrs)
			if err == nil {
				storage.RememberProviderFetchSuccess(provider, "libp2p")
				log.Printf("download route=libp2p miner=%s cid=%s", route.MinerAddress, route.ShardCID)
				return data, nil
			}
			storage.RememberProviderFetchFailure(provider, "libp2p", err)
			lastErr = err
		case "http-block":
			if route.Endpoint == "" || route.ShardCID == "" {
				continue
			}
			data, err := client.NewHTTP(route.Endpoint).GetBytes("/blocks/" + route.ShardCID)
			if err == nil {
				storage.RememberProviderFetchSuccess(provider, "http-block")
				log.Printf("download route=http-block endpoint=%s cid=%s", route.Endpoint, route.ShardCID)
				return data, nil
			}
			storage.RememberProviderFetchFailure(provider, "http-block", err)
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
				storage.RememberProviderFetchSuccess(provider, "http-shard")
				log.Printf("download route=http-shard endpoint=%s hash=%s", route.Endpoint, shardHash)
				return data, nil
			}
			storage.RememberProviderFetchFailure(provider, "http-shard", err)
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no reachable storage route for shard %s", receipt.ShardHash)
	}
	return nil, lastErr
}

func discoverStorageRoutes(chainURL string, shardHash string, shardCID string) []wire.StorageRoute {
	if chainURL == "" || (shardHash == "" && shardCID == "") {
		return nil
	}
	var resp wire.StorageRoutesResponse
	query := url.Values{}
	if shardHash != "" {
		query.Set("shard_hash", shardHash)
	}
	if shardCID != "" {
		query.Set("shard_cid", shardCID)
	}
	if err := client.NewHTTP(chainURL).Get("/storage/routes?"+query.Encode(), &resp); err != nil {
		return nil
	}
	return resp.Routes
}

func providerFromRoute(route wire.StorageRoute) wire.StorageProviderRecord {
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

// filterProvidersByShard returns providers whose ShardHashes or Shards contain the given shard hash.
func filterProvidersByShard(providers []wire.StorageProviderRecord, shardHash string) []wire.StorageProviderRecord {
	var out []wire.StorageProviderRecord
	for _, p := range providers {
		for _, h := range p.ShardHashes {
			if h == shardHash {
				out = append(out, p)
				goto next
			}
		}
		for _, s := range p.Shards {
			if s.ShardHash == shardHash {
				out = append(out, p)
				goto next
			}
		}
		// If provider has no shard list (e.g. from DHT), include it and let downloadShard try.
		if len(p.ShardHashes) == 0 && len(p.Shards) == 0 && (p.Endpoint != "" || p.PeerID != "") {
			out = append(out, p)
		}
	next:
	}
	return out
}

func discoverStorageEndpoints(chainURL string, shardHash string, shardCID string) []string {
	providers := discoverStorageProviders(chainURL, shardHash, shardCID)
	return providerEndpoints(providers)
}

func discoverStorageProviders(chainURL string, shardHash string, shardCID string) []wire.StorageProviderRecord {
	if chainURL == "" || (shardHash == "" && shardCID == "") {
		return nil
	}
	var resp wire.StorageProvidersResponse
	path := "/storage/providers?shard_hash=" + url.QueryEscape(shardHash)
	if shardCID != "" {
		path = "/storage/providers?shard_cid=" + url.QueryEscape(shardCID)
		if shardHash != "" {
			path += "&shard_hash=" + url.QueryEscape(shardHash)
		}
	}
	if err := client.NewHTTP(chainURL).Get(path, &resp); err != nil {
		return nil
	}
	return resp.Providers
}

func providerEndpoints(providers []wire.StorageProviderRecord) []string {
	return storage.PreferredProviderEndpoints("", providers)
}

func agentKeyCommand(args []string) {
	if len(args) < 1 {
		log.Fatal("agent-key requires a subcommand: create, list, or revoke")
	}
	switch args[0] {
	case "create":
		agentKeyCreate(args[1:])
	case "list":
		agentKeyList(args[1:])
	case "revoke":
		agentKeyRevoke(args[1:])
	default:
		log.Fatalf("unknown agent-key subcommand: %s", args[0])
	}
}

func agentKeyCreate(args []string) {
	fs := flag.NewFlagSet("agent-key create", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	name := fs.String("name", "", "agent key name")
	permissions := fs.String("allow", "", "comma-separated permissions (empty = all)")
	dailyLimit := fs.Uint64("daily-limit", 0, "daily spending limit")
	totalLimit := fs.Uint64("total-limit", 0, "total spending limit")
	expire := fs.Duration("expire", 0, "key lifetime (e.g. 90d)")
	keyFile := fs.String("key-file", "", "existing agent public key file")
	keyOut := fs.String("key-out", "./agent.key", "output path for agent private key")
	masterKey := fs.String("master-key", "", "master account key file")
	fs.Parse(args)
	if *name == "" {
		log.Fatal("-name is required")
	}
	if *masterKey == "" {
		log.Fatal("-master-key is required")
	}
	master, err := loadAccountKey(*masterKey)
	if err != nil {
		log.Fatal(err)
	}
	agentPub := *keyFile
	var agentPriv *ecdsa.PrivateKey
	if agentPub == "" {
		var err error
		agentPriv, err = ethcrypto.GenerateKey()
		if err != nil {
			log.Fatal(err)
		}
		agentPub = encodeHex(ethcrypto.FromECDSAPub(&agentPriv.PublicKey))
	} else {
		log.Fatal("-key-file with pre-existing key not supported with new format; omit -key-file to generate a new key")
	}

	var expiresAt int64
	if *expire > 0 {
		expiresAt = time.Now().Add(*expire).Unix()
	}
	var perms []string
	if *permissions != "" {
		perms = strings.Split(*permissions, ",")
	}
	req := wire.RegisterAgentKeyRequest{
		ChainID:     chainID(*chainURL),
		Master:      master.Address,
		Name:        *name,
		AgentPub:    agentPub,
		Permissions: perms,
		DailyLimit:  *dailyLimit,
		TotalLimit:  *totalLimit,
		ExpiresAt:   expiresAt,
		Nonce:       accountNonce(*chainURL, master.Address),
	}
	if err := wire.SignRegisterAgentKey(&req, master.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp wire.RegisterAgentKeyResponse
	if err := client.NewHTTP(*chainURL).Post("/agent-keys", req, &resp); err != nil {
		log.Fatal(err)
	}

	keyFileContent := agentKeyFile{
		AgentKeyID: resp.Key.KeyID,
		Master:     master.Address,
		Address:    wire.AccountAddress(&agentPriv.PublicKey),
		PrivateKey: encodeHex(ethcrypto.FromECDSA(agentPriv)),
	}
	raw, err := json.MarshalIndent(keyFileContent, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*keyOut, raw, 0o600); err != nil {
		log.Fatal(err)
	}

	agentKeyString := wire.EncodeAgentKeyString(
		resp.Key.KeyID,
		master.Address,
		wire.AccountAddress(&agentPriv.PublicKey),
		encodeHex(ethcrypto.FromECDSA(agentPriv)),
	)
	agentKeyReference := wire.EncodeAgentKeyReferenceString(
		resp.Key.KeyID,
		master.Address,
		wire.AccountAddress(&agentPriv.PublicKey),
	)

	fmt.Printf("\n============================================================\n")
	fmt.Printf("  Copy this key to your AI agent:\n")
	fmt.Printf("  %s\n", agentKeyString)
	fmt.Printf("\n  Production gateway reference (no private key in header):\n")
	fmt.Printf("  %s\n", agentKeyReference)
	fmt.Printf("============================================================\n\n")
	fmt.Printf("  name:         %s\n", *name)
	fmt.Printf("  permissions:  %v\n", resp.Key.Permissions)
	fmt.Printf("  daily_limit:  %d\n", resp.Key.DailyLimit)
	fmt.Printf("  total_limit:  %d\n", resp.Key.TotalLimit)
	if resp.Key.ExpiresAt > 0 {
		fmt.Printf("  expires_at:   %s\n", time.Unix(resp.Key.ExpiresAt, 0))
	}
	fmt.Printf("  key file:     %s  (backup)\n", *keyOut)
}

func agentKeyList(args []string) {
	fs := flag.NewFlagSet("agent-key list", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	master := fs.String("master", "", "master address")
	masterKey := fs.String("master-key", "", "master account key file")
	fs.Parse(args)
	if *master == "" && *masterKey != "" {
		key, err := loadAccountKey(*masterKey)
		if err != nil {
			log.Fatal(err)
		}
		*master = key.Address
	}
	if *master == "" {
		log.Fatal("-master or -master-key is required")
	}
	var resp wire.ListAgentKeysResponse
	if err := client.NewHTTP(*chainURL).Get("/agent-keys?master="+*master, &resp); err != nil {
		log.Fatal(err)
	}
	for _, key := range resp.Keys {
		status := "active"
		if key.Revoked {
			status = "revoked"
		} else if key.ExpiresAt > 0 && time.Now().Unix() > key.ExpiresAt {
			status = "expired"
		}
		fmt.Printf("%-16s %-24s %-24s %-10d %-10d %-10s\n",
			key.KeyID, key.Name, strings.Join(key.Permissions, ","),
			key.DailyLimit, key.UsedToday, status)
	}
}

func agentKeyRevoke(args []string) {
	fs := flag.NewFlagSet("agent-key revoke", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	keyID := fs.String("id", "", "agent key id")
	master := fs.String("master", "", "master address")
	masterKey := fs.String("master-key", "", "master account key file")
	fs.Parse(args)
	if *keyID == "" {
		log.Fatal("-id is required")
	}
	if *master == "" && *masterKey != "" {
		key, err := loadAccountKey(*masterKey)
		if err != nil {
			log.Fatal(err)
		}
		*master = key.Address
	}
	if *master == "" {
		log.Fatal("-master or -master-key is required")
	}
	masterKeyObj, err := loadAccountKey(*masterKey)
	if err != nil {
		log.Fatal(err)
	}
	revokeNonce := accountNonce(*chainURL, *master)
	revokeReq := wire.RevokeAgentKeyRequest{
		ChainID: chainID(*chainURL),
		KeyID:   *keyID,
		Master:  *master,
		Nonce:   revokeNonce,
	}
	if err := wire.SignRevokeAgentKey(&revokeReq, masterKeyObj.PrivateKey); err != nil {
		log.Fatal(err)
	}
	var resp map[string]string
	if err := client.NewHTTP(*chainURL).Post("/agent-keys/revoke", revokeReq, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("agent key %s revoked\n", *keyID)
}

func genesisCommand(args []string) {
	if len(args) < 1 || args[0] != "init" {
		log.Fatal("genesis requires subcommand: init")
	}
	genesisInit(args[1:])
}

func genesisInit(args []string) {
	fs := flag.NewFlagSet("genesis init", flag.ExitOnError)
	outPath := fs.String("out", "./genesis.json", "output genesis file path")
	chainID := fs.String("chain-id", "falari-1", "chain identifier")
	fs.Parse(args)

	now := time.Now().Unix()
	doc := wire.GenesisDoc{
		ChainID:     *chainID,
		GenesisTime: now,
		RewardPools: &wire.GenesisRewardPools{
			StoragePoolRemaining:    wire.TokenStoragePoolInitial,
			RetrievalPoolRemaining:  wire.TokenRetrievalPoolInitial,
			ValidatorPoolRemaining:  wire.TokenValidatorPoolInitial,
			PermanentFundRemaining:  wire.TokenPermanentFundPoolInitial,
			FoundationPoolRemaining: wire.TokenFoundationPoolInitial,
		},
	}

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*outPath, raw, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("genesis file created: %s\n", *outPath)
	fmt.Printf("edit this file to add accounts and validators, then start chainnode with:\n")
	fmt.Printf("  chainnode -genesis %s\n", *outPath)
}

func garbageCollect(args []string) {
	fs := flag.NewFlagSet("gc", flag.ExitOnError)
	chainURL := fs.String("chain", "http://localhost:8080", "chain node URL")
	data := fs.String("data", "./data/miner1", "storage data directory")
	fs.Parse(args)

	minerKey := os.Getenv("MINER_PRIVATE_KEY")
	if minerKey == "" {
		log.Fatal("MINER_PRIVATE_KEY environment variable is not set")
	}
	node, err := storage.OpenNode(*data, minerKey)
	if err != nil {
		log.Fatalf("open storage node: %v", err)
	}

	blocks, err := node.ListStoredBlocks()
	if err != nil {
		log.Fatalf("list local blocks: %v", err)
	}
	if len(blocks) == 0 {
		fmt.Println("gc: no local shards found")
		return
	}

	var resp wire.MinerShardsResponse
	if err := client.NewHTTP(*chainURL).Get("/miners/"+node.Address()+"/shards", &resp); err != nil {
		log.Fatalf("query chain shards: %v", err)
	}

	assigned := make(map[string]bool, len(resp.ShardHashes))
	for _, h := range resp.ShardHashes {
		assigned[h] = true
	}

	var orphans []storage.StoredBlock
	for _, block := range blocks {
		if !assigned[block.Hash] {
			orphans = append(orphans, block)
		}
	}

	if len(orphans) == 0 {
		fmt.Printf("gc complete local=%d assigned=%d orphans=0\n", len(blocks), len(resp.ShardHashes))
		return
	}

	var deleted int
	var freedBytes int64
	for _, orphan := range orphans {
		if err := node.DeleteShard(orphan.Hash); err != nil {
			log.Printf("gc delete failed shard=%s error=%v", orphan.Hash, err)
			continue
		}
		deleted++
		freedBytes += orphan.Size
		fmt.Printf("gc deleted shard=%s size=%s\n", orphan.Hash, formatBytes(uint64(orphan.Size)))
	}
	fmt.Printf("gc complete local=%d assigned=%d orphans=%d deleted=%d freed=%s\n",
		len(blocks), len(resp.ShardHashes), len(orphans), deleted, formatBytes(uint64(freedBytes)))
}

func usage() {
	fmt.Println(`usage:
  chainctl status        -chain http://localhost:8080 -storage http://localhost:9090,http://localhost:9091
  chainctl manifest      -chain http://localhost:8080 -intent intent_xxx -out ./download-plan.json
  chainctl collection-create -chain http://localhost:8080 -user user_demo -name agent-memory
  chainctl collection-create -chain http://localhost:8080 -key ./alice.json -name agent-memory
  chainctl collection-append -chain http://localhost:8080 -collection collection_xxx -intent intent_xxx -kind memory -key session/1
  chainctl collection-append -chain http://localhost:8080 -account-key ./alice.json -collection collection_xxx -intent intent_xxx -kind memory -key session/1
  chainctl collection-records -chain http://localhost:8080 -collection collection_xxx -kind memory -key session/1 -latest -limit 5
  chainctl collection-download -gateway http://localhost:9090 -collection collection_xxx -out ./downloads
  chainctl record       -chain http://localhost:8080 -id record_xxx
  chainctl create-intent -chain http://localhost:8080 -file ./data.bin -out ./upload-plan.json -data-shards 4 -parity-shards 2
  chainctl create-intent -chain http://localhost:8080 -file ./data.bin -out ./upload-plan.json -encrypt -key-out ./storage.key
  chainctl upload        -chain http://localhost:8080 -storage http://localhost:9090,http://localhost:9091 -plan ./upload-plan.json -file ./data.bin
  chainctl upload        -chain http://localhost:8080 -storage http://localhost:9090,http://localhost:9091 -plan ./upload-plan.json -file ./data.bin -key ./storage.key
  chainctl finalize      -chain http://localhost:8080 -plan ./upload-plan.json
  chainctl settle-intent -chain http://localhost:8080 -plan ./upload-plan.json
  chainctl renew        -chain http://localhost:8080 -plan ./upload-plan.json -duration 31536000
  chainctl permanent-fund -chain http://localhost:8080 -plan ./upload-plan.json -amount 1000000
  chainctl terminate-deal -chain http://localhost:8080 -plan ./upload-plan.json
  chainctl delete-tasks  -chain http://localhost:8080 -intent intent_xxx
  chainctl access-policy -chain http://localhost:8080 -intent intent_xxx -user user_demo -status blocked -reason-hash hash
  chainctl governance-keygen -out ./governance-key.json
  chainctl governance-propose -chain http://localhost:8080 -key ./governance-key.json -intent intent_xxx -action freeze -reason-hash hash -expires-at-unix 1893456000
  chainctl governance-propose -chain http://localhost:8080 -key ./governance-key.json -action add_operator -reason-hash hash -target-operator addr -target-public-key base64key -target-permissions freeze,block
  chainctl governance-vote -chain http://localhost:8080 -key ./governance-key.json -proposal gov_proposal_xxx -approve
  chainctl governance-execute -chain http://localhost:8080 -key ./governance-key.json -proposal gov_proposal_xxx
  chainctl governance-proposals -chain http://localhost:8080 -status pending
  chainctl governance-operators -chain http://localhost:8080
  chainctl governance-audit -chain http://localhost:8080 -intent intent_xxx
  chainctl retrieval-receipt -chain http://localhost:8080 -storage http://localhost:9090 -intent intent_xxx -shard shard_hash -key ./alice.json
  chainctl download      -plan ./upload-plan.json -out ./restored.bin
  chainctl download      -chain http://localhost:8080 -intent intent_xxx -out ./restored.bin
  chainctl download      -chain http://localhost:8080 -record record_xxx -out ./restored.bin
  chainctl download      -chain http://localhost:8080 -intent intent_xxx -out ./restored.bin -key ./storage.key
  chainctl mine          -chain http://localhost:8080 -addr :9090 -data ./data/miner1 -endpoint http://localhost:9090
  chainctl gc            -chain http://localhost:8080 -data ./data/miner1
  chainctl repair        -chain http://localhost:8080 -storage http://localhost:9090,http://localhost:9091 -plan ./upload-plan.json -unavailable miner_xxx
  chainctl prove         -chain http://localhost:8080 -intent intent_xxx -count 3
  chainctl storage-providers -chain http://localhost:8080 -shard shard_hash
  chainctl epoch         -chain http://localhost:8080 -intent intent_xxx -challenges 3 -reward 10
  chainctl finalize-epoch -chain http://localhost:8080 -epoch epoch_xxx
  chainctl miner         -chain http://localhost:8080 -address miner_xxx
  chainctl account-new   -out ./alice.json
  chainctl balance       -chain http://localhost:8080 -address user_demo
  chainctl transfer      -chain http://localhost:8080 -from user_demo -to user_2 -amount 10
  chainctl transfer      -chain http://localhost:8080 -key ./alice.json -to user_2 -amount 10
  legacy compatibility:
  chainctl evm-deploy    -chain http://localhost:8080 -key ./alice.json -bytecode 0x600a600c600039600a6000f3602a60005260206000f3
  chainctl evm-send      -chain http://localhost:8080 -key ./alice.json -to 0xContract -data 0x
  chainctl evm-call      -chain http://localhost:8080 -to 0xContract -data 0x
  chainctl evm-collection-create -chain http://localhost:8080 -key ./alice.json -name agent-memory
  chainctl evm-record-append -chain http://localhost:8080 -key ./alice.json -collection collection_xxx -intent intent_xxx -kind memory -record-key session/1
  chainctl intent        -chain http://localhost:8080 -id intent_xxx
  chainctl mempool       -chain http://localhost:8080
  chainctl consensus     -chain http://localhost:8080
  chainctl consensus-votes -chain http://localhost:8080 -height 1 -type precommit
  chainctl block         -chain http://localhost:8080 -height latest
  chainctl produce-block -chain http://localhost:8080
  chainctl vote-block    -chain http://localhost:8080 -height latest -validator-key ./data/validator.json
  chainctl agent-key create -name my-agent -allow upload,retrieval -daily-limit 500
  chainctl agent-key list   -master 0xa1b2c3...
  chainctl agent-key revoke -id key_abc123 -master 0xa1b2c3...
  chainctl validators    -chain http://localhost:8080
  chainctl peers         -chain http://localhost:8080
  chainctl genesis init  -out ./genesis.json`)
}
