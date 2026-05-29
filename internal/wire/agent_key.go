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
const AgentKeyReferencePrefix = "fara_ref_"

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

// EncodeAgentKeyReferenceString packs only public agent key routing metadata.
// The gateway must load the matching private key from local configuration.
func EncodeAgentKeyReferenceString(agentKeyID, master, address string) string {
	raw := agentKeyID + "|" + master + "|" + address
	return AgentKeyReferencePrefix + base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeAgentKeyReferenceString parses a fara_ref_... string back into its public components.
func DecodeAgentKeyReferenceString(encoded string) (AgentKeyParts, error) {
	if !strings.HasPrefix(encoded, AgentKeyReferencePrefix) {
		return AgentKeyParts{}, errors.New("agent key reference must start with " + AgentKeyReferencePrefix)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, AgentKeyReferencePrefix))
	if err != nil {
		return AgentKeyParts{}, fmt.Errorf("decode agent key reference: %w", err)
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 3 {
		return AgentKeyParts{}, errors.New("invalid agent key reference format")
	}
	return AgentKeyParts{
		AgentKeyID: parts[0],
		Master:     parts[1],
		Address:    parts[2],
	}, nil
}

type registerAgentKeySigningPayload struct {
	ChainID     string   `json:"chain_id"`
	Master      string   `json:"master"`
	AgentPub    string   `json:"agent_pub"`
	Permissions []string `json:"permissions"`
	DailyLimit  uint64   `json:"daily_limit"`
	TotalLimit  uint64   `json:"total_limit"`
	ExpiresAt   int64    `json:"expires_at"`
	Nonce       uint64   `json:"nonce"`
}

type revokeAgentKeySigningPayload struct {
	ChainID string `json:"chain_id"`
	KeyID   string `json:"key_id"`
	Master  string `json:"master"`
	Nonce   uint64 `json:"nonce"`
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
		ChainID:     req.ChainID,
		Master:      NormalizeAddress(req.Master),
		AgentPub:    req.AgentPub,
		Permissions: req.Permissions,
		DailyLimit:  req.DailyLimit,
		TotalLimit:  req.TotalLimit,
		ExpiresAt:   req.ExpiresAt,
		Nonce:       req.Nonce,
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
		ChainID: req.ChainID,
		KeyID:   req.KeyID,
		Master:  NormalizeAddress(req.Master),
		Nonce:   req.Nonce,
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

// --- ExtendAgentKey ---

type extendAgentKeySigningPayload struct {
	ChainID   string `json:"chain_id"`
	KeyID     string `json:"key_id"`
	Master    string `json:"master"`
	ExpiresAt int64  `json:"expires_at"`
	Nonce     uint64 `json:"nonce"`
}

func ExtendAgentKeyHash(req ExtendAgentKeyRequest) ([]byte, error) {
	payload, err := json.Marshal(extendAgentKeySigningPayload{
		ChainID:   req.ChainID,
		KeyID:     req.KeyID,
		Master:    NormalizeAddress(req.Master),
		ExpiresAt: req.ExpiresAt,
		Nonce:     req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

func SignExtendAgentKey(req *ExtendAgentKeyRequest, privateKey *ecdsa.PrivateKey) error {
	if req.Master == "" {
		req.Master = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := ExtendAgentKeyHash(*req)
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

func VerifyExtendAgentKey(req ExtendAgentKeyRequest) error {
	if req.Signature == "" {
		return errors.New("extend agent key requires signature")
	}
	_, address, err := recoverExtendAgentKeySigner(req)
	if err != nil {
		return err
	}
	if !strings.EqualFold(address, req.Master) {
		return errors.New("extend agent key signature does not match master")
	}
	return nil
}

func recoverExtendAgentKeySigner(req ExtendAgentKeyRequest) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	if len(signature) != 65 {
		return nil, "", errors.New("invalid extend agent key signature size")
	}
	hash, err := ExtendAgentKeyHash(req)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}

// --- TopupAgentKey ---

type topupAgentKeySigningPayload struct {
	ChainID    string `json:"chain_id"`
	KeyID      string `json:"key_id"`
	Master     string `json:"master"`
	TotalLimit uint64 `json:"total_limit"`
	Nonce      uint64 `json:"nonce"`
}

func TopupAgentKeyHash(req TopupAgentKeyRequest) ([]byte, error) {
	payload, err := json.Marshal(topupAgentKeySigningPayload{
		ChainID:    req.ChainID,
		KeyID:      req.KeyID,
		Master:     NormalizeAddress(req.Master),
		TotalLimit: req.TotalLimit,
		Nonce:      req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

func SignTopupAgentKey(req *TopupAgentKeyRequest, privateKey *ecdsa.PrivateKey) error {
	if req.Master == "" {
		req.Master = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := TopupAgentKeyHash(*req)
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

func VerifyTopupAgentKey(req TopupAgentKeyRequest) error {
	if req.Signature == "" {
		return errors.New("topup agent key requires signature")
	}
	_, address, err := recoverTopupAgentKeySigner(req)
	if err != nil {
		return err
	}
	if !strings.EqualFold(address, req.Master) {
		return errors.New("topup agent key signature does not match master")
	}
	return nil
}

func recoverTopupAgentKeySigner(req TopupAgentKeyRequest) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	if len(signature) != 65 {
		return nil, "", errors.New("invalid topup agent key signature size")
	}
	hash, err := TopupAgentKeyHash(req)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}
