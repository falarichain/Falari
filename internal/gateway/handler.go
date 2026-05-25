package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"chain/internal/client"
	chaincrypto "chain/internal/crypto"
	falaridht "chain/internal/dht"
	"chain/internal/wire"
)

type Config struct {
	ChainURL         string
	StorageEndpoints []string
	TmpDir           string
	DataShards       int
	ParityShards     int
	SegmentSize      int64
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

func New(cfg Config) (*Handler, error) {
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
	mux.HandleFunc("GET /status/{intent_id}", h.handleStatus)
	mux.HandleFunc("GET /gateway/health", h.handleHealth)
	return mux
}

func (h *Handler) decodeAPIKey(r *http.Request) (*agentKeyCtx, error) {
	key := r.Header.Get("X-Api-Key")
	if key == "" {
		return nil, errors.New("missing X-Api-Key header")
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

	if err := r.ParseMultipartForm(1 << 30); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("parse multipart: %w", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing file field: %w", err))
		return
	}
	defer file.Close()

	fileName := header.Filename
	if fileName == "" {
		fileName = "upload.bin"
	}

	sessionDir, err := os.MkdirTemp(h.cfg.TmpDir, "upload-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

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

	result, err := h.runUpload(ak, tmpPath, fileName, written, sessionDir)
	if err != nil {
		_ = os.RemoveAll(sessionDir)
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) runUpload(ak *agentKeyCtx, filePath, fileName string, fileSize int64, sessionDir string) (map[string]any, error) {
	planFileSize, planSegments, planSegmentRoots, planFileRoot, err :=
		client.ComputeErasurePlan(filePath, h.cfg.SegmentSize, h.cfg.DataShards, h.cfg.ParityShards)
	if err != nil {
		return nil, fmt.Errorf("compute erasure plan: %w", err)
	}

	intentReq := wire.CreateIntentRequest{
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
			Class:    "permanent",
			Duration: 86400 * 365,
		},
		DeadlineUnix: time.Now().Add(24 * time.Hour).Unix(),
	}

	chainClient := client.NewHTTP(h.cfg.ChainURL)

	var intentResp wire.CreateIntentResponse
	if err := chainClient.Post("/intents", intentReq, &intentResp); err != nil {
		return nil, fmt.Errorf("create intent: %w", err)
	}

	if err := h.uploadSegments(intentResp.IntentID, planFileRoot, planSegmentRoots, planSegments, fileSize, filePath); err != nil {
		return nil, err
	}

	manifestRoot := chaincrypto.HashBytes([]byte(planFileRoot + ":" + fileName))
	finalizeReq := wire.FinalizeRequest{
		IntentID:     intentResp.IntentID,
		User:         ak.Master,
		ManifestRoot: manifestRoot,
	}
	var finalizeResp wire.FinalizeResponse
	if err := chainClient.Post("/finalize", finalizeReq, &finalizeResp); err != nil {
		return nil, fmt.Errorf("finalize: %w", err)
	}

	_ = os.RemoveAll(sessionDir)

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

func (h *Handler) uploadSegments(intentID, fileRoot string, segmentRoots []string, segments []wire.SegmentPlan, fileSize int64, filePath string) error {
	totalShards := h.cfg.DataShards + h.cfg.ParityShards

	for segIdx := range segments {
		segBytes := segmentBytes(fileSize, int64(segIdx), h.cfg.SegmentSize)
		if segBytes <= 0 {
			continue
		}

		shardFiles, cleanup, err := client.EncodeSegmentToTempFiles(
			mustOpenFile(filePath),
			int64(segIdx)*h.cfg.SegmentSize,
			segBytes,
			h.cfg.DataShards,
			h.cfg.ParityShards,
			h.cfg.TmpDir,
		)
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

				endpoint := h.cfg.StorageEndpoints[shardIndex%len(h.cfg.StorageEndpoints)]
				shardData, err := os.ReadFile(shard.Path)
				if err != nil {
					mu.Lock()
					firstErr = err
					mu.Unlock()
					return
				}

				uploadReq := wire.UploadRequest{
					IntentID:    intentID,
					FileRoot:    fileRoot,
					SegmentID:   segIdx,
					SegmentRoot: segmentRoots[segIdx],
					ShardIndex:  shardIndex,
					ShardID:     fmt.Sprintf("%s:%d:%d", intentID, segIdx, shardIndex),
					ShardHash:   shard.Hash,
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

		chainClient := client.NewHTTP(h.cfg.ChainURL)
		batchReq := wire.BatchCommitRequest{
			IntentID: intentID,
			Receipts: receipts,
		}
		var batchResp wire.BatchCommitResponse
		if err := chainClient.Post("/batch-commits", batchReq, &batchResp); err != nil {
			return fmt.Errorf("batch commit segment %d: %w", segIdx, err)
		}
		_ = batchResp
	}

	return nil
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

func mustOpenFile(path string) *os.File {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	return f
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
