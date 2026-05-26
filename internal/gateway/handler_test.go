package gateway

import (
	"net/http"
	"testing"

	"chain/internal/wire"
)

func TestDecodeAPIKeyAcceptsReferenceWithoutPrivateKey(t *testing.T) {
	h := &Handler{}
	req, err := http.NewRequest(http.MethodPost, "/upload", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", wire.EncodeAgentKeyReferenceString("key_1", "0xmaster", "0xagent"))

	ctx, err := h.decodeAPIKey(req)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.AgentKeyID != "key_1" || ctx.Master != "0xmaster" || ctx.Address != "0xagent" || ctx.PrivateKey != "" {
		t.Fatalf("unexpected agent key context: %+v", ctx)
	}
}

func TestDecodeAPIKeyRejectsPrivateKeyWhenDisabled(t *testing.T) {
	h := &Handler{cfg: Config{AllowPrivateKeyAPIKeys: false}}
	req, err := http.NewRequest(http.MethodPost, "/upload", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", wire.EncodeAgentKeyString("key_1", "0xmaster", "0xagent", "priv"))

	if _, err := h.decodeAPIKey(req); err == nil {
		t.Fatal("expected private-key API key to be rejected")
	}
}
