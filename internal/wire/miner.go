package wire

import (
	"crypto/ecdsa"
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
