package wire

import (
	"crypto/ecdsa"
	"encoding/json"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type createKeyEnvelopeSigningPayload struct {
	ChainID          string             `json:"chain_id"`
	Action           string             `json:"action"`
	IntentID         string             `json:"intent_id"`
	Owner            string             `json:"owner"`
	Recipient        string             `json:"recipient"`
	RecipientType    string             `json:"recipient_type"`
	Algorithm        string             `json:"algorithm"`
	EncryptedDataKey string             `json:"encrypted_data_key"`
	Nonce            string             `json:"nonce,omitempty"`
	KDF              *PasscodeKDFParams `json:"kdf,omitempty"`
	ExpiresAtUnix    int64              `json:"expires_at_unix,omitempty"`
	AccountNonce     uint64             `json:"account_nonce"`
}

type revokeShareSigningPayload struct {
	ChainID      string `json:"chain_id"`
	Action       string `json:"action"`
	ShareID      string `json:"share_id"`
	Owner        string `json:"owner"`
	AccountNonce uint64 `json:"account_nonce"`
}

func CreateKeyEnvelopeHash(req CreateKeyEnvelopeRequest) ([]byte, error) {
	p, err := json.Marshal(createKeyEnvelopeSigningPayload{
		ChainID:          req.ChainID,
		Action:           "create_key_envelope",
		IntentID:         req.IntentID,
		Owner:            NormalizeAddress(req.Owner),
		Recipient:        req.Recipient,
		RecipientType:    req.RecipientType,
		Algorithm:        req.Algorithm,
		EncryptedDataKey: req.EncryptedDataKey,
		Nonce:            req.Nonce,
		KDF:              req.KDF,
		ExpiresAtUnix:    req.ExpiresAtUnix,
		AccountNonce:     req.AccountNonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignCreateKeyEnvelope(req *CreateKeyEnvelopeRequest, priv *ecdsa.PrivateKey) error {
	if req.Owner == "" {
		req.Owner = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return CreateKeyEnvelopeHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyCreateKeyEnvelope(req CreateKeyEnvelopeRequest) error {
	return verifyRequestSig(req.Owner, req.Signature, func() ([]byte, error) { return CreateKeyEnvelopeHash(req) })
}

func CreateAddressShareHash(req CreateAddressShareRequest) ([]byte, error) {
	return CreateKeyEnvelopeHash(CreateKeyEnvelopeRequest{
		ChainID:          req.ChainID,
		IntentID:         req.IntentID,
		Owner:            req.Owner,
		Recipient:        NormalizeAddress(req.Recipient),
		RecipientType:    KeyEnvelopeRecipientAddress,
		Algorithm:        req.Algorithm,
		EncryptedDataKey: req.EncryptedDataKey,
		Nonce:            req.Nonce,
		KDF:              req.KDF,
		ExpiresAtUnix:    req.ExpiresAtUnix,
		AccountNonce:     req.AccountNonce,
	})
}

func SignCreateAddressShare(req *CreateAddressShareRequest, priv *ecdsa.PrivateKey) error {
	if req.Owner == "" {
		req.Owner = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return CreateAddressShareHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyCreateAddressShare(req CreateAddressShareRequest) error {
	return verifyRequestSig(req.Owner, req.Signature, func() ([]byte, error) { return CreateAddressShareHash(req) })
}

func CreatePasscodeShareHash(req CreatePasscodeShareRequest) ([]byte, error) {
	return CreateKeyEnvelopeHash(CreateKeyEnvelopeRequest{
		ChainID:          req.ChainID,
		IntentID:         req.IntentID,
		Owner:            req.Owner,
		Recipient:        "passcode",
		RecipientType:    KeyEnvelopeRecipientPasscode,
		Algorithm:        req.Algorithm,
		EncryptedDataKey: req.EncryptedDataKey,
		Nonce:            req.Nonce,
		KDF:              req.KDF,
		ExpiresAtUnix:    req.ExpiresAtUnix,
		AccountNonce:     req.AccountNonce,
	})
}

func SignCreatePasscodeShare(req *CreatePasscodeShareRequest, priv *ecdsa.PrivateKey) error {
	if req.Owner == "" {
		req.Owner = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return CreatePasscodeShareHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyCreatePasscodeShare(req CreatePasscodeShareRequest) error {
	return verifyRequestSig(req.Owner, req.Signature, func() ([]byte, error) { return CreatePasscodeShareHash(req) })
}

func RevokeShareHash(req RevokeShareRequest) ([]byte, error) {
	p, err := json.Marshal(revokeShareSigningPayload{
		ChainID:      req.ChainID,
		Action:       "revoke_share",
		ShareID:      req.ShareID,
		Owner:        NormalizeAddress(req.Owner),
		AccountNonce: req.AccountNonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignRevokeShare(req *RevokeShareRequest, priv *ecdsa.PrivateKey) error {
	if req.Owner == "" {
		req.Owner = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return RevokeShareHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyRevokeShare(req RevokeShareRequest) error {
	return verifyRequestSig(req.Owner, req.Signature, func() ([]byte, error) { return RevokeShareHash(req) })
}
