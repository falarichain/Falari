package wire

type GenesisDoc struct {
	ChainID             string                      `json:"chain_id"`
	GenesisTime         int64                       `json:"genesis_time_unix"`
	Accounts            []GenesisAccount            `json:"accounts,omitempty"`
	Validators          []GenesisValidator          `json:"validators,omitempty"`
	GovernanceOperators []GenesisGovernanceOperator `json:"governance_operators,omitempty"`
	RewardPools         *GenesisRewardPools         `json:"reward_pools,omitempty"`
}

type GenesisAccount struct {
	Address string `json:"address"`
	Balance uint64 `json:"balance"`
}

type GenesisValidator struct {
	Address   string `json:"address"`
	PublicKey string `json:"public_key"`
	Endpoint  string `json:"endpoint"`
	Stake     uint64 `json:"stake"`
}

type GenesisRewardPools struct {
	StoragePoolRemaining   uint64 `json:"storage_pool_remaining"`
	RetrievalPoolRemaining uint64 `json:"retrieval_pool_remaining"`
	ValidatorPoolRemaining uint64 `json:"validator_pool_remaining"`
	RepairPoolRemaining    uint64 `json:"repair_pool_remaining"`
}

type GenesisGovernanceOperator struct {
	Operator    string   `json:"operator"`
	Permissions []string `json:"permissions"`
	Enabled     *bool    `json:"enabled,omitempty"`
}
