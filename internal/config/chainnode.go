package config

// ChainNodeConfig holds all configuration for a validator chain node.
type ChainNodeConfig struct {
	HTTP      HTTPConfig      `yaml:"http"`
	P2P       P2PConfig       `yaml:"p2p"`
	State     string          `yaml:"state"`
	Genesis   string          `yaml:"genesis"`
	Epoch     EpochConfig     `yaml:"epoch"`
	Settle    Duration        `yaml:"settle_interval"`
	Renew     Duration        `yaml:"renew_interval"`
	Block     Duration        `yaml:"block_interval"`
	Sync      Duration        `yaml:"sync_interval"`
	Validator ValidatorConfig `yaml:"validator"`
	Peers     string          `yaml:"peers"`
}

// EpochConfig holds automatic proof epoch scheduler settings.
type EpochConfig struct {
	Interval   Duration `yaml:"interval"`
	Duration   Duration `yaml:"duration"`
	Challenges int      `yaml:"challenges"`
	Reward     uint64   `yaml:"reward"`
	Slash      uint64   `yaml:"slash"`
}

// ValidatorConfig holds validator registration parameters.
type ValidatorConfig struct {
	Endpoint      string `yaml:"endpoint"`
	Stake         uint64 `yaml:"stake"`
	CommissionBPS uint64 `yaml:"commission_bps"`
}
