package chain

import (
	"errors"
	"sort"
	"strings"
	"time"

	"chain/internal/wire"
)

type createCollectionTxPayload struct {
	Collection wire.DataCollection          `json:"collection"`
	Request    wire.CreateCollectionRequest `json:"request,omitempty"`
	Nonce      uint64                       `json:"nonce,omitempty"`
	PublicKey  string                       `json:"public_key,omitempty"`
}

type appendRecordTxPayload struct {
	Record    wire.DataRecord          `json:"record"`
	Request   wire.AppendRecordRequest `json:"request,omitempty"`
	Nonce     uint64                   `json:"nonce,omitempty"`
	PublicKey string                   `json:"public_key,omitempty"`
}

func (s *Store) CreateCollection(req wire.CreateCollectionRequest) (wire.CreateCollectionResponse, error) {
	req.User = wire.NormalizeAddress(req.User)
	if strings.TrimSpace(req.User) == "" {
		return wire.CreateCollectionResponse{}, errors.New("user is required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return wire.CreateCollectionResponse{}, errors.New("collection name is required")
	}
	collectionID, err := randomID("collection")
	if err != nil {
		return wire.CreateCollectionResponse{}, err
	}
	now := time.Now().Unix()
	collection := wire.DataCollection{
		CollectionID:  collectionID,
		User:          req.User,
		Name:          req.Name,
		Description:   req.Description,
		Metadata:      copyStringMap(req.Metadata),
		CreatedAtUnix: now,
		UpdatedAtUnix: now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.authorizeCreateCollectionLocked(&req); err != nil {
		return wire.CreateCollectionResponse{}, err
	}
	s.data.Collections[collectionID] = collection
	s.recordTxLocked("create_collection", req.User, createCollectionTxPayload{
		Collection: collection,
		Request:    req,
		Nonce:      req.Nonce,
		PublicKey:  req.PublicKey,
	})
	if err := s.saveLocked(); err != nil {
		return wire.CreateCollectionResponse{}, err
	}
	return wire.CreateCollectionResponse{Collection: collection}, nil
}

func (s *Store) AppendRecord(req wire.AppendRecordRequest) (wire.AppendRecordResponse, error) {
	if strings.TrimSpace(req.CollectionID) == "" {
		return wire.AppendRecordResponse{}, errors.New("collection is required")
	}
	if strings.TrimSpace(req.IntentID) == "" {
		return wire.AppendRecordResponse{}, errors.New("intent is required")
	}
	req.User = wire.NormalizeAddress(req.User)

	s.mu.Lock()
	defer s.mu.Unlock()

	collection, ok := s.data.Collections[req.CollectionID]
	if !ok {
		return wire.AppendRecordResponse{}, errors.New("collection not found")
	}
	user := req.User
	if user == "" {
		user = collection.User
	}
	req.User = user
	if user != collection.User {
		return wire.AppendRecordResponse{}, errors.New("collection user mismatch")
	}
	if err := s.authorizeAppendRecordLocked(&req); err != nil {
		return wire.AppendRecordResponse{}, err
	}
	intent, ok := s.data.Intents[req.IntentID]
	if !ok {
		return wire.AppendRecordResponse{}, errors.New("intent not found")
	}
	if intent.User != collection.User {
		return wire.AppendRecordResponse{}, errors.New("intent user mismatch")
	}
	if intent.Status != wire.StatusFinalized {
		return wire.AppendRecordResponse{}, errors.New("intent must be finalized before indexing")
	}
	if req.ParentRecord != "" {
		parent, ok := s.data.DataRecords[req.ParentRecord]
		if !ok {
			return wire.AppendRecordResponse{}, errors.New("parent record not found")
		}
		if parent.CollectionID != req.CollectionID {
			return wire.AppendRecordResponse{}, errors.New("parent record belongs to another collection")
		}
	}
	recordID, err := randomID("record")
	if err != nil {
		return wire.AppendRecordResponse{}, err
	}
	record := wire.DataRecord{
		RecordID:      recordID,
		CollectionID:  req.CollectionID,
		User:          collection.User,
		IntentID:      intent.IntentID,
		DealID:        intent.DealID,
		ParentRecord:  req.ParentRecord,
		Kind:          req.Kind,
		Key:           req.Key,
		FileRoot:      intent.FileRoot,
		ManifestRoot:  req.ManifestRoot,
		Metadata:      copyStringMap(req.Metadata),
		CreatedAtUnix: time.Now().Unix(),
	}
	s.applyDataRecordLocked(record)
	collection.UpdatedAtUnix = record.CreatedAtUnix
	s.data.Collections[collection.CollectionID] = collection
	s.recordTxLocked("append_record", collection.User, appendRecordTxPayload{
		Record:    record,
		Request:   req,
		Nonce:     req.Nonce,
		PublicKey: req.PublicKey,
	})
	if err := s.saveLocked(); err != nil {
		return wire.AppendRecordResponse{}, err
	}
	return wire.AppendRecordResponse{Record: record}, nil
}

func (s *Store) Collection(collectionID string) (wire.CollectionResponse, error) {
	if strings.TrimSpace(collectionID) == "" {
		return wire.CollectionResponse{}, errors.New("collection is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	collection, ok := s.data.Collections[collectionID]
	if !ok {
		return wire.CollectionResponse{}, errors.New("collection not found")
	}
	return wire.CollectionResponse{Collection: collection}, nil
}

func (s *Store) CollectionRecords(collectionID string) (wire.CollectionRecordsResponse, error) {
	return s.CollectionRecordsFiltered(collectionID, wire.CollectionRecordFilter{})
}

func (s *Store) DataRecord(recordID string) (wire.DataRecordResponse, error) {
	if strings.TrimSpace(recordID) == "" {
		return wire.DataRecordResponse{}, errors.New("record is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.data.DataRecords[recordID]
	if !ok {
		return wire.DataRecordResponse{}, errors.New("record not found")
	}
	return wire.DataRecordResponse{Record: record}, nil
}

func (s *Store) CollectionRecordsFiltered(collectionID string, filter wire.CollectionRecordFilter) (wire.CollectionRecordsResponse, error) {
	if strings.TrimSpace(collectionID) == "" {
		return wire.CollectionRecordsResponse{}, errors.New("collection is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	collection, ok := s.data.Collections[collectionID]
	if !ok {
		return wire.CollectionRecordsResponse{}, errors.New("collection not found")
	}
	records := make([]wire.DataRecord, 0, len(s.data.CollectionRecords[collectionID]))
	for _, recordID := range s.data.CollectionRecords[collectionID] {
		record, ok := s.data.DataRecords[recordID]
		if ok && recordMatchesFilter(record, filter) {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAtUnix != records[j].CreatedAtUnix {
			return records[i].CreatedAtUnix < records[j].CreatedAtUnix
		}
		return records[i].RecordID < records[j].RecordID
	})
	if filter.Reverse {
		for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
			records[i], records[j] = records[j], records[i]
		}
	}
	if filter.Limit > 0 && len(records) > filter.Limit {
		records = records[:filter.Limit]
	}
	return wire.CollectionRecordsResponse{Collection: collection, Filter: filter, Records: records}, nil
}

func recordMatchesFilter(record wire.DataRecord, filter wire.CollectionRecordFilter) bool {
	if filter.Kind != "" && record.Kind != filter.Kind {
		return false
	}
	if filter.Key != "" && record.Key != filter.Key {
		return false
	}
	if filter.ParentRecord != "" && record.ParentRecord != filter.ParentRecord {
		return false
	}
	if filter.AfterUnix > 0 && record.CreatedAtUnix <= filter.AfterUnix {
		return false
	}
	if filter.BeforeUnix > 0 && record.CreatedAtUnix >= filter.BeforeUnix {
		return false
	}
	return true
}

func (s *Store) applyDataCollectionLocked(collection wire.DataCollection) {
	if _, exists := s.data.Collections[collection.CollectionID]; exists {
		return
	}
	s.data.Collections[collection.CollectionID] = collection
}

func (s *Store) applyDataCollectionPayloadLocked(payload createCollectionTxPayload) error {
	if payload.Request.Signature == "" {
		return errors.New("replay collection owner signature is required")
	}
	if err := s.authorizeCreateCollectionLocked(&payload.Request); err != nil {
		return err
	}
	if payload.Collection.User != wire.NormalizeAddress(payload.Request.User) {
		return errors.New("replay collection user mismatch")
	}
	if payload.PublicKey != "" {
		if !strings.EqualFold(payload.PublicKey, payload.Request.PublicKey) {
			return errors.New("replay collection public key mismatch")
		}
	}
	if payload.Nonce != payload.Request.Nonce {
		return errors.New("replay collection nonce mismatch")
	}
	s.applyDataCollectionLocked(payload.Collection)
	return nil
}

func (s *Store) applyDataRecordLocked(record wire.DataRecord) {
	if _, exists := s.data.DataRecords[record.RecordID]; exists {
		return
	}
	s.data.DataRecords[record.RecordID] = record
	s.data.CollectionRecords[record.CollectionID] = append(s.data.CollectionRecords[record.CollectionID], record.RecordID)
}

func (s *Store) applyDataRecordPayloadLocked(payload appendRecordTxPayload) error {
	if payload.Request.Signature == "" {
		return errors.New("replay record owner signature is required")
	}
	if err := s.authorizeAppendRecordLocked(&payload.Request); err != nil {
		return err
	}
	if payload.Record.User != wire.NormalizeAddress(payload.Request.User) {
		return errors.New("replay record user mismatch")
	}
	if payload.PublicKey != "" {
		if !strings.EqualFold(payload.PublicKey, payload.Request.PublicKey) {
			return errors.New("replay record public key mismatch")
		}
	}
	if payload.Nonce != payload.Request.Nonce {
		return errors.New("replay record nonce mismatch")
	}
	s.applyDataRecordLocked(payload.Record)
	return nil
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (s *Store) authorizeCreateCollectionLocked(req *wire.CreateCollectionRequest) error {
	if !wire.IsSignedCreateCollection(*req) {
		return errors.New("collection owner signature is required")
	}
	if req.ChainID == "" {
		return errors.New("chain_id is required")
	}
	if req.ChainID != s.data.ChainID {
		return errors.New("request chain_id mismatch")
	}
	recoveredPublicKey, err := wire.RecoverCreateCollectionPublicKey(*req)
	if err != nil {
		return err
	}
	if req.PublicKey != "" && !strings.EqualFold(req.PublicKey, recoveredPublicKey) {
		return errors.New("collection public key does not match signature")
	}
	req.PublicKey = recoveredPublicKey
	if err := wire.VerifyCreateCollectionSignature(*req); err != nil {
		return err
	}
	account := s.accountLocked(req.User)
	if account.PublicKey != "" && !strings.EqualFold(account.PublicKey, req.PublicKey) {
		return errors.New("collection public key mismatch with account")
	}
	if req.Nonce != account.Nonce {
		return errors.New("invalid collection nonce")
	}
	account.Nonce++
	account.PublicKey = req.PublicKey
	s.data.Accounts[account.Address] = account
	return nil
}

func (s *Store) authorizeAppendRecordLocked(req *wire.AppendRecordRequest) error {
	if !wire.IsSignedAppendRecord(*req) {
		return errors.New("record owner signature is required")
	}
	if req.ChainID == "" {
		return errors.New("chain_id is required")
	}
	if req.ChainID != s.data.ChainID {
		return errors.New("request chain_id mismatch")
	}
	recoveredPublicKey, err := wire.RecoverAppendRecordPublicKey(*req)
	if err != nil {
		return err
	}
	if req.PublicKey != "" && !strings.EqualFold(req.PublicKey, recoveredPublicKey) {
		return errors.New("record public key does not match signature")
	}
	req.PublicKey = recoveredPublicKey
	if err := wire.VerifyAppendRecordSignature(*req); err != nil {
		return err
	}
	account := s.accountLocked(req.User)
	if account.PublicKey != "" && !strings.EqualFold(account.PublicKey, req.PublicKey) {
		return errors.New("record public key mismatch with account")
	}
	if req.Nonce != account.Nonce {
		return errors.New("invalid record nonce")
	}
	account.Nonce++
	account.PublicKey = req.PublicKey
	s.data.Accounts[account.Address] = account
	return nil
}
