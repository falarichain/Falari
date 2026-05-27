package config

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDurationUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    time.Duration
		wantErr bool
	}{
		{"seconds", `interval: "5s"`, 5 * time.Second, false},
		{"minutes", `interval: "10m"`, 10 * time.Minute, false},
		{"hours", `interval: "1h30m"`, 90 * time.Minute, false},
		{"zero", `interval: "0s"`, 0, false},
		{"invalid", `interval: "bad"`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg struct {
				Interval Duration `yaml:"interval"`
			}
			tmp := writeTempYAML(t, tt.yaml)
			err := Load(tmp, &cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && cfg.Interval.Duration() != tt.want {
				t.Errorf("got %v, want %v", cfg.Interval.Duration(), tt.want)
			}
		})
	}
}

func TestDurationMarshalYAML(t *testing.T) {
	d := Duration(5 * time.Second)
	v, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if v != "5s" {
		t.Errorf("got %q, want %q", v, "5s")
	}
}

func TestLoadChainNodeConfig(t *testing.T) {
	yaml := `
http:
  addr: ":9090"
  cors_origins:
    - "http://localhost:3000"
  rate_limit_rps: 10.5
  production: true
state: "/data/chain.ldb"
genesis: "/data/genesis.json"
validator:
  endpoint: "http://validator:8080"
  stake: 500000
  commission_bps: 100
block_interval: "10s"
epoch:
  interval: "1h"
  duration: "20m"
  challenges: 5
  reward: 2
  slash: 3
settle_interval: "2m"
renew_interval: "3m"
sync_interval: "10s"
p2p:
  listen: "/ip4/0.0.0.0/tcp/4001"
  topic: "falari-mainnet"
peers: "http://peer1:8080,http://peer2:8080"
`
	tmp := writeTempYAML(t, yaml)
	var cfg ChainNodeConfig
	if err := Load(tmp, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":9090" {
		t.Errorf("HTTP.Addr = %q", cfg.HTTP.Addr)
	}
	if len(cfg.HTTP.CORSOrigins) != 1 || cfg.HTTP.CORSOrigins[0] != "http://localhost:3000" {
		t.Errorf("HTTP.CORSOrigins = %v", cfg.HTTP.CORSOrigins)
	}
	if cfg.HTTP.RateLimitRPS != 10.5 {
		t.Errorf("HTTP.RateLimitRPS = %v", cfg.HTTP.RateLimitRPS)
	}
	if !cfg.HTTP.Production {
		t.Error("HTTP.Production = false")
	}
	if cfg.State != "/data/chain.ldb" {
		t.Errorf("State = %q", cfg.State)
	}
	if cfg.Validator.Stake != 500000 {
		t.Errorf("Validator.Stake = %d", cfg.Validator.Stake)
	}
	if cfg.Block.Duration() != 10*time.Second {
		t.Errorf("Block = %v", cfg.Block.Duration())
	}
	if cfg.Epoch.Challenges != 5 {
		t.Errorf("Epoch.Challenges = %d", cfg.Epoch.Challenges)
	}
	if cfg.Peers != "http://peer1:8080,http://peer2:8080" {
		t.Errorf("Peers = %q", cfg.Peers)
	}
}

func TestLoadStorageNodeConfig(t *testing.T) {
	yaml := `
http:
  addr: ":9090"
data: "/data/miner1"
chain:
  url: "http://chain:8080"
  endpoint: "http://storage:9090"
  capacity: 107374182400
  stake: 5000
auto_prove:
  enabled: true
  interval: "3s"
auto_repair:
  enabled: false
  interval: "15s"
auto_delete:
  enabled: true
  interval: "20s"
p2p:
  listen: "/ip4/0.0.0.0/tcp/0"
  topic: "testnet-providers"
dht:
  enabled: true
  bootstrap:
    - "/ip4/1.2.3.4/tcp/4001"
    - "/ip4/5.6.7.8/tcp/4001"
  namespace: "/test"
  republish: "120s"
`
	tmp := writeTempYAML(t, yaml)
	var cfg StorageNodeConfig
	if err := Load(tmp, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Chain.URL != "http://chain:8080" {
		t.Errorf("Chain.URL = %q", cfg.Chain.URL)
	}
	if cfg.Chain.Capacity != 107374182400 {
		t.Errorf("Chain.Capacity = %d", cfg.Chain.Capacity)
	}
	if !cfg.AutoProve.Enabled || cfg.AutoProve.Interval.Duration() != 3*time.Second {
		t.Errorf("AutoProve = %+v", cfg.AutoProve)
	}
	if cfg.AutoRepair.Enabled {
		t.Error("AutoRepair.Enabled should be false")
	}
	if !cfg.DHT.Enabled {
		t.Error("DHT.Enabled should be true")
	}
	if len(cfg.DHT.Bootstrap) != 2 {
		t.Errorf("DHT.Bootstrap len = %d", len(cfg.DHT.Bootstrap))
	}
	if cfg.DHT.Republish.Duration() != 120*time.Second {
		t.Errorf("DHT.Republish = %v", cfg.DHT.Republish.Duration())
	}
}

func TestLoadRetrievalNodeConfig(t *testing.T) {
	yaml := `
http:
  addr: ":9091"
data: "/data/retrieval1"
chain:
  url: "http://chain:8080"
  capacity: 107374182400
auto_collect:
  enabled: true
  interval: "45s"
cache_size: 1024
gateway:
  enabled: true
  storage_endpoints:
    - "http://miner1:9090"
    - "http://miner2:9090"
  data_shards: 6
  parity_shards: 3
  segment_size: 134217728
  max_upload_bytes: 2147483648
  agent_key_file: "/keys/agent.json"
  allow_private_key_api_keys: false
`
	tmp := writeTempYAML(t, yaml)
	var cfg RetrievalNodeConfig
	if err := Load(tmp, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AutoCollect.Interval.Duration() != 45*time.Second {
		t.Errorf("AutoCollect.Interval = %v", cfg.AutoCollect.Interval.Duration())
	}
	if cfg.CacheSize != 1024 {
		t.Errorf("CacheSize = %d", cfg.CacheSize)
	}
	if !cfg.Gateway.Enabled {
		t.Error("Gateway.Enabled should be true")
	}
	if len(cfg.Gateway.StorageEndpoints) != 2 {
		t.Errorf("Gateway.StorageEndpoints len = %d", len(cfg.Gateway.StorageEndpoints))
	}
	if cfg.Gateway.DataShards != 6 {
		t.Errorf("Gateway.DataShards = %d", cfg.Gateway.DataShards)
	}
	if cfg.Gateway.SegmentSize != 134217728 {
		t.Errorf("Gateway.SegmentSize = %d", cfg.Gateway.SegmentSize)
	}
	if cfg.Gateway.AllowPrivateKeyAPIKeys {
		t.Error("Gateway.AllowPrivateKeyAPIKeys should be false")
	}
}

func TestLoadFileNotFound(t *testing.T) {
	var cfg ChainNodeConfig
	err := Load("/nonexistent/path.yaml", &cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestIsFlagSet(t *testing.T) {
	// Use a separate FlagSet to avoid polluting the global flag state.
	// IsFlagSet uses flag.Visit on the default FlagSet, so we test with CommandLine.
	fs := flag.CommandLine
	_ = fs.String("test-config-flag", "default", "test flag")

	// Flag was not set.
	if IsFlagSet("test-config-flag") {
		t.Error("flag should not be set")
	}
	// Non-existent flag.
	if IsFlagSet("nonexistent-flag") {
		t.Error("nonexistent flag should not be set")
	}
}

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
