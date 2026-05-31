package wire

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type minerRegistrationPayload struct {
	Action        string `json:"action"`
	CapacityBytes uint64 `json:"capacity_bytes"`
	ChainID       string `json:"chain_id"`
	Endpoint      string `json:"endpoint"`
	MinerAddress  string `json:"miner_address"`
	Nonce         uint64 `json:"nonce"`
	PublicKey     string `json:"public_key"`
	Stake         uint64 `json:"stake"`
}

func MinerRegistrationPayload(req RegisterMinerRequest) ([]byte, error) {
	payload := minerRegistrationPayload{
		Action:        "register_miner",
		CapacityBytes: req.CapacityBytes,
		ChainID:       req.ChainID,
		Endpoint:      req.Endpoint,
		MinerAddress:  req.MinerAddress,
		Nonce:         req.Nonce,
		PublicKey:     req.PublicKey,
		Stake:         req.Stake,
	}
	return json.Marshal(payload)
}

func SignMinerRegistration(req *RegisterMinerRequest, chainID string, nonce uint64, privateKey *ecdsa.PrivateKey) error {
	req.ChainID = chainID
	req.Nonce = nonce
	payload := minerRegistrationPayload{
		Action:        "register_miner",
		CapacityBytes: req.CapacityBytes,
		ChainID:       req.ChainID,
		Endpoint:      req.Endpoint,
		MinerAddress:  req.MinerAddress,
		Nonce:         req.Nonce,
		PublicKey:     req.PublicKey,
		Stake:         req.Stake,
	}
	sig, pub, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	req.Signature = sig
	if req.PublicKey == "" {
		req.PublicKey = pub
	}
	return nil
}

func VerifyMinerRegistration(req RegisterMinerRequest) error {
	payload := minerRegistrationPayload{
		Action:        "register_miner",
		CapacityBytes: req.CapacityBytes,
		ChainID:       req.ChainID,
		Endpoint:      req.Endpoint,
		MinerAddress:  req.MinerAddress,
		Nonce:         req.Nonce,
		PublicKey:     req.PublicKey,
		Stake:         req.Stake,
	}
	return verifyInfraSignature(req.MinerAddress, req.Signature, payload)
}

type deregisterMinerPayload struct {
	Action       string `json:"action"`
	ChainID      string `json:"chain_id"`
	MinerAddress string `json:"miner_address"`
	Nonce        uint64 `json:"nonce"`
}

func SignDeregisterMiner(req *DeregisterMinerRequest, privateKey *ecdsa.PrivateKey) error {
	payload := deregisterMinerPayload{
		Action:       "deregister_miner",
		ChainID:      req.ChainID,
		MinerAddress: req.MinerAddress,
		Nonce:        req.Nonce,
	}
	sig, _, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	req.Signature = sig
	return nil
}

func VerifyDeregisterMiner(req DeregisterMinerRequest) error {
	payload := deregisterMinerPayload{
		Action:       "deregister_miner",
		ChainID:      req.ChainID,
		MinerAddress: req.MinerAddress,
		Nonce:        req.Nonce,
	}
	return verifyInfraSignature(req.MinerAddress, req.Signature, payload)
}

type claimMiningRewardsPayload struct {
	Action       string `json:"action"`
	ChainID      string `json:"chain_id"`
	MinerAddress string `json:"miner_address"`
	Nonce        uint64 `json:"nonce"`
}

func SignClaimMiningRewards(req *ClaimMiningRewardsRequest, privateKey *ecdsa.PrivateKey) error {
	payload := claimMiningRewardsPayload{
		Action:       "claim_mining_rewards",
		ChainID:      req.ChainID,
		MinerAddress: req.MinerAddress,
		Nonce:        req.Nonce,
	}
	sig, _, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	req.Signature = sig
	return nil
}

func VerifyClaimMiningRewards(req ClaimMiningRewardsRequest) error {
	payload := claimMiningRewardsPayload{
		Action:       "claim_mining_rewards",
		ChainID:      req.ChainID,
		MinerAddress: req.MinerAddress,
		Nonce:        req.Nonce,
	}
	return verifyInfraSignature(req.MinerAddress, req.Signature, payload)
}

type adjustCapacityPayload struct {
	Action           string `json:"action"`
	ChainID          string `json:"chain_id"`
	MinerAddress     string `json:"miner_address"`
	NewCapacityBytes uint64 `json:"new_capacity_bytes"`
	Nonce            uint64 `json:"nonce"`
}

func SignAdjustCapacity(req *AdjustCapacityRequest, privateKey *ecdsa.PrivateKey) error {
	payload := adjustCapacityPayload{
		Action:           "adjust_capacity",
		ChainID:          req.ChainID,
		MinerAddress:     req.MinerAddress,
		NewCapacityBytes: req.NewCapacityBytes,
		Nonce:            req.Nonce,
	}
	sig, _, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	req.Signature = sig
	return nil
}

func VerifyAdjustCapacity(req AdjustCapacityRequest) error {
	payload := adjustCapacityPayload{
		Action:           "adjust_capacity",
		ChainID:          req.ChainID,
		MinerAddress:     req.MinerAddress,
		NewCapacityBytes: req.NewCapacityBytes,
		Nonce:            req.Nonce,
	}
	return verifyInfraSignature(req.MinerAddress, req.Signature, payload)
}

type uploadNFTTemplatePayload struct {
	Action       string `json:"action"`
	ChainID      string `json:"chain_id"`
	MinerAddress string `json:"miner_address"`
	ContentType  string `json:"content_type"`
	ContentHash  string `json:"content_hash"`
	Nonce        uint64 `json:"nonce"`
}

func SignUploadNFTTemplate(req *UploadNFTTemplateRequest, privateKey *ecdsa.PrivateKey) error {
	contentHash := contentHashForSigning(req.Content)
	payload := uploadNFTTemplatePayload{
		Action:       "upload_nft_template",
		ChainID:      req.ChainID,
		MinerAddress: req.MinerAddress,
		ContentType:  req.ContentType,
		ContentHash:  contentHash,
		Nonce:        req.Nonce,
	}
	sig, _, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	req.Signature = sig
	return nil
}

func VerifyUploadNFTTemplate(req UploadNFTTemplateRequest) error {
	contentHash := contentHashForSigning(req.Content)
	payload := uploadNFTTemplatePayload{
		Action:       "upload_nft_template",
		ChainID:      req.ChainID,
		MinerAddress: req.MinerAddress,
		ContentType:  req.ContentType,
		ContentHash:  contentHash,
		Nonce:        req.Nonce,
	}
	return verifyInfraSignature(req.MinerAddress, req.Signature, payload)
}

// contentHashForSigning computes a SHA-256 hash of the base64 content for signing,
// avoiding the need to sign potentially large binary data directly.
func contentHashForSigning(base64Content string) string {
	h := sha256.Sum256([]byte(base64Content))
	return hex.EncodeToString(h[:])
}
