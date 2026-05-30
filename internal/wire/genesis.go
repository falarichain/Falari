package wire

type GenesisDoc struct {
	ChainID                    string                      `json:"chain_id"`
	GenesisTime                int64                       `json:"genesis_time_unix"`
	Accounts                   []GenesisAccount            `json:"accounts,omitempty"`
	Validators                 []GenesisValidator          `json:"validators,omitempty"`
	GovernanceOperators        []GenesisGovernanceOperator `json:"governance_operators,omitempty"`
	RewardPools                *GenesisRewardPools         `json:"reward_pools,omitempty"`
	FoundationAddress          string                      `json:"foundation_address,omitempty"`
	RetrievalAddress           string                      `json:"retrieval_address,omitempty"`
	DataModerationThresholdNum int                         `json:"data_moderation_threshold_num,omitempty"`
	DataModerationThresholdDen int                         `json:"data_moderation_threshold_den,omitempty"`
	OperatorChangeThresholdNum int                         `json:"operator_change_threshold_num,omitempty"`
	OperatorChangeThresholdDen int                         `json:"operator_change_threshold_den,omitempty"`
}

type GenesisAccount struct {
	Address string `json:"address"`
	Balance uint64 `json:"balance"`
}

type GenesisValidator struct {
	OwnerAddress      string `json:"owner_address"`
	OperatorAddress   string `json:"operator_address"`
	OperatorPublicKey string `json:"operator_public_key"`
	Endpoint          string `json:"endpoint"`
	Stake             uint64 `json:"stake"`
}

type GenesisRewardPools struct {
	StoragePoolRemaining    uint64 `json:"storage_pool_remaining"`
	RetrievalPoolRemaining  uint64 `json:"retrieval_pool_remaining"`
	ValidatorPoolRemaining  uint64 `json:"validator_pool_remaining"`
	PermanentFundRemaining uint64 `json:"repair_pool_remaining"`
	FoundationPoolRemaining uint64 `json:"foundation_pool_remaining"`
}

type GenesisGovernanceOperator struct {
	Operator    string   `json:"operator"`
	PublicKey   string   `json:"public_key,omitempty"`
	Permissions []string `json:"permissions"`
	Enabled     *bool    `json:"enabled,omitempty"`
}
