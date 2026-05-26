package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type createCollectionSigningPayload struct {
	ChainID     string            `json:"chain_id"`
	User        string            `json:"user"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Nonce       uint64            `json:"nonce"`
}

type appendRecordSigningPayload struct {
	ChainID      string            `json:"chain_id"`
	CollectionID string            `json:"collection_id"`
	User         string            `json:"user"`
	IntentID     string            `json:"intent_id"`
	ParentRecord string            `json:"parent_record,omitempty"`
	Kind         string            `json:"kind,omitempty"`
	Key          string            `json:"key,omitempty"`
	ManifestRoot string            `json:"manifest_root,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Nonce        uint64            `json:"nonce"`
}

func SignCreateCollection(req *CreateCollectionRequest, privateKey *ecdsa.PrivateKey) error {
	if req.PublicKey == "" {
		req.PublicKey = encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey))
	}
	if req.User == "" {
		req.User = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := CreateCollectionHash(*req)
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

func VerifyCreateCollectionSignature(req CreateCollectionRequest) error {
	_, address, err := recoverCreateCollectionSigner(req)
	if err != nil {
		return err
	}
	if !strings.EqualFold(address, req.User) {
		return errors.New("collection signature does not match user")
	}
	return nil
}

func RecoverCreateCollectionPublicKey(req CreateCollectionRequest) (string, error) {
	publicKey, _, err := recoverCreateCollectionSigner(req)
	if err != nil {
		return "", err
	}
	return encodeHex(ethcrypto.FromECDSAPub(publicKey)), nil
}

func CreateCollectionHash(req CreateCollectionRequest) ([]byte, error) {
	payload, err := json.Marshal(createCollectionSigningPayload{
		ChainID:     req.ChainID,
		User:        NormalizeAddress(req.User),
		Name:        req.Name,
		Description: req.Description,
		Metadata:    req.Metadata,
		Nonce:       req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

func SignAppendRecord(req *AppendRecordRequest, privateKey *ecdsa.PrivateKey) error {
	if req.PublicKey == "" {
		req.PublicKey = encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey))
	}
	if req.User == "" {
		req.User = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := AppendRecordHash(*req)
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

func VerifyAppendRecordSignature(req AppendRecordRequest) error {
	_, address, err := recoverAppendRecordSigner(req)
	if err != nil {
		return err
	}
	if !strings.EqualFold(address, req.User) {
		return errors.New("record signature does not match user")
	}
	return nil
}

func RecoverAppendRecordPublicKey(req AppendRecordRequest) (string, error) {
	publicKey, _, err := recoverAppendRecordSigner(req)
	if err != nil {
		return "", err
	}
	return encodeHex(ethcrypto.FromECDSAPub(publicKey)), nil
}

func AppendRecordHash(req AppendRecordRequest) ([]byte, error) {
	payload, err := json.Marshal(appendRecordSigningPayload{
		ChainID:      req.ChainID,
		CollectionID: req.CollectionID,
		User:         NormalizeAddress(req.User),
		IntentID:     req.IntentID,
		ParentRecord: req.ParentRecord,
		Kind:         req.Kind,
		Key:          req.Key,
		ManifestRoot: req.ManifestRoot,
		Metadata:     req.Metadata,
		Nonce:        req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

func IsSignedCreateCollection(req CreateCollectionRequest) bool {
	return req.PublicKey != "" || req.Signature != ""
}

func IsSignedAppendRecord(req AppendRecordRequest) bool {
	return req.PublicKey != "" || req.Signature != ""
}

func RequiresAccountSignature(address string) bool {
	return common.IsHexAddress(address)
}

func recoverCreateCollectionSigner(req CreateCollectionRequest) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	if len(signature) != 65 {
		return nil, "", errors.New("invalid collection signature size")
	}
	hash, err := CreateCollectionHash(req)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}

func recoverAppendRecordSigner(req AppendRecordRequest) (*ecdsa.PublicKey, string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return nil, "", err
	}
	if len(signature) != 65 {
		return nil, "", errors.New("invalid record signature size")
	}
	hash, err := AppendRecordHash(req)
	if err != nil {
		return nil, "", err
	}
	publicKey, err := ethcrypto.SigToPub(hash, signature)
	if err != nil {
		return nil, "", err
	}
	return publicKey, AccountAddress(publicKey), nil
}
