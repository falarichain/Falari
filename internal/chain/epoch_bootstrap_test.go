package chain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// testGenesisFileAt writes a genesis document the way a deployment does and returns its
// path, so the boot rehearsal exercises JSON unmarshalling and the loader too.
func testGenesisFileAt(t *testing.T, doc wire.GenesisDoc) string {
	t.Helper()
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "genesis.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testStateFileAt(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "chain.json")
}

// TestBootWithGenesisOperatorDrivesEpochs walks the sequence chainnode runs at startup:
// open the state from genesis, bind the operator identity, then start an epoch. Genesis
// that does not register that operator as an admin governance operator stops working here,
// which is the failure P0 #10 turned from latent into a launch blocker.
func TestBootWithGenesisOperatorDrivesEpochs(t *testing.T) {
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	operatorAddress := wire.AccountAddress(&privateKey.PublicKey)

	doc := validTestGenesisDoc()
	// The devnet shape: one validator whose operator key is also the governance operator
	// that drives epochs, so the booting node needs no separate driver identity.
	doc.Validators[0].OperatorAddress = operatorAddress
	doc.Validators[0].OperatorPublicKey = wire.EncodeHex(ethcrypto.CompressPubkey(&privateKey.PublicKey))
	doc.GovernanceOperators = []wire.GenesisGovernanceOperator{{
		Operator:    operatorAddress,
		Permissions: []string{"admin"},
		Enabled:     genesisBool(true),
	}}
	genesisPath := testGenesisFileAt(t, doc)
	owner := wire.NormalizeAddress(doc.Validators[0].OwnerAddress)
	// What LoadOperatorIdentityFromEnv produces on the node: owner from the environment,
	// operator derived from the operator private key.
	identity := &OperatorIdentity{
		OwnerAddress:       owner,
		OperatorAddress:    operatorAddress,
		OperatorPublicKey:  &privateKey.PublicKey,
		OperatorPrivateKey: privateKey,
	}

	statePath := testStateFileAt(t)
	store, err := OpenStoreWithGenesis(statePath, genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	seedFinalizedDealForEpochTest(store)
	if !store.HasActiveValidator(owner, operatorAddress) {
		t.Fatal("boot must skip registration for a validator that genesis already declared")
	}
	store.SetOperatorIdentity(identity)

	if operator, err := store.EpochDriverStatus(); err != nil || operator != operatorAddress {
		t.Fatalf("expected the genesis operator to drive epochs, got %s: %v", operator, err)
	}
	if _, err := store.StartEpoch(wire.StartEpochRequest{}); err != nil {
		t.Fatalf("start epoch after boot: %v", err)
	}
	if len(store.data.Epochs) != 1 {
		t.Fatalf("expected one epoch, got %d", len(store.data.Epochs))
	}
	if store.data.OperatorNonces[operatorAddress] != 1 {
		t.Fatalf("expected the boot epoch to consume nonce 1, got %d", store.data.OperatorNonces[operatorAddress])
	}
	produced := lastEpochTx(t, store, "start_epoch")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// A restart reads the persisted state, not genesis, so the operator book must survive.
	// The node identity is loaded from the environment, so it is bound again after opening.
	restarted, err := OpenStoreWithGenesis(statePath, "")
	if err != nil {
		t.Fatal(err)
	}
	restarted.SetOperatorIdentity(identity)
	defer restarted.Close()
	if len(restarted.data.Epochs) != 1 {
		t.Fatalf("epoch must survive a restart, got %d", len(restarted.data.Epochs))
	}
	if _, err := restarted.EpochDriverStatus(); err != nil {
		t.Fatalf("operator must stay authorized after a restart: %v", err)
	}
	if !restarted.HasActiveValidator(owner, operatorAddress) {
		t.Fatal("the restarted node must skip re-registration, which a duplicate would fail")
	}

	// A peer booted from the same genesis file replays the transaction and consumes the
	// same nonce, so the driver needs no privileged state beyond what genesis seeded.
	peer, err := OpenStoreWithGenesis(testStateFileAt(t), genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := peer.applyStartEpochLocked(produced); err != nil {
		t.Fatalf("peer replay of the genesis-driven epoch: %v", err)
	}
	if len(peer.data.Epochs) != 1 || peer.data.OperatorNonces[operatorAddress] != 1 {
		t.Fatalf("peer should apply the epoch and consume the nonce, epochs=%d nonce=%d",
			len(peer.data.Epochs), peer.data.OperatorNonces[operatorAddress])
	}
	if _, err := peer.EpochDriverStatus(); err == nil {
		t.Fatal("a peer without the driver key must report itself as unable to drive epochs")
	}
	if peer.HasActiveValidator(owner, genesisTestAddress(9)) {
		t.Fatal("an operator address that is not mapped to this owner must not count as registered")
	}
}
