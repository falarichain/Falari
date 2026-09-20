package config

import (
	"fmt"

	"chain/internal/reward"
)

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

// maxEpochChallenges mirrors the on-chain storage_proof_samples ceiling; a node
// asking for more challenges than governance allows would simply be ignored.
const maxEpochChallenges = 64

// Validate rejects values that are expressed in the wrong unit or that would
// silently switch off an economic mechanism. Token amounts in this file are in
// the smallest unit, like every other on-chain amount.
func (c ChainNodeConfig) Validate() error {
	interval := c.Epoch.Interval.Duration()
	duration := c.Epoch.Duration.Duration()
	if interval < 0 || duration < 0 {
		return fmt.Errorf("epoch.interval and epoch.duration must not be negative")
	}
	if interval > 0 && duration > interval {
		return fmt.Errorf("epoch.duration %v exceeds epoch.interval %v: proof windows would overlap", duration, interval)
	}
	if c.Epoch.Challenges < 0 || c.Epoch.Challenges > maxEpochChallenges {
		return fmt.Errorf("epoch.challenges %d out of range [0, %d]", c.Epoch.Challenges, maxEpochChallenges)
	}
	if c.Epoch.Reward != 0 && c.Epoch.Reward < reward.TokenUnit {
		return fmt.Errorf("epoch.reward %d is below one GF; proof rewards are in the smallest unit (1 GF = %d)",
			c.Epoch.Reward, reward.TokenUnit)
	}
	if c.Validator.CommissionBPS > 10_000 {
		return fmt.Errorf("validator.commission_bps %d exceeds 10000 (100%%)", c.Validator.CommissionBPS)
	}
	if c.Block.Duration() < 0 || c.Sync.Duration() < 0 {
		return fmt.Errorf("block_interval and sync_interval must not be negative")
	}
	return nil
}
