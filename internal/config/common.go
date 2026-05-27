package config

// HTTPConfig holds HTTP server and middleware settings shared by all node types.
type HTTPConfig struct {
	Addr           string   `yaml:"addr"`
	CORSOrigins    []string `yaml:"cors_origins"`
	RateLimitRPS   float64  `yaml:"rate_limit_rps"`
	RateLimitBurst int      `yaml:"rate_limit_burst"`
	TrustedProxies []string `yaml:"trusted_proxies"`
	Production     bool     `yaml:"production"`
}

// P2PConfig holds libp2p gossipsub settings.
type P2PConfig struct {
	Listen string `yaml:"listen"`
	Peers  string `yaml:"peers"`
	Topic  string `yaml:"topic"`
}

// DHTConfig holds Kademlia DHT settings for storage and retrieval nodes.
type DHTConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Bootstrap []string `yaml:"bootstrap"`
	Namespace string   `yaml:"namespace"`
	Republish Duration `yaml:"republish"`
}

// ChainClientConfig holds chain node connection settings for storage and retrieval nodes.
type ChainClientConfig struct {
	URL      string `yaml:"url"`
	Endpoint string `yaml:"endpoint"`
	Capacity uint64 `yaml:"capacity"`
	Stake    uint64 `yaml:"stake"`
}
