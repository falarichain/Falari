package config

// RetrievalNodeConfig holds all configuration for a retrieval node.
type RetrievalNodeConfig struct {
	HTTP        HTTPConfig        `yaml:"http"`
	Data        string            `yaml:"data"`
	Chain       ChainClientConfig `yaml:"chain"`
	AutoCollect struct {
		Enabled  bool     `yaml:"enabled"`
		Interval Duration `yaml:"interval"`
	} `yaml:"auto_collect"`
	CacheSize int           `yaml:"cache_size"`
	P2P       P2PConfig     `yaml:"p2p"`
	DHT       DHTConfig     `yaml:"dht"`
	Gateway   GatewayConfig `yaml:"gateway"`
}

// GatewayConfig holds upload gateway settings for erasure coding and miner dispatch.
type GatewayConfig struct {
	Enabled                bool     `yaml:"enabled"`
	StorageEndpoints       []string `yaml:"storage_endpoints"`
	TmpDir                 string   `yaml:"tmp_dir"`
	DataShards             int      `yaml:"data_shards"`
	ParityShards           int      `yaml:"parity_shards"`
	SegmentSize            int64    `yaml:"segment_size"`
	MaxUploadBytes         int64    `yaml:"max_upload_bytes"`
	AgentKeyFile           string   `yaml:"agent_key_file"`
	AllowPrivateKeyAPIKeys bool     `yaml:"allow_private_key_api_keys"`
}
