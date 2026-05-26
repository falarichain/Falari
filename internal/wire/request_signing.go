package wire

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// --- Generic helpers ---

func signRequest(hashFunc func() ([]byte, error), priv *ecdsa.PrivateKey) (sig, pub string, err error) {
	h, err := hashFunc()
	if err != nil {
		return "", "", err
	}
	s, err := ethcrypto.Sign(h, priv)
	if err != nil {
		return "", "", err
	}
	return encodeHex(s), encodeHex(ethcrypto.CompressPubkey(&priv.PublicKey)), nil
}

func verifyRequestSig(expectedAddr, sigHex string, hashFunc func() ([]byte, error)) error {
	if sigHex == "" {
		return errors.New("request requires signature")
	}
	sigBytes, err := decodeHex(sigHex)
	if err != nil {
		return errors.New("invalid signature encoding")
	}
	if len(sigBytes) != 65 {
		return errors.New("invalid signature length")
	}
	h, err := hashFunc()
	if err != nil {
		return err
	}
	pub, err := ethcrypto.SigToPub(h, sigBytes)
	if err != nil {
		return errors.New("failed to recover signer")
	}
	addr := AccountAddress(pub)
	if !strings.EqualFold(addr, expectedAddr) {
		return errors.New("signature does not match expected address: recovered=" + addr + " expected=" + expectedAddr + " hash=" + encodeHex(h))
	}
	return nil
}

func verifyAgentRequestSig(expectedAgentPub, sigHex string, hashFunc func() ([]byte, error)) error {
	if sigHex == "" {
		return errors.New("agent request requires signature")
	}
	sigBytes, err := decodeHex(sigHex)
	if err != nil {
		return errors.New("invalid agent signature encoding")
	}
	if len(sigBytes) != 65 {
		return errors.New("invalid agent signature length")
	}
	h, err := hashFunc()
	if err != nil {
		return err
	}
	pub, err := ethcrypto.SigToPub(h, sigBytes)
	if err != nil {
		return errors.New("failed to recover agent signer")
	}
	recoveredUncompressed := encodeHex(ethcrypto.FromECDSAPub(pub))
	recoveredCompressed := encodeHex(ethcrypto.CompressPubkey(pub))
	if !strings.EqualFold(recoveredUncompressed, expectedAgentPub) && !strings.EqualFold(recoveredCompressed, expectedAgentPub) {
		return errors.New("agent signature does not match registered public key")
	}
	return nil
}

// --- CreateIntent ---

type createIntentSigningPayload struct {
	ChainID      string              `json:"chain_id"`
	Action       string              `json:"action"`
	User         string              `json:"user"`
	FileName     string              `json:"file_name"`
	FileSize     int64               `json:"file_size"`
	SegmentSize  int64               `json:"segment_size"`
	FileRoot     string              `json:"file_root"`
	SegmentRoots []string            `json:"segment_roots"`
	Segments     []SegmentPlan       `json:"segments"`
	Erasure      ErasurePolicy       `json:"erasure"`
	Encryption   *EncryptionMetadata `json:"encryption,omitempty"`
	LockedFee    uint64              `json:"locked_fee"`
	DeadlineUnix int64               `json:"deadline_unix"`
	Nonce        uint64              `json:"nonce"`
	AgentKeyID   string              `json:"agent_key_id,omitempty"`
	AgentNonce   uint64              `json:"agent_nonce,omitempty"`
	Policy       StoragePolicy       `json:"policy"`
}

func CreateIntentHash(req CreateIntentRequest) ([]byte, error) {
	p, err := json.Marshal(createIntentSigningPayload{
		ChainID:      req.ChainID,
		Action:       "create_intent",
		User:         NormalizeAddress(req.User),
		FileName:     req.FileName,
		FileSize:     req.FileSize,
		SegmentSize:  req.SegmentSize,
		FileRoot:     req.FileRoot,
		SegmentRoots: req.SegmentRoots,
		Segments:     req.Segments,
		Erasure:      req.Erasure,
		Encryption:   req.Encryption,
		LockedFee:    req.LockedFee,
		DeadlineUnix: req.DeadlineUnix,
		Nonce:        req.Nonce,
		AgentKeyID:   req.AgentKeyID,
		AgentNonce:   req.AgentNonce,
		Policy:       req.Policy,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignCreateIntent(req *CreateIntentRequest, priv *ecdsa.PrivateKey) error {
	if req.User == "" {
		req.User = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return CreateIntentHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyCreateIntent(req CreateIntentRequest) error {
	return verifyRequestSig(req.User, req.Signature, func() ([]byte, error) { return CreateIntentHash(req) })
}

func SignCreateIntentAgent(req *CreateIntentRequest, priv *ecdsa.PrivateKey) error {
	sig, pub, err := signRequest(func() ([]byte, error) { return CreateIntentHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.AgentSignature = sig
	req.AgentPublicKey = pub
	return nil
}

func VerifyCreateIntentAgent(req CreateIntentRequest, agentPub string) error {
	return verifyAgentRequestSig(agentPub, req.AgentSignature, func() ([]byte, error) { return CreateIntentHash(req) })
}

// --- PermanentFundTopUp ---

type permanentFundTopUpSigningPayload struct {
	ChainID  string `json:"chain_id"`
	Action   string `json:"action"`
	IntentID string `json:"intent_id"`
	User     string `json:"user"`
	Amount   uint64 `json:"amount"`
	Nonce    uint64 `json:"nonce"`
}

func PermanentFundTopUpHash(req PermanentFundTopUpRequest) ([]byte, error) {
	p, err := json.Marshal(permanentFundTopUpSigningPayload{
		ChainID:  req.ChainID,
		Action:   "permanent_fund_topup",
		IntentID: req.IntentID,
		User:     NormalizeAddress(req.User),
		Amount:   req.Amount,
		Nonce:    req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignPermanentFundTopUp(req *PermanentFundTopUpRequest, priv *ecdsa.PrivateKey) error {
	if req.User == "" {
		req.User = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return PermanentFundTopUpHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyPermanentFundTopUp(req PermanentFundTopUpRequest) error {
	return verifyRequestSig(req.User, req.Signature, func() ([]byte, error) { return PermanentFundTopUpHash(req) })
}

// --- BatchCommit ---

type batchCommitSigningPayload struct {
	ChainID    string         `json:"chain_id"`
	Action     string         `json:"action"`
	IntentID   string         `json:"intent_id"`
	User       string         `json:"user"`
	Receipts   []MinerReceipt `json:"receipts"`
	Nonce      uint64         `json:"nonce"`
	AgentKeyID string         `json:"agent_key_id,omitempty"`
	AgentNonce uint64         `json:"agent_nonce,omitempty"`
}

func BatchCommitHash(req BatchCommitRequest) ([]byte, error) {
	p, err := json.Marshal(batchCommitSigningPayload{
		ChainID:    req.ChainID,
		Action:     "batch_commit",
		IntentID:   req.IntentID,
		User:       NormalizeAddress(req.User),
		Receipts:   req.Receipts,
		Nonce:      req.Nonce,
		AgentKeyID: req.AgentKeyID,
		AgentNonce: req.AgentNonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignBatchCommit(req *BatchCommitRequest, priv *ecdsa.PrivateKey) error {
	if req.User == "" {
		req.User = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return BatchCommitHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyBatchCommit(req BatchCommitRequest) error {
	return verifyRequestSig(req.User, req.Signature, func() ([]byte, error) { return BatchCommitHash(req) })
}

func SignBatchCommitAgent(req *BatchCommitRequest, priv *ecdsa.PrivateKey) error {
	sig, pub, err := signRequest(func() ([]byte, error) { return BatchCommitHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.AgentSignature = sig
	req.AgentPublicKey = pub
	return nil
}

func VerifyBatchCommitAgent(req BatchCommitRequest, agentPub string) error {
	return verifyAgentRequestSig(agentPub, req.AgentSignature, func() ([]byte, error) { return BatchCommitHash(req) })
}

// --- Finalize ---

type finalizeSigningPayload struct {
	ChainID      string `json:"chain_id"`
	Action       string `json:"action"`
	IntentID     string `json:"intent_id"`
	User         string `json:"user"`
	ManifestRoot string `json:"manifest_root"`
	Nonce        uint64 `json:"nonce"`
	AgentKeyID   string `json:"agent_key_id,omitempty"`
	AgentNonce   uint64 `json:"agent_nonce,omitempty"`
}

func FinalizeHash(req FinalizeRequest) ([]byte, error) {
	p, err := json.Marshal(finalizeSigningPayload{
		ChainID:      req.ChainID,
		Action:       "finalize",
		IntentID:     req.IntentID,
		User:         NormalizeAddress(req.User),
		ManifestRoot: req.ManifestRoot,
		Nonce:        req.Nonce,
		AgentKeyID:   req.AgentKeyID,
		AgentNonce:   req.AgentNonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignFinalize(req *FinalizeRequest, priv *ecdsa.PrivateKey) error {
	if req.User == "" {
		req.User = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return FinalizeHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyFinalize(req FinalizeRequest) error {
	return verifyRequestSig(req.User, req.Signature, func() ([]byte, error) { return FinalizeHash(req) })
}

func SignFinalizeAgent(req *FinalizeRequest, priv *ecdsa.PrivateKey) error {
	sig, pub, err := signRequest(func() ([]byte, error) { return FinalizeHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.AgentSignature = sig
	req.AgentPublicKey = pub
	return nil
}

func VerifyFinalizeAgent(req FinalizeRequest, agentPub string) error {
	return verifyAgentRequestSig(agentPub, req.AgentSignature, func() ([]byte, error) { return FinalizeHash(req) })
}

// --- SettleIntent ---

type settleIntentSigningPayload struct {
	ChainID  string `json:"chain_id"`
	Action   string `json:"action"`
	IntentID string `json:"intent_id"`
	User     string `json:"user"`
	Nonce    uint64 `json:"nonce"`
}

func SettleIntentHash(req SettleIntentRequest) ([]byte, error) {
	p, err := json.Marshal(settleIntentSigningPayload{
		ChainID:  req.ChainID,
		Action:   "settle_intent",
		IntentID: req.IntentID,
		User:     NormalizeAddress(req.User),
		Nonce:    req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignSettleIntent(req *SettleIntentRequest, priv *ecdsa.PrivateKey) error {
	if req.User == "" {
		req.User = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return SettleIntentHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifySettleIntent(req SettleIntentRequest) error {
	return verifyRequestSig(req.User, req.Signature, func() ([]byte, error) { return SettleIntentHash(req) })
}

// --- RenewDeal ---

type renewDealSigningPayload struct {
	ChainID  string `json:"chain_id"`
	Action   string `json:"action"`
	IntentID string `json:"intent_id"`
	User     string `json:"user"`
	Duration int64  `json:"duration"`
	Nonce    uint64 `json:"nonce"`
}

func RenewDealHash(req RenewDealRequest) ([]byte, error) {
	p, err := json.Marshal(renewDealSigningPayload{
		ChainID:  req.ChainID,
		Action:   "renew_deal",
		IntentID: req.IntentID,
		User:     NormalizeAddress(req.User),
		Duration: req.Duration,
		Nonce:    req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignRenewDeal(req *RenewDealRequest, priv *ecdsa.PrivateKey) error {
	if req.User == "" {
		req.User = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return RenewDealHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyRenewDeal(req RenewDealRequest) error {
	return verifyRequestSig(req.User, req.Signature, func() ([]byte, error) { return RenewDealHash(req) })
}

// --- TerminateDeal ---

type terminateDealSigningPayload struct {
	ChainID  string `json:"chain_id"`
	Action   string `json:"action"`
	IntentID string `json:"intent_id"`
	User     string `json:"user"`
	Reason   string `json:"reason,omitempty"`
	Nonce    uint64 `json:"nonce"`
}

func TerminateDealHash(req TerminateDealRequest) ([]byte, error) {
	p, err := json.Marshal(terminateDealSigningPayload{
		ChainID:  req.ChainID,
		Action:   "terminate_deal",
		IntentID: req.IntentID,
		User:     NormalizeAddress(req.User),
		Reason:   req.Reason,
		Nonce:    req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignTerminateDeal(req *TerminateDealRequest, priv *ecdsa.PrivateKey) error {
	if req.User == "" {
		req.User = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return TerminateDealHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyTerminateDeal(req TerminateDealRequest) error {
	return verifyRequestSig(req.User, req.Signature, func() ([]byte, error) { return TerminateDealHash(req) })
}

// --- SetAccessPolicy ---

type setAccessPolicySigningPayload struct {
	ChainID      string `json:"chain_id"`
	Action       string `json:"action"`
	IntentID     string `json:"intent_id"`
	User         string `json:"user"`
	AccessStatus string `json:"access_status"`
	ReasonHash   string `json:"reason_hash,omitempty"`
	Nonce        uint64 `json:"nonce"`
}

func SetAccessPolicyHash(req SetAccessPolicyRequest) ([]byte, error) {
	p, err := json.Marshal(setAccessPolicySigningPayload{
		ChainID:      req.ChainID,
		Action:       "set_access_policy",
		IntentID:     req.IntentID,
		User:         NormalizeAddress(req.User),
		AccessStatus: req.AccessStatus,
		ReasonHash:   req.ReasonHash,
		Nonce:        req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignSetAccessPolicy(req *SetAccessPolicyRequest, priv *ecdsa.PrivateKey) error {
	if req.User == "" {
		req.User = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return SetAccessPolicyHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifySetAccessPolicy(req SetAccessPolicyRequest) error {
	return verifyRequestSig(req.User, req.Signature, func() ([]byte, error) { return SetAccessPolicyHash(req) })
}

// --- DelegateStake ---

type delegateStakeSigningPayload struct {
	ChainID   string `json:"chain_id"`
	Action    string `json:"action"`
	Delegator string `json:"delegator"`
	Validator string `json:"validator"`
	Amount    uint64 `json:"amount"`
	Nonce     uint64 `json:"nonce"`
}

func DelegateStakeHash(req DelegateStakeRequest) ([]byte, error) {
	p, err := json.Marshal(delegateStakeSigningPayload{
		ChainID:   req.ChainID,
		Action:    "delegate_stake",
		Delegator: NormalizeAddress(req.Delegator),
		Validator: req.Validator,
		Amount:    req.Amount,
		Nonce:     req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignDelegateStake(req *DelegateStakeRequest, priv *ecdsa.PrivateKey) error {
	if req.Delegator == "" {
		req.Delegator = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return DelegateStakeHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyDelegateStake(req DelegateStakeRequest) error {
	return verifyRequestSig(req.Delegator, req.Signature, func() ([]byte, error) { return DelegateStakeHash(req) })
}

// --- UndelegateStake ---

type undelegateStakeSigningPayload struct {
	ChainID   string `json:"chain_id"`
	Action    string `json:"action"`
	Delegator string `json:"delegator"`
	Validator string `json:"validator"`
	Amount    uint64 `json:"amount"`
	Nonce     uint64 `json:"nonce"`
}

func UndelegateStakeHash(req UndelegateStakeRequest) ([]byte, error) {
	p, err := json.Marshal(undelegateStakeSigningPayload{
		ChainID:   req.ChainID,
		Action:    "undelegate_stake",
		Delegator: NormalizeAddress(req.Delegator),
		Validator: req.Validator,
		Amount:    req.Amount,
		Nonce:     req.Nonce,
	})
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256(p), nil
}

func SignUndelegateStake(req *UndelegateStakeRequest, priv *ecdsa.PrivateKey) error {
	if req.Delegator == "" {
		req.Delegator = AccountAddress(&priv.PublicKey)
	}
	sig, pub, err := signRequest(func() ([]byte, error) { return UndelegateStakeHash(*req) }, priv)
	if err != nil {
		return err
	}
	req.Signature = sig
	req.PublicKey = pub
	return nil
}

func VerifyUndelegateStake(req UndelegateStakeRequest) error {
	return verifyRequestSig(req.Delegator, req.Signature, func() ([]byte, error) { return UndelegateStakeHash(req) })
}
