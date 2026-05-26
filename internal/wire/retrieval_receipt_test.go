package wire

import (
	"testing"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestRetrievalClientReceiptRequiresServedAt(t *testing.T) {
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	receipt := RetrievalReceipt{
		ReceiptID:     "receipt_served_at",
		RequestID:     "request_served_at",
		IntentID:      "intent_served_at",
		ShardHash:     "shard_served_at",
		BytesServed:   1,
		ClientAddress: AccountAddress(&key.PublicKey),
		User:          AccountAddress(&key.PublicKey),
	}
	if err := SignRetrievalClientReceipt(&receipt, key); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRetrievalClientReceipt(receipt); err == nil {
		t.Fatal("expected missing served_at_unix to be rejected")
	}
}
