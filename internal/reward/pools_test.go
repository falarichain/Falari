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
	if p.PermanentFundRemaining != PermanentFundPoolInitial {
		t.Fatalf("permanent fund: expected %d got %d", PermanentFundPoolInitial, p.PermanentFundRemaining)
	}
	if p.FoundationRemaining != FoundationPoolInitial {
		t.Fatalf("foundation pool: expected %d got %d", FoundationPoolInitial, p.FoundationRemaining)
	}
	if p.TokensReleased != 0 {
		t.Fatalf("tokens released: expected 0 got %d", p.TokensReleased)
	}
}

// TestPoolsCoverWholeSupply guards the emission envelope: the four pools are the
// entire supply, so the permanent-storage fund can only ever be carved out of a
// stream and can never push total issuance past TotalSupply.
func TestPoolsCoverWholeSupply(t *testing.T) {
	sum := StoragePoolInitial + ValidatorPoolInitial + FoundationPoolInitial + RetrievalPoolInitial + PermanentFundPoolInitial
	if sum != TotalSupply {
		t.Fatalf("pools %d do not add up to total supply %d", sum, TotalSupply)
	}
	if PermanentFundCap > StoragePoolInitial+ValidatorPoolInitial {
		t.Fatalf("permanent fund cap %d exceeds the streams that feed it %d",
			PermanentFundCap, StoragePoolInitial+ValidatorPoolInitial)
	}
}

func TestSpendPermanentFund(t *testing.T) {
	p := &Pools{PermanentFundRemaining: 100, TokensReleased: 1_000}

	if !p.SpendPermanentFund(50) {
		t.Fatal("expected spend to succeed")
	}
	if p.PermanentFundRemaining != 50 {
		t.Fatalf("permanent fund: expected 50 got %d", p.PermanentFundRemaining)
	}
	if p.TokensReleased != 1_000 {
		t.Fatalf("fund spend must not count as new issuance, got %d", p.TokensReleased)
	}

	if p.SpendPermanentFund(1_000) {
		t.Fatal("expected spend to fail when the fund is insufficient")
	}
	if p.PermanentFundRemaining != 50 {
		t.Fatalf("permanent fund after failed spend: expected 50 got %d", p.PermanentFundRemaining)
	}
}

func TestSaturatingAdd(t *testing.T) {
	if SaturatingAdd(1, 2) != 3 {
		t.Fatal("expected 1+2=3")
	}
	max := ^uint64(0)
	if SaturatingAdd(max, 1) != max {
		t.Fatal("expected saturating add to cap at max")
	}
}
