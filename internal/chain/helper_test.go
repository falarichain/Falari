package chain

import (
	"crypto/ecdsa"
	"testing"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// testOperatorIdentity generates a fresh ECDSA operator identity for testing.
// Owner and operator use the same key for simplicity.
func testOperatorIdentity(t *testing.T) *OperatorIdentity {
	t.Helper()
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	addr := wire.AccountAddress(&key.PublicKey)
	return &OperatorIdentity{
		OwnerAddress:       addr,
		OperatorAddress:    addr,
		OperatorPublicKey:  &key.PublicKey,
		OperatorPrivateKey: key,
		ownerPrivateKey:    key,
	}
}

// testECDSAKey generates a fresh ECDSA key pair for testing miner/node operations.
func testECDSAKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return key
}

// testKeyHex returns the hex-encoded private key string (suitable for OpenNode or env vars).
func testKeyHex(t *testing.T, key *ecdsa.PrivateKey) string {
	t.Helper()
	return wire.EncodeHex(ethcrypto.FromECDSA(key))
}
