package wire

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type validatorRegistrationPayload struct {
	Action            string `json:"action"`
	OwnerAddress      string `json:"owner_address"`
	OperatorAddress   string `json:"operator_address"`
	OperatorPublicKey string `json:"operator_public_key"`
	CommissionRateBPS uint64 `json:"commission_rate_bps,omitempty"`
	ChainID           string `json:"chain_id"`
	Endpoint          string `json:"endpoint,omitempty"`
	Nonce             uint64 `json:"nonce"`
	Stake             uint64 `json:"stake"`
}

func ValidatorRegistrationPayload(req RegisterValidatorRequest) ([]byte, error) {
	payload := validatorRegistrationPayload{
		Action:            "register_validator",
		OwnerAddress:      req.OwnerAddress,
		OperatorAddress:   req.OperatorAddress,
		OperatorPublicKey: req.OperatorPublicKey,
		CommissionRateBPS: req.CommissionRateBPS,
		ChainID:           req.ChainID,
		Endpoint:          req.Endpoint,
		Nonce:             req.Nonce,
		Stake:             req.Stake,
	}
	return json.Marshal(payload)
}

// SignValidatorRegistration signs the registration with the Owner key.
// The operator proof-of-possession must be set separately via SignOperatorProofOfPossession.
func SignValidatorRegistration(req *RegisterValidatorRequest, chainID string, nonce uint64, ownerKey *ecdsa.PrivateKey) error {
	req.ChainID = chainID
	req.Nonce = nonce
	payload := validatorRegistrationPayload{
		Action:            "register_validator",
		OwnerAddress:      req.OwnerAddress,
		OperatorAddress:   req.OperatorAddress,
		OperatorPublicKey: req.OperatorPublicKey,
		CommissionRateBPS: req.CommissionRateBPS,
		ChainID:           req.ChainID,
		Endpoint:          req.Endpoint,
		Nonce:             req.Nonce,
		Stake:             req.Stake,
	}
	sig, _, err := signInfraPayload(payload, ownerKey)
	if err != nil {
		return err
	}
	req.Signature = sig
	return nil
}

// SignOperatorProofOfPossession signs the registration payload with the Operator key
// to prove possession of the operator private key.
func SignOperatorProofOfPossession(req *RegisterValidatorRequest, operatorKey *ecdsa.PrivateKey) error {
	payload := validatorRegistrationPayload{
		Action:            "register_validator",
		OwnerAddress:      req.OwnerAddress,
		OperatorAddress:   req.OperatorAddress,
		OperatorPublicKey: req.OperatorPublicKey,
		CommissionRateBPS: req.CommissionRateBPS,
		ChainID:           req.ChainID,
		Endpoint:          req.Endpoint,
		Nonce:             req.Nonce,
		Stake:             req.Stake,
	}
	sig, pub, err := signInfraPayload(payload, operatorKey)
	if err != nil {
		return err
	}
	req.OperatorSignature = sig
	if req.OperatorPublicKey == "" {
		req.OperatorPublicKey = pub
	}
	return nil
}

func VerifyValidatorRegistration(req RegisterValidatorRequest) error {
	payload := validatorRegistrationPayload{
		Action:            "register_validator",
		OwnerAddress:      req.OwnerAddress,
		OperatorAddress:   req.OperatorAddress,
		OperatorPublicKey: req.OperatorPublicKey,
		CommissionRateBPS: req.CommissionRateBPS,
		ChainID:           req.ChainID,
		Endpoint:          req.Endpoint,
		Nonce:             req.Nonce,
		Stake:             req.Stake,
	}
	if err := verifyInfraSignature(req.OwnerAddress, req.Signature, payload); err != nil {
		return err
	}
	return verifyInfraSignature(req.OperatorAddress, req.OperatorSignature, payload)
}

type rotateOperatorPayload struct {
	Action               string `json:"action"`
	OwnerAddress         string `json:"owner_address"`
	NewOperatorAddress   string `json:"new_operator_address"`
	NewOperatorPublicKey string `json:"new_operator_public_key"`
	ChainID              string `json:"chain_id"`
	Nonce                uint64 `json:"nonce"`
}

func SignRotateOperator(req *RotateOperatorRequest, ownerKey *ecdsa.PrivateKey) error {
	payload := rotateOperatorPayload{
		Action:               "rotate_operator",
		OwnerAddress:         req.OwnerAddress,
		NewOperatorAddress:   req.NewOperatorAddress,
		NewOperatorPublicKey: req.NewOperatorPublicKey,
		ChainID:              req.ChainID,
		Nonce:                req.Nonce,
	}
	sig, _, err := signInfraPayload(payload, ownerKey)
	if err != nil {
		return err
	}
	req.Signature = sig
	return nil
}

func SignRotateOperatorProofOfPossession(req *RotateOperatorRequest, newOperatorKey *ecdsa.PrivateKey) error {
	payload := rotateOperatorPayload{
		Action:               "rotate_operator",
		OwnerAddress:         req.OwnerAddress,
		NewOperatorAddress:   req.NewOperatorAddress,
		NewOperatorPublicKey: req.NewOperatorPublicKey,
		ChainID:              req.ChainID,
		Nonce:                req.Nonce,
	}
	sig, pub, err := signInfraPayload(payload, newOperatorKey)
	if err != nil {
		return err
	}
	req.OperatorSignature = sig
	if req.NewOperatorPublicKey == "" {
		req.NewOperatorPublicKey = pub
	}
	return nil
}

func VerifyRotateOperator(req RotateOperatorRequest) error {
	payload := rotateOperatorPayload{
		Action:               "rotate_operator",
		OwnerAddress:         req.OwnerAddress,
		NewOperatorAddress:   req.NewOperatorAddress,
		NewOperatorPublicKey: req.NewOperatorPublicKey,
		ChainID:              req.ChainID,
		Nonce:                req.Nonce,
	}
	if err := verifyInfraSignature(req.OwnerAddress, req.Signature, payload); err != nil {
		return err
	}
	return verifyInfraSignature(req.NewOperatorAddress, req.OperatorSignature, payload)
}

type deregisterValidatorPayload struct {
	Action           string `json:"action"`
	ChainID          string `json:"chain_id"`
	ValidatorAddress string `json:"validator_address"`
	Nonce            uint64 `json:"nonce"`
}

func SignDeregisterValidator(req *DeregisterValidatorRequest, privateKey *ecdsa.PrivateKey) error {
	payload := deregisterValidatorPayload{
		Action:           "deregister_validator",
		ChainID:          req.ChainID,
		ValidatorAddress: req.ValidatorAddress,
		Nonce:            req.Nonce,
	}
	sig, _, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	req.Signature = sig
	return nil
}

func VerifyDeregisterValidator(req DeregisterValidatorRequest) error {
	payload := deregisterValidatorPayload{
		Action:           "deregister_validator",
		ChainID:          req.ChainID,
		ValidatorAddress: req.ValidatorAddress,
		Nonce:            req.Nonce,
	}
	return verifyInfraSignature(req.ValidatorAddress, req.Signature, payload)
}

type blockSigningPayload struct {
	Height          uint64   `json:"height"`
	TimeUnix        int64    `json:"time_unix"`
	PrevHash        string   `json:"prev_hash"`
	ProducerAddress string   `json:"producer_address"`
	TxLeaves        []string `json:"tx_leaves"`
	TxRoot          string   `json:"tx_root"`
}

type blockVotePayload struct {
	BlockHash        string `json:"block_hash"`
	Height           uint64 `json:"height"`
	Power            uint64 `json:"power"`
	ValidatorAddress string `json:"validator_address"`
}

type consensusVotePayload struct {
	BlockHash        string `json:"block_hash"`
	Height           uint64 `json:"height"`
	Power            uint64 `json:"power"`
	Round            uint64 `json:"round"`
	Type             string `json:"type"`
	ValidatorAddress string `json:"validator_address"`
}

func BlockPayload(block Block) ([]byte, error) {
	txLeaves := make([]string, 0, len(block.Transactions))
	for _, tx := range block.Transactions {
		txLeaves = append(txLeaves, TransactionLeaf(tx))
	}
	payload := blockSigningPayload{
		Height:          block.Height,
		TimeUnix:        block.TimeUnix,
		PrevHash:        block.PrevHash,
		ProducerAddress: block.ProducerAddress,
		TxLeaves:        txLeaves,
		TxRoot:          block.TxRoot,
	}
	return json.Marshal(payload)
}

// TransactionLeaf computes the SHA256 hash of a transaction for Merkle tree leaves.
// This is NOT a signing operation — it is a data hash and intentionally remains SHA256.
func TransactionLeaf(tx Transaction) string {
	raw, _ := json.Marshal(struct {
		TxID           string `json:"tx_id"`
		Type           string `json:"type"`
		From           string `json:"from,omitempty"`
		Nonce          uint64 `json:"nonce,omitempty"`
		NonceProtected bool   `json:"nonce_protected,omitempty"`
		AgentKeyID     string `json:"agent_key_id,omitempty"`
		AgentNonce     uint64 `json:"agent_nonce,omitempty"`
		Fee            uint64 `json:"fee,omitempty"`
		PayloadHash    string `json:"payload_hash"`
		CreatedAtUnix  int64  `json:"created_at_unix"`
		Signature      string `json:"signature,omitempty"`
		PublicKey      string `json:"public_key,omitempty"`
		DeadlineUnix   int64  `json:"deadline_unix,omitempty"`
	}{
		TxID:           tx.TxID,
		Type:           tx.Type,
		From:           NormalizeAddress(tx.From),
		Nonce:          tx.Nonce,
		NonceProtected: tx.NonceProtected,
		AgentKeyID:     tx.AgentKeyID,
		AgentNonce:     tx.AgentNonce,
		Fee:            tx.Fee,
		PayloadHash:    tx.PayloadHash,
		CreatedAtUnix:  tx.CreatedAtUnix,
		Signature:      tx.Signature,
		PublicKey:      tx.PublicKey,
		DeadlineUnix:   tx.DeadlineUnix,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func SignBlock(block *Block, privateKey *ecdsa.PrivateKey) error {
	txLeaves := make([]string, 0, len(block.Transactions))
	for _, tx := range block.Transactions {
		txLeaves = append(txLeaves, TransactionLeaf(tx))
	}
	payload := blockSigningPayload{
		Height:          block.Height,
		TimeUnix:        block.TimeUnix,
		PrevHash:        block.PrevHash,
		ProducerAddress: block.ProducerAddress,
		TxLeaves:        txLeaves,
		TxRoot:          block.TxRoot,
	}
	sig, pub, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	block.Signature = sig
	if block.ProducerPublicKey == "" {
		block.ProducerPublicKey = pub
	}
	return nil
}

func VerifyBlockSignature(block Block) error {
	txLeaves := make([]string, 0, len(block.Transactions))
	for _, tx := range block.Transactions {
		txLeaves = append(txLeaves, TransactionLeaf(tx))
	}
	payload := blockSigningPayload{
		Height:          block.Height,
		TimeUnix:        block.TimeUnix,
		PrevHash:        block.PrevHash,
		ProducerAddress: block.ProducerAddress,
		TxLeaves:        txLeaves,
		TxRoot:          block.TxRoot,
	}
	return verifyInfraSignature(block.ProducerAddress, block.Signature, payload)
}

func BlockVotePayload(vote BlockVote) ([]byte, error) {
	payload := blockVotePayload{
		Height:           vote.Height,
		BlockHash:        vote.BlockHash,
		ValidatorAddress: vote.ValidatorAddress,
		Power:            vote.Power,
	}
	return json.Marshal(payload)
}

func SignBlockVote(vote *BlockVote, privateKey *ecdsa.PrivateKey) error {
	payload := blockVotePayload{
		Height:           vote.Height,
		BlockHash:        vote.BlockHash,
		ValidatorAddress: vote.ValidatorAddress,
		Power:            vote.Power,
	}
	sig, pub, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	vote.Signature = sig
	if vote.ValidatorPublicKey == "" {
		vote.ValidatorPublicKey = pub
	}
	return nil
}

func VerifyBlockVote(vote BlockVote) error {
	payload := blockVotePayload{
		Height:           vote.Height,
		BlockHash:        vote.BlockHash,
		ValidatorAddress: vote.ValidatorAddress,
		Power:            vote.Power,
	}
	return verifyInfraSignature(vote.ValidatorAddress, vote.Signature, payload)
}

func ConsensusVotePayload(vote ConsensusVote) ([]byte, error) {
	payload := consensusVotePayload{
		Height:           vote.Height,
		Round:            vote.Round,
		Type:             vote.Type,
		BlockHash:        vote.BlockHash,
		ValidatorAddress: vote.ValidatorAddress,
		Power:            vote.Power,
	}
	return json.Marshal(payload)
}

func SignConsensusVote(vote *ConsensusVote, privateKey *ecdsa.PrivateKey) error {
	payload := consensusVotePayload{
		Height:           vote.Height,
		Round:            vote.Round,
		Type:             vote.Type,
		BlockHash:        vote.BlockHash,
		ValidatorAddress: vote.ValidatorAddress,
		Power:            vote.Power,
	}
	sig, pub, err := signInfraPayload(payload, privateKey)
	if err != nil {
		return err
	}
	vote.Signature = sig
	if vote.ValidatorPublicKey == "" {
		vote.ValidatorPublicKey = pub
	}
	return nil
}

func VerifyConsensusVote(vote ConsensusVote) error {
	payload := consensusVotePayload{
		Height:           vote.Height,
		Round:            vote.Round,
		Type:             vote.Type,
		BlockHash:        vote.BlockHash,
		ValidatorAddress: vote.ValidatorAddress,
		Power:            vote.Power,
	}
	return verifyInfraSignature(vote.ValidatorAddress, vote.Signature, payload)
}
