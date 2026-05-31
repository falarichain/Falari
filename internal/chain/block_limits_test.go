package chain

import (
	"encoding/json"
	"testing"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

func TestBlockLimitsUseMiningParams(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.data.MiningParams = &MiningParams{
		TargetBlockBytes:  300_000,
		MaxBlockBytes:     900_000,
		MaxBlockTxs:       77,
		MaxTxBytes:        8_000,
		MaxStorageTxBytes: 80_000,
	}
	limits := store.blockLimitsLocked()
	if limits.targetBlockBytes != 300_000 || limits.maxBlockBytes != 900_000 ||
		limits.maxBlockTxs != 77 || limits.maxTxBytes != 8_000 || limits.maxStorageTxBytes != 80_000 {
		t.Fatalf("unexpected block limits: %+v", limits)
	}
}

func TestStorageTransactionMetadataMustMatchPayload(t *testing.T) {
	req := wire.CreateIntentRequest{
		User:      "0x1111111111111111111111111111111111111111",
		Nonce:     9,
		LockedFee: 123,
	}
	payload := createIntentTxPayload{IntentID: "intent_metadata", Request: req}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.Transaction{
		Type:           "create_intent",
		From:           req.User,
		Nonce:          req.Nonce,
		NonceProtected: true,
		Fee:            100,
		Payload:        raw,
		PayloadHash:    chaincrypto.HashBytes(raw),
		CreatedAtUnix:  1,
	}
	tx.TxID = chaincrypto.HashBytes([]byte(tx.Type + ":" + tx.PayloadHash))
	if err := validateTransactionShape(tx); err != nil {
		t.Fatal(err)
	}
	// Tamper with nonce — must be rejected since it no longer matches the payload.
	tx.Nonce = 0
	if err := validateTransactionShape(tx); err == nil {
		t.Fatal("expected mismatched storage transaction metadata to be rejected")
	}
}
