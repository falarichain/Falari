package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// ── Deploy Contract Signing ──

type deployContractSigningPayload struct {
	ChainID      string            `json:"chain_id"`
	Deployer     string            `json:"deployer"`
	Label        string            `json:"label"`
	BytecodeHash string            `json:"bytecode_hash"`
	InitMethod   string            `json:"init_method"`
	InitArgs     string            `json:"init_args"`
	InitFund     uint64            `json:"init_fund"`
	CronJobs     []WasmCronJobSpec `json:"cron_jobs,omitempty"`
	PublicKV     bool              `json:"public_kv,omitempty"`
	Nonce        uint64            `json:"nonce"`
	Fee          uint64            `json:"fee"`
}

// DeployContractHash returns the keccak256 hash of the deploy contract signing payload.
func DeployContractHash(req DeployContractRequest, chainID string, bytecodeHash string) ([]byte, error) {
	payload := deployContractSigningPayload{
		ChainID:      chainID,
		Deployer:     req.Deployer,
		Label:        req.Label,
		BytecodeHash: bytecodeHash,
		InitMethod:   req.InitMethod,
		InitArgs:     req.InitArgs,
		InitFund:     req.InitFund,
		CronJobs:     req.CronJobs,
		PublicKV:     req.PublicKV,
		Nonce:        req.Nonce,
		Fee:          req.Fee,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(raw), nil
}

// VerifyDeployContractSignature verifies the deployer's signature on a deploy request.
func VerifyDeployContractSignature(req DeployContractRequest, chainID string, bytecodeHash string) error {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return err
	}
	hash, err := DeployContractHash(req, chainID, bytecodeHash)
	if err != nil {
		return err
	}
	publicKey, err := recoverSigner(hash, signature)
	if err != nil {
		return err
	}
	address := AccountAddress(publicKey)
	if !stringsEqualFold(address, req.Deployer) {
		return errSignatureMismatch
	}
	return nil
}

// RecoverDeployContractPublicKey recovers the public key from a deploy contract signature.
func RecoverDeployContractPublicKey(req DeployContractRequest, chainID string, bytecodeHash string) (string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return "", err
	}
	hash, err := DeployContractHash(req, chainID, bytecodeHash)
	if err != nil {
		return "", err
	}
	publicKey, err := recoverSigner(hash, signature)
	if err != nil {
		return "", err
	}
	return encodeHex(ethcrypto.FromECDSAPub(publicKey)), nil
}

// SignDeployContract signs a deploy contract request.
func SignDeployContract(req *DeployContractRequest, privateKey *ecdsa.PrivateKey, chainID string, bytecodeHash string) error {
	if req.PublicKey == "" {
		req.PublicKey = encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey))
	}
	if req.Deployer == "" {
		req.Deployer = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := DeployContractHash(*req, chainID, bytecodeHash)
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

// ── Call Contract Signing ──

type callContractSigningPayload struct {
	ChainID         string `json:"chain_id"`
	Caller          string `json:"caller"`
	ContractAddress string `json:"contract_address"`
	Method          string `json:"method"`
	Args            string `json:"args"`
	Fund            uint64 `json:"fund"`
	Nonce           uint64 `json:"nonce"`
	Fee             uint64 `json:"fee"`
	GasLimit        uint64 `json:"gas_limit"`
}

// CallContractHash returns the keccak256 hash of the call contract signing payload.
func CallContractHash(req CallContractRequest) ([]byte, error) {
	payload := callContractSigningPayload{
		ChainID:         req.ChainID,
		Caller:          req.Caller,
		ContractAddress: req.ContractAddress,
		Method:          req.Method,
		Args:            req.Args,
		Fund:            req.Fund,
		Nonce:           req.Nonce,
		Fee:             req.Fee,
		GasLimit:        req.GasLimit,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(raw), nil
}

// VerifyCallContractSignature verifies the caller's signature on a call request.
func VerifyCallContractSignature(req CallContractRequest) error {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return err
	}
	hash, err := CallContractHash(req)
	if err != nil {
		return err
	}
	publicKey, err := recoverSigner(hash, signature)
	if err != nil {
		return err
	}
	address := AccountAddress(publicKey)
	if !stringsEqualFold(address, req.Caller) {
		return errSignatureMismatch
	}
	return nil
}

// RecoverCallContractPublicKey recovers the public key from a call contract signature.
func RecoverCallContractPublicKey(req CallContractRequest) (string, error) {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return "", err
	}
	hash, err := CallContractHash(req)
	if err != nil {
		return "", err
	}
	publicKey, err := recoverSigner(hash, signature)
	if err != nil {
		return "", err
	}
	return encodeHex(ethcrypto.FromECDSAPub(publicKey)), nil
}

// SignCallContract signs a call contract request.
func SignCallContract(req *CallContractRequest, privateKey *ecdsa.PrivateKey) error {
	if req.PublicKey == "" {
		req.PublicKey = encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey))
	}
	if req.Caller == "" {
		req.Caller = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := CallContractHash(*req)
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

// ── Destroy Contract Signing ──

type destroyContractSigningPayload struct {
	ChainID         string `json:"chain_id"`
	Admin           string `json:"admin"`
	ContractAddress string `json:"contract_address"`
	Nonce           uint64 `json:"nonce"`
	Fee             uint64 `json:"fee"`
}

// DestroyContractHash returns the keccak256 hash of the destroy contract signing payload.
func DestroyContractHash(req DestroyContractRequest) ([]byte, error) {
	payload := destroyContractSigningPayload{
		ChainID:         req.ChainID,
		Admin:           req.Admin,
		ContractAddress: req.ContractAddress,
		Nonce:           req.Nonce,
		Fee:             req.Fee,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(raw), nil
}

// VerifyDestroyContractSignature verifies the admin's signature on a destroy request.
func VerifyDestroyContractSignature(req DestroyContractRequest) error {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return err
	}
	hash, err := DestroyContractHash(req)
	if err != nil {
		return err
	}
	publicKey, err := recoverSigner(hash, signature)
	if err != nil {
		return err
	}
	address := AccountAddress(publicKey)
	if !stringsEqualFold(address, req.Admin) {
		return errSignatureMismatch
	}
	return nil
}

// SignDestroyContract signs a destroy contract request.
func SignDestroyContract(req *DestroyContractRequest, privateKey *ecdsa.PrivateKey) error {
	if req.PublicKey == "" {
		req.PublicKey = encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey))
	}
	if req.Admin == "" {
		req.Admin = AccountAddress(&privateKey.PublicKey)
	}
	hash, err := DestroyContractHash(*req)
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

// stringsEqualFold is a helper for case-insensitive string comparison.
func stringsEqualFold(a, b string) bool {
	return strings.EqualFold(NormalizeAddress(a), NormalizeAddress(b))
}

var errSignatureMismatch = errors.New("signature does not match sender address")

// ── Agent Key Verification ──

// VerifyDeployContractAgent verifies that the deploy signature was made by the given agent public key.
func VerifyDeployContractAgent(req DeployContractRequest, chainID string, bytecodeHash string, agentPub string) error {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return err
	}
	hash, err := DeployContractHash(req, chainID, bytecodeHash)
	if err != nil {
		return err
	}
	publicKey, err := recoverSigner(hash, signature)
	if err != nil {
		return err
	}
	recoveredPub := encodeHex(ethcrypto.FromECDSAPub(publicKey))
	if !strings.EqualFold(recoveredPub, agentPub) {
		return errors.New("deploy contract agent signature mismatch")
	}
	return nil
}

// VerifyCallContractAgent verifies that the call signature was made by the given agent public key.
func VerifyCallContractAgent(req CallContractRequest, agentPub string) error {
	signature, err := decodeHex(req.Signature)
	if err != nil {
		return err
	}
	hash, err := CallContractHash(req)
	if err != nil {
		return err
	}
	publicKey, err := recoverSigner(hash, signature)
	if err != nil {
		return err
	}
	recoveredPub := encodeHex(ethcrypto.FromECDSAPub(publicKey))
	if !strings.EqualFold(recoveredPub, agentPub) {
		return errors.New("call contract agent signature mismatch")
	}
	return nil
}
