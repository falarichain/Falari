package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"math/big"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// secp256k1HalfN is half the secp256k1 curve order (EIP-2).
// Signatures with S > halfN are considered malleable and rejected.
var secp256k1HalfN *big.Int

func init() {
	secp256k1HalfN, _ = new(big.Int).SetString("7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF5D576E7357A4501DDFE92F46681B20A0", 16)
}

// recoverSigner validates signature V/S fields and recovers the public key.
// It enforces EIP-2 S-value malleability protection and recovery ID bounds.
func recoverSigner(hash, sigBytes []byte) (*ecdsa.PublicKey, error) {
	if len(sigBytes) != 65 {
		return nil, errors.New("invalid signature length")
	}
	// Recovery ID (V) must be 0 or 1 for secp256k1.
	if sigBytes[64] > 1 {
		return nil, errors.New("invalid recovery id: must be 0 or 1")
	}
	// EIP-2: reject malleable signatures where S > halfN.
	s := new(big.Int).SetBytes(sigBytes[32:64])
	if s.Cmp(secp256k1HalfN) > 0 {
		return nil, errors.New("signature S value exceeds half curve order (EIP-2)")
	}
	return ethcrypto.SigToPub(hash, sigBytes)
}

// infraPayloadHash marshals the payload to JSON and returns its Keccak256 hash.
func infraPayloadHash(payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(raw), nil
}

// signInfraPayload signs a JSON-serializable payload with ECDSA.
// Returns hex-encoded signature (65 bytes: r+s+v) and hex-encoded compressed public key (33 bytes).
func signInfraPayload(payload any, priv *ecdsa.PrivateKey) (sig, pub string, err error) {
	h, err := infraPayloadHash(payload)
	if err != nil {
		return "", "", err
	}
	s, err := ethcrypto.Sign(h, priv)
	if err != nil {
		return "", "", err
	}
	return encodeHex(s), encodeHex(ethcrypto.CompressPubkey(&priv.PublicKey)), nil
}

// verifyInfraSignature recovers the signer from an ECDSA signature and compares
// the derived address with the expected address.
func verifyInfraSignature(expectedAddr, sigHex string, payload any) error {
	if sigHex == "" {
		return errors.New("missing signature")
	}
	sigBytes, err := decodeHex(sigHex)
	if err != nil {
		return errors.New("invalid signature encoding")
	}
	h, err := infraPayloadHash(payload)
	if err != nil {
		return err
	}
	pub, err := recoverSigner(h, sigBytes)
	if err != nil {
		return errors.New("failed to recover signer: " + err.Error())
	}
	addr := AccountAddress(pub)
	if addr != NormalizeAddress(expectedAddr) {
		return errors.New("signature does not match expected address: recovered=" + addr + " expected=" + expectedAddr)
	}
	return nil
}
