package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// txSigningPayload is the canonical payload signed for transaction authentication.
type txSigningPayload struct {
	ChainID        string `json:"chain_id"`
	Type           string `json:"type"`
	From           string `json:"from"`
	PayloadHash    string `json:"payload_hash"`
	Nonce          uint64 `json:"nonce"`
	NonceProtected bool   `json:"nonce_protected"`
	Fee            uint64 `json:"fee"`
	Deadline       int64  `json:"deadline_unix"`
	AgentKeyID     string `json:"agent_key_id,omitempty"`
	AgentNonce     uint64 `json:"agent_nonce,omitempty"`
}

// TransactionSigningHash computes the Keccak256 hash of the transaction signing payload.
func TransactionSigningHash(tx Transaction, chainID string) ([]byte, error) {
	payload, err := json.Marshal(txSigningPayload{
		ChainID:        chainID,
		Type:           tx.Type,
		From:           NormalizeAddress(tx.From),
		PayloadHash:    tx.PayloadHash,
		Nonce:          tx.Nonce,
		NonceProtected: tx.NonceProtected,
		Fee:            tx.Fee,
		Deadline:       tx.DeadlineUnix,
		AgentKeyID:     tx.AgentKeyID,
		AgentNonce:     tx.AgentNonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(payload), nil
}

// SignTransaction signs a transaction with the given private key and chain ID.
// It fills in From and PublicKey if they are empty.
func SignTransaction(tx *Transaction, privateKey *ecdsa.PrivateKey, chainID string) error {
	if tx.From == "" {
		tx.From = AccountAddress(&privateKey.PublicKey)
	}
	if tx.PublicKey == "" {
		tx.PublicKey = encodeHex(ethcrypto.CompressPubkey(&privateKey.PublicKey))
	}
	hash, err := TransactionSigningHash(*tx, chainID)
	if err != nil {
		return err
	}
	signature, err := ethcrypto.Sign(hash, privateKey)
	if err != nil {
		return err
	}
	tx.Signature = encodeHex(signature)
	return nil
}

// VerifyTransactionSignature verifies the transaction envelope signature.
func VerifyTransactionSignature(tx Transaction, chainID string) error {
	if tx.Signature == "" {
		return errors.New("transaction requires signature")
	}
	_, address, err := recoverTransactionSigner(tx, chainID)
	if err != nil {
		return err
	}
	if address != NormalizeAddress(tx.From) {
		return errors.New("transaction signature does not match from address")
	}
	return nil
}

// RecoverTransactionSigner recovers the public key and address from a transaction signature.
func RecoverTransactionSigner(tx Transaction, chainID string) (*ecdsa.PublicKey, string, error) {
	return recoverTransactionSigner(tx, chainID)
}

func recoverTransactionSigner(tx Transaction, chainID string) (*ecdsa.PublicKey, string, error) {
	hash, err := TransactionSigningHash(tx, chainID)
	if err != nil {
		return nil, "", err
	}
	sigBytes, err := decodeHex(tx.Signature)
	if err != nil {
		return nil, "", errors.New("invalid signature encoding")
	}
	pubKey, err := recoverSigner(hash, sigBytes)
	if err != nil {
		return nil, "", errors.New("failed to recover signer from signature: " + err.Error())
	}
	address := AccountAddress(pubKey)
	return pubKey, address, nil
}

// IsSignedTransaction returns true if the transaction has a signature.
func IsSignedTransaction(tx Transaction) bool {
	return tx.Signature != ""
}
