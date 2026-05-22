package chain

import (
	"errors"
	"strings"
	"time"

	"chain/internal/wire"
)

type createKeyEnvelopeTxPayload struct {
	Envelope wire.KeyEnvelope `json:"envelope"`
}

type createShareTxPayload struct {
	Share    wire.ShareRecord `json:"share"`
	Envelope wire.KeyEnvelope `json:"envelope"`
}

type revokeShareTxPayload struct {
	ShareID       string `json:"share_id"`
	Owner         string `json:"owner"`
	RevokedAtUnix int64  `json:"revoked_at_unix"`
}

func (s *Store) CreateKeyEnvelope(req wire.CreateKeyEnvelopeRequest) (wire.CreateKeyEnvelopeResponse, error) {
	envelope, err := s.buildKeyEnvelope(req, "")
	if err != nil {
		return wire.CreateKeyEnvelopeResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validateEnvelopeOwnerLocked(envelope.IntentID, envelope.Owner); err != nil {
		return wire.CreateKeyEnvelopeResponse{}, err
	}
	s.data.KeyEnvelopes[envelope.EnvelopeID] = envelope
	s.recordTxLocked("create_key_envelope", envelope.Owner, createKeyEnvelopeTxPayload{Envelope: envelope})
	if err := s.saveLocked(); err != nil {
		return wire.CreateKeyEnvelopeResponse{}, err
	}
	return wire.CreateKeyEnvelopeResponse{Envelope: envelope}, nil
}

func (s *Store) CreateAddressShare(req wire.CreateAddressShareRequest) (wire.CreateShareResponse, error) {
	owner := wire.NormalizeAddress(req.Owner)
	recipient := wire.NormalizeAddress(req.Recipient)
	if recipient == "" {
		return wire.CreateShareResponse{}, errors.New("recipient is required")
	}
	envelopeReq := wire.CreateKeyEnvelopeRequest{
		IntentID:         req.IntentID,
		Owner:            owner,
		Recipient:        recipient,
		RecipientType:    wire.KeyEnvelopeRecipientAddress,
		Algorithm:        req.Algorithm,
		EncryptedDataKey: req.EncryptedDataKey,
		Nonce:            req.Nonce,
		ExpiresAtUnix:    req.ExpiresAtUnix,
	}
	return s.createShare(envelopeReq, wire.ShareModeAddress, recipient)
}

func (s *Store) CreatePasscodeShare(req wire.CreatePasscodeShareRequest) (wire.CreateShareResponse, error) {
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = wire.ShareModePasscode
	}
	if mode != wire.ShareModePasscode && mode != wire.ShareModeLinkFragment {
		return wire.CreateShareResponse{}, errors.New("invalid passcode share mode")
	}
	if req.KDF == nil {
		return wire.CreateShareResponse{}, errors.New("kdf params are required")
	}
	if strings.TrimSpace(req.KDF.Name) == "" || strings.TrimSpace(req.KDF.Salt) == "" {
		return wire.CreateShareResponse{}, errors.New("kdf name and salt are required")
	}
	envelopeReq := wire.CreateKeyEnvelopeRequest{
		IntentID:         req.IntentID,
		Owner:            wire.NormalizeAddress(req.Owner),
		Recipient:        "passcode",
		RecipientType:    wire.KeyEnvelopeRecipientPasscode,
		Algorithm:        req.Algorithm,
		EncryptedDataKey: req.EncryptedDataKey,
		Nonce:            req.Nonce,
		KDF:              req.KDF,
		ExpiresAtUnix:    req.ExpiresAtUnix,
	}
	return s.createShare(envelopeReq, mode, "")
}

func (s *Store) createShare(envelopeReq wire.CreateKeyEnvelopeRequest, mode string, recipient string) (wire.CreateShareResponse, error) {
	shareID, err := randomID("share")
	if err != nil {
		return wire.CreateShareResponse{}, err
	}
	envelope, err := s.buildKeyEnvelope(envelopeReq, shareID)
	if err != nil {
		return wire.CreateShareResponse{}, err
	}
	now := time.Now().Unix()
	share := wire.ShareRecord{
		ShareID:       shareID,
		IntentID:      envelope.IntentID,
		Owner:         envelope.Owner,
		Mode:          mode,
		Recipient:     recipient,
		EnvelopeID:    envelope.EnvelopeID,
		CreatedAtUnix: now,
		ExpiresAtUnix: envelope.ExpiresAtUnix,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validateEnvelopeOwnerLocked(envelope.IntentID, envelope.Owner); err != nil {
		return wire.CreateShareResponse{}, err
	}
	s.data.KeyEnvelopes[envelope.EnvelopeID] = envelope
	s.data.ShareRecords[share.ShareID] = share
	s.recordTxLocked("create_share", envelope.Owner, createShareTxPayload{Share: share, Envelope: envelope})
	if err := s.saveLocked(); err != nil {
		return wire.CreateShareResponse{}, err
	}
	return wire.CreateShareResponse{Share: share, Envelope: envelope}, nil
}

func (s *Store) RevokeShare(req wire.RevokeShareRequest) error {
	owner := wire.NormalizeAddress(req.Owner)
	if req.ShareID == "" {
		return errors.New("share id is required")
	}
	if owner == "" {
		return errors.New("owner is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	share, ok := s.data.ShareRecords[req.ShareID]
	if !ok {
		return errors.New("share not found")
	}
	if share.Owner != owner {
		return errors.New("only the owner can revoke this share")
	}
	if share.Revoked {
		return nil
	}
	share.Revoked = true
	s.data.ShareRecords[share.ShareID] = share
	if envelope, ok := s.data.KeyEnvelopes[share.EnvelopeID]; ok {
		envelope.Revoked = true
		s.data.KeyEnvelopes[envelope.EnvelopeID] = envelope
	}
	now := time.Now().Unix()
	s.recordTxLocked("revoke_share", owner, revokeShareTxPayload{ShareID: req.ShareID, Owner: owner, RevokedAtUnix: now})
	return s.saveLocked()
}

func (s *Store) ListShares(intentID string, owner string, recipient string, shareID string, includeRevoked bool) (wire.ListSharesResponse, error) {
	owner = wire.NormalizeAddress(owner)
	recipient = wire.NormalizeAddress(recipient)
	now := time.Now().Unix()

	s.mu.Lock()
	defer s.mu.Unlock()

	shares := []wire.ShareRecord{}
	envelopes := []wire.KeyEnvelope{}
	for _, share := range s.data.ShareRecords {
		if shareID != "" && share.ShareID != shareID {
			continue
		}
		if intentID != "" && share.IntentID != intentID {
			continue
		}
		if owner != "" && share.Owner != owner {
			continue
		}
		if recipient != "" && share.Recipient != recipient {
			continue
		}
		if !includeRevoked && share.Revoked {
			continue
		}
		if !includeRevoked && share.ExpiresAtUnix > 0 && share.ExpiresAtUnix < now {
			continue
		}
		shares = append(shares, share)
		if envelope, ok := s.data.KeyEnvelopes[share.EnvelopeID]; ok {
			if includeRevoked || (!envelope.Revoked && (envelope.ExpiresAtUnix == 0 || envelope.ExpiresAtUnix >= now)) {
				envelopes = append(envelopes, envelope)
			}
		}
	}
	return wire.ListSharesResponse{Shares: shares, Envelopes: envelopes}, nil
}

func (s *Store) ListKeyEnvelopes(intentID string, recipient string, recipientType string, shareID string, includeRevoked bool) ([]wire.KeyEnvelope, error) {
	recipient = normalizeEnvelopeRecipient(recipient, recipientType)
	now := time.Now().Unix()

	s.mu.Lock()
	defer s.mu.Unlock()

	envelopes := []wire.KeyEnvelope{}
	for _, envelope := range s.data.KeyEnvelopes {
		if intentID != "" && envelope.IntentID != intentID {
			continue
		}
		if recipient != "" && envelope.Recipient != recipient {
			continue
		}
		if recipientType != "" && envelope.RecipientType != recipientType {
			continue
		}
		if shareID != "" && envelope.ShareID != shareID {
			continue
		}
		if !includeRevoked && envelope.Revoked {
			continue
		}
		if !includeRevoked && envelope.ExpiresAtUnix > 0 && envelope.ExpiresAtUnix < now {
			continue
		}
		envelopes = append(envelopes, envelope)
	}
	return envelopes, nil
}

func (s *Store) buildKeyEnvelope(req wire.CreateKeyEnvelopeRequest, shareID string) (wire.KeyEnvelope, error) {
	owner := wire.NormalizeAddress(req.Owner)
	recipientType := strings.TrimSpace(req.RecipientType)
	recipient := normalizeEnvelopeRecipient(req.Recipient, recipientType)
	if req.IntentID == "" {
		return wire.KeyEnvelope{}, errors.New("intent is required")
	}
	if owner == "" {
		return wire.KeyEnvelope{}, errors.New("owner is required")
	}
	if recipient == "" {
		return wire.KeyEnvelope{}, errors.New("recipient is required")
	}
	if recipientType == "" {
		return wire.KeyEnvelope{}, errors.New("recipient type is required")
	}
	if !validEnvelopeRecipientType(recipientType) {
		return wire.KeyEnvelope{}, errors.New("invalid recipient type")
	}
	if strings.TrimSpace(req.Algorithm) == "" {
		return wire.KeyEnvelope{}, errors.New("envelope algorithm is required")
	}
	if strings.TrimSpace(req.EncryptedDataKey) == "" {
		return wire.KeyEnvelope{}, errors.New("encrypted data key is required")
	}
	envelopeID, err := randomID("env")
	if err != nil {
		return wire.KeyEnvelope{}, err
	}
	return wire.KeyEnvelope{
		EnvelopeID:       envelopeID,
		IntentID:         req.IntentID,
		ShareID:          shareID,
		Owner:            owner,
		Recipient:        recipient,
		RecipientType:    recipientType,
		Algorithm:        strings.TrimSpace(req.Algorithm),
		EncryptedDataKey: strings.TrimSpace(req.EncryptedDataKey),
		Nonce:            strings.TrimSpace(req.Nonce),
		KDF:              req.KDF,
		CreatedAtUnix:    time.Now().Unix(),
		ExpiresAtUnix:    req.ExpiresAtUnix,
	}, nil
}

func (s *Store) validateEnvelopeOwnerLocked(intentID string, owner string) error {
	intent, ok := s.data.Intents[intentID]
	if !ok {
		return errors.New("intent not found")
	}
	if intent.User != owner {
		return errors.New("intent owner mismatch")
	}
	if intent.Encryption == nil {
		return errors.New("intent is not encrypted")
	}
	return nil
}

func validEnvelopeRecipientType(value string) bool {
	switch value {
	case wire.KeyEnvelopeRecipientOwner, wire.KeyEnvelopeRecipientAddress, wire.KeyEnvelopeRecipientAgent, wire.KeyEnvelopeRecipientPasscode:
		return true
	default:
		return false
	}
}

func normalizeEnvelopeRecipient(value string, recipientType string) string {
	value = strings.TrimSpace(value)
	switch recipientType {
	case wire.KeyEnvelopeRecipientOwner, wire.KeyEnvelopeRecipientAddress:
		return wire.NormalizeAddress(value)
	default:
		return value
	}
}

func (s *Store) applyCreateKeyEnvelopeLocked(payload createKeyEnvelopeTxPayload) error {
	if payload.Envelope.EnvelopeID == "" {
		return errors.New("envelope id is required")
	}
	s.data.KeyEnvelopes[payload.Envelope.EnvelopeID] = payload.Envelope
	return nil
}

func (s *Store) applyCreateShareLocked(payload createShareTxPayload) error {
	if payload.Share.ShareID == "" || payload.Envelope.EnvelopeID == "" {
		return errors.New("share and envelope ids are required")
	}
	s.data.KeyEnvelopes[payload.Envelope.EnvelopeID] = payload.Envelope
	s.data.ShareRecords[payload.Share.ShareID] = payload.Share
	return nil
}

func (s *Store) applyRevokeShareLocked(payload revokeShareTxPayload) error {
	share, ok := s.data.ShareRecords[payload.ShareID]
	if !ok {
		return nil
	}
	share.Revoked = true
	s.data.ShareRecords[payload.ShareID] = share
	if envelope, ok := s.data.KeyEnvelopes[share.EnvelopeID]; ok {
		envelope.Revoked = true
		s.data.KeyEnvelopes[envelope.EnvelopeID] = envelope
	}
	return nil
}
