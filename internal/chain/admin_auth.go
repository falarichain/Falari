package chain

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"chain/internal/wire"
)

const operatorRequestTimeSkew = 5 * time.Minute

// requireOperator wraps an HTTP handler with governance operator authentication.
// The client must provide X-Operator-Address, X-Operator-Nonce,
// X-Operator-Timestamp, and X-Operator-Signature headers. The signature is
// bound to chain_id, method, path, body hash, timestamp, and nonce.
func (s *Server) requireOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		address := r.Header.Get("X-Operator-Address")
		signature := r.Header.Get("X-Operator-Signature")
		nonceRaw := r.Header.Get("X-Operator-Nonce")
		timestampRaw := r.Header.Get("X-Operator-Timestamp")

		if address == "" || signature == "" || nonceRaw == "" || timestampRaw == "" {
			writeError(w, http.StatusForbidden, errors.New("operator authentication required"))
			return
		}

		nonce, err := strconv.ParseUint(nonceRaw, 10, 64)
		if err != nil {
			writeError(w, http.StatusForbidden, errors.New("invalid operator nonce"))
			return
		}
		timestampUnix, err := strconv.ParseInt(timestampRaw, 10, 64)
		if err != nil {
			writeError(w, http.StatusForbidden, errors.New("invalid operator timestamp"))
			return
		}
		now := time.Now()
		signedAt := time.Unix(timestampUnix, 0)
		if signedAt.Before(now.Add(-operatorRequestTimeSkew)) || signedAt.After(now.Add(operatorRequestTimeSkew)) {
			writeError(w, http.StatusForbidden, errors.New("operator signature timestamp outside allowed window"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestSize)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("failed to read request body"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		address = normalizeGovernanceOperator(address)
		if address == "" {
			writeError(w, http.StatusForbidden, errors.New("invalid operator address"))
			return
		}

		s.store.mu.Lock()
		operator, ok := s.store.data.GovernanceOperators[address]
		if !ok || !operator.Enabled {
			s.store.mu.Unlock()
			writeError(w, http.StatusForbidden, errors.New("governance operator is not authorized"))
			return
		}
		if !hasAdminPermission(operator.Permissions) {
			s.store.mu.Unlock()
			writeError(w, http.StatusForbidden, errors.New("governance operator lacks admin permission"))
			return
		}
		expectedNonce := s.store.data.OperatorNonces[address]
		if nonce != expectedNonce {
			s.store.mu.Unlock()
			writeError(w, http.StatusForbidden, errors.New("invalid operator nonce"))
			return
		}
		err = wire.VerifyOperatorRequestSignature(
			s.store.data.ChainID,
			r.Method,
			r.URL.EscapedPath(),
			body,
			nonce,
			timestampUnix,
			address,
			signature,
		)
		if err != nil {
			s.store.mu.Unlock()
			writeError(w, http.StatusForbidden, err)
			return
		}
		s.store.data.OperatorNonces[address] = expectedNonce + 1
		if err := s.store.saveLocked(); err != nil {
			s.store.mu.Unlock()
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.store.mu.Unlock()

		next(w, r)
	}
}

func hasAdminPermission(permissions []string) bool {
	if len(permissions) == 0 {
		// No permissions specified means all allowed (per existing governance logic)
		return true
	}
	for _, p := range permissions {
		if p == "admin" || p == "upgrade" || p == "all" {
			return true
		}
	}
	return false
}
