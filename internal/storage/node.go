package storage

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"chain/internal/client"
	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type Node struct {
	dataDir                   string
	address                   string
	publicKey                 *ecdsa.PublicKey
	privateKey                *ecdsa.PrivateKey
	backend                   StorageBackend
	blockstore                Blockstore
	transport                 transportCounters
	chainURL                  string
	endpoint                  string
	requireChainAuthorization bool
}

type transportCounters struct {
	libp2pFetchSuccess uint64
	libp2pFetchErrors  uint64
	httpFallbacks      uint64
	httpBlockFetchHits uint64
	httpShardFetchHits uint64
	libp2pServeHits    uint64
	httpBlockServeHits uint64
	httpShardServeHits uint64
}

type Meta struct {
	Address          string `json:"address"`
	PublicKey        string `json:"public_key"`
	SectorCommitment string `json:"sector_commitment"`
}

// OpenNode opens or initializes a storage node using the given hex-encoded ECDSA
// private key. The key is passed via environment variable (MINER_PRIVATE_KEY) by
// the CLI; no private key material is stored on disk.
func OpenNode(dataDir string, privKeyHex string) (*Node, error) {
	backend, err := NewFileBackend(dataDir)
	if err != nil {
		return nil, err
	}

	cleanHex := strings.TrimPrefix(strings.TrimPrefix(privKeyHex, "0x"), "0X")
	priv, err := ethcrypto.HexToECDSA(cleanHex)
	if err != nil {
		return nil, errors.New("invalid MINER_PRIVATE_KEY: " + err.Error())
	}
	addr := wire.AccountAddress(&priv.PublicKey)

	metaPath := filepath.Join(dataDir, "node.json")
	raw, err := os.ReadFile(metaPath)
	if errors.Is(err, os.ErrNotExist) {
		return newNode(dataDir, metaPath, addr, priv, backend), nil
	}
	if err != nil {
		return nil, err
	}

	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	if !strings.EqualFold(meta.Address, addr) {
		return nil, fmt.Errorf("node.json address %s does not match key-derived address %s", meta.Address, addr)
	}
	return &Node{
		dataDir:    dataDir,
		address:    addr,
		publicKey:  &priv.PublicKey,
		privateKey: priv,
		backend:    backend,
		blockstore: NewBackendBlockstore(backend),
	}, nil
}

func newNode(dataDir, metaPath, address string, priv *ecdsa.PrivateKey, backend StorageBackend) *Node {
	pubHex := wire.EncodeHex(ethcrypto.CompressPubkey(&priv.PublicKey))
	meta := Meta{
		Address:   address,
		PublicKey: pubHex,
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err == nil {
		_ = os.WriteFile(metaPath, raw, 0o600)
	}
	return &Node{
		dataDir:    dataDir,
		address:    address,
		publicKey:  &priv.PublicKey,
		privateKey: priv,
		backend:    backend,
		blockstore: NewBackendBlockstore(backend),
	}
}

func (n *Node) Address() string {
	return n.address
}

func (n *Node) PublicKeyHex() string {
	return wire.EncodeHex(ethcrypto.CompressPubkey(n.publicKey))
}

func (n *Node) PrivateKey() *ecdsa.PrivateKey {
	return n.privateKey
}

func (n *Node) Blockstore() Blockstore {
	return n.blockstore
}

func (n *Node) ConfigureChain(chainURL string, endpoint string) {
	n.chainURL = strings.TrimRight(chainURL, "/")
	n.endpoint = endpoint
}

func (n *Node) RequireChainAuthorization(required bool) {
	n.requireChainAuthorization = required
}

func (n *Node) Status() wire.StorageNodeStatusResponse {
	resp := wire.StorageNodeStatusResponse{
		Status:                 "ok",
		Address:                n.address,
		PublicKey:              n.PublicKeyHex(),
		DataDir:                n.dataDir,
		AccessServiceRequired:  true,
		UploadServiceEnabled:   true,
		DownloadServiceEnabled: true,
	}
	blocks, _ := n.backend.ListBlocks()
	for _, block := range blocks {
		resp.ShardCount++
		resp.StoredBytes += uint64(block.Size)
	}
	resp.TransportStats = n.transportStats()
	resp.RecentProviderMemories = ProviderTransportMemorySnapshot(maxProviderMemoryEntries)
	return resp
}

func (n *Node) transportStats() wire.StorageTransportStats {
	if n == nil {
		return wire.StorageTransportStats{}
	}
	return wire.StorageTransportStats{
		LibP2PFetchSuccess: atomic.LoadUint64(&n.transport.libp2pFetchSuccess),
		LibP2PFetchErrors:  atomic.LoadUint64(&n.transport.libp2pFetchErrors),
		HTTPFallbacks:      atomic.LoadUint64(&n.transport.httpFallbacks),
		HTTPBlockFetchHits: atomic.LoadUint64(&n.transport.httpBlockFetchHits),
		HTTPShardFetchHits: atomic.LoadUint64(&n.transport.httpShardFetchHits),
		LibP2PServeHits:    atomic.LoadUint64(&n.transport.libp2pServeHits),
		HTTPBlockServeHits: atomic.LoadUint64(&n.transport.httpBlockServeHits),
		HTTPShardServeHits: atomic.LoadUint64(&n.transport.httpShardServeHits),
	}
}

func (n *Node) recordLibP2PFetchSuccess() {
	if n != nil {
		atomic.AddUint64(&n.transport.libp2pFetchSuccess, 1)
	}
}

func (n *Node) recordLibP2PFetchError() {
	if n != nil {
		atomic.AddUint64(&n.transport.libp2pFetchErrors, 1)
	}
}

func (n *Node) recordHTTPFallback() {
	if n != nil {
		atomic.AddUint64(&n.transport.httpFallbacks, 1)
	}
}

func (n *Node) recordHTTPBlockFetchHit() {
	if n != nil {
		atomic.AddUint64(&n.transport.httpBlockFetchHits, 1)
	}
}

func (n *Node) recordHTTPShardFetchHit() {
	if n != nil {
		atomic.AddUint64(&n.transport.httpShardFetchHits, 1)
	}
}

func (n *Node) recordLibP2PServeHit() {
	if n != nil {
		atomic.AddUint64(&n.transport.libp2pServeHits, 1)
	}
}

func (n *Node) recordHTTPBlockServeHit() {
	if n != nil {
		atomic.AddUint64(&n.transport.httpBlockServeHits, 1)
	}
}

func (n *Node) recordHTTPShardServeHit() {
	if n != nil {
		atomic.AddUint64(&n.transport.httpShardServeHits, 1)
	}
}

func (n *Node) ProviderRecord(endpoint string, capacityBytes uint64, peerID string, peerAddrs []string, ttl time.Duration) (wire.StorageProviderRecord, error) {
	status := n.Status()
	now := time.Now().Unix()
	record := wire.StorageProviderRecord{
		MinerAddress:           n.address,
		PublicKey:              n.PublicKeyHex(),
		Endpoint:               endpoint,
		PeerID:                 peerID,
		PeerAddrs:              append([]string(nil), peerAddrs...),
		CapacityBytes:          capacityBytes,
		StoredBytes:            status.StoredBytes,
		ShardCount:             status.ShardCount,
		AccessServiceRequired:  true,
		UploadServiceEnabled:   true,
		DownloadServiceEnabled: true,
		ShardHashes:            n.ShardHashes(),
		LastSeenUnix:           now,
		ExpiresAtUnix:          now + int64(ttl.Seconds()),
	}
	record.Shards = make([]wire.ProviderShard, 0, len(record.ShardHashes))
	blocks, _ := n.backend.ListBlocks()
	for _, block := range blocks {
		record.Shards = append(record.Shards, wire.ProviderShard{ShardHash: block.Hash, ShardCID: block.CID, Size: block.Size})
	}
	if err := wire.SignStorageProvider(&record, n.privateKey); err != nil {
		return wire.StorageProviderRecord{}, err
	}
	return record, nil
}

func (n *Node) ShardHashes() []string {
	blocks, _ := n.backend.ListBlocks()
	hashes := make([]string, 0, len(blocks))
	for _, block := range blocks {
		hashes = append(hashes, block.Hash)
	}
	return hashes
}

func (n *Node) Register(chainURL string, endpoint string, capacityBytes uint64, stake uint64) error {
	if chainURL == "" {
		return nil
	}
	req := wire.RegisterMinerRequest{
		MinerAddress:  n.address,
		PublicKey:     n.PublicKeyHex(),
		Endpoint:      endpoint,
		CapacityBytes: capacityBytes,
		Stake:         stake,
	}
	if err := wire.SignMinerRegistration(&req, n.privateKey); err != nil {
		return err
	}
	return postJSON(chainURL, "/miners", req)
}

func (n *Node) Store(req wire.UploadRequest) (wire.MinerReceipt, error) {
	data, err := base64.StdEncoding.DecodeString(req.DataBase64)
	if err != nil {
		return wire.MinerReceipt{}, err
	}
	if int64(len(data)) != req.ShardSize {
		return wire.MinerReceipt{}, errors.New("shard size mismatch")
	}
	if hash := chaincrypto.HashBytes(data); hash != req.ShardHash {
		return wire.MinerReceipt{}, errors.New("shard hash mismatch")
	}
	shardCID := req.ShardCID
	if shardCID == "" {
		var err error
		shardCID, err = wire.RawCIDForBytes(data)
		if err != nil {
			return wire.MinerReceipt{}, err
		}
	}
	if err := n.authorizeUpload(req, shardCID); err != nil {
		return wire.MinerReceipt{}, err
	}

	if err := n.backend.PutBlock(StoredBlock{
		CID:  shardCID,
		Hash: req.ShardHash,
		Size: int64(len(data)),
	}, data); err != nil {
		return wire.MinerReceipt{}, err
	}

	receipt := wire.MinerReceipt{
		Version:          1,
		MinerAddress:     n.address,
		MinerPublicKey:   n.PublicKeyHex(),
		User:             req.User,
		IntentID:         req.IntentID,
		FileRoot:         req.FileRoot,
		SegmentID:        req.SegmentID,
		SegmentRoot:      req.SegmentRoot,
		ShardIndex:       req.ShardIndex,
		ShardID:          req.ShardID,
		ShardHash:        req.ShardHash,
		ShardCID:         shardCID,
		ShardSize:        req.ShardSize,
		SectorCommitment: chaincrypto.DataMerkleRoot(data, chaincrypto.DefaultLeafSize),
		ExpiresAtUnix:    time.Now().Add(30 * time.Minute).Unix(),
	}
	if err := wire.SignReceipt(&receipt, n.privateKey); err != nil {
		return wire.MinerReceipt{}, err
	}
	return receipt, nil
}

func (n *Node) authorizeUpload(req wire.UploadRequest, shardCID string) error {
	if n.chainURL == "" {
		if n.requireChainAuthorization {
			return errors.New("chain authorization is required for uploads")
		}
		return nil
	}
	if req.IntentID == "" {
		return errors.New("intent id is required")
	}
	var intent wire.IntentView
	if err := client.NewHTTP(n.chainURL).Get("/intents/"+url.PathEscape(req.IntentID), &intent); err != nil {
		return fmt.Errorf("verify upload assignment: %w", err)
	}
	if intent.User != "" && req.User != "" && !strings.EqualFold(intent.User, req.User) {
		return errors.New("upload user does not match intent")
	}
	if intent.FileRoot != "" && req.FileRoot != "" && intent.FileRoot != req.FileRoot {
		return errors.New("upload file root does not match intent")
	}
	if req.SegmentID < 0 || req.ShardIndex < 0 {
		return errors.New("invalid upload segment or shard index")
	}
	if req.SegmentID >= len(intent.Segments) {
		return errors.New("upload segment is not in intent plan")
	}
	segment := intent.Segments[req.SegmentID]
	if segment.SegmentID != req.SegmentID {
		return errors.New("upload segment id mismatch")
	}
	if segment.SegmentRoot != "" && req.SegmentRoot != "" && segment.SegmentRoot != req.SegmentRoot {
		return errors.New("upload segment root does not match intent")
	}
	if req.ShardIndex >= len(segment.ShardHashes) {
		return errors.New("upload shard index is not in intent plan")
	}
	if expectedHash := segment.ShardHashes[req.ShardIndex]; expectedHash != "" && expectedHash != req.ShardHash {
		return errors.New("upload shard hash does not match intent plan")
	}
	for _, assignment := range intent.Assignments {
		if assignment.SegmentID != req.SegmentID || assignment.ShardIndex != req.ShardIndex {
			continue
		}
		if !strings.EqualFold(assignment.MinerAddress, n.address) {
			continue
		}
		if assignment.ShardHash != "" && assignment.ShardHash != req.ShardHash {
			return errors.New("upload shard hash does not match assignment")
		}
		if assignment.ShardCID != "" && shardCID != "" && assignment.ShardCID != shardCID {
			return errors.New("upload shard cid does not match assignment")
		}
		if assignment.ShardSize > 0 && assignment.ShardSize != req.ShardSize {
			return errors.New("upload shard size does not match assignment")
		}
		return nil
	}
	return errors.New("upload shard is not assigned to this storage node")
}

func (n *Node) ReadShard(hash string) ([]byte, error) {
	return n.backend.GetByHash(hash)
}

func (n *Node) ReadShardByCID(cid string) ([]byte, error) {
	return n.backend.GetByCID(cid)
}

func (n *Node) DeleteShard(hash string) error {
	return n.backend.DeleteByHash(hash)
}

func (n *Node) DeleteShardByCID(cid string) error {
	return n.backend.DeleteByCID(cid)
}

// ShardHashForCID returns the shard hash for a given block CID.
func (n *Node) ShardHashForCID(blockCID string) string {
	blocks, _ := n.backend.ListBlocks()
	for _, block := range blocks {
		if block.CID == blockCID {
			return block.Hash
		}
	}
	return ""
}

func (n *Node) SignRetrievalReceipt(receipt wire.RetrievalReceipt) (wire.RetrievalReceipt, error) {
	if receipt.MinerAddress != "" && receipt.MinerAddress != n.address {
		return wire.RetrievalReceipt{}, errors.New("retrieval receipt is for a different miner")
	}
	if receipt.MinerPublicKey != "" && receipt.MinerPublicKey != n.PublicKeyHex() {
		return wire.RetrievalReceipt{}, errors.New("retrieval receipt public key mismatch")
	}
	data, err := n.ReadShard(receipt.ShardHash)
	if err != nil {
		return wire.RetrievalReceipt{}, err
	}
	if receipt.BytesServed == 0 {
		receipt.BytesServed = uint64(len(data))
	}
	if receipt.BytesServed > uint64(len(data)) {
		return wire.RetrievalReceipt{}, errors.New("retrieval bytes exceed local shard size")
	}
	receipt.MinerAddress = n.address
	receipt.MinerPublicKey = n.PublicKeyHex()
	if err := wire.VerifyRetrievalClientReceipt(receipt); err != nil {
		return wire.RetrievalReceipt{}, err
	}
	if err := wire.SignRetrievalReceiptMiner(&receipt, n.privateKey); err != nil {
		return wire.RetrievalReceipt{}, err
	}
	return receipt, nil
}

func (n *Node) Prove(challenge wire.StorageChallenge) (wire.StorageProof, error) {
	if challenge.MinerAddress != n.address {
		return wire.StorageProof{}, errors.New("challenge is for a different miner")
	}
	if challenge.ExpiresAtUnix > 0 && time.Now().Unix() > challenge.ExpiresAtUnix {
		return wire.StorageProof{}, errors.New("challenge expired")
	}
	data, err := n.ReadShard(challenge.ShardHash)
	if err != nil {
		return wire.StorageProof{}, err
	}
	if hash := chaincrypto.HashBytes(data); hash != challenge.ShardHash {
		return wire.StorageProof{}, errors.New("local shard hash mismatch")
	}
	if int64(len(data)) != challenge.ShardSize {
		return wire.StorageProof{}, errors.New("local shard size mismatch")
	}
	indices := challenge.LeafIndices
	if len(indices) == 0 {
		indices = []int{challenge.LeafIndex}
	}
	leafHashes := make([]string, 0, len(indices))
	leafPayloads := make([]string, 0, len(indices))
	merklePaths := make([][]string, 0, len(indices))
	for _, index := range indices {
		proofData, err := chaincrypto.BuildMerkleProof(data, challenge.LeafSize, index)
		if err != nil {
			return wire.StorageProof{}, err
		}
		if proofData.Root != challenge.SectorCommitment {
			return wire.StorageProof{}, errors.New("local sector commitment mismatch")
		}
		payload := leafPayload(data, challenge.LeafSize, index)
		if chaincrypto.HashBytes(payload) != proofData.LeafHash {
			return wire.StorageProof{}, errors.New("local leaf payload hash mismatch")
		}
		leafHashes = append(leafHashes, proofData.LeafHash)
		leafPayloads = append(leafPayloads, base64.StdEncoding.EncodeToString(payload))
		merklePaths = append(merklePaths, proofData.Path)
	}
	proof := wire.StorageProof{
		ChallengeID:        challenge.ChallengeID,
		ProofType:          challenge.ProofType,
		ChallengeHash:      challenge.ChallengeHash,
		MinerAddress:       n.address,
		MinerPublicKey:     n.PublicKeyHex(),
		ShardHash:          challenge.ShardHash,
		ShardSize:          int64(len(data)),
		SectorCommitment:   challenge.SectorCommitment,
		LeafSize:           challenge.LeafSize,
		LeafIndex:          indices[0],
		LeafIndices:        indices,
		LeafHash:           leafHashes[0],
		LeafHashes:         leafHashes,
		LeafDataBase64:     leafPayloads[0],
		LeafPayloadsBase64: leafPayloads,
		MerklePath:         merklePaths[0],
		MerklePaths:        merklePaths,
		ProofHash:          proofHash(challenge, leafHashes, leafPayloads),
	}
	if err := wire.SignProof(&proof, n.privateKey); err != nil {
		return wire.StorageProof{}, err
	}
	return proof, nil
}

func leafPayload(data []byte, leafSize int, index int) []byte {
	if leafSize <= 0 {
		leafSize = chaincrypto.DefaultLeafSize
	}
	start := index * leafSize
	if start >= len(data) {
		return nil
	}
	end := start + leafSize
	if end > len(data) {
		end = len(data)
	}
	return data[start:end]
}

func proofHash(challenge wire.StorageChallenge, leafHashes []string, leafPayloads []string) string {
	return chaincrypto.HashBytes([]byte(challenge.ChallengeID + ":" + challenge.Nonce + ":" + challenge.ChallengeHash + ":" + strings.Join(leafHashes, ",") + ":" + strings.Join(leafPayloads, ",") + ":" + challenge.SectorCommitment))
}

func postJSON(baseURL string, path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	resp, err := http.Post(strings.TrimRight(baseURL, "/")+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		return fmt.Errorf("post %s failed: http %d: %s", path, resp.StatusCode, body.String())
	}
	return nil
}

func (n *Node) StartAutoReceiptCollector(chainURL string, interval time.Duration) {
	if chainURL == "" || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if err := n.collectAndSubmitRetrievalReceipts(chainURL); err != nil {
				log.Printf("auto receipt collector error: %v", err)
			}
		}
	}()
}

func (n *Node) collectAndSubmitRetrievalReceipts(chainURL string) error {
	receipts, err := n.pendingRetrievalReceipts(chainURL)
	if err != nil {
		return err
	}
	for _, receipt := range receipts {
		signed, err := n.SignRetrievalReceipt(receipt)
		if err != nil {
			log.Printf("sign retrieval receipt error: %v", err)
			continue
		}
		if err := postJSON(chainURL, "/retrievals", signed); err != nil {
			log.Printf("submit retrieval receipt error: %v", err)
			continue
		}
	}
	return nil
}

func (n *Node) pendingRetrievalReceipts(chainURL string) ([]wire.RetrievalReceipt, error) {
	return nil, nil
}

func (n *Node) EnableShardCache(maxEntries int) {
	if maxEntries <= 0 {
		return
	}
	log.Printf("shard cache enabled max_entries=%d", maxEntries)
}
