package wire

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type transferSigningPayload struct {
	ChainID string `json:"chain_id"`
	From    string `json:"from"`
	To      string `json:"to"`
	Amount  uint64 `json:"amount"`
	Nonce   uint64 `json:"nonce"`
	Fee     uint64 `json:"fee"`
}

func AccountAddress(publicKey *ecdsa.PublicKey) string {
	return ethcrypto.PubkeyToAddress(*publicKey).Hex()
}

func TransferPayload(req TransferRequest, chainID string) ([]byte, error) {
	payload := transferSigningPayload{
		ChainID: chainID,
		From:    req.From,
		To:      req.To,
		Amount:  req.Amount,
		Nonce:   req.Nonce,
		Fee:     req.Fee,
	}
	return json.Marshal(payload)
}

func TransferHash(req TransferRequest, chainID string) ([]byte, error) {
	payload, err := TransferPayload(req, chainID)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

func SignTransfer(req *TransferRequest, privateKey *ecdsa.PrivateKey, chainID string) error {
	if req.PublicKey == "" {
		req.PublicKey = encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey))
	}
	if req.From == "" {
		req.From = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := TransferHash(*req, chainID)
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

func VerifyTransferSignature(req TransferRequest, chainID string) error {
	_, address, err := recoverTransferSigner(req, chainID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(address, req.From) {
		return errors.New("transfer signature does not match from address")
	}
	return nil
}

func RecoverTransferPublicKey(req TransferRequest, chainID string) (string, error) {
	publicKey, _, err := recoverTransferSigner(req, chainID)
	if err != nil {
		return "", err
	}
	return encodeHex(ethcrypto.FromECDSAPub(publicKey)), nil
}

func recoverTransferSigner(req TransferRequest, chainID string) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	if len(signature) != 65 {
		return nil, "", errors.New("invalid transfer signature size")
	}
	hash, err := TransferHash(req, chainID)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}

func IsSignedTransfer(req TransferRequest) bool {
	return req.PublicKey != "" || req.Signature != ""
}

func encodeHex(raw []byte) string {
	return "0x" + hex.EncodeToString(raw)
}

func decodeHex(raw string) ([]byte, error) {
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "0x"), "0X")
	return hex.DecodeString(raw)
}
