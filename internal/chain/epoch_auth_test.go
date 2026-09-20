package chain

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"chain/internal/reward"
	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// testRegisterEpochOperator registers an enabled governance operator holding admin
// permission under its own address.
func testRegisterEpochOperator(store *Store, address string) string {
	normalized := normalizeGovernanceOperator(address)
	store.data.GovernanceOperators[normalized] = wire.GovernanceOperator{
		Operator:    normalized,
		Permissions: []string{"admin"},
		Enabled:     true,
	}
	return normalized
}

// testEpochOperator generates a governance operator key and registers it.
func testEpochOperator(t *testing.T, store *Store) (*ecdsa.PrivateKey, string) {
	t.Helper()
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	address := wire.AccountAddress(&privateKey.PublicKey)
	return privateKey, testRegisterEpochOperator(store, address)
}

// testEpochOperatorIdentity binds a governance operator as the node's own operator
// identity, which is how the epoch scheduler authorizes the requests it originates.
func testEpochOperatorIdentity(t *testing.T, store *Store) (*ecdsa.PrivateKey, string) {
	t.Helper()
	privateKey, address := testEpochOperator(t, store)
	store.SetOperatorIdentity(&OperatorIdentity{
		OwnerAddress:       address,
		OperatorAddress:    address,
		OperatorPublicKey:  &privateKey.PublicKey,
		OperatorPrivateKey: privateKey,
	})
	return privateKey, address
}

// testEpochStore returns a store holding one finalized deal, so StartEpoch can generate
// challenges and reach the transaction recording step.
func testEpochStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedFinalizedDealForEpochTest(store)
	return store
}

func testSignStartEpoch(t *testing.T, store *Store, req *wire.StartEpochRequest, privateKey *ecdsa.PrivateKey, address string) *wire.StartEpochRequest {
	t.Helper()
	req.OperatorAddress = address
	req.ChainID = store.data.ChainID
	req.Nonce = store.data.OperatorNonces[normalizeGovernanceOperator(address)]
	req.CreatedAtUnix = time.Now().Unix()
	if err := wire.SignStartEpochRequest(req, privateKey); err != nil {
		t.Fatal(err)
	}
	return req
}

func testValidStartEpochRequest(t *testing.T, store *Store, privateKey *ecdsa.PrivateKey, address string) wire.StartEpochRequest {
	t.Helper()
	req := wire.StartEpochRequest{
		IntentID:            "intent_epoch",
		ChallengesPerDeal:   1,
		DurationSeconds:     600,
		RewardPerProof:      reward.TokenUnit,
		SlashPerMissedProof: 1,
	}
	return *testSignStartEpoch(t, store, &req, privateKey, address)
}

func TestStartEpochRejectsUnauthorizedOperators(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, store *Store, req *wire.StartEpochRequest, privateKey *ecdsa.PrivateKey, address string)
		want   string
	}{
		{
			name: "no operator at all",
			mutate: func(_ *testing.T, _ *Store, req *wire.StartEpochRequest, _ *ecdsa.PrivateKey, _ string) {
				req.OperatorAddress = ""
				req.ChainID = ""
				req.Signature = ""
			},
			want: "operator address is required",
		},
		{
			name: "address that is not an operator",
			mutate: func(t *testing.T, store *Store, req *wire.StartEpochRequest, _ *ecdsa.PrivateKey, _ string) {
				externalKey, externalAddress := testEpochOperator(t, store)
				delete(store.data.GovernanceOperators, externalAddress)
				*req = testValidStartEpochRequest(t, store, externalKey, externalAddress)
			},
			want: "governance operator is not authorized",
		},
		{
			name: "disabled operator",
			mutate: func(_ *testing.T, store *Store, _ *wire.StartEpochRequest, _ *ecdsa.PrivateKey, address string) {
				operator := store.data.GovernanceOperators[address]
				operator.Enabled = false
				store.data.GovernanceOperators[address] = operator
			},
			want: "governance operator is not authorized",
		},
		{
			name: "operator without admin permission",
			mutate: func(_ *testing.T, store *Store, _ *wire.StartEpochRequest, _ *ecdsa.PrivateKey, address string) {
				operator := store.data.GovernanceOperators[address]
				operator.Permissions = []string{"reader"}
				store.data.GovernanceOperators[address] = operator
			},
			want: "lacks admin permission",
		},
		{
			name: "operator with an empty permission list",
			mutate: func(_ *testing.T, store *Store, _ *wire.StartEpochRequest, _ *ecdsa.PrivateKey, address string) {
				operator := store.data.GovernanceOperators[address]
				operator.Permissions = nil
				store.data.GovernanceOperators[address] = operator
			},
			want: "lacks admin permission",
		},
		{
			name: "another chain",
			mutate: func(t *testing.T, store *Store, req *wire.StartEpochRequest, privateKey *ecdsa.PrivateKey, address string) {
				req.ChainID = "other-chain"
				if err := wire.SignStartEpochRequest(req, privateKey); err != nil {
					t.Fatal(err)
				}
			},
			want: "chain_id mismatch",
		},
		{
			name: "missing signed timestamp",
			mutate: func(t *testing.T, store *Store, req *wire.StartEpochRequest, privateKey *ecdsa.PrivateKey, address string) {
				req.CreatedAtUnix = 0
				if err := wire.SignStartEpochRequest(req, privateKey); err != nil {
					t.Fatal(err)
				}
			},
			want: "missing signed timestamp",
		},
		{
			name: "consumed nonce",
			mutate: func(_ *testing.T, store *Store, _ *wire.StartEpochRequest, _ *ecdsa.PrivateKey, address string) {
				store.data.OperatorNonces[address] = 1
			},
			want: "operator nonce mismatch",
		},
		{
			name: "signed by a different key than the claimed operator",
			mutate: func(t *testing.T, store *Store, req *wire.StartEpochRequest, _ *ecdsa.PrivateKey, address string) {
				attackerKey, _ := testEpochOperator(t, store)
				// Keep the claimed operator address but sign with another operator's key.
				req.ChainID = store.data.ChainID
				req.Nonce = store.data.OperatorNonces[address]
				req.CreatedAtUnix = time.Now().Unix()
				if err := wire.SignStartEpochRequest(req, attackerKey); err != nil {
					t.Fatal(err)
				}
			},
			want: "signature does not match operator address",
		},
		{
			name: "fields changed after signing",
			mutate: func(_ *testing.T, _ *Store, req *wire.StartEpochRequest, _ *ecdsa.PrivateKey, _ string) {
				req.RewardPerProof = req.RewardPerProof * 1000
			},
			want: "signature does not match operator address",
		},
		{
			name: "signed parameters that would still need defaulting",
			mutate: func(t *testing.T, _ *Store, req *wire.StartEpochRequest, privateKey *ecdsa.PrivateKey, _ string) {
				req.RewardPerProof = 0
				if err := wire.SignStartEpochRequest(req, privateKey); err != nil {
					t.Fatal(err)
				}
			},
			want: "must be set before signing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := testEpochStore(t)
			privateKey, address := testEpochOperator(t, store)
			req := testValidStartEpochRequest(t, store, privateKey, address)
			tc.mutate(t, store, &req, privateKey, address)
			nonceBefore := store.data.OperatorNonces[address]

			if _, err := store.StartEpoch(req); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
			if store.data.OperatorNonces[address] != nonceBefore {
				t.Fatalf("rejected request must not consume the operator nonce, got %d", store.data.OperatorNonces[address])
			}
			if len(store.data.Epochs) != 0 {
				t.Fatalf("rejected request must not create an epoch, got %d", len(store.data.Epochs))
			}
		})
	}
}

func TestStartEpochRecordsOperatorSignatureAndConsumesNonce(t *testing.T) {
	store := testEpochStore(t)
	privateKey, address := testEpochOperator(t, store)
	req := testValidStartEpochRequest(t, store, privateKey, address)

	resp, err := store.StartEpoch(req)
	if err != nil {
		t.Fatal(err)
	}
	if store.data.OperatorNonces[address] != 1 {
		t.Fatalf("expected operator nonce 1 after start epoch, got %d", store.data.OperatorNonces[address])
	}
	payload := lastEpochTx(t, store, "start_epoch")
	if err := wire.VerifyStartEpochRequest(payload.Request, address); err != nil {
		t.Fatalf("recorded epoch transaction must carry a verifiable operator signature: %v", err)
	}
	if payload.Request.OperatorAddress != address {
		t.Fatalf("recorded epoch transaction operator mismatch: %s", payload.Request.OperatorAddress)
	}
	if resp.Epoch.EpochID != payload.Epoch.EpochID {
		t.Fatalf("recorded epoch mismatch: %s vs %s", resp.Epoch.EpochID, payload.Epoch.EpochID)
	}
}

func TestFinalizeEpochRequiresAuthorizedOperator(t *testing.T) {
	store := testEpochStore(t)
	_, address := testEpochOperatorIdentity(t, store)
	startResp, err := store.StartEpoch(wire.StartEpochRequest{
		IntentID:            "intent_epoch",
		ChallengesPerDeal:   1,
		DurationSeconds:     600,
		RewardPerProof:      reward.TokenUnit,
		SlashPerMissedProof: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The scheduler-signed start consumed the operator's first nonce.
	if store.data.OperatorNonces[address] != 1 {
		t.Fatalf("expected start epoch to consume nonce 0, got %d", store.data.OperatorNonces[address])
	}

	// A request naming an operator but carrying no signature authorizes nothing, even on
	// a node that holds that operator's identity.
	if _, err := store.FinalizeEpoch(wire.FinalizeEpochRequest{
		EpochID:         startResp.Epoch.EpochID,
		OperatorAddress: address,
		ChainID:         store.data.ChainID,
		Nonce:           store.data.OperatorNonces[address],
		CreatedAtUnix:   time.Now().Unix(),
	}); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected an unsigned finalize to be rejected, got %v", err)
	}
	if store.data.OperatorNonces[address] != 1 {
		t.Fatalf("rejected finalize must not consume the nonce, got %d", store.data.OperatorNonces[address])
	}

	resp, err := store.FinalizeEpoch(wire.FinalizeEpochRequest{EpochID: startResp.Epoch.EpochID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.EpochID != startResp.Epoch.EpochID {
		t.Fatalf("unexpected finalize response %+v", resp)
	}
	if store.data.OperatorNonces[address] != 2 {
		t.Fatalf("expected finalize to consume nonce 1, got %d", store.data.OperatorNonces[address])
	}
	payload := lastFinalizeEpochTx(t, store)
	if err := wire.VerifyFinalizeEpochRequest(payload.Request, address); err != nil {
		t.Fatalf("recorded finalize transaction must carry a verifiable operator signature: %v", err)
	}
}

// TestFinalizeEpochRejectsRequestWithoutOperator is the reported bypass: an epoch
// transaction recording no operator could not be attributed to anyone.
func TestFinalizeEpochRejectsRequestWithoutOperator(t *testing.T) {
	store := testEpochStore(t)
	privateKey, address := testEpochOperator(t, store)
	startResp, err := store.StartEpoch(testValidStartEpochRequest(t, store, privateKey, address))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinalizeEpoch(wire.FinalizeEpochRequest{EpochID: startResp.Epoch.EpochID}); err == nil ||
		!strings.Contains(err.Error(), "operator address is required") {
		t.Fatalf("expected a finalize without an operator to be rejected, got %v", err)
	}
	if store.data.Epochs[startResp.Epoch.EpochID].Status != "active" {
		t.Fatal("epoch must stay active when the finalize is not authorized")
	}
}

// TestEpochSchedulerRequiresRegisteredOperator covers the node-local path: holding an
// operator key is not enough, the address must be a registered governance operator.
func TestEpochSchedulerRequiresRegisteredOperator(t *testing.T) {
	store := testEpochStore(t)
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	address := wire.AccountAddress(&privateKey.PublicKey)
	store.SetOperatorIdentity(&OperatorIdentity{
		OwnerAddress:       address,
		OperatorAddress:    address,
		OperatorPublicKey:  &privateKey.PublicKey,
		OperatorPrivateKey: privateKey,
	})

	if _, err := store.StartEpoch(wire.StartEpochRequest{IntentID: "intent_epoch", ChallengesPerDeal: 1, DurationSeconds: 600}); err == nil ||
		!strings.Contains(err.Error(), "governance operator is not authorized") {
		t.Fatalf("expected an unregistered node operator to be rejected, got %v", err)
	}
	if len(store.data.Epochs) != 0 {
		t.Fatalf("rejected epoch must not be stored, got %d", len(store.data.Epochs))
	}
}

func TestFinalizeExpiredEpochsRequiresAuthorizedOperator(t *testing.T) {
	store := testEpochStore(t)
	privateKey, address := testEpochOperator(t, store)
	startResp, err := store.StartEpoch(testValidStartEpochRequest(t, store, privateKey, address))
	if err != nil {
		t.Fatal(err)
	}
	epoch := store.data.Epochs[startResp.Epoch.EpochID]
	epoch.DeadlineUnix = time.Now().Add(-time.Hour).Unix()
	store.data.Epochs[epoch.EpochID] = epoch

	// A node without an operator identity must not settle epochs it cannot sign for.
	if _, err := store.FinalizeExpiredEpochs(); err == nil ||
		!strings.Contains(err.Error(), "operator address is required") {
		t.Fatalf("expected auto finalize to refuse without an operator identity, got %v", err)
	}
	if store.data.Epochs[epoch.EpochID].Status != "active" {
		t.Fatal("epoch must stay active when it cannot be authorized")
	}

	_, identityAddress := testEpochOperatorIdentity(t, store)
	responses, err := store.FinalizeExpiredEpochs()
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 1 {
		t.Fatalf("expected one auto-finalized epoch, got %d", len(responses))
	}
	if store.data.OperatorNonces[identityAddress] != 1 {
		t.Fatalf("expected auto finalize to consume its own nonce 0, got %d", store.data.OperatorNonces[identityAddress])
	}
}

func TestReplayEpochRejectsMissingOrTamperedOperatorSignature(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, store *Store, payload *startEpochTxPayload)
		want   string
	}{
		{
			name: "empty operator address",
			mutate: func(_ *testing.T, _ *Store, payload *startEpochTxPayload) {
				payload.Request = wire.StartEpochRequest{}
			},
			want: "operator address is required",
		},
		{
			name: "operator named but request not signed",
			mutate: func(_ *testing.T, _ *Store, payload *startEpochTxPayload) {
				payload.Request.Signature = ""
			},
			want: "invalid signature length",
		},
		{
			name: "reward changed after signing",
			mutate: func(_ *testing.T, _ *Store, payload *startEpochTxPayload) {
				payload.Request.RewardPerProof *= 10
			},
			want: "signature does not match operator address",
		},
		{
			name: "operator revoked",
			mutate: func(_ *testing.T, store *Store, payload *startEpochTxPayload) {
				delete(store.data.GovernanceOperators, normalizeGovernanceOperator(payload.Request.OperatorAddress))
			},
			want: "governance operator is not authorized",
		},
		{
			name: "nonce already used",
			mutate: func(_ *testing.T, store *Store, payload *startEpochTxPayload) {
				store.data.OperatorNonces[normalizeGovernanceOperator(payload.Request.OperatorAddress)] = payload.Request.Nonce + 1
			},
			want: "operator nonce mismatch",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			producer := testEpochStore(t)
			privateKey, address := testEpochOperator(t, producer)
			if _, err := producer.StartEpoch(testValidStartEpochRequest(t, producer, privateKey, address)); err != nil {
				t.Fatal(err)
			}
			payload := lastEpochTx(t, producer, "start_epoch")

			replica, err := OpenStore("")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = replica.Close() })
			seedFinalizedDealForEpochTest(replica)
			testRegisterEpochOperator(replica, address)
			tc.mutate(t, replica, &payload)

			if err := replica.applyStartEpochLocked(payload); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected replay error containing %q, got %v", tc.want, err)
			}
			if len(replica.data.Epochs) != 0 {
				t.Fatalf("rejected replay must not store an epoch, got %d", len(replica.data.Epochs))
			}
		})
	}
}

func TestReplayEpochAcceptsOperatorSignedTransaction(t *testing.T) {
	producer := testEpochStore(t)
	privateKey, address := testEpochOperator(t, producer)
	if _, err := producer.StartEpoch(testValidStartEpochRequest(t, producer, privateKey, address)); err != nil {
		t.Fatal(err)
	}
	payload := lastEpochTx(t, producer, "start_epoch")

	replica, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replica.Close() })
	seedFinalizedDealForEpochTest(replica)
	testRegisterEpochOperator(replica, address)

	if err := replica.applyStartEpochLocked(payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := replica.data.Epochs[payload.Epoch.EpochID]; !ok {
		t.Fatal("replica should replay the signed epoch")
	}
	if replica.data.OperatorNonces[address] != payload.Request.Nonce+1 {
		t.Fatalf("replay should consume the operator nonce, got %d", replica.data.OperatorNonces[address])
	}
	// Replaying the same transaction again must fail on the nonce, not silently re-apply.
	if err := replica.applyStartEpochLocked(payload); err == nil ||
		!strings.Contains(err.Error(), "operator nonce mismatch") {
		t.Fatalf("expected the replayed transaction to be rejected, got %v", err)
	}
}

func TestStartEpochHTTPEndpointAuthenticatesOperator(t *testing.T) {
	store := testEpochStore(t)
	privateKey, address := testEpochOperator(t, store)
	server := httptest.NewServer(NewServer(store, nil).Routes())
	t.Cleanup(server.Close)

	send := func(body []byte, headers map[string]string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+"/epochs", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}

	requestBody := func(req wire.StartEpochRequest) []byte {
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	signed := testValidStartEpochRequest(t, store, privateKey, address)
	headerFor := func(body []byte) map[string]string {
		return operatorHeaders(t, store, privateKey, address, body)
	}
	// A request body that names no operator and carries no signature.
	noOperator := requestBody(wire.StartEpochRequest{
		IntentID:          "intent_epoch",
		ChallengesPerDeal: 1,
		DurationSeconds:   600,
		RewardPerProof:    reward.TokenUnit,
	})

	// A caller that knows the operator address and its current nonce still cannot start
	// an epoch: the headers must be signed, and the signature is bound to the body.
	if code := send(requestBody(signed), nil); code != http.StatusForbidden {
		t.Fatalf("expected 403 without operator headers, got %d", code)
	}
	forged := headerFor(requestBody(wire.StartEpochRequest{ChallengesPerDeal: 9}))
	if code := send(requestBody(signed), forged); code != http.StatusForbidden {
		t.Fatalf("expected 403 for headers signed over a different body, got %d", code)
	}
	if code := send(noOperator, headerFor(noOperator)); code != http.StatusForbidden {
		t.Fatalf("expected 403 for a body without an operator signature, got %d", code)
	}
	// Another operator's headers cannot carry this operator's signed body.
	attackerKey, attackerAddress := testEpochOperator(t, store)
	crossBound := operatorHeaders(t, store, attackerKey, attackerAddress, requestBody(signed))
	if code := send(requestBody(signed), crossBound); code != http.StatusForbidden {
		t.Fatalf("expected 403 when the header operator differs from the body operator, got %d", code)
	}

	if len(store.data.Epochs) != 0 {
		t.Fatalf("rejected requests must not create epochs, got %d", len(store.data.Epochs))
	}
	if store.data.OperatorNonces[address] != 0 {
		t.Fatalf("rejected requests must not consume the nonce, got %d", store.data.OperatorNonces[address])
	}

	if code := send(requestBody(signed), headerFor(requestBody(signed))); code != http.StatusCreated {
		t.Fatalf("expected 201 for a fully signed epoch request, got %d", code)
	}
	if len(store.data.Epochs) != 1 {
		t.Fatalf("expected one epoch, got %d", len(store.data.Epochs))
	}
	if store.data.OperatorNonces[address] != 1 {
		t.Fatalf("expected the accepted request to consume nonce 0, got %d", store.data.OperatorNonces[address])
	}
}

// operatorHeaders signs the operator request headers over body with the current stored
// nonce, the way an operator client does.
func operatorHeaders(t *testing.T, store *Store, privateKey *ecdsa.PrivateKey, address string, body []byte) map[string]string {
	t.Helper()
	nonce := store.data.OperatorNonces[address]
	timestamp := time.Now().Unix()
	signature, err := wire.SignOperatorRequest(store.data.ChainID, http.MethodPost, "/epochs", body, nonce, timestamp, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"X-Operator-Address":   address,
		"X-Operator-Nonce":     strconv.FormatUint(nonce, 10),
		"X-Operator-Timestamp": strconv.FormatInt(timestamp, 10),
		"X-Operator-Signature": signature,
	}
}

func lastEpochTx(t *testing.T, store *Store, txType string) startEpochTxPayload {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := len(store.data.PendingTxs) - 1; i >= 0; i-- {
		tx := store.data.PendingTxs[i]
		if tx.Type != txType {
			continue
		}
		var payload startEpochTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	t.Fatalf("no %s transaction recorded", txType)
	return startEpochTxPayload{}
}

func lastFinalizeEpochTx(t *testing.T, store *Store) finalizeEpochTxPayload {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := len(store.data.PendingTxs) - 1; i >= 0; i-- {
		tx := store.data.PendingTxs[i]
		if tx.Type != "finalize_epoch" {
			continue
		}
		var payload finalizeEpochTxPayload
		if err := json.Unmarshal(tx.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	t.Fatal("no finalize_epoch transaction recorded")
	return finalizeEpochTxPayload{}
}
