package chain

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"chain/internal/wire"
)

type ValidatorIdentity struct {
	Address    string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

type validatorIdentityFile struct {
	Address    string `json:"address"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

func LoadOrCreateValidatorIdentity(path string) (*ValidatorIdentity, error) {
	if path == "" {
		return createValidatorIdentity("")
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return createValidatorIdentity(path)
	}
	if err != nil {
		return nil, err
	}
	var meta validatorIdentityFile
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	pub, err := base64.StdEncoding.DecodeString(meta.PublicKey)
	if err != nil {
		return nil, err
	}
	priv, err := base64.StdEncoding.DecodeString(meta.PrivateKey)
	if err != nil {
		return nil, err
	}
	return &ValidatorIdentity{
		Address:    meta.Address,
		PublicKey:  ed25519.PublicKey(pub),
		PrivateKey: ed25519.PrivateKey(priv),
	}, nil
}

func (v *ValidatorIdentity) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(v.PublicKey)
}

func (v *ValidatorIdentity) RegistrationRequest(endpoint string, stake uint64, commissionRateBPS uint64) (wire.RegisterValidatorRequest, error) {
	req := wire.RegisterValidatorRequest{
		Address:           v.Address,
		PublicKey:         v.PublicKeyBase64(),
		Endpoint:          endpoint,
		Stake:             stake,
		CommissionRateBPS: commissionRateBPS,
	}
	if err := wire.SignValidatorRegistration(&req, v.PrivateKey); err != nil {
		return wire.RegisterValidatorRequest{}, err
	}
	return req, nil
}

func createValidatorIdentity(path string) (*ValidatorIdentity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	identity := &ValidatorIdentity{
		Address:    validatorAddress(pub),
		PublicKey:  pub,
		PrivateKey: priv,
	}
	if path == "" {
		return identity, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	meta := validatorIdentityFile{
		Address:    identity.Address,
		PublicKey:  identity.PublicKeyBase64(),
		PrivateKey: base64.StdEncoding.EncodeToString(identity.PrivateKey),
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return nil, err
	}
	return identity, nil
}

func validatorAddress(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	addr := "0x" + hex.EncodeToString(sum[:20])
	return wire.NormalizeAddress(addr)
}
