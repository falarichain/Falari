package chain

import (
	"crypto/ecdsa"
	"errors"
	"os"
	"strings"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type OperatorIdentity struct {
	OwnerAddress       string
	OperatorAddress    string
	OperatorPublicKey  *ecdsa.PublicKey
	OperatorPrivateKey *ecdsa.PrivateKey
	ownerPrivateKey    *ecdsa.PrivateKey // optional, used only for registration
}

// LoadOperatorIdentityFromEnv loads an operator identity from environment variables:
//   - OWNER_ADDRESS: the owner (cold wallet) Ethereum-compatible address
//   - OPERATOR_PRIVATE_KEY: hex-encoded secp256k1 key for the operator (hot node)
//   - OWNER_PRIVATE_KEY: (optional) hex-encoded secp256k1 key for the owner, used only during registration
func LoadOperatorIdentityFromEnv() (*OperatorIdentity, error) {
	ownerAddr := os.Getenv("OWNER_ADDRESS")
	if ownerAddr == "" {
		return nil, errors.New("OWNER_ADDRESS environment variable is required")
	}
	operatorHex := os.Getenv("OPERATOR_PRIVATE_KEY")
	if operatorHex == "" {
		return nil, errors.New("OPERATOR_PRIVATE_KEY environment variable is required; generate a key with: genkey")
	}
	operatorKey, err := ethcrypto.HexToECDSA(strings.TrimPrefix(strings.TrimPrefix(operatorHex, "0x"), "0X"))
	if err != nil {
		return nil, errors.New("invalid OPERATOR_PRIVATE_KEY: " + err.Error())
	}
	operatorAddr := wire.AccountAddress(&operatorKey.PublicKey)

	identity := &OperatorIdentity{
		OwnerAddress:       wire.NormalizeAddress(ownerAddr),
		OperatorAddress:    operatorAddr,
		OperatorPublicKey:  &operatorKey.PublicKey,
		OperatorPrivateKey: operatorKey,
	}

	// Optional owner private key for registration signing.
	if ownerHex := os.Getenv("OWNER_PRIVATE_KEY"); ownerHex != "" {
		ownerKey, err := ethcrypto.HexToECDSA(strings.TrimPrefix(strings.TrimPrefix(ownerHex, "0x"), "0X"))
		if err != nil {
			return nil, errors.New("invalid OWNER_PRIVATE_KEY: " + err.Error())
		}
		identity.ownerPrivateKey = ownerKey
	}

	return identity, nil
}

func (o *OperatorIdentity) OperatorPublicKeyHex() string {
	return wire.EncodeHex(ethcrypto.CompressPubkey(o.OperatorPublicKey))
}

func (o *OperatorIdentity) RegistrationRequest(endpoint string, stake uint64, commissionRateBPS uint64) (wire.RegisterValidatorRequest, error) {
	req := wire.RegisterValidatorRequest{
		OwnerAddress:      o.OwnerAddress,
		OperatorAddress:   o.OperatorAddress,
		OperatorPublicKey: o.OperatorPublicKeyHex(),
		Endpoint:          endpoint,
		Stake:             stake,
		CommissionRateBPS: commissionRateBPS,
	}
	if o.ownerPrivateKey == nil {
		return req, errors.New("OWNER_PRIVATE_KEY is required for initial registration; set it in the environment")
	}
	if err := wire.SignValidatorRegistration(&req, o.ownerPrivateKey); err != nil {
		return wire.RegisterValidatorRequest{}, err
	}
	if err := wire.SignOperatorProofOfPossession(&req, o.OperatorPrivateKey); err != nil {
		return wire.RegisterValidatorRequest{}, err
	}
	return req, nil
}
