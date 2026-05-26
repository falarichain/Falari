package chain

import (
	"testing"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestCollectionAppendRecordIndexesFinalizedIntent(t *testing.T) {
	store, _, resp, alice := setupCommittedAssignedIntent(t)
	finReq := wire.FinalizeRequest{
		IntentID:     resp.IntentID,
		User:         alice.Addr,
		ManifestRoot: chaincrypto.HashBytes([]byte("manifest")),
	}
	signFinalize(t, store, &finReq, alice)
	if _, err := store.Finalize(finReq); err != nil {
		t.Fatal(err)
	}
	collReq := wire.CreateCollectionRequest{
		User: alice.Addr,
		Name: "agent-memory",
	}
	signCreateCollection(t, store, &collReq, alice)
	collectionResp, err := store.CreateCollection(collReq)
	if err != nil {
		t.Fatal(err)
	}
	appendReq := wire.AppendRecordRequest{
		CollectionID: collectionResp.Collection.CollectionID,
		IntentID:     resp.IntentID,
		Kind:         "memory",
		Key:          "session/1",
		Metadata:     map[string]string{"agent": "demo"},
	}
	signAppendRecord(t, store, &appendReq, alice)
	recordResp, err := store.AppendRecord(appendReq)
	if err != nil {
		t.Fatal(err)
	}
	if recordResp.Record.DealID == "" || recordResp.Record.FileRoot == "" {
		t.Fatalf("record did not copy finalized intent metadata: %+v", recordResp.Record)
	}
	records, err := store.CollectionRecords(collectionResp.Collection.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records.Records) != 1 || records.Records[0].RecordID != recordResp.Record.RecordID {
		t.Fatalf("unexpected collection records: %+v", records)
	}
	recordByID, err := store.DataRecord(recordResp.Record.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	if recordByID.Record.IntentID != resp.IntentID {
		t.Fatalf("unexpected record lookup: %+v", recordByID)
	}
	recordManifest, err := store.RecordManifest(recordResp.Record.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	if recordManifest.Record.RecordID != recordResp.Record.RecordID || recordManifest.Manifest.Plan.IntentID != resp.IntentID {
		t.Fatalf("unexpected record manifest: %+v", recordManifest)
	}
	status := store.Status()
	if status.Collections != 1 || status.DataRecords != 1 {
		t.Fatalf("status did not count collections: %+v", status)
	}
}

func TestCollectionRecordsSupportsFiltersAndLatestLimit(t *testing.T) {
	store, _, resp, alice := setupCommittedAssignedIntent(t)
	finReq := wire.FinalizeRequest{
		IntentID:     resp.IntentID,
		User:         alice.Addr,
		ManifestRoot: chaincrypto.HashBytes([]byte("manifest")),
	}
	signFinalize(t, store, &finReq, alice)
	if _, err := store.Finalize(finReq); err != nil {
		t.Fatal(err)
	}
	collReq2 := wire.CreateCollectionRequest{User: alice.Addr, Name: "agent-memory"}
	signCreateCollection(t, store, &collReq2, alice)
	collectionResp, err := store.CreateCollection(collReq2)
	if err != nil {
		t.Fatal(err)
	}
	appendReq1 := wire.AppendRecordRequest{
		CollectionID: collectionResp.Collection.CollectionID,
		IntentID:     resp.IntentID,
		Kind:         "memory",
		Key:          "session/1",
	}
	signAppendRecord(t, store, &appendReq1, alice)
	first, err := store.AppendRecord(appendReq1)
	if err != nil {
		t.Fatal(err)
	}
	appendReq2 := wire.AppendRecordRequest{
		CollectionID: collectionResp.Collection.CollectionID,
		IntentID:     resp.IntentID,
		ParentRecord: first.Record.RecordID,
		Kind:         "summary",
		Key:          "session/1",
	}
	signAppendRecord(t, store, &appendReq2, alice)
	second, err := store.AppendRecord(appendReq2)
	if err != nil {
		t.Fatal(err)
	}

	filtered, err := store.CollectionRecordsFiltered(collectionResp.Collection.CollectionID, wire.CollectionRecordFilter{
		Kind:    "summary",
		Key:     "session/1",
		Reverse: true,
		Limit:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Records) != 1 || filtered.Records[0].RecordID != second.Record.RecordID {
		t.Fatalf("unexpected filtered records: %+v", filtered)
	}
	children, err := store.CollectionRecordsFiltered(collectionResp.Collection.CollectionID, wire.CollectionRecordFilter{
		ParentRecord: first.Record.RecordID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(children.Records) != 1 || children.Records[0].ParentRecord != first.Record.RecordID {
		t.Fatalf("unexpected child records: %+v", children)
	}
}

func TestCollectionAppendRejectsUnfinalizedIntent(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	registerTestMiner(t, store, "miner_a", "http://miner-a", 100)
	registerTestMiner(t, store, "miner_b", "http://miner-b", 100)
	registerTestMiner(t, store, "miner_c", "http://miner-c", 100)
	alice := newTestUser(t)
	fundAccount(store, alice.Addr, gfTokens(100))
	intentResp, err := store.CreateIntent(testRepairIntentRequest(t, store, alice))
	if err != nil {
		t.Fatal(err)
	}
	collReq := wire.CreateCollectionRequest{
		User: alice.Addr,
		Name: "agent-memory",
	}
	signCreateCollection(t, store, &collReq, alice)
	collectionResp, err := store.CreateCollection(collReq)
	if err != nil {
		t.Fatal(err)
	}
	appendReq := wire.AppendRecordRequest{
		CollectionID: collectionResp.Collection.CollectionID,
		User:         alice.Addr,
		IntentID:     intentResp.IntentID,
	}
	signAppendRecord(t, store, &appendReq, alice)
	_, err = store.AppendRecord(appendReq)
	if err == nil {
		t.Fatal("expected unfinalized intent to be rejected")
	}
}

func TestCollectionAndRecordRequireSignatureForEthereumOwner(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	address := wire.AccountAddress(&privateKey.PublicKey)
	store.data.Accounts[address] = wire.Account{Address: address, Balance: gfTokens(100)}

	if _, err := store.CreateCollection(wire.CreateCollectionRequest{
		User: address,
		Name: "signed-memory",
	}); err == nil {
		t.Fatal("expected unsigned Ethereum collection owner to be rejected")
	}

	createReq := wire.CreateCollectionRequest{
		User:  address,
		Name:  "signed-memory",
		Nonce: 0,
	}
	createReq.ChainID = store.data.ChainID
	if err := wire.SignCreateCollection(&createReq, privateKey); err != nil {
		t.Fatal(err)
	}
	collectionResp, err := store.CreateCollection(createReq)
	if err != nil {
		t.Fatal(err)
	}
	if store.data.Accounts[address].Nonce != 1 {
		t.Fatalf("expected collection nonce increment, account=%+v", store.data.Accounts[address])
	}

	store.data.Intents["intent_signed"] = &Intent{
		IntentView: wire.IntentView{
			IntentID: "intent_signed",
			User:     address,
			FileRoot: "file_root",
			Status:   wire.StatusFinalized,
			DealID:   "deal_signed",
		},
		Receipts: map[int]map[int]wire.MinerReceipt{},
	}
	appendReq := wire.AppendRecordRequest{
		CollectionID: collectionResp.Collection.CollectionID,
		User:         address,
		IntentID:     "intent_signed",
		Kind:         "memory",
		Key:          "session/eth",
		Nonce:        1,
	}
	appendReq.ChainID = store.data.ChainID
	if err := wire.SignAppendRecord(&appendReq, privateKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRecord(appendReq); err != nil {
		t.Fatal(err)
	}
	if store.data.Accounts[address].Nonce != 2 {
		t.Fatalf("expected record nonce increment, account=%+v", store.data.Accounts[address])
	}
}
