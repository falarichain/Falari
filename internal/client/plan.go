package client

import (
	"encoding/json"
	"os"
	"sort"

	"chain/internal/wire"
)

func LoadPlan(path string) (wire.UploadPlan, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return wire.UploadPlan{}, err
	}
	var plan wire.UploadPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return wire.UploadPlan{}, err
	}
	if len(plan.CommittedShards) == 0 && len(plan.CommittedSegments) > 0 {
		committedSegments := IntSet(plan.CommittedSegments)
		for _, receipt := range plan.Receipts {
			if committedSegments[receipt.SegmentID] {
				plan.CommittedShards = append(plan.CommittedShards, wire.ShardRef{
					SegmentID:  receipt.SegmentID,
					ShardIndex: receipt.ShardIndex,
				})
			}
		}
	}
	return plan, nil
}

func SavePlan(path string, plan wire.UploadPlan) error {
	raw, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func ReceiptSegmentSet(receipts []wire.MinerReceipt) map[int]bool {
	out := make(map[int]bool, len(receipts))
	for _, receipt := range receipts {
		out[receipt.SegmentID] = true
	}
	return out
}

func ReceiptShardSet(receipts []wire.MinerReceipt) map[wire.ShardRef]bool {
	out := make(map[wire.ShardRef]bool, len(receipts))
	for _, receipt := range receipts {
		out[wire.ShardRef{SegmentID: receipt.SegmentID, ShardIndex: receipt.ShardIndex}] = true
	}
	return out
}

func CommittedShardSet(plan wire.UploadPlan) map[wire.ShardRef]bool {
	out := make(map[wire.ShardRef]bool, len(plan.CommittedShards))
	for _, ref := range plan.CommittedShards {
		out[ref] = true
	}
	return out
}

func PendingCommitReceipts(plan wire.UploadPlan) []wire.MinerReceipt {
	committed := CommittedShardSet(plan)
	pending := make([]wire.MinerReceipt, 0)
	for _, receipt := range plan.Receipts {
		ref := wire.ShardRef{SegmentID: receipt.SegmentID, ShardIndex: receipt.ShardIndex}
		if !committed[ref] {
			pending = append(pending, receipt)
		}
	}
	return pending
}

func HasAllShardReceipts(plan wire.UploadPlan, segmentID int) bool {
	uploaded := ReceiptShardSet(plan.Receipts)
	totalShards := plan.Erasure.DataShards + plan.Erasure.ParityShards
	for shardIndex := 0; shardIndex < totalShards; shardIndex++ {
		if !uploaded[wire.ShardRef{SegmentID: segmentID, ShardIndex: shardIndex}] {
			return false
		}
	}
	return true
}

func IntSet(values []int) map[int]bool {
	out := make(map[int]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func MarkCommitted(plan *wire.UploadPlan, receipts []wire.MinerReceipt) {
	committedShards := CommittedShardSet(*plan)
	receiptsBySegment := map[int]map[int]bool{}
	for _, receipt := range plan.Receipts {
		if receiptsBySegment[receipt.SegmentID] == nil {
			receiptsBySegment[receipt.SegmentID] = map[int]bool{}
		}
		receiptsBySegment[receipt.SegmentID][receipt.ShardIndex] = true
	}
	for _, receipt := range receipts {
		ref := wire.ShardRef{SegmentID: receipt.SegmentID, ShardIndex: receipt.ShardIndex}
		if !committedShards[ref] {
			plan.CommittedShards = append(plan.CommittedShards, ref)
			committedShards[ref] = true
		}
		if receiptsBySegment[receipt.SegmentID] == nil {
			receiptsBySegment[receipt.SegmentID] = map[int]bool{}
		}
		receiptsBySegment[receipt.SegmentID][receipt.ShardIndex] = true
	}

	seenSegments := map[int]bool{}
	for _, segmentID := range plan.CommittedSegments {
		seenSegments[segmentID] = true
	}
	for segmentID, shards := range receiptsBySegment {
		if len(shards) >= plan.Erasure.DataShards && !seenSegments[segmentID] {
			plan.CommittedSegments = append(plan.CommittedSegments, segmentID)
			seenSegments[segmentID] = true
		}
	}
	sort.Ints(plan.CommittedSegments)
	sort.Slice(plan.CommittedShards, func(i, j int) bool {
		if plan.CommittedShards[i].SegmentID != plan.CommittedShards[j].SegmentID {
			return plan.CommittedShards[i].SegmentID < plan.CommittedShards[j].SegmentID
		}
		return plan.CommittedShards[i].ShardIndex < plan.CommittedShards[j].ShardIndex
	})
}
