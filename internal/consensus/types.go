package consensus

type UpgradePlan struct {
	Name       string `json:"name"`
	HaltHeight uint64 `json:"halt_height,omitempty"`
	HaltTime   int64  `json:"halt_time,omitempty"`
	Info       string `json:"info,omitempty"`
}

type State struct {
	Height       uint64 `json:"height"`
	Round        uint64 `json:"round"`
	Phase        string `json:"phase"`
	Proposer     string `json:"proposer,omitempty"`
	VotingPower  uint64 `json:"voting_power,omitempty"`
	TotalPower   uint64 `json:"total_power,omitempty"`
	BlockTimeout int64  `json:"block_timeout_ms,omitempty"`
}

const (
	PhasePropose   = "propose"
	PhasePrevote   = "prevote"
	PhasePrecommit = "precommit"
	PhaseCommit    = "commit"
	PhaseWait      = "wait"
)

const (
	DefaultBlockTimeoutMs int64  = 5000
	MaxConsensusRounds    uint64 = 5
)
