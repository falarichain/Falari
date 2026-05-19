package chain

import (
	"errors"
	"sort"

	"chain/internal/wire"
)

func (s *Store) Manifest(intentID string) (wire.StorageManifestResponse, error) {
	if intentID == "" {
		return wire.StorageManifestResponse{}, errors.New("intent is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.data.Intents[intentID]
	if !ok {
		return wire.StorageManifestResponse{}, errors.New("intent not found")
	}
	if !intentAllowsProviderDiscovery(intent) {
		return wire.StorageManifestResponse{}, errors.New("intent access is not available")
	}
	return manifestResponseForIntent(intent), nil
}

func (s *Store) RecordManifest(recordID string) (wire.DataRecordManifestResponse, error) {
	if recordID == "" {
		return wire.DataRecordManifestResponse{}, errors.New("record is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.data.DataRecords[recordID]
	if !ok {
		return wire.DataRecordManifestResponse{}, errors.New("record not found")
	}
	intent, ok := s.data.Intents[record.IntentID]
	if !ok {
		return wire.DataRecordManifestResponse{}, errors.New("record intent not found")
	}
	if !intentAllowsProviderDiscovery(intent) {
		return wire.DataRecordManifestResponse{}, errors.New("record intent access is not available")
	}
	return wire.DataRecordManifestResponse{
		Record:   record,
		Manifest: manifestResponseForIntent(intent),
	}, nil
}

func manifestResponseForIntent(intent *Intent) wire.StorageManifestResponse {
	receipts := sortedIntentReceipts(intent)
	committedShards := make([]wire.ShardRef, 0, len(receipts))
	for _, receipt := range receipts {
		committedShards = append(committedShards, wire.ShardRef{
			SegmentID:  receipt.SegmentID,
			ShardIndex: receipt.ShardIndex,
		})
	}
	committedSegments := committedSegmentIDs(intent)
	plan := wire.UploadPlan{
		IntentID:          intent.IntentID,
		User:              intent.User,
		FileName:          intent.FileName,
		FileSize:          intent.FileSize,
		SegmentSize:       intent.SegmentSize,
		FileRoot:          intent.FileRoot,
		SegmentRoots:      append([]string(nil), intent.SegmentRoots...),
		Segments:          append([]wire.SegmentPlan(nil), intent.Segments...),
		Assignments:       append([]wire.StorageAssignment(nil), intent.Assignments...),
		Erasure:           intent.Erasure,
		Encryption:        intent.Encryption,
		Policy:            intent.Policy,
		LockedFee:         intent.LockedFee,
		Receipts:          receipts,
		CommittedSegments: committedSegments,
		CommittedShards:   committedShards,
	}
	return wire.StorageManifestResponse{
		IntentID:     intent.IntentID,
		Status:       intent.Status,
		DealID:       intent.DealID,
		Complete:     len(committedSegments) == len(intent.SegmentRoots),
		ReceiptCount: len(receipts),
		Plan:         plan,
	}
}

func sortedIntentReceipts(intent *Intent) []wire.MinerReceipt {
	receipts := make([]wire.MinerReceipt, 0)
	for _, byShard := range intent.Receipts {
		for _, receipt := range byShard {
			receipts = append(receipts, receipt)
		}
	}
	sort.Slice(receipts, func(i, j int) bool {
		if receipts[i].SegmentID != receipts[j].SegmentID {
			return receipts[i].SegmentID < receipts[j].SegmentID
		}
		if receipts[i].ShardIndex != receipts[j].ShardIndex {
			return receipts[i].ShardIndex < receipts[j].ShardIndex
		}
		return receipts[i].MinerAddress < receipts[j].MinerAddress
	})
	return receipts
}

func committedSegmentIDs(intent *Intent) []int {
	segments := make([]int, 0, len(intent.Receipts))
	for segmentID, receipts := range intent.Receipts {
		if len(receipts) >= intent.Erasure.DataShards {
			segments = append(segments, segmentID)
		}
	}
	sort.Ints(segments)
	return segments
}
