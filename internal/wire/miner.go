package wire

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
)

type minerRegistrationPayload struct {
	MinerAddress  string `json:"miner_address"`
	PublicKey     string `json:"public_key"`
	Endpoint      string `json:"endpoint"`
	CapacityBytes uint64 `json:"capacity_bytes"`
	Stake         uint64 `json:"stake"`
}

func MinerRegistrationPayload(req RegisterMinerRequest) ([]byte, error) {
	payload := minerRegistrationPayload{
		MinerAddress:  req.MinerAddress,
		PublicKey:     req.PublicKey,
		Endpoint:      req.Endpoint,
		CapacityBytes: req.CapacityBytes,
		Stake:         req.Stake,
	}
	return json.Marshal(payload)
}

func SignMinerRegistration(req *RegisterMinerRequest, privateKey ed25519.PrivateKey) error {
	payload, err := MinerRegistrationPayload(*req)
	if err != nil {
		return err
	}
	req.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}

func VerifyMinerRegistration(req RegisterMinerRequest) error {
	publicKey, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid miner public key size")
	}
	signature, err := base64.StdEncoding.DecodeString(req.Signature)
	if err != nil {
		return err
	}
	payload, err := MinerRegistrationPayload(req)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("invalid miner registration signature")
	}
	return nil
}
