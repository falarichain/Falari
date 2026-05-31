package chain

import (
	"encoding/base64"
	"strings"
	"testing"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestGenerateDefaultNFTTemplate(t *testing.T) {
	tpl := GenerateDefaultNFTTemplate()
	if tpl == "" {
		t.Fatal("template should not be empty")
	}
	if !strings.Contains(tpl, "{{MINER_ID}}") {
		t.Error("template must contain {{MINER_ID}} placeholder")
	}
	if !strings.Contains(tpl, "{{MINER_ADDR}}") {
		t.Error("template must contain {{MINER_ADDR}} placeholder")
	}
	if !strings.Contains(tpl, "<svg") {
		t.Error("template must be a valid SVG")
	}
}

func TestGenerateDefaultNFTTemplate_Deterministic(t *testing.T) {
	a := GenerateDefaultNFTTemplate()
	b := GenerateDefaultNFTTemplate()
	if a != b {
		t.Error("template generation must be deterministic")
	}
}

func TestNFTTemplateGeneratedOnRegistration(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	params := store.miningParamsLocked()
	params.MinCapacityBytes = 0
	params.StakePerTiB = 0

	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := wire.AccountAddress(&key.PublicKey)
	store.data.Accounts[addr] = wire.Account{Address: addr, Balance: gfTokens(10)}

	req := wire.RegisterMinerRequest{
		MinerAddress:  addr,
		PublicKey:     wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey)),
		Endpoint:      "http://localhost:9000",
		CapacityBytes: 1024,
		Stake:         gfTokens(1),
	}
	if err := wire.SignMinerRegistration(&req, store.data.ChainID, store.accountLocked(addr).Nonce, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterMiner(req); err != nil {
		t.Fatal(err)
	}

	if store.data.MinerNFTTemplate == "" {
		t.Fatal("NFT template should be set after first miner registration")
	}
	if store.data.MinerNFTContentType != "image/svg+xml" {
		t.Errorf("expected content type image/svg+xml, got %s", store.data.MinerNFTContentType)
	}
	if store.data.MinerNFTTemplateHash == "" {
		t.Error("template hash should not be empty")
	}
}

func TestNFTTemplateNotOverwrittenOnSecondRegistration(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	params := store.miningParamsLocked()
	params.MinCapacityBytes = 0
	params.StakePerTiB = 0

	// Register miner #1
	key1, _ := ethcrypto.GenerateKey()
	addr1 := wire.AccountAddress(&key1.PublicKey)
	store.data.Accounts[addr1] = wire.Account{Address: addr1, Balance: gfTokens(10)}
	req1 := wire.RegisterMinerRequest{
		MinerAddress:  addr1,
		PublicKey:     wire.EncodeHex(ethcrypto.CompressPubkey(&key1.PublicKey)),
		Endpoint:      "http://localhost:9001",
		CapacityBytes: 1024,
		Stake:         gfTokens(1),
	}
	if err := wire.SignMinerRegistration(&req1, store.data.ChainID, store.accountLocked(addr1).Nonce, key1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterMiner(req1); err != nil {
		t.Fatal(err)
	}

	// Upload a custom template as miner #1
	customSVG := `<svg><text>{{MINER_ID}}</text><text>{{MINER_ADDR}}</text></svg>`
	customB64 := base64.StdEncoding.EncodeToString([]byte(customSVG))
	uploadReq := wire.UploadNFTTemplateRequest{
		MinerAddress: addr1,
		ContentType:  "image/svg+xml",
		Content:      customB64,
		ChainID:      store.data.ChainID,
		Nonce:        store.accountLocked(addr1).Nonce,
	}
	if err := wire.SignUploadNFTTemplate(&uploadReq, key1); err != nil {
		t.Fatal(err)
	}
	if err := store.UploadNFTTemplate(uploadReq); err != nil {
		t.Fatal(err)
	}

	// Register miner #2 - should NOT overwrite the custom template
	key2, _ := ethcrypto.GenerateKey()
	addr2 := wire.AccountAddress(&key2.PublicKey)
	store.data.Accounts[addr2] = wire.Account{Address: addr2, Balance: gfTokens(10)}
	req2 := wire.RegisterMinerRequest{
		MinerAddress:  addr2,
		PublicKey:     wire.EncodeHex(ethcrypto.CompressPubkey(&key2.PublicKey)),
		Endpoint:      "http://localhost:9002",
		CapacityBytes: 1024,
		Stake:         gfTokens(1),
	}
	if err := wire.SignMinerRegistration(&req2, store.data.ChainID, store.accountLocked(addr2).Nonce, key2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterMiner(req2); err != nil {
		t.Fatal(err)
	}

	if store.data.MinerNFTTemplate != customB64 {
		t.Error("second miner registration should not overwrite existing custom template")
	}
}

func TestUploadNFTTemplate_MinerOneOnly(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	params := store.miningParamsLocked()
	params.MinCapacityBytes = 0
	params.StakePerTiB = 0

	// Register miner #1
	key1, _ := ethcrypto.GenerateKey()
	addr1 := wire.AccountAddress(&key1.PublicKey)
	store.data.Accounts[addr1] = wire.Account{Address: addr1, Balance: gfTokens(10)}
	req1 := wire.RegisterMinerRequest{
		MinerAddress:  addr1,
		PublicKey:     wire.EncodeHex(ethcrypto.CompressPubkey(&key1.PublicKey)),
		Endpoint:      "http://localhost:9001",
		CapacityBytes: 1024,
		Stake:         gfTokens(1),
	}
	if err := wire.SignMinerRegistration(&req1, store.data.ChainID, store.accountLocked(addr1).Nonce, key1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterMiner(req1); err != nil {
		t.Fatal(err)
	}

	// Register miner #2
	key2, _ := ethcrypto.GenerateKey()
	addr2 := wire.AccountAddress(&key2.PublicKey)
	store.data.Accounts[addr2] = wire.Account{Address: addr2, Balance: gfTokens(10)}
	req2 := wire.RegisterMinerRequest{
		MinerAddress:  addr2,
		PublicKey:     wire.EncodeHex(ethcrypto.CompressPubkey(&key2.PublicKey)),
		Endpoint:      "http://localhost:9002",
		CapacityBytes: 1024,
		Stake:         gfTokens(1),
	}
	if err := wire.SignMinerRegistration(&req2, store.data.ChainID, store.accountLocked(addr2).Nonce, key2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterMiner(req2); err != nil {
		t.Fatal(err)
	}

	// Miner #2 tries to upload - should fail
	customSVG := `<svg><text>{{MINER_ID}}</text><text>{{MINER_ADDR}}</text></svg>`
	customB64 := base64.StdEncoding.EncodeToString([]byte(customSVG))
	uploadReq := wire.UploadNFTTemplateRequest{
		MinerAddress: addr2,
		ContentType:  "image/svg+xml",
		Content:      customB64,
		ChainID:      store.data.ChainID,
		Nonce:        store.accountLocked(addr2).Nonce,
	}
	if err := wire.SignUploadNFTTemplate(&uploadReq, key2); err != nil {
		t.Fatal(err)
	}
	if err := store.UploadNFTTemplate(uploadReq); err == nil {
		t.Fatal("expected error when non-miner-#1 uploads template")
	} else if !strings.Contains(err.Error(), "only miner #1") {
		t.Errorf("expected 'only miner #1' error, got: %v", err)
	}
}

func TestUploadNFTTemplate_InvalidContentType(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	params := store.miningParamsLocked()
	params.MinCapacityBytes = 0
	params.StakePerTiB = 0

	key, _ := ethcrypto.GenerateKey()
	addr := wire.AccountAddress(&key.PublicKey)
	store.data.Accounts[addr] = wire.Account{Address: addr, Balance: gfTokens(10)}
	regReq := wire.RegisterMinerRequest{
		MinerAddress:  addr,
		PublicKey:     wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey)),
		Endpoint:      "http://localhost:9001",
		CapacityBytes: 1024,
		Stake:         gfTokens(1),
	}
	if err := wire.SignMinerRegistration(&regReq, store.data.ChainID, store.accountLocked(addr).Nonce, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterMiner(regReq); err != nil {
		t.Fatal(err)
	}

	uploadReq := wire.UploadNFTTemplateRequest{
		MinerAddress: addr,
		ContentType:  "image/jpeg",
		Content:      base64.StdEncoding.EncodeToString([]byte("fake-jpeg")),
		ChainID:      store.data.ChainID,
		Nonce:        store.accountLocked(addr).Nonce,
	}
	if err := wire.SignUploadNFTTemplate(&uploadReq, key); err != nil {
		t.Fatal(err)
	}
	if err := store.UploadNFTTemplate(uploadReq); err == nil {
		t.Fatal("expected error for invalid content type")
	} else if !strings.Contains(err.Error(), "content type") {
		t.Errorf("expected content type error, got: %v", err)
	}
}

func TestUploadNFTTemplate_SVGMustHavePlaceholders(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	params := store.miningParamsLocked()
	params.MinCapacityBytes = 0
	params.StakePerTiB = 0

	key, _ := ethcrypto.GenerateKey()
	addr := wire.AccountAddress(&key.PublicKey)
	store.data.Accounts[addr] = wire.Account{Address: addr, Balance: gfTokens(10)}
	regReq := wire.RegisterMinerRequest{
		MinerAddress:  addr,
		PublicKey:     wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey)),
		Endpoint:      "http://localhost:9001",
		CapacityBytes: 1024,
		Stake:         gfTokens(1),
	}
	if err := wire.SignMinerRegistration(&regReq, store.data.ChainID, store.accountLocked(addr).Nonce, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterMiner(regReq); err != nil {
		t.Fatal(err)
	}

	// SVG without placeholders
	badSVG := `<svg><text>Hello</text></svg>`
	uploadReq := wire.UploadNFTTemplateRequest{
		MinerAddress: addr,
		ContentType:  "image/svg+xml",
		Content:      base64.StdEncoding.EncodeToString([]byte(badSVG)),
		ChainID:      store.data.ChainID,
		Nonce:        store.accountLocked(addr).Nonce,
	}
	if err := wire.SignUploadNFTTemplate(&uploadReq, key); err != nil {
		t.Fatal(err)
	}
	if err := store.UploadNFTTemplate(uploadReq); err == nil {
		t.Fatal("expected error for SVG without placeholders")
	} else if !strings.Contains(err.Error(), "placeholder") {
		t.Errorf("expected placeholder error, got: %v", err)
	}
}

func TestGetNFTTemplate(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}

	// Initially empty
	tpl, ct := store.GetNFTTemplate()
	if tpl != "" || ct != "" {
		t.Error("template should be empty before any registration")
	}

	// Set manually for testing
	store.mu.Lock()
	store.data.MinerNFTTemplate = "test-template"
	store.data.MinerNFTContentType = "image/svg+xml"
	store.mu.Unlock()

	tpl, ct = store.GetNFTTemplate()
	if tpl != "test-template" {
		t.Errorf("expected 'test-template', got %q", tpl)
	}
	if ct != "image/svg+xml" {
		t.Errorf("expected 'image/svg+xml', got %q", ct)
	}
}

func TestUploadNFTTemplate_SuccessfulPNG(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	params := store.miningParamsLocked()
	params.MinCapacityBytes = 0
	params.StakePerTiB = 0

	key, _ := ethcrypto.GenerateKey()
	addr := wire.AccountAddress(&key.PublicKey)
	store.data.Accounts[addr] = wire.Account{Address: addr, Balance: gfTokens(10)}
	regReq := wire.RegisterMinerRequest{
		MinerAddress:  addr,
		PublicKey:     wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey)),
		Endpoint:      "http://localhost:9001",
		CapacityBytes: 1024,
		Stake:         gfTokens(1),
	}
	if err := wire.SignMinerRegistration(&regReq, store.data.ChainID, store.accountLocked(addr).Nonce, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterMiner(regReq); err != nil {
		t.Fatal(err)
	}

	// PNG doesn't need placeholders
	fakePNG := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a} // PNG header
	uploadReq := wire.UploadNFTTemplateRequest{
		MinerAddress: addr,
		ContentType:  "image/png",
		Content:      base64.StdEncoding.EncodeToString(fakePNG),
		ChainID:      store.data.ChainID,
		Nonce:        store.accountLocked(addr).Nonce,
	}
	if err := wire.SignUploadNFTTemplate(&uploadReq, key); err != nil {
		t.Fatal(err)
	}
	if err := store.UploadNFTTemplate(uploadReq); err != nil {
		t.Fatalf("PNG upload should succeed: %v", err)
	}

	tpl, ct := store.GetNFTTemplate()
	if ct != "image/png" {
		t.Errorf("expected content type image/png, got %s", ct)
	}
	if tpl == "" {
		t.Error("template should not be empty after upload")
	}
}
