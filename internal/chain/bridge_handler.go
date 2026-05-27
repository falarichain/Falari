package chain

import (
	"errors"
	"net/http"
	"strconv"

	"chain/internal/wire"
)

// ── Bridge GET handlers ──

func (s *Server) getBridgeConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.store.BridgeConfig()
	if cfg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) getBridgeOutbound(w http.ResponseWriter, r *http.Request) {
	nonceStr := r.PathValue("nonce")
	nonce, err := strconv.ParseUint(nonceStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ob, err := s.store.BridgeOutbound(nonce)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, ob)
}

func (s *Server) getBridgeInbound(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if hash == "" {
		writeError(w, http.StatusBadRequest, errors.New("hash path parameter is required"))
		return
	}
	ib, err := s.store.BridgeInbound(hash)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, ib)
}

func (s *Server) getBridgePending(w http.ResponseWriter, _ *http.Request) {
	resp := s.store.BridgePending()
	writeJSON(w, http.StatusOK, resp)
}

// ── Bridge POST handlers ──

func (s *Server) bridgeOut(w http.ResponseWriter, r *http.Request) {
	var req wire.BridgeOutRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.BridgeOut(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) bridgeClaim(w http.ResponseWriter, r *http.Request) {
	var req wire.BridgeInClaimRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.BridgeClaim(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) bridgeAdminConfig(w http.ResponseWriter, r *http.Request) {
	var req wire.BridgeSetConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.BridgeAdminSetConfig(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
