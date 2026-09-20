package chain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chain/internal/reward"
	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func genesisTestAddress(seed uint64) string {
	return fmt.Sprintf("0x%040x", seed)
}

func genesisBool(value bool) *bool { return &value }

func validTestGenesisDoc() wire.GenesisDoc {
	return wire.GenesisDoc{
		ChainID:           "genesis-test",
		GenesisTime:       1_700_000_000,
		FoundationAddress: genesisTestAddress(1),
		RetrievalAddress:  genesisTestAddress(2),
		Accounts: []wire.GenesisAccount{
			{Address: genesisTestAddress(3), Balance: 2 * MinValidatorStake},
		},
		Validators: []wire.GenesisValidator{{
			OwnerAddress:      genesisTestAddress(3),
			OperatorAddress:   genesisTestAddress(4),
			OperatorPublicKey: "operator-public-key",
			Endpoint:          "http://localhost:8080",
			Stake:             MinValidatorStake,
		}},
		GovernanceOperators: []wire.GenesisGovernanceOperator{{
			Operator:    genesisTestAddress(5),
			Permissions: []string{"admin"},
			Enabled:     genesisBool(true),
		}},
		RewardPools: &wire.GenesisRewardPools{
			StoragePoolRemaining:    reward.StoragePoolInitial,
			RetrievalPoolRemaining:  reward.RetrievalPoolInitial,
			ValidatorPoolRemaining:  reward.ValidatorPoolInitial - 2*MinValidatorStake,
			FoundationPoolRemaining: reward.FoundationPoolInitial,
		},
	}
}

func TestGenesisLocksValidatorStakeFromOwnerBalance(t *testing.T) {
	state, err := newStateFromGenesis(validTestGenesisDoc())
	if err != nil {
		t.Fatal(err)
	}
	owner := wire.NormalizeAddress(genesisTestAddress(3))
	account := state.Accounts[owner]
	if account.Balance != MinValidatorStake {
		t.Fatalf("expected owner balance %d after staking, got %d", MinValidatorStake, account.Balance)
	}
	if account.LockedStake != MinValidatorStake {
		t.Fatalf("expected owner locked stake %d, got %d", MinValidatorStake, account.LockedStake)
	}
	if state.RewardPools.FoundationRemaining != reward.FoundationPoolInitial {
		t.Fatalf("staking must not drain the foundation pool: expected %d, got %d",
			reward.FoundationPoolInitial, state.RewardPools.FoundationRemaining)
	}
	if state.RewardPools.PermanentFundRemaining != 0 {
		t.Fatalf("expected empty permanent fund at genesis, got %d", state.RewardPools.PermanentFundRemaining)
	}
}

func TestGenesisRejectsUncoveredStake(t *testing.T) {
	doc := validTestGenesisDoc()
	doc.Accounts[0].Balance = MinValidatorStake - 1
	if _, err := newStateFromGenesis(doc); err == nil {
		t.Fatal("expected stake beyond the owner balance to be rejected")
	}
}

func TestGenesisRejectsSupplyOverallocation(t *testing.T) {
	doc := validTestGenesisDoc()
	doc.Accounts[0].Balance = 2*MinValidatorStake + 1
	if _, err := newStateFromGenesis(doc); err == nil {
		t.Fatal("expected accounts plus pools above total supply to be rejected")
	}
}

func TestGenesisRejectsPoolAboveItsLifetimeTotal(t *testing.T) {
	doc := validTestGenesisDoc()
	doc.RewardPools.ValidatorPoolRemaining = reward.ValidatorPoolInitial + 1
	doc.RewardPools.StoragePoolRemaining = reward.StoragePoolInitial - 1
	if _, err := newStateFromGenesis(doc); err == nil {
		t.Fatal("expected a validator pool above its lifetime total to be rejected")
	}
}

func TestGenesisRejectsPlaceholderAddress(t *testing.T) {
	doc := validTestGenesisDoc()
	doc.RewardPools.StoragePoolRemaining -= 1
	doc.Accounts = append(doc.Accounts, wire.GenesisAccount{Address: "0xSTAKING_RESERVE_PLACEHOLDER", Balance: 1})
	if _, err := newStateFromGenesis(doc); err == nil {
		t.Fatal("expected a non-hex genesis address to be rejected")
	}
}

func TestGenesisRequiresStreamBeneficiaryAddresses(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*wire.GenesisDoc)
	}{
		{"retrieval", func(doc *wire.GenesisDoc) { doc.RetrievalAddress = "" }},
		{"foundation", func(doc *wire.GenesisDoc) { doc.FoundationAddress = "" }},
	} {
		doc := validTestGenesisDoc()
		tc.edit(&doc)
		if _, err := newStateFromGenesis(doc); err == nil {
			t.Fatalf("expected a funded %s pool without its address to be rejected", tc.name)
		}
	}
}

func TestGenesisResolvesGovernanceOperatorFromPublicKey(t *testing.T) {
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	address := wire.AccountAddress(&privateKey.PublicKey)
	doc := validTestGenesisDoc()
	doc.GovernanceOperators = []wire.GenesisGovernanceOperator{{
		Operator:    "0x" + strings.ToUpper(address[2:]),
		PublicKey:   wire.EncodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey)),
		Permissions: []string{"admin"},
	}}
	state, err := newStateFromGenesis(doc)
	if err != nil {
		t.Fatal(err)
	}
	record, ok := state.GovernanceOperators[address]
	if !ok {
		t.Fatalf("expected the address derived from the public key to be registered, got %v", state.GovernanceOperators)
	}
	if !record.Enabled {
		t.Fatal("a genesis operator with no explicit enabled flag must start enabled")
	}
	if record.CreatedAtUnix != doc.GenesisTime {
		t.Fatalf("expected created_at_unix %d, got %d", doc.GenesisTime, record.CreatedAtUnix)
	}
}

func TestGenesisRejectsUnusableGovernanceOperators(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*wire.GenesisDoc)
	}{
		{"no operator at all", func(doc *wire.GenesisDoc) { doc.GovernanceOperators = nil }},
		{"no admin permission", func(doc *wire.GenesisDoc) { doc.GovernanceOperators[0].Permissions = []string{"moderation"} }},
		{"admin disabled", func(doc *wire.GenesisDoc) { doc.GovernanceOperators[0].Enabled = genesisBool(false) }},
		{"empty permissions", func(doc *wire.GenesisDoc) { doc.GovernanceOperators[0].Permissions = nil }},
		{"address is not hex", func(doc *wire.GenesisDoc) { doc.GovernanceOperators[0].Operator = "operator-one" }},
		{"neither address nor key", func(doc *wire.GenesisDoc) { doc.GovernanceOperators[0].Operator = "" }},
		{"key is not hex", func(doc *wire.GenesisDoc) { doc.GovernanceOperators[0].PublicKey = "zz" }},
		{"duplicate operator", func(doc *wire.GenesisDoc) {
			doc.GovernanceOperators = append(doc.GovernanceOperators, doc.GovernanceOperators[0])
		}},
	} {
		doc := validTestGenesisDoc()
		tc.edit(&doc)
		if _, err := newStateFromGenesis(doc); err == nil {
			t.Fatalf("expected a genesis whose %s to be rejected", tc.name)
		}
	}
}

func TestGenesisRejectsOperatorAddressConflictingWithKey(t *testing.T) {
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	doc := validTestGenesisDoc()
	doc.GovernanceOperators = []wire.GenesisGovernanceOperator{{
		Operator:    genesisTestAddress(5),
		PublicKey:   wire.EncodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey)),
		Permissions: []string{"admin"},
	}}
	if _, err := newStateFromGenesis(doc); err == nil {
		t.Fatal("expected an operator address that does not match its public key to be rejected")
	}
}

// testDeployDir locates the repository's deploy/ directory, which holds the genesis files
// operators actually boot with. A genesis the loader rejects is a failed deployment, not
// a failed unit test.
func testDeployDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "deploy", "genesis.json")); err == nil {
			return filepath.Join(dir, "deploy")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("deploy/genesis.json not reachable from the test working directory")
		}
		dir = parent
	}
}

// TestDeployedGenesisFilesValidate guards the files operators actually boot with:
// a genesis the loader rejects is a failed deployment, not a failed unit test.
func TestDeployedGenesisFilesValidate(t *testing.T) {
	deployDir := testDeployDir(t)
	for _, name := range []string{"genesis.json", "genesis-test.json"} {
		state, err := newStateFromGenesisFile(filepath.Join(deployDir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if state.RetrievalAddress == "" || state.FoundationAddress == "" {
			t.Fatalf("%s: both emission beneficiary addresses are required", name)
		}
		// A genesis nobody can sign epochs with boots into a chain that finalizes nothing,
		// so the deployed files must seed the driver themselves.
		drivers := 0
		for _, record := range state.GovernanceOperators {
			if !record.Enabled || !hasAdminPermission(record.Permissions) {
				continue
			}
			drivers++
			matchesValidator := false
			for _, validator := range state.Validators {
				if validator.OperatorAddress == record.Operator {
					matchesValidator = true
				}
			}
			if !matchesValidator {
				t.Fatalf("%s: governance operator %s is not the operator address of any genesis validator, "+
					"so no node started with these keys can sign epoch transactions", name, record.Operator)
			}
		}
		if drivers == 0 {
			t.Fatalf("%s: needs at least one enabled governance operator with the admin permission", name)
		}
	}
}
