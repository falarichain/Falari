package wire

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type operatorRequestSigningPayload struct {
	ChainID       string `json:"chain_id"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	BodyHash      string `json:"body_hash"`
	Nonce         uint64 `json:"nonce"`
	TimestampUnix int64  `json:"timestamp_unix"`
}

func OperatorRequestHash(chainID string, method string, path string, body []byte, nonce uint64, timestampUnix int64) ([]byte, error) {
	bodyHash := sha256.Sum256(body)
	payload, err := json.Marshal(operatorRequestSigningPayload{
		ChainID:       chainID,
		Method:        strings.ToUpper(method),
		Path:          path,
		BodyHash:      hex.EncodeToString(bodyHash[:]),
		Nonce:         nonce,
		TimestampUnix: timestampUnix,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

func SignOperatorRequest(chainID string, method string, path string, body []byte, nonce uint64, timestampUnix int64, privateKey *ecdsa.PrivateKey) (string, error) {
	hash, err := OperatorRequestHash(chainID, method, path, body, nonce, timestampUnix)
	if err != nil {
		return "", err
	}
	signature, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(signature), nil
}

func VerifyOperatorRequestSignature(chainID string, method string, path string, body []byte, nonce uint64, timestampUnix int64, expectedAddress string, signatureHex string) error {
	signature, err := hex.DecodeString(strings.TrimPrefix(signatureHex, "0x"))
	if err != nil {
		return errors.New("invalid signature encoding")
	}
	if len(signature) != 65 {
		return errors.New("invalid signature length")
	}
	hash, err := OperatorRequestHash(chainID, method, path, body, nonce, timestampUnix)
	if err != nil {
		return err
	}
	publicKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return errors.New("failed to recover signer")
	}
	recovered := AccountAddress(publicKey)
	if !strings.EqualFold(recovered, expectedAddress) {
		return errors.New("signature does not match operator address")
	}
	return nil
}
