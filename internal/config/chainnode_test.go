package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chain/internal/reward"
)

func validChainNodeConfig() ChainNodeConfig {
	return ChainNodeConfig{
		State:   "./data/chain.ldb",
		Genesis: "../genesis.json",
		Epoch: EpochConfig{
			Interval:   Duration(60 * time.Minute),
			Duration:   Duration(10 * time.Minute),
			Challenges: 4,
			Reward:     reward.TokenUnit,
			Slash:      1,
		},
		Block:     Duration(5 * time.Second),
		Sync:      Duration(5 * time.Second),
		Validator: ValidatorConfig{Stake: 1_000_000 * reward.TokenUnit},
	}
}

func TestChainNodeConfigValidAcceptsDeployedDefaults(t *testing.T) {
	if err := validChainNodeConfig().Validate(); err != nil {
		t.Fatalf("expected deployed defaults to validate, got %v", err)
	}
	cfg := ChainNodeConfig{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("an empty config falls back to flag defaults and must validate, got %v", err)
	}
}

func TestChainNodeConfigRejectsSubGFProofReward(t *testing.T) {
	cfg := validChainNodeConfig()
	cfg.Epoch.Reward = 1
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected a proof reward below one GF to be rejected")
	}
	if !strings.Contains(err.Error(), "smallest unit") {
		t.Fatalf("error should explain the unit, got %v", err)
	}
}

func TestChainNodeConfigRejectsOverlappingEpochWindow(t *testing.T) {
	cfg := validChainNodeConfig()
	cfg.Epoch.Interval = Duration(time.Minute)
	cfg.Epoch.Duration = Duration(10 * time.Minute)
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected a proof window longer than the epoch interval to be rejected")
	}
}

func TestChainNodeConfigRejectsOutOfRangeValues(t *testing.T) {
	for _, edit := range []func(*ChainNodeConfig){
		func(c *ChainNodeConfig) { c.Epoch.Challenges = maxEpochChallenges + 1 },
		func(c *ChainNodeConfig) { c.Validator.CommissionBPS = 10_001 },
		func(c *ChainNodeConfig) { c.Block = Duration(-time.Second) },
	} {
		cfg := validChainNodeConfig()
		edit(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected out-of-range value to be rejected")
		}
	}
}

// TestDeployedChainNodeYAMLValidates guards the shipped template: an operator
// starting a node with it must not hit a rejected config at boot.
func TestDeployedChainNodeYAMLValidates(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		path := filepath.Join(dir, "deploy", "chainnode.yaml")
		if _, err := os.Stat(path); err == nil {
			var cfg ChainNodeConfig
			if err := Load(path, &cfg); err != nil {
				t.Fatal(err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if cfg.Epoch.Challenges != 4 {
				t.Fatalf("deployed template must match the CLI default of 4 challenges, got %d", cfg.Epoch.Challenges)
			}
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("deploy/chainnode.yaml not reachable from the test working directory")
		}
		dir = parent
	}
}
