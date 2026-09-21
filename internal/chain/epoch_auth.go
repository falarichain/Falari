package chain

import (
	"errors"
	"time"

	"chain/internal/wire"
)

// Epoch transitions pay rewards, slash miners and rotate validators. They reach every
// node as a system transaction, so the producing node and the replaying nodes must
// apply the same authorization: an enabled governance operator with admin permission
// that signed this exact request for this chain at its next nonce.
const (
	epochActionStart    = "start_epoch"
	epochActionFinalize = "finalize_epoch"
)

type epochAuthFields struct {
	operatorAddress string
	chainID         string
	nonce           uint64
	createdAtUnix   int64
}

func startEpochAuthFields(req wire.StartEpochRequest) epochAuthFields {
	return epochAuthFields{
		operatorAddress: req.OperatorAddress,
		chainID:         req.ChainID,
		nonce:           req.Nonce,
		createdAtUnix:   req.CreatedAtUnix,
	}
}

func finalizeEpochAuthFields(req wire.FinalizeEpochRequest) epochAuthFields {
	return epochAuthFields{
		operatorAddress: req.OperatorAddress,
		chainID:         req.ChainID,
		nonce:           req.Nonce,
		createdAtUnix:   req.CreatedAtUnix,
	}
}

// validateEpochOperatorLocked checks the operator binding shared by both epoch actions
// and returns the normalized operator address. It never consumes the nonce: the
// producer consumes it at intake, replaying nodes consume it when applying the
// transaction. The request signature is verified by the caller because the canonical
// payload differs per action.
func (s *Store) validateEpochOperatorLocked(action string, auth epochAuthFields) (string, error) {
	operatorAddress := normalizeGovernanceOperator(auth.operatorAddress)
	if operatorAddress == "" {
		return "", errors.New(action + ": operator address is required")
	}
	if auth.chainID != s.data.ChainID {
		return "", errors.New(action + ": chain_id mismatch")
	}
	if auth.createdAtUnix == 0 {
		return "", errors.New(action + ": missing signed timestamp")
	}
	operator, ok := s.data.GovernanceOperators[operatorAddress]
	if !ok || !operator.Enabled {
		return "", errors.New(action + ": governance operator is not authorized")
	}
	if !hasAdminPermission(operator.Permissions) {
		return "", errors.New(action + ": governance operator lacks admin permission")
	}
	if auth.nonce != s.data.OperatorNonces[operatorAddress] {
		return "", errors.New(action + ": operator nonce mismatch")
	}
	return operatorAddress, nil
}

// setOperatorNonceLocked consumes an operator nonce. The map is tagged omitempty, so the
// JSON round trip a failed block's rollback snapshot restores through hands back a nil one,
// and a bare write to that panics the node.
func (s *Store) setOperatorNonceLocked(address string, nonce uint64) {
	if s.data.OperatorNonces == nil {
		s.data.OperatorNonces = map[string]uint64{}
	}
	s.data.OperatorNonces[address] = nonce
}

// EpochDriverStatus applies the start-epoch authorization rule to the node's own operator
// identity and returns the reason it would be refused. The epoch scheduler runs on every
// node, so a genesis that does not register this node's operator shows up as a repeating
// error instead of a decision; calling this at boot puts the decision in the startup log.
func (s *Store) EpochDriverStatus() (string, error) {
	address := s.operatorIdentityAddress()
	if address == "" {
		return "", errors.New("no operator identity loaded")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.validateEpochOperatorLocked(epochActionStart, epochAuthFields{
		operatorAddress: address,
		chainID:         s.data.ChainID,
		nonce:           s.data.OperatorNonces[normalizeGovernanceOperator(address)],
		createdAtUnix:   time.Now().Unix(),
	})
	return address, err
}

// authorizeStartEpochLocked accepts an externally signed request or signs it with the
// node's own operator identity, which is how the epoch scheduler starts a round.
func (s *Store) authorizeStartEpochLocked(req *wire.StartEpochRequest) (string, error) {
	if req.OperatorAddress == "" && s.operatorIdentity != nil {
		identity := s.operatorIdentity
		req.OperatorAddress = identity.OperatorAddress
		req.ChainID = s.data.ChainID
		req.Nonce = s.data.OperatorNonces[normalizeGovernanceOperator(identity.OperatorAddress)]
		req.CreatedAtUnix = time.Now().Unix()
		if err := wire.SignStartEpochRequest(req, identity.OperatorPrivateKey); err != nil {
			return "", errors.New(epochActionStart + ": " + err.Error())
		}
	}
	operatorAddress, err := s.validateEpochOperatorLocked(epochActionStart, startEpochAuthFields(*req))
	if err != nil {
		return "", err
	}
	if err := wire.VerifyStartEpochRequest(*req, operatorAddress); err != nil {
		return "", errors.New(epochActionStart + ": " + err.Error())
	}
	if abs64(req.CreatedAtUnix-time.Now().Unix()) > governanceClockSkewSeconds {
		return "", errors.New(epochActionStart + ": signed timestamp outside acceptable clock skew")
	}
	return operatorAddress, nil
}

// authorizeFinalizeEpochLocked is the finalize counterpart of authorizeStartEpochLocked.
func (s *Store) authorizeFinalizeEpochLocked(req *wire.FinalizeEpochRequest) (string, error) {
	if req.OperatorAddress == "" && s.operatorIdentity != nil {
		identity := s.operatorIdentity
		req.OperatorAddress = identity.OperatorAddress
		req.ChainID = s.data.ChainID
		req.Nonce = s.data.OperatorNonces[normalizeGovernanceOperator(identity.OperatorAddress)]
		req.CreatedAtUnix = time.Now().Unix()
		if err := wire.SignFinalizeEpochRequest(req, identity.OperatorPrivateKey); err != nil {
			return "", errors.New(epochActionFinalize + ": " + err.Error())
		}
	}
	operatorAddress, err := s.validateEpochOperatorLocked(epochActionFinalize, finalizeEpochAuthFields(*req))
	if err != nil {
		return "", err
	}
	if err := wire.VerifyFinalizeEpochRequest(*req, operatorAddress); err != nil {
		return "", errors.New(epochActionFinalize + ": " + err.Error())
	}
	if abs64(req.CreatedAtUnix-time.Now().Unix()) > governanceClockSkewSeconds {
		return "", errors.New(epochActionFinalize + ": signed timestamp outside acceptable clock skew")
	}
	return operatorAddress, nil
}
