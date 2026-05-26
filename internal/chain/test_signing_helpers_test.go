package chain

import (
	"crypto/ecdsa"
	"testing"
	"time"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// gfTokens converts whole GF tokens to the smallest internal unit.
// e.g. gfTokens(100) = 10_000_000_000 (100 GF in gf-sat).
func gfTokens(n uint64) uint64 { return n * wire.TokenUnit }

func fundValidatorForTest(t *testing.T, store *Store, identity *ValidatorIdentity, stake uint64) {
	t.Helper()
	store.data.Accounts[identity.Address] = wire.Account{Address: identity.Address, Balance: stake}
}

// testUser holds an ECDSA key pair and the derived account address for tests.
type testUser struct {
	Key  *ecdsa.PrivateKey
	Addr string
}

// newTestUser generates a fresh ECDSA key and derives the address.
func newTestUser(t *testing.T) testUser {
	t.Helper()
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return testUser{Key: priv, Addr: wire.AccountAddress(&priv.PublicKey)}
}

// fundAccount credits the given address with the specified balance.
func fundAccount(store *Store, addr string, balance uint64) {
	store.data.Accounts[addr] = wire.Account{Address: addr, Balance: balance}
}

// signCreateIntent signs a CreateIntentRequest with the given key.
func signCreateIntent(t *testing.T, store *Store, req *wire.CreateIntentRequest, u testUser) {
	t.Helper()
	if req.User == "" {
		req.User = u.Addr
	}
	req.ChainID = store.data.ChainID
	req.Nonce = store.accountLocked(req.User).Nonce
	if req.DeadlineUnix == 0 {
		req.DeadlineUnix = time.Now().Add(24 * time.Hour).Unix()
	}
	if req.LockedFee == 0 {
		quote, err := store.storageQuoteForIntentLocked(*req)
		if err != nil {
			t.Fatal(err)
		}
		req.LockedFee = quote.RequiredFee
	}
	if err := wire.SignCreateIntent(req, u.Key); err != nil {
		t.Fatal(err)
	}
}

// signBatchCommit signs a BatchCommitRequest with the given key.
func signBatchCommit(t *testing.T, store *Store, req *wire.BatchCommitRequest, u testUser) {
	t.Helper()
	if req.User == "" {
		req.User = u.Addr
	}
	req.ChainID = store.data.ChainID
	req.Nonce = store.accountLocked(req.User).Nonce
	if err := wire.SignBatchCommit(req, u.Key); err != nil {
		t.Fatal(err)
	}
}

// signFinalize signs a FinalizeRequest with the given key.
func signFinalize(t *testing.T, store *Store, req *wire.FinalizeRequest, u testUser) {
	t.Helper()
	if req.User == "" {
		req.User = u.Addr
	}
	req.ChainID = store.data.ChainID
	req.Nonce = store.accountLocked(req.User).Nonce
	if err := wire.SignFinalize(req, u.Key); err != nil {
		t.Fatal(err)
	}
}

// signSettleIntent signs a SettleIntentRequest with the given key.
func signSettleIntent(t *testing.T, store *Store, req *wire.SettleIntentRequest, u testUser) {
	t.Helper()
	if req.User == "" {
		req.User = u.Addr
	}
	req.ChainID = store.data.ChainID
	req.Nonce = store.accountLocked(req.User).Nonce
	if err := wire.SignSettleIntent(req, u.Key); err != nil {
		t.Fatal(err)
	}
}

// signTerminateDeal signs a TerminateDealRequest with the given key.
func signTerminateDeal(t *testing.T, store *Store, req *wire.TerminateDealRequest, u testUser) {
	t.Helper()
	if req.User == "" {
		req.User = u.Addr
	}
	req.ChainID = store.data.ChainID
	req.Nonce = store.accountLocked(req.User).Nonce
	if err := wire.SignTerminateDeal(req, u.Key); err != nil {
		t.Fatal(err)
	}
}

// signSetAccessPolicy signs a SetAccessPolicyRequest with the given key.
func signSetAccessPolicy(t *testing.T, store *Store, req *wire.SetAccessPolicyRequest, u testUser) {
	t.Helper()
	if req.User == "" {
		req.User = u.Addr
	}
	req.ChainID = store.data.ChainID
	req.Nonce = store.accountLocked(req.User).Nonce
	if err := wire.SignSetAccessPolicy(req, u.Key); err != nil {
		t.Fatal(err)
	}
}

// signRenewDeal signs a RenewDealRequest with the given key.
func signRenewDeal(t *testing.T, store *Store, req *wire.RenewDealRequest, u testUser) {
	t.Helper()
	if req.User == "" {
		req.User = u.Addr
	}
	req.ChainID = store.data.ChainID
	req.Nonce = store.accountLocked(req.User).Nonce
	if err := wire.SignRenewDeal(req, u.Key); err != nil {
		t.Fatal(err)
	}
}

// signTopUpPermanentFund signs a PermanentFundTopUpRequest with the given key.
func signTopUpPermanentFund(t *testing.T, store *Store, req *wire.PermanentFundTopUpRequest, u testUser) {
	t.Helper()
	if req.User == "" {
		req.User = u.Addr
	}
	req.ChainID = store.data.ChainID
	req.Nonce = store.accountLocked(req.User).Nonce
	if err := wire.SignPermanentFundTopUp(req, u.Key); err != nil {
		t.Fatal(err)
	}
}

func signCreateCollection(t *testing.T, store *Store, req *wire.CreateCollectionRequest, u testUser) {
	t.Helper()
	if req.User == "" {
		req.User = u.Addr
	}
	req.ChainID = store.data.ChainID
	req.Nonce = store.accountLocked(req.User).Nonce
	if err := wire.SignCreateCollection(req, u.Key); err != nil {
		t.Fatal(err)
	}
}

func signAppendRecord(t *testing.T, store *Store, req *wire.AppendRecordRequest, u testUser) {
	t.Helper()
	if req.User == "" {
		req.User = u.Addr
	}
	req.ChainID = store.data.ChainID
	req.Nonce = store.accountLocked(req.User).Nonce
	if err := wire.SignAppendRecord(req, u.Key); err != nil {
		t.Fatal(err)
	}
}

// testAssignedIntentRequest creates a CreateIntentRequest using a testUser.
func testAssignedIntentRequest(t *testing.T, store *Store, u testUser, lockedFee uint64) wire.CreateIntentRequest {
	t.Helper()
	req := wire.CreateIntentRequest{
		User:         u.Addr,
		FileName:     "file.bin",
		FileSize:     6,
		SegmentSize:  6,
		FileRoot:     "file-root",
		SegmentRoots: []string{"segment-root"},
		Segments: []wire.SegmentPlan{{
			SegmentID:   0,
			SegmentRoot: "segment-root",
			ShardHashes: []string{
				"shard-a",
				"shard-b",
			},
		}},
		Erasure:      wire.ErasurePolicy{DataShards: 2, ParityShards: 0},
		Policy:       wire.StoragePolicy{Duration: int64(30 * 24 * time.Hour / time.Second)},
		LockedFee:    lockedFee,
		DeadlineUnix: time.Now().Add(time.Hour).Unix(),
	}
	signCreateIntent(t, store, &req, u)
	return req
}

// testLifecycleIntentWithUser creates a finalized Intent for lifecycle tests using a testUser address.
func testLifecycleIntentWithUser(userAddr string) *Intent {
	return &Intent{
		IntentView: wire.IntentView{
			IntentID:         "intent_lifecycle",
			User:             userAddr,
			FileName:         "data.bin",
			FileSize:         32,
			SegmentSize:      32,
			FileRoot:         "file_lifecycle",
			SegmentRoots:     []string{"segment_lifecycle"},
			Segments:         []wire.SegmentPlan{{SegmentID: 0, SegmentRoot: "segment_lifecycle", ShardHashes: []string{"shard_lifecycle"}}},
			Erasure:          wire.ErasurePolicy{DataShards: 1, ParityShards: 0, ShardSize: 32},
			Policy:           wire.StoragePolicy{Duration: int64(365 * 24 * time.Hour / time.Second)},
			ExpiresAtUnix:    time.Now().Add(365 * 24 * time.Hour).Unix(),
			LockedFee:        gfTokens(10),
			Status:           wire.StatusFinalized,
			StorageStatus:    wire.StorageStatusActive,
			AccessStatus:     wire.AccessStatusPublic,
			ModerationStatus: wire.ModerationStatusNone,
			DealID:           "deal_lifecycle",
		},
		Receipts: map[int]map[int]wire.MinerReceipt{
			0: {
				0: {
					MinerAddress:   "miner_lifecycle",
					MinerPublicKey: "miner_pub",
					MinerEndpoint:  "http://miner",
					ShardHash:      "shard_lifecycle",
					ShardSize:      32,
				},
			},
		},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
}
