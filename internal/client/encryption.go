package client

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"

	"chain/internal/wire"
)

const EncryptionAlgorithmAESGCM = "AES-256-GCM/segment-v1"

func GenerateEncryptionKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

func GenerateEncryptionNonce() ([]byte, error) {
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return nonce, nil
}

func EncryptionMetadata(key []byte, nonce []byte, plaintextSize int64, plaintextSegmentSize int64) (*wire.EncryptionMetadata, error) {
	if len(key) != 32 {
		return nil, errors.New("encryption key must be 32 bytes")
	}
	if len(nonce) == 0 {
		return nil, errors.New("encryption nonce is required")
	}
	if plaintextSize < 0 || plaintextSegmentSize <= 0 {
		return nil, errors.New("invalid plaintext size")
	}
	sum := sha256.Sum256(key)
	return &wire.EncryptionMetadata{
		Algorithm:            EncryptionAlgorithmAESGCM,
		KeyHash:              hex.EncodeToString(sum[:]),
		NonceBase64:          base64.StdEncoding.EncodeToString(nonce),
		PlaintextSize:        plaintextSize,
		PlaintextSegmentSize: plaintextSegmentSize,
	}, nil
}

func ParseEncryptionKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, errors.New("encryption key is required")
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	decoded, err := hex.DecodeString(trimHexPrefix(raw))
	if err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, errors.New("encryption key must be base64 or hex encoded 32 bytes")
}

func FormatEncryptionKey(key []byte) string {
	return base64.StdEncoding.EncodeToString(key)
}

func ValidateEncryptionKey(meta *wire.EncryptionMetadata, key []byte) error {
	if meta == nil {
		return nil
	}
	if len(key) != 32 {
		return errors.New("encryption key must be 32 bytes")
	}
	if meta.Algorithm != EncryptionAlgorithmAESGCM {
		return errors.New("unsupported encryption algorithm")
	}
	sum := sha256.Sum256(key)
	if meta.KeyHash != "" && meta.KeyHash != hex.EncodeToString(sum[:]) {
		return errors.New("encryption key does not match manifest key hash")
	}
	return nil
}

func EncryptSegment(plaintext []byte, key []byte, meta wire.EncryptionMetadata, segmentID int) ([]byte, error) {
	aead, err := segmentAEAD(key, meta)
	if err != nil {
		return nil, err
	}
	nonce, err := segmentNonce(key, meta, segmentID)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonce, plaintext, segmentAAD(meta, segmentID)), nil
}

func DecryptSegment(ciphertext []byte, key []byte, meta wire.EncryptionMetadata, segmentID int) ([]byte, error) {
	aead, err := segmentAEAD(key, meta)
	if err != nil {
		return nil, err
	}
	nonce, err := segmentNonce(key, meta, segmentID)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, segmentAAD(meta, segmentID))
}

func EncryptedSegmentSize(meta wire.EncryptionMetadata, segmentID int) int {
	plain := meta.PlaintextSegmentSize
	offset := int64(segmentID) * meta.PlaintextSegmentSize
	if remaining := meta.PlaintextSize - offset; remaining < plain {
		plain = remaining
	}
	if plain < 0 {
		plain = 0
	}
	return int(plain + 16)
}

func segmentAEAD(key []byte, meta wire.EncryptionMetadata) (cipher.AEAD, error) {
	if err := ValidateEncryptionKey(&meta, key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func segmentNonce(key []byte, meta wire.EncryptionMetadata, segmentID int) ([]byte, error) {
	base, err := base64.StdEncoding.DecodeString(meta.NonceBase64)
	if err != nil {
		return nil, err
	}
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], uint64(segmentID))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(meta.Algorithm))
	mac.Write(base)
	mac.Write(id[:])
	sum := mac.Sum(nil)
	return sum[:12], nil
}

func segmentAAD(meta wire.EncryptionMetadata, segmentID int) []byte {
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], uint64(segmentID))
	return append([]byte(meta.Algorithm+":"+meta.KeyHash+":"), id[:]...)
}

func trimHexPrefix(raw string) string {
	if len(raw) >= 2 && raw[0] == '0' && (raw[1] == 'x' || raw[1] == 'X') {
		return raw[2:]
	}
	return raw
}
