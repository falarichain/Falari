package wire

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
)

type validatorRegistrationPayload struct {
	Address   string `json:"address"`
	PublicKey string `json:"public_key"`
	Endpoint  string `json:"endpoint,omitempty"`
	Stake     uint64 `json:"stake"`
}

func ValidatorRegistrationPayload(req RegisterValidatorRequest) ([]byte, error) {
	payload := validatorRegistrationPayload{
		Address:   req.Address,
		PublicKey: req.PublicKey,
		Endpoint:  req.Endpoint,
		Stake:     req.Stake,
	}
	return json.Marshal(payload)
}

func SignValidatorRegistration(req *RegisterValidatorRequest, privateKey ed25519.PrivateKey) error {
	payload, err := ValidatorRegistrationPayload(*req)
	if err != nil {
		return err
	}
	req.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}

func VerifyValidatorRegistration(req RegisterValidatorRequest) error {
	publicKey, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid validator public key size")
	}
	signature, err := base64.StdEncoding.DecodeString(req.Signature)
	if err != nil {
		return err
	}
	payload, err := ValidatorRegistrationPayload(req)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("invalid validator registration signature")
	}
	return nil
}

type blockSigningPayload struct {
	Height          uint64   `json:"height"`
	TimeUnix        int64    `json:"time_unix"`
	PrevHash        string   `json:"prev_hash"`
	TxRoot          string   `json:"tx_root"`
	ProducerAddress string   `json:"producer_address"`
	TxLeaves        []string `json:"tx_leaves"`
}

type blockVotePayload struct {
	Height           uint64 `json:"height"`
	BlockHash        string `json:"block_hash"`
	ValidatorAddress string `json:"validator_address"`
	Power            uint64 `json:"power"`
}

type consensusVotePayload struct {
	Height           uint64 `json:"height"`
	Round            uint64 `json:"round"`
	Type             string `json:"type"`
	BlockHash        string `json:"block_hash"`
	ValidatorAddress string `json:"validator_address"`
	Power            uint64 `json:"power"`
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
		TxRoot:          block.TxRoot,
		ProducerAddress: block.ProducerAddress,
		TxLeaves:        txLeaves,
	}
	return json.Marshal(payload)
}

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

func SignBlock(block *Block, privateKey ed25519.PrivateKey) error {
	payload, err := BlockPayload(*block)
	if err != nil {
		return err
	}
	block.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}

func VerifyBlockSignature(block Block) error {
	publicKey, err := base64.StdEncoding.DecodeString(block.ProducerPublicKey)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid validator public key size")
	}
	signature, err := base64.StdEncoding.DecodeString(block.Signature)
	if err != nil {
		return err
	}
	payload, err := BlockPayload(block)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("invalid block signature")
	}
	return nil
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

func SignBlockVote(vote *BlockVote, privateKey ed25519.PrivateKey) error {
	payload, err := BlockVotePayload(*vote)
	if err != nil {
		return err
	}
	vote.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}

func VerifyBlockVote(vote BlockVote) error {
	publicKey, err := base64.StdEncoding.DecodeString(vote.ValidatorPublicKey)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid validator public key size")
	}
	signature, err := base64.StdEncoding.DecodeString(vote.Signature)
	if err != nil {
		return err
	}
	payload, err := BlockVotePayload(vote)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("invalid block vote signature")
	}
	return nil
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

func SignConsensusVote(vote *ConsensusVote, privateKey ed25519.PrivateKey) error {
	payload, err := ConsensusVotePayload(*vote)
	if err != nil {
		return err
	}
	vote.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}

func VerifyConsensusVote(vote ConsensusVote) error {
	publicKey, err := base64.StdEncoding.DecodeString(vote.ValidatorPublicKey)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid validator public key size")
	}
	signature, err := base64.StdEncoding.DecodeString(vote.Signature)
	if err != nil {
		return err
	}
	payload, err := ConsensusVotePayload(vote)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("invalid consensus vote signature")
	}
	return nil
}
