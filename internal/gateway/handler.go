package gateway

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chain/internal/client"
	chaincrypto "chain/internal/crypto"
	falaridht "chain/internal/dht"
	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type Config struct {
	ChainURL               string
	StorageEndpoints       []string
	TmpDir                 string
	DataShards             int
	ParityShards           int
	SegmentSize            int64
	MaxUploadBytes         int64
	AgentPrivateKeys       map[string]string
	AllowPrivateKeyAPIKeys bool
}

type Handler struct {
	cfg        Config
	mu         sync.Mutex
	resumeJobs map[string]*ResumeJob
	dhtService *falaridht.Service
}

type ResumeJob struct {
	IntentID          string
	FileName          string
	FileSize          int64
	FilePath          string
	Segments          []wire.SegmentPlan
	SegmentRoots      []string
	FileRoot          string
	CommittedSegments []int
	CreatedAt         time.Time
}

type agentKeyCtx struct {
	AgentKeyID string
	Master     string
	Address    string
	PrivateKey string
}

const (
	defaultGatewayMaxUploadBytes = 1 << 30
	multipartMemoryBytes         = 32 << 20
)

type agentUploadAuth struct {
	ChainID    string
	KeyID      string
	Master     string
	Nonce      uint64
	PrivateKey *ecdsa.PrivateKey
}

func New(cfg Config) (*Handler, error) {
	if cfg.MaxUploadBytes == 0 {
		cfg.MaxUploadBytes = defaultGatewayMaxUploadBytes
	}
	if err := os.MkdirAll(cfg.TmpDir, 0o700); err != nil {
		return nil, err
	}
	return &Handler{
		cfg:        cfg,
		resumeJobs: map[string]*ResumeJob{},
	}, nil
}

// SetDHTService attaches a DHT service for fallback shard discovery.
func (h *Handler) SetDHTService(svc *falaridht.Service) {
	h.dhtService = svc
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload", h.handleUpload)
	mux.HandleFunc("GET /download/{intent_id}", h.handleDownload)
	mux.HandleFunc("GET /collection/{collection_id}/files", h.handleCollectionFiles)
	mux.HandleFunc("GET /status/{intent_id}", h.handleStatus)
	mux.HandleFunc("GET /gateway/health", h.handleHealth)
	return mux
}

func (h *Handler) decodeAPIKey(r *http.Request) (*agentKeyCtx, error) {
	key := r.Header.Get("X-Api-Key")
	if key == "" {
		return nil, errors.New("missing X-Api-Key header")
	}
	if strings.HasPrefix(key, wire.AgentKeyReferencePrefix) {
		parts, err := wire.DecodeAgentKeyReferenceString(key)
		if err != nil {
			return nil, fmt.Errorf("invalid api key reference: %w", err)
		}
		return &agentKeyCtx{
			AgentKeyID: parts.AgentKeyID,
			Master:     parts.Master,
			Address:    parts.Address,
		}, nil
	}
	if !h.cfg.AllowPrivateKeyAPIKeys {
		return nil, errors.New("api key contains private key; use an agent key reference configured on the gateway")
	}
	parts, err := wire.DecodeAgentKeyString(key)
	if err != nil {
		return nil, fmt.Errorf("invalid api key: %w", err)
	}
	return &agentKeyCtx{
		AgentKeyID: parts.AgentKeyID,
		Master:     parts.Master,
		Address:    parts.Address,
		PrivateKey: parts.PrivateKey,
	}, nil
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	ak, err := h.decodeAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	if h.cfg.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxUploadBytes)
	}
	if err := r.ParseMultipartForm(multipartMemoryBytes); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("parse multipart: %w", err))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing file field: %w", err))
		return
	}
	defer file.Close()

	// Sanitize filename: strip path components to prevent directory traversal
	fileName := filepath.Base(header.Filename)
	if fileName == "" || fileName == "." || fileName == ".." {
		fileName = "upload.bin"
	}

	sessionDir, err := os.MkdirTemp(h.cfg.TmpDir, "upload-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer os.RemoveAll(sessionDir)

	tmpPath := filepath.Join(sessionDir, fileName)
	f, err := os.Create(tmpPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	written, err := io.Copy(f, file)
	f.Close()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	result, err := h.runUpload(ak, tmpPath, fileName, written)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) runUpload(ak *agentKeyCtx, filePath, fileName string, fileSize int64) (map[string]any, error) {
	planFileSize, planSegments, planSegmentRoots, planFileRoot, err :=
		client.ComputeErasurePlan(filePath, h.cfg.SegmentSize, h.cfg.DataShards, h.cfg.ParityShards)
	if err != nil {
		return nil, fmt.Errorf("compute erasure plan: %w", err)
	}

	chainClient := client.NewHTTP(h.cfg.ChainURL)
	chainID, err := h.chainID(chainClient)
	if err != nil {
		return nil, err
	}
	auth, err := h.agentUploadAuth(chainClient, ak, chainID)
	if err != nil {
		return nil, err
	}

	intentReq := wire.CreateIntentRequest{
		ChainID:      chainID,
		User:         ak.Master,
		FileName:     fileName,
		FileSize:     planFileSize,
		SegmentSize:  h.cfg.SegmentSize,
		FileRoot:     planFileRoot,
		SegmentRoots: planSegmentRoots,
		Segments:     planSegments,
		Erasure: wire.ErasurePolicy{
			DataShards:   h.cfg.DataShards,
			ParityShards: h.cfg.ParityShards,
			ShardSize:    int(h.cfg.SegmentSize) / h.cfg.DataShards,
		},
		Policy: wire.StoragePolicy{
			Class:     "permanent",
			Duration:  86400 * 365,
			Renewable: true,
		},
		DeadlineUnix: time.Now().Add(24 * time.Hour).Unix(),
		AgentKeyID:   auth.KeyID,
		AgentNonce:   auth.Nonce,
	}

	var quote wire.StorageQuoteResponse
	if err := chainClient.Post("/storage/quote", wire.StorageQuoteRequest{
		FileSize: planFileSize,
		Erasure:  intentReq.Erasure,
		Policy:   intentReq.Policy,
	}, &quote); err != nil {
		return nil, fmt.Errorf("storage quote: %w", err)
	}
	intentReq.LockedFee = quote.RequiredFee
	if err := wire.SignCreateIntentAgent(&intentReq, auth.PrivateKey); err != nil {
		return nil, fmt.Errorf("sign create intent with agent key: %w", err)
	}

	var intentResp wire.CreateIntentResponse
	if err := chainClient.Post("/intents", intentReq, &intentResp); err != nil {
		return nil, fmt.Errorf("create intent: %w", err)
	}
	auth.Nonce++

	if err := h.uploadSegments(&auth, intentResp.IntentID, planFileRoot, planSegmentRoots, planSegments, intentResp.Assignments, fileSize, filePath); err != nil {
		return nil, err
	}

	manifestRoot := chaincrypto.HashBytes([]byte(planFileRoot + ":" + fileName))
	finalizeReq := wire.FinalizeRequest{
		ChainID:      auth.ChainID,
		IntentID:     intentResp.IntentID,
		User:         ak.Master,
		ManifestRoot: manifestRoot,
		AgentKeyID:   auth.KeyID,
		AgentNonce:   auth.Nonce,
	}
	if err := wire.SignFinalizeAgent(&finalizeReq, auth.PrivateKey); err != nil {
		return nil, fmt.Errorf("sign finalize with agent key: %w", err)
	}
	var finalizeResp wire.FinalizeResponse
	if err := chainClient.Post("/finalize", finalizeReq, &finalizeResp); err != nil {
		return nil, fmt.Errorf("finalize: %w", err)
	}

	return map[string]any{
		"intent_id":     finalizeResp.IntentID,
		"deal_id":       finalizeResp.DealID,
		"status":        finalizeResp.Status,
		"file_name":     fileName,
		"file_size":     fileSize,
		"data_shards":   h.cfg.DataShards,
		"parity_shards": h.cfg.ParityShards,
	}, nil
}

func (h *Handler) uploadSegments(auth *agentUploadAuth, intentID, fileRoot string, segmentRoots []string, segments []wire.SegmentPlan, assignments []wire.StorageAssignment, fileSize int64, filePath string) error {
	totalShards := h.cfg.DataShards + h.cfg.ParityShards
	chainClient := client.NewHTTP(h.cfg.ChainURL)

	for segIdx := range segments {
		segBytes := segmentBytes(fileSize, int64(segIdx), h.cfg.SegmentSize)
		if segBytes <= 0 {
			continue
		}

		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("open upload file for segment %d: %w", segIdx, err)
		}
		shardFiles, cleanup, err := client.EncodeSegmentToTempFiles(
			file,
			int64(segIdx)*h.cfg.SegmentSize,
			segBytes,
			h.cfg.DataShards,
			h.cfg.ParityShards,
			h.cfg.TmpDir,
		)
		_ = file.Close()
		if err != nil {
			if cleanup != nil {
				cleanup()
			}
			return fmt.Errorf("encode segment %d: %w", segIdx, err)
		}

		segmentPlan := segments[segIdx]
		receipts := make([]wire.MinerReceipt, totalShards)

		var wg sync.WaitGroup
		var mu sync.Mutex
		var firstErr error

		for i := 0; i < totalShards; i++ {
			wg.Add(1)
			go func(shardIndex int) {
				defer wg.Done()
				shard := shardFiles[shardIndex]

				if shard.Hash != segmentPlan.ShardHashes[shardIndex] {
					mu.Lock()
					firstErr = fmt.Errorf("shard %d hash mismatch", shardIndex)
					mu.Unlock()
					return
				}

				assignment, ok := uploadAssignment(assignments, segIdx, shardIndex)
				if !ok {
					mu.Lock()
					firstErr = fmt.Errorf("missing storage assignment for segment %d shard %d", segIdx, shardIndex)
					mu.Unlock()
					return
				}
				endpoint := assignment.Endpoint
				if endpoint == "" && len(h.cfg.StorageEndpoints) > 0 {
					endpoint = h.cfg.StorageEndpoints[shardIndex%len(h.cfg.StorageEndpoints)]
				}
				if endpoint == "" {
					mu.Lock()
					firstErr = fmt.Errorf("storage assignment missing endpoint for segment %d shard %d", segIdx, shardIndex)
					mu.Unlock()
					return
				}
				shardData, err := os.ReadFile(shard.Path)
				if err != nil {
					mu.Lock()
					firstErr = err
					mu.Unlock()
					return
				}

				uploadReq := wire.UploadRequest{
					IntentID:    intentID,
					User:        auth.Master,
					FileRoot:    fileRoot,
					SegmentID:   segIdx,
					SegmentRoot: segmentRoots[segIdx],
					ShardIndex:  shardIndex,
					ShardID:     fmt.Sprintf("%s:%d:%d", intentID, segIdx, shardIndex),
					ShardHash:   shard.Hash,
					ShardCID:    assignment.ShardCID,
					ShardSize:   shard.Size,
					DataBase64:  base64.StdEncoding.EncodeToString(shardData),
				}

				var receipt wire.MinerReceipt
				stClient := client.NewHTTP(endpoint)
				if err := stClient.Post("/upload", uploadReq, &receipt); err != nil {
					mu.Lock()
					firstErr = fmt.Errorf("upload shard %d to %s: %w", shardIndex, endpoint, err)
					mu.Unlock()
					return
				}
				receipt.MinerEndpoint = endpoint

				mu.Lock()
				receipts[shardIndex] = receipt
				mu.Unlock()
			}(i)
		}
		wg.Wait()
		cleanup()

		if firstErr != nil {
			return firstErr
		}

		batchReq := wire.BatchCommitRequest{
			ChainID:    auth.ChainID,
			IntentID:   intentID,
			User:       auth.Master,
			Receipts:   receipts,
			AgentKeyID: auth.KeyID,
			AgentNonce: auth.Nonce,
		}
		if err := wire.SignBatchCommitAgent(&batchReq, auth.PrivateKey); err != nil {
			return fmt.Errorf("sign batch commit segment %d with agent key: %w", segIdx, err)
		}
		var batchResp wire.BatchCommitResponse
		if err := chainClient.Post("/batch-commits", batchReq, &batchResp); err != nil {
			return fmt.Errorf("batch commit segment %d: %w", segIdx, err)
		}
		auth.Nonce++
		_ = batchResp
	}

	return nil
}

func (h *Handler) chainID(chainClient *client.HTTP) (string, error) {
	var status wire.ChainStatusResponse
	if err := chainClient.Get("/status", &status); err != nil {
		return "", fmt.Errorf("chain status: %w", err)
	}
	if status.ChainID == "" {
		return "", errors.New("chain status did not include chain_id")
	}
	return status.ChainID, nil
}

func (h *Handler) agentUploadAuth(chainClient *client.HTTP, ak *agentKeyCtx, chainID string) (agentUploadAuth, error) {
	privateKeyHex := ak.PrivateKey
	if privateKeyHex == "" {
		privateKeyHex = h.cfg.AgentPrivateKeys[ak.AgentKeyID]
	}
	if privateKeyHex == "" {
		return agentUploadAuth{}, errors.New("agent private key is not configured on gateway")
	}
	privateKey, err := ethcrypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return agentUploadAuth{}, fmt.Errorf("parse agent private key: %w", err)
	}
	agentAddress := wire.AccountAddress(&privateKey.PublicKey)
	if !strings.EqualFold(agentAddress, ak.Address) {
		return agentUploadAuth{}, errors.New("agent private key does not match api key address")
	}
	var resp wire.ListAgentKeysResponse
	if err := chainClient.Get("/agent-keys?master="+url.QueryEscape(ak.Master), &resp); err != nil {
		return agentUploadAuth{}, fmt.Errorf("load agent key state: %w", err)
	}
	for _, key := range resp.Keys {
		if key.KeyID != ak.AgentKeyID {
			continue
		}
		if !strings.EqualFold(key.Master, ak.Master) {
			return agentUploadAuth{}, errors.New("agent key master mismatch")
		}
		if key.Revoked {
			return agentUploadAuth{}, errors.New("agent key has been revoked")
		}
		if key.ExpiresAt > 0 && time.Now().Unix() > key.ExpiresAt {
			return agentUploadAuth{}, errors.New("agent key expired")
		}
		agentPub := hex.EncodeToString(ethcrypto.FromECDSAPub(&privateKey.PublicKey))
		if !strings.EqualFold(agentPub, key.AgentPub) {
			return agentUploadAuth{}, errors.New("agent private key does not match registered public key")
		}
		if !agentPermission(key.Permissions, "create_intent") || !agentPermission(key.Permissions, "batch_commit") || !agentPermission(key.Permissions, "finalize") {
			return agentUploadAuth{}, errors.New("agent key lacks upload permissions")
		}
		return agentUploadAuth{
			ChainID:    chainID,
			KeyID:      key.KeyID,
			Master:     wire.NormalizeAddress(ak.Master),
			Nonce:      key.Nonce,
			PrivateKey: privateKey,
		}, nil
	}
	return agentUploadAuth{}, errors.New("agent key not found on chain")
}

func agentPermission(permissions []string, permission string) bool {
	for _, p := range permissions {
		if p == permission {
			return true
		}
	}
	return false
}

func uploadAssignment(assignments []wire.StorageAssignment, segmentID int, shardIndex int) (wire.StorageAssignment, bool) {
	for _, assignment := range assignments {
		if assignment.SegmentID == segmentID && assignment.ShardIndex == shardIndex {
			return assignment, true
		}
	}
	return wire.StorageAssignment{}, false
}

func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	intentID := r.PathValue("intent_id")
	if intentID == "" {
		writeError(w, http.StatusBadRequest, errors.New("intent_id is required"))
		return
	}

	chainClient := client.NewHTTP(h.cfg.ChainURL)
	var manifest wire.StorageManifestResponse
	if err := chainClient.Get("/manifests/"+intentID, &manifest); err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("get manifest: %w", err))
		return
	}

	plan := manifest.Plan
	fileName := plan.FileName
	if fileName == "" {
		fileName = intentID + ".bin"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	w.Header().Set("Content-Type", "application/octet-stream")

	for segIdx, segment := range plan.Segments {
		shards := make([][]byte, len(segment.ShardHashes))
		var wg sync.WaitGroup
		var mu sync.Mutex
		var firstErr error

		for i := range segment.ShardHashes {
			wg.Add(1)
			go func(shardIndex int) {
				defer wg.Done()
				cid := ""
				if shardIndex < len(segment.ShardCIDs) {
					cid = segment.ShardCIDs[shardIndex]
				}
				data, err := h.fetchShard(segment.ShardHashes[shardIndex], cid)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("fetch shard %d/%d: %w", segIdx, shardIndex, err)
					}
					mu.Unlock()
					return
				}
				mu.Lock()
				shards[shardIndex] = data
				mu.Unlock()
			}(i)
		}
		wg.Wait()
		if firstErr != nil {
			writeError(w, http.StatusBadGateway, firstErr)
			return
		}

		segBytes := int(segmentBytes(plan.FileSize, int64(segIdx), plan.SegmentSize))
		reconstructed, err := client.DecodeShards(shards, h.cfg.DataShards, h.cfg.ParityShards, segBytes)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("decode segment %d: %w", segIdx, err))
			return
		}

		if _, err := w.Write(reconstructed); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
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

func (h *Handler) handleCollectionFiles(w http.ResponseWriter, r *http.Request) {
	collectionID := r.PathValue("collection_id")
	if collectionID == "" {
		writeError(w, http.StatusBadRequest, errors.New("collection_id is required"))
		return
	}

	chainClient := client.NewHTTP(h.cfg.ChainURL)

	var recordsResp wire.CollectionRecordsResponse
	if err := chainClient.Get("/collections/"+collectionID+"/records", &recordsResp); err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("get collection records: %w", err))
		return
	}

	files := make([]collectionFileInfo, 0, len(recordsResp.Records))
	for _, rec := range recordsResp.Records {
		var manifest wire.StorageManifestResponse
		if err := chainClient.Get("/manifests/"+rec.IntentID, &manifest); err != nil {
			continue
		}
		fileName := manifest.Plan.FileName
		if fileName == "" {
			fileName = rec.IntentID + ".bin"
		}
		files = append(files, collectionFileInfo{
			IntentID:  rec.IntentID,
			RecordID:  rec.RecordID,
			FileName:  fileName,
			FileSize:  manifest.Plan.FileSize,
			Encrypted: manifest.Plan.Encryption != nil,
			Kind:      rec.Kind,
			Key:       rec.Key,
		})
	}

	resp := collectionFilesResponse{
		CollectionID: collectionID,
		Name:         recordsResp.Collection.Name,
		Files:        files,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) fetchShard(shardHash, shardCID string) ([]byte, error) {
	// Phase 1: Try known static endpoints (existing behavior).
	for _, endpoint := range h.cfg.StorageEndpoints {
		stClient := client.NewHTTP(endpoint)
		if shardCID != "" {
			data, err := stClient.GetBytes("/blocks/" + shardCID)
			if err == nil {
				return data, nil
			}
		}
		data, err := stClient.GetBytes("/shards/" + shardHash + ".bin")
		if err == nil {
			return data, nil
		}
	}
	// Phase 2: DHT-based discovery (fallback when gateway endpoints fail).
	if h.dhtService != nil {
		ctx, cancel := context.WithTimeout(context.Background(), falaridht.DefaultLookupTimeout)
		defer cancel()
		providers, err := h.dhtService.FindProviders(ctx, shardHash)
		if err == nil {
			for _, provider := range providers {
				if provider.Endpoint == "" {
					continue
				}
				stClient := client.NewHTTP(provider.Endpoint)
				if shardCID != "" {
					data, err := stClient.GetBytes("/blocks/" + shardCID)
					if err == nil {
						return data, nil
					}
				}
				data, err := stClient.GetBytes("/shards/" + shardHash + ".bin")
				if err == nil {
					return data, nil
				}
			}
		}
	}
	// Phase 3: Chain API fallback (safety net).
	if h.cfg.ChainURL != "" {
		chainClient := client.NewHTTP(h.cfg.ChainURL)
		var providersResp struct {
			Providers []wire.StorageProviderRecord `json:"providers"`
		}
		if err := chainClient.Get("/storage/providers?shard_hash="+shardHash, &providersResp); err == nil {
			for _, provider := range providersResp.Providers {
				if provider.Endpoint == "" {
					continue
				}
				stClient := client.NewHTTP(provider.Endpoint)
				if shardCID != "" {
					data, err := stClient.GetBytes("/blocks/" + shardCID)
					if err == nil {
						return data, nil
					}
				}
				data, err := stClient.GetBytes("/shards/" + shardHash + ".bin")
				if err == nil {
					return data, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("shard %s not found on any storage node", shardHash)
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	intentID := r.PathValue("intent_id")
	chainClient := client.NewHTTP(h.cfg.ChainURL)
	var intent wire.IntentView
	if err := chainClient.Get("/intents/"+intentID, &intent); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, intent)
}

func segmentBytes(fileSize int64, segIdx int64, segSize int64) int64 {
	start := segIdx * segSize
	if start >= fileSize {
		return 0
	}
	remaining := fileSize - start
	if remaining > segSize {
		return segSize
	}
	return remaining
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
