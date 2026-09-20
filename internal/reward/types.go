package reward

const (
	TokenUnit uint64 = 100_000_000 // 10^8, one GF in smallest units

	TotalSupply uint64 = 10_000_000_000 * TokenUnit

	// The four emission pools are the whole supply: nothing is reserved at
	// genesis, and every token that ever leaves a pool is either paid to a
	// recipient or parked in the permanent-storage fund.
	StoragePoolInitial       uint64 = 6_000_000_000 * TokenUnit // miners
	ValidatorPoolInitial     uint64 = 2_000_000_000 * TokenUnit // validators
	FoundationPoolInitial    uint64 = 1_000_000_000 * TokenUnit // foundation
	RetrievalPoolInitial     uint64 = 1_000_000_000 * TokenUnit // retrieval gateway
	PermanentFundPoolInitial uint64 = 0

	// PermanentFundCap is the lifetime amount the permanent-storage fund may
	// accumulate out of the storage and validator streams. Once filled, those
	// streams pay their recipients in full again.
	PermanentFundCap uint64 = 2_000_000_000 * TokenUnit
)

type Pools struct {
	StorageRemaining       uint64 `json:"storage_pool_remaining"`
	RetrievalRemaining     uint64 `json:"retrieval_pool_remaining"`
	ValidatorRemaining     uint64 `json:"validator_pool_remaining"`
	PermanentFundRemaining uint64 `json:"permanent_fund_remaining"`
	FoundationRemaining    uint64 `json:"foundation_pool_remaining"`
	TokensReleased         uint64 `json:"tokens_released"`
}

func NewPools() *Pools {
	return &Pools{
		StorageRemaining:       StoragePoolInitial,
		RetrievalRemaining:     RetrievalPoolInitial,
		ValidatorRemaining:     ValidatorPoolInitial,
		PermanentFundRemaining: PermanentFundPoolInitial,
		FoundationRemaining:    FoundationPoolInitial,
	}
}

// SpendPermanentFund moves already-issued fund tokens to a miner. It does not
// touch TokensReleased: those tokens were counted when the fund received them.
func (p *Pools) SpendPermanentFund(amount uint64) bool {
	if amount == 0 {
		return true
	}
	if p.PermanentFundRemaining < amount {
		return false
	}
	p.PermanentFundRemaining -= amount
	return true
}

func SaturatingAdd(a, b uint64) uint64 {
	if b > ^uint64(0)-a {
		return ^uint64(0)
	}
	return a + b
}
