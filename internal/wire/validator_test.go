package wire

import (
	"strings"
	"testing"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestParseOperatorPublicKeyAcceptsCanonicalEncoding(t *testing.T) {
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	raw := EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey))
	pub, err := ParseOperatorPublicKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := AccountAddress(pub); got != AccountAddress(&key.PublicKey) {
		t.Fatalf("parsed key derives %s, expected %s", got, AccountAddress(&key.PublicKey))
	}
}

func TestParseOperatorPublicKeyRejectsNonCanonicalForms(t *testing.T) {
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	canonical := EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey))

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"ed25519 base64", "R1EoUTkTzJI0TljJH8Wb2eiljTUH1t2wSQBdgJDZp5U="},
		{"uppercase hex", "0X" + strings.ToUpper(canonical[2:])},
		{"missing prefix", canonical[2:]},
		{"wrong length", canonical[:66]},
		{"uncompressed point", EncodeHex(ethcrypto.FromECDSAPub(&key.PublicKey))},
		{"not a curve point", "0x" + strings.Repeat("00", 33)},
	} {
		if _, err := ParseOperatorPublicKey(tc.raw); err == nil {
			t.Fatalf("expected the %s form to be rejected", tc.name)
		}
	}
}
