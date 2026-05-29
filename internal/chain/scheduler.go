package chain

import (
	"log"
	"time"

	"chain/internal/reward"
	"chain/internal/wire"
)

type EpochSchedulerConfig struct {
	Interval          time.Duration
	Duration          time.Duration
	ChallengesPerDeal int
	RewardPerProof    uint64
	SlashPerMissed    uint64
}

type IntentSettlementSchedulerConfig struct {
	Interval time.Duration
}

func (s *Store) StartEpochScheduler(config EpochSchedulerConfig) {
	if config.Interval <= 0 {
		return
	}
	if config.Duration <= 0 {
		config.Duration = 10 * time.Minute
	}
	if config.ChallengesPerDeal <= 0 {
		config.ChallengesPerDeal = 1
	}
	if config.RewardPerProof == 0 {
		config.RewardPerProof = reward.TokenUnit
	}

	go func() {
		ticker := time.NewTicker(config.Interval)
		defer ticker.Stop()
		for range ticker.C {
			s.checkAndLogMinerWeights()
			s.checkAndLogDealHealths()

			s.mu.Lock()
			s.expireMinerBonusesLocked()
			s.finalizeExitingValidatorsLocked()
			s.finalizeExitingMinersLocked()
			// NOTE: token release is deterministic from block production/acceptance.
			// Vested mining rewards move to balance only when miners claim them.
			s.mu.Unlock()

			finalized, err := s.FinalizeExpiredEpochs()
			if err != nil {
				log.Printf("auto finalize epochs failed: %v", err)
			}
			for _, resp := range finalized {
				round := resp.EpochID
				log.Printf("auto finalized epoch %s accepted=%d missed=%d rewards=%d slashed=%d repairs=%d",
					round, resp.AcceptedProofs, resp.MissedProofs,
					resp.StorageRewardsPaid, resp.StorageSlashed, resp.RepairTasksCreated)
			}

			resp, err := s.StartEpoch(wire.StartEpochRequest{
				ChallengesPerDeal:   config.ChallengesPerDeal,
				DurationSeconds:     int64(config.Duration.Seconds()),
				RewardPerProof:      config.RewardPerProof,
				SlashPerMissedProof: config.SlashPerMissed,
			})
			if err != nil {
				continue
			}
			log.Printf("auto started epoch %s challenges=%d", resp.Epoch.EpochID, len(resp.Challenges))
		}
	}()
}

func (s *Store) checkAndLogMinerWeights() {
	s.mu.Lock()
	s.RecomputeAllMinerWeightsLocked()
	s.RecomputeAllAntiSpamScoresLocked()
	s.mu.Unlock()
}

func (s *Store) checkAndLogDealHealths() {
	s.mu.Lock()
	healths := s.checkDealHealthAllLocked()
	atRisk := 0
	critical := 0
	for _, h := range healths {
		switch h.Status {
		case wire.DealHealthAtRisk:
			atRisk++
		case wire.DealHealthCritical:
			critical++
		}
	}
	s.mu.Unlock()
	if atRisk+critical > 0 {
		log.Printf("deal health check: at_risk=%d critical=%d", atRisk, critical)
	}
}

func (s *Store) StartIntentSettlementScheduler(config IntentSettlementSchedulerConfig) {
	if config.Interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(config.Interval)
		defer ticker.Stop()
		for range ticker.C {
			settled, err := s.SettleExpiredIntents()
			if err != nil {
				log.Printf("auto settle intents failed: %v", err)
				continue
			}
			for _, resp := range settled {
				log.Printf("auto settled intent %s status=%s refunded=%d paid=%d", resp.IntentID, resp.Status, resp.RefundedFee, resp.PaidFee)
			}
		}
	}()
}
