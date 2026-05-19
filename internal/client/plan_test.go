package client

import (
	"testing"

	"chain/internal/wire"
)

func TestMarkCommittedTracksShardLevelResumeState(t *testing.T) {
	plan := wire.UploadPlan{
		Erasure: wire.ErasurePolicy{DataShards: 2, ParityShards: 1},
		Receipts: []wire.MinerReceipt{
			{SegmentID: 0, ShardIndex: 0},
			{SegmentID: 0, ShardIndex: 1},
			{SegmentID: 0, ShardIndex: 2},
		},
	}
	MarkCommitted(&plan, plan.Receipts[:2])
	if len(plan.CommittedShards) != 2 {
		t.Fatalf("expected 2 committed shards, got %d", len(plan.CommittedShards))
	}
	if len(plan.CommittedSegments) != 1 || plan.CommittedSegments[0] != 0 {
		t.Fatalf("expected segment 0 committed after two data shards, got %v", plan.CommittedSegments)
	}
	pending := PendingCommitReceipts(plan)
	if len(pending) != 1 || pending[0].ShardIndex != 2 {
		t.Fatalf("expected parity shard pending, got %+v", pending)
	}
	MarkCommitted(&plan, pending)
	if len(PendingCommitReceipts(plan)) != 0 {
		t.Fatal("expected no pending receipts after parity commit")
	}
}
