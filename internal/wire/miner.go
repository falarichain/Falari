package wire

import (
	"crypto/ecdsa"
	"encoding/json"
)

type minerRegistrationPayload struct {
	CapacityBytes uint64 `json:"capacity_bytes"`
	Endpoint      string `json:"endpoint"`
	MinerAddress  string `json:"miner_address"`
	PublicKey     string `json:"public_key"`
	Stake         uint64 `json:"stake"`
}

func MinerRegistrationPayload(req RegisterMinerRequest) ([]byte, error) {
	payload := minerRegistrationPayload{
		CapacityBytes: req.CapacityBytes,
		Endpoint:      req.Endpoint,
		MinerAddress:  req.MinerAddress,
		PublicKey:     req.PublicKey,
		Stake:         req.Stake,
	}
	return json.Marshal(payload)
}

func SignMinerRegistration(req *RegisterMinerRequest, privateKey *ecdsa.PrivateKey) error {
	payload := minerRegistrationPayload{
		CapacityBytes: req.CapacityBytes,
		Endpoint:      req.Endpoint,
		MinerAddress:  req.MinerAddress,
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
		CapacityBytes: req.CapacityBytes,
		Endpoint:      req.Endpoint,
		MinerAddress:  req.MinerAddress,
		PublicKey:     req.PublicKey,
		Stake:         req.Stake,
	}
	return verifyInfraSignature(req.MinerAddress, req.Signature, payload)
}
