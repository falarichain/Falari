package reward

import (
	"testing"
)

func TestNewPoolsInitialBalances(t *testing.T) {
	p := NewPools()
	if p.StorageRemaining != StoragePoolInitial {
		t.Fatalf("storage pool: expected %d got %d", StoragePoolInitial, p.StorageRemaining)
	}
	if p.RetrievalRemaining != RetrievalPoolInitial {
		t.Fatalf("retrieval pool: expected %d got %d", RetrievalPoolInitial, p.RetrievalRemaining)
	}
	if p.ValidatorRemaining != ValidatorPoolInitial {
		t.Fatalf("validator pool: expected %d got %d", ValidatorPoolInitial, p.ValidatorRemaining)
	}
	if p.RepairRemaining != RepairPoolInitial {
		t.Fatalf("repair pool: expected %d got %d", RepairPoolInitial, p.RepairRemaining)
	}
	if p.TokensReleased != 0 {
		t.Fatalf("tokens released: expected 0 got %d", p.TokensReleased)
	}
}

func TestReleaseEpochRewards(t *testing.T) {
	p := NewPools()
	// Use default release rates matching DefaultMiningParams()
	const (
		defaultStorageBPS   uint64 = 3
		defaultRetrievalBPS uint64 = 20
		defaultValidatorBPS uint64 = 2
	)
	s1, r1, v1 := p.ReleaseEpochRewards(defaultStorageBPS, defaultRetrievalBPS, defaultValidatorBPS)

	if s1 == 0 || r1 == 0 || v1 == 0 {
		t.Fatalf("expected non-zero epoch release: storage=%d retrieval=%d validator=%d", s1, r1, v1)
	}
	if p.TokensReleased != s1+r1+v1 {
		t.Fatalf("tokens released mismatch: %d != %d", p.TokensReleased, s1+r1+v1)
	}

	expectedStorage := StoragePoolInitial - s1
	if p.StorageRemaining != expectedStorage {
		t.Fatalf("storage pool after release: expected %d got %d", expectedStorage, p.StorageRemaining)
	}
}

func TestPayFromPool(t *testing.T) {
	p := NewPools()

	if !p.PayFromStoragePool(100) {
		t.Fatal("expected pay to succeed")
	}
	if p.StorageRemaining != StoragePoolInitial-100 {
		t.Fatalf("storage pool: expected %d got %d", StoragePoolInitial-100, p.StorageRemaining)
	}

	if p.PayFromStoragePool(StoragePoolInitial) {
		t.Fatal("expected pay to fail when pool insufficient")
	}

	if !p.PayFromRepairPool(50) {
		t.Fatal("expected repair pay to succeed")
	}
	if p.RepairRemaining != RepairPoolInitial-50 {
		t.Fatalf("repair pool: expected %d got %d", RepairPoolInitial-50, p.RepairRemaining)
	}
}

func TestReleaseEpochDepletesPool(t *testing.T) {
	p := &Pools{
		StorageRemaining:   10,
		RetrievalRemaining: 10,
		ValidatorRemaining: 10,
		RepairRemaining:    RepairPoolInitial,
	}
	s, r, v := p.ReleaseEpochRewards(3, 20, 2)
	total := s + r + v
	if total == 0 || total > 30 {
		t.Fatalf("unexpected release from tiny pools: %d+%d+%d=%d", s, r, v, total)
	}
}

func TestSaturatingAdd(t *testing.T) {
	if saturatingAdd(1, 2) != 3 {
		t.Fatal("expected 1+2=3")
	}
	max := ^uint64(0)
	if saturatingAdd(max, 1) != max {
		t.Fatal("expected saturating add to cap at max")
	}
}
