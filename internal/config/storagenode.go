package config

// StorageNodeConfig holds all configuration for a storage node.
type StorageNodeConfig struct {
	HTTP       HTTPConfig        `yaml:"http"`
	Data       string            `yaml:"data"`
	Chain      ChainClientConfig `yaml:"chain"`
	AutoProve  AutoTaskConfig    `yaml:"auto_prove"`
	AutoRepair AutoTaskConfig    `yaml:"auto_repair"`
	AutoDelete AutoTaskConfig    `yaml:"auto_delete"`
	P2P        P2PConfig         `yaml:"p2p"`
	DHT        DHTConfig         `yaml:"dht"`
}

// AutoTaskConfig holds settings for an automatic background task.
type AutoTaskConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Interval Duration `yaml:"interval"`
}
