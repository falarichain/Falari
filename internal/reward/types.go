package reward

const (
	TokenUnit uint64 = 100_000_000 // 10^8, one GF in smallest units

	TotalSupply           uint64 = 10_000_000_000 * TokenUnit
	MiningSupply          uint64 = 9_000_000_000 * TokenUnit
	StoragePoolInitial    uint64 = 6_000_000_000 * TokenUnit
	RetrievalPoolInitial  uint64 = 600_000_000 * TokenUnit
	ValidatorPoolInitial  uint64 = 1_200_000_000 * TokenUnit
	PermanentFundPoolInitial uint64 = 1_200_000_000 * TokenUnit
	FoundationPoolInitial uint64 = 1_000_000_000 * TokenUnit
)

type Pools struct {
	StorageRemaining    uint64 `json:"storage_pool_remaining"`
	RetrievalRemaining  uint64 `json:"retrieval_pool_remaining"`
	ValidatorRemaining  uint64 `json:"validator_pool_remaining"`
	PermanentFundRemaining uint64 `json:"repair_pool_remaining"`
	FoundationRemaining uint64 `json:"foundation_pool_remaining"`
	TokensReleased      uint64 `json:"tokens_released"`
}

func NewPools() *Pools {
	return &Pools{
		StorageRemaining:    StoragePoolInitial,
		RetrievalRemaining:  RetrievalPoolInitial,
		ValidatorRemaining:  ValidatorPoolInitial,
		PermanentFundRemaining: PermanentFundPoolInitial,
		FoundationRemaining: FoundationPoolInitial,
	}
}

func (p *Pools) ReleaseEpochRewards(storageRateBPS, retrievalRateBPS, validatorRateBPS, foundationRateBPS uint64) (storage, retrieval, validator, foundation uint64) {
	storage = p.releaseFromPool(&p.StorageRemaining, storageRateBPS)
	retrieval = p.releaseFromPool(&p.RetrievalRemaining, retrievalRateBPS)
	validator = p.releaseFromPool(&p.ValidatorRemaining, validatorRateBPS)
	foundation = p.releaseFromPool(&p.FoundationRemaining, foundationRateBPS)
	total := storage + retrieval + validator + foundation
	p.TokensReleased = saturatingAdd(p.TokensReleased, total)
	return
}

func (p *Pools) releaseFromPool(pool *uint64, rateBPS uint64) uint64 {
	if *pool == 0 {
		return 0
	}
	amount := *pool * rateBPS / 10000
	if amount == 0 {
		amount = 1
	}
	if amount > *pool {
		amount = *pool
	}
	*pool -= amount
	return amount
}

// ReleaseLinear releases a linear (fixed) amount based on the initial pool size,
// not the remaining balance. This produces constant token emission over time.
// amount = initialAmount * rateBPS / 10000, capped at poolRemaining.
func (p *Pools) ReleaseLinear(poolRemaining *uint64, initialAmount, rateBPS uint64) uint64 {
	if *poolRemaining == 0 || rateBPS == 0 {
		return 0
	}
	amount := initialAmount * rateBPS / 10000
	if amount > *poolRemaining {
		amount = *poolRemaining
	}
	if amount == 0 {
		return 0
	}
	*poolRemaining -= amount
	p.TokensReleased = saturatingAdd(p.TokensReleased, amount)
	return amount
}

func (p *Pools) PayFromStoragePool(reward uint64) bool {
	return p.payFromPool(&p.StorageRemaining, reward)
}

func (p *Pools) PayFromRetrievalPool(reward uint64) bool {
	return p.payFromPool(&p.RetrievalRemaining, reward)
}

func (p *Pools) PayFromPermanentFund(reward uint64) bool {
	return p.payFromPool(&p.PermanentFundRemaining, reward)
}

func (p *Pools) payFromPool(pool *uint64, reward uint64) bool {
	if reward == 0 {
		return true
	}
	if *pool < reward {
		return false
	}
	*pool -= reward
	p.TokensReleased = saturatingAdd(p.TokensReleased, reward)
	return true
}

func saturatingAdd(a, b uint64) uint64 {
	sum := a + b
	if sum < a {
		return ^uint64(0)
	}
	return sum
}
