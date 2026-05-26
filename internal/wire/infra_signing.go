package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

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
	if len(sigBytes) != 65 {
		return errors.New("invalid signature length")
	}
	h, err := infraPayloadHash(payload)
	if err != nil {
		return err
	}
	pub, err := ethcrypto.SigToPub(h, sigBytes)
	if err != nil {
		return errors.New("failed to recover signer")
	}
	addr := AccountAddress(pub)
	if !strings.EqualFold(addr, expectedAddr) {
		return errors.New("signature does not match expected address: recovered=" + addr + " expected=" + expectedAddr)
	}
	return nil
}
