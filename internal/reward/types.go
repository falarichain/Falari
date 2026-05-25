package reward

const (
	TotalSupply          uint64 = 10_000_000_000
	MiningSupply         uint64 = 9_000_000_000
	StoragePoolInitial   uint64 = 6_000_000_000
	RetrievalPoolInitial uint64 = 1_000_000_000
	ValidatorPoolInitial uint64 = 1_000_000_000
	RepairPoolInitial    uint64 = 1_000_000_000
)

type Pools struct {
	StorageRemaining   uint64 `json:"storage_pool_remaining"`
	RetrievalRemaining uint64 `json:"retrieval_pool_remaining"`
	ValidatorRemaining uint64 `json:"validator_pool_remaining"`
	RepairRemaining    uint64 `json:"repair_pool_remaining"`
	TokensReleased     uint64 `json:"tokens_released"`
}

func NewPools() *Pools {
	return &Pools{
		StorageRemaining:   StoragePoolInitial,
		RetrievalRemaining: RetrievalPoolInitial,
		ValidatorRemaining: ValidatorPoolInitial,
		RepairRemaining:    RepairPoolInitial,
	}
}

func (p *Pools) ReleaseEpochRewards(storageRateBPS, retrievalRateBPS, validatorRateBPS uint64) (storage, retrieval, validator uint64) {
	storage = p.releaseFromPool(&p.StorageRemaining, storageRateBPS)
	retrieval = p.releaseFromPool(&p.RetrievalRemaining, retrievalRateBPS)
	validator = p.releaseFromPool(&p.ValidatorRemaining, validatorRateBPS)
	total := storage + retrieval + validator
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

func (p *Pools) PayFromStoragePool(reward uint64) bool {
	return p.payFromPool(&p.StorageRemaining, reward)
}

func (p *Pools) PayFromRetrievalPool(reward uint64) bool {
	return p.payFromPool(&p.RetrievalRemaining, reward)
}

func (p *Pools) PayFromRepairPool(reward uint64) bool {
	return p.payFromPool(&p.RepairRemaining, reward)
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
