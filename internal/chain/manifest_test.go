package chain

import (
	"testing"
)

func TestManifestExportsDownloadPlanWithCommittedReceipts(t *testing.T) {
	store, _, resp := setupCommittedAssignedIntent(t)

	manifest, err := store.Manifest(resp.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.IntentID != resp.IntentID || !manifest.Complete {
		t.Fatalf("unexpected manifest header: %+v", manifest)
	}
	if manifest.ReceiptCount != len(resp.Assignments) {
		t.Fatalf("expected %d receipts, got %d", len(resp.Assignments), manifest.ReceiptCount)
	}
	if manifest.Plan.IntentID != resp.IntentID || manifest.Plan.FileRoot == "" {
		t.Fatalf("manifest plan missing intent metadata: %+v", manifest.Plan)
	}
	if len(manifest.Plan.CommittedShards) != len(resp.Assignments) {
		t.Fatalf("expected committed shard refs, got %+v", manifest.Plan.CommittedShards)
	}
	for i := 1; i < len(manifest.Plan.Receipts); i++ {
		prev := manifest.Plan.Receipts[i-1]
		current := manifest.Plan.Receipts[i]
		if prev.SegmentID > current.SegmentID || (prev.SegmentID == current.SegmentID && prev.ShardIndex > current.ShardIndex) {
			t.Fatalf("receipts are not sorted: %+v", manifest.Plan.Receipts)
		}
	}
}
