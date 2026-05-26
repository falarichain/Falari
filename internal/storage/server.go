package storage

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"chain/internal/wire"
)

type Server struct {
	node     *Node
	provider *ProviderNetwork
}

func NewServer(node *Node) *Server {
	return &Server{node: node}
}

func NewServerWithProviderNetwork(node *Node, provider *ProviderNetwork) *Server {
	return &Server{node: node, provider: provider}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("OPTIONS /", s.options)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /status", s.status)
	mux.HandleFunc("GET /identity", s.identity)
	mux.HandleFunc("GET /providers", s.providers)
	mux.HandleFunc("GET /providers/", s.providers)
	mux.HandleFunc("POST /upload", s.upload)
	mux.HandleFunc("POST /prove", s.prove)
	mux.HandleFunc("POST /retrieval-receipts/sign", s.signRetrievalReceipt)
	mux.HandleFunc("GET /blocks/", s.getBlock)
	mux.HandleFunc("GET /shards/", s.getShard)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) options(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	status := s.node.Status()
	if s.provider != nil {
		status.PeerID = s.provider.PeerID()
		status.PeerAddrs = s.provider.Addrs()
		if dhtSvc := s.provider.DHTService(); dhtSvc != nil {
			status.DHTEnabled = true
			status.DHTPeers = dhtSvc.RoutingTableSize()
			status.DHTShardCount = dhtSvc.ShardCount()
			if bl := dhtSvc.Blacklist(); bl != nil {
				status.BlacklistCount = bl.Count()
			}
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) identity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"address":    s.node.Address(),
		"public_key": s.node.PublicKeyHex(),
	})
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	var req wire.UploadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	receipt, err := s.node.Store(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, receipt)
	if s.provider != nil {
		go s.provider.AnnounceOnce()
	}
}

func (s *Server) providers(w http.ResponseWriter, r *http.Request) {
	shardHash := r.URL.Query().Get("shard_hash")
	if shardHash == "" && strings.HasPrefix(r.URL.Path, "/providers/") {
		shardHash = strings.TrimPrefix(r.URL.Path, "/providers/")
	}
	providers := []wire.StorageProviderRecord{}
	if s.provider != nil {
		providers = s.provider.Providers(shardHash)
	}
	writeJSON(w, http.StatusOK, wire.StorageProvidersResponse{
		ShardHash: shardHash,
		Providers: providers,
	})
}

func (s *Server) prove(w http.ResponseWriter, r *http.Request) {
	var req wire.ProveRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := validateProofRequest(req.Challenge); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	proof, err := s.node.Prove(req.Challenge)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, proof)
}

func validateProofRequest(challenge wire.StorageChallenge) error {
	if challenge.ChallengeID == "" || challenge.Nonce == "" || challenge.ChallengeHash == "" {
		return errors.New("chain challenge id, nonce, and hash are required")
	}
	if challenge.ProofType == "" {
		return errors.New("challenge proof type is required")
	}
	if challenge.ExpiresAtUnix <= 0 {
		return errors.New("challenge expiry is required")
	}
	if time.Now().Unix() > challenge.ExpiresAtUnix {
		return errors.New("challenge expired")
	}
	return nil
}

func (s *Server) signRetrievalReceipt(w http.ResponseWriter, r *http.Request) {
	var receipt wire.RetrievalReceipt
	if !decodeJSON(w, r, &receipt) {
		return
	}
	signed, err := s.node.SignRetrievalReceipt(receipt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, signed)
}

func (s *Server) getShard(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/shards/")
	hash = strings.TrimSuffix(hash, ".bin")
	if s.isShardBlacklisted(hash) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "shard is blacklisted"})
		return
	}
	data, err := s.node.ReadShard(hash)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.node.recordHTTPShardServeHit()
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(data)
}

func (s *Server) getBlock(w http.ResponseWriter, r *http.Request) {
	cid := strings.TrimPrefix(r.URL.Path, "/blocks/")
	if shardHash := s.node.ShardHashForCID(cid); s.isShardBlacklisted(shardHash) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "shard is blacklisted"})
		return
	}
	data, err := s.node.ReadShardByCID(cid)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.node.recordHTTPBlockServeHit()
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(data)
}

// isShardBlacklisted checks if a shard hash is on the blacklist.
// Returns false if blacklist is not available or shardHash is empty.
func (s *Server) isShardBlacklisted(shardHash string) bool {
	if shardHash == "" || s.provider == nil {
		return false
	}
	dhtSvc := s.provider.DHTService()
	if dhtSvc == nil {
		return false
	}
	bl := dhtSvc.Blacklist()
	if bl == nil {
		return false
	}
	return bl.IsBlocked(shardHash)
}

const maxStorageRequestSize = 32 << 20 // 32 MB (shard data can be large)

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxStorageRequestSize)
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		if err.Error() == "http: request body too large" {
			writeError(w, http.StatusRequestEntityTooLarge, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
