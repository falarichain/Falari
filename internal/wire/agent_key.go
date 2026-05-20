package wire

import (
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

const AgentKeyPrefix = "fara_"

// EncodeAgentKeyString packs agent_key_id, master, address, and private_key
// into a single copy‑pasteable string: fara_<base64url(key_id|master|address|privkey)>
func EncodeAgentKeyString(agentKeyID, master, address, privateKeyHex string) string {
	raw := agentKeyID + "|" + master + "|" + address + "|" + privateKeyHex
	return AgentKeyPrefix + base64.RawURLEncoding.EncodeToString([]byte(raw))
}

type AgentKeyParts struct {
	AgentKeyID string
	Master     string
	Address    string
	PrivateKey string
}

// DecodeAgentKeyString parses a fara_... string back into its components.
func DecodeAgentKeyString(encoded string) (AgentKeyParts, error) {
	if !strings.HasPrefix(encoded, AgentKeyPrefix) {
		return AgentKeyParts{}, errors.New("agent key string must start with " + AgentKeyPrefix)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, AgentKeyPrefix))
	if err != nil {
		return AgentKeyParts{}, fmt.Errorf("decode agent key: %w", err)
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 4 {
		return AgentKeyParts{}, errors.New("invalid agent key string format")
	}
	return AgentKeyParts{
		AgentKeyID: parts[0],
		Master:     parts[1],
		Address:    parts[2],
		PrivateKey: parts[3],
	}, nil
}

type registerAgentKeySigningPayload struct {
	Master      string   `json:"master"`
	AgentPub    string   `json:"agent_pub"`
	Permissions []string `json:"permissions"`
	DailyLimit  uint64   `json:"daily_limit"`
	TotalLimit  uint64   `json:"total_limit"`
	ExpiresAt   int64    `json:"expires_at"`
}

type revokeAgentKeySigningPayload struct {
	KeyID  string `json:"key_id"`
	Master string `json:"master"`
	Nonce  uint64 `json:"nonce"`
}

func SignRegisterAgentKey(req *RegisterAgentKeyRequest, privateKey *ecdsa.PrivateKey) error {
	if req.Master == "" {
		req.Master = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := RegisterAgentKeyHash(*req)
	if err != nil {
		return err
	}
	signature, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return err
	}
	req.Signature = encodeHex(signature)
	return nil
}

func VerifyRegisterAgentKey(req RegisterAgentKeyRequest) error {
	if req.Signature == "" {
		return errors.New("register agent key requires signature")
	}
	_, address, err := recoverRegisterAgentKeySigner(req)
	if err != nil {
		return err
	}
	if !strings.EqualFold(address, req.Master) {
		return errors.New("register agent key signature does not match master")
	}
	return nil
}

func RegisterAgentKeyHash(req RegisterAgentKeyRequest) ([]byte, error) {
	payload, err := json.Marshal(registerAgentKeySigningPayload{
		Master:      NormalizeAddress(req.Master),
		AgentPub:    req.AgentPub,
		Permissions: req.Permissions,
		DailyLimit:  req.DailyLimit,
		TotalLimit:  req.TotalLimit,
		ExpiresAt:   req.ExpiresAt,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

func SignRevokeAgentKey(req *RevokeAgentKeyRequest, privateKey *ecdsa.PrivateKey) error {
	if req.Master == "" {
		req.Master = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := RevokeAgentKeyHash(*req)
	if err != nil {
		return err
	}
	signature, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return err
	}
	req.Signature = encodeHex(signature)
	return nil
}

func VerifyRevokeAgentKey(req RevokeAgentKeyRequest) error {
	if req.Signature == "" {
		return errors.New("revoke agent key requires signature")
	}
	_, address, err := recoverRevokeAgentKeySigner(req)
	if err != nil {
		return err
	}
	if !strings.EqualFold(address, req.Master) {
		return errors.New("revoke agent key signature does not match master")
	}
	return nil
}

func RevokeAgentKeyHash(req RevokeAgentKeyRequest) ([]byte, error) {
	payload, err := json.Marshal(revokeAgentKeySigningPayload{
		KeyID:  req.KeyID,
		Master: NormalizeAddress(req.Master),
		Nonce:  req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

func recoverRegisterAgentKeySigner(req RegisterAgentKeyRequest) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	if len(signature) != 65 {
		return nil, "", errors.New("invalid register agent key signature size")
	}
	hash, err := RegisterAgentKeyHash(req)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}

func recoverRevokeAgentKeySigner(req RevokeAgentKeyRequest) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	if len(signature) != 65 {
		return nil, "", errors.New("invalid revoke agent key signature size")
	}
	hash, err := RevokeAgentKeyHash(req)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}
