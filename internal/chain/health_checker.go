package chain

import (
	"log"
	"sort"
	"time"

	"chain/internal/wire"
)

type healthCheckTxPayload struct {
	AtRiskCount   int   `json:"at_risk_count"`
	CriticalCount int   `json:"critical_count"`
	CheckedAtUnix int64 `json:"checked_at_unix"`
}

func (s *Store) CheckDealHealthAll() ([]wire.DealHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	healths := s.checkDealHealthAllLocked()
	if len(healths) > 0 {
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
		s.recordTxLocked("deal_health_check", "", healthCheckTxPayload{
			AtRiskCount:   atRisk,
			CriticalCount: critical,
			CheckedAtUnix: time.Now().Unix(),
		})
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return healths, nil
}

func (s *Store) checkDealHealthAllLocked() []wire.DealHealth {
	if s.data.DealHealths == nil {
		s.data.DealHealths = map[string]wire.DealHealth{}
	}
	now := time.Now().Unix()
	healths := make([]wire.DealHealth, 0)
	for _, intent := range s.data.Intents {
		if intent.Status != wire.StatusFinalized {
			continue
		}
		if intent.StorageStatus != wire.StorageStatusActive {
			continue
		}
		totalShards := intent.Erasure.DataShards + intent.Erasure.ParityShards
		if totalShards <= 0 {
			continue
		}
		available := 0
		missingMiners := make([]string, 0)
		for segmentID := range intent.SegmentRoots {
			receipts := intent.Receipts[segmentID]
			for shardIndex := 0; shardIndex < totalShards; shardIndex++ {
				if _, hasReceipt := receipts[shardIndex]; hasReceipt {
					available++
				} else {
					missingMiners = append(missingMiners, missingShardLabel(intent.IntentID, segmentID, shardIndex))
				}
			}
		}
		missing := totalShards*len(intent.SegmentRoots) - available
		status := wire.DealHealthOK
		if missing > 0 && available >= intent.Erasure.DataShards*len(intent.SegmentRoots) {
			status = wire.DealHealthAtRisk
		}
		if available < intent.Erasure.DataShards*len(intent.SegmentRoots) {
			status = wire.DealHealthCritical
		}
		sort.Strings(missingMiners)
		health := wire.DealHealth{
			IntentID:          intent.IntentID,
			Status:            status,
			TotalShards:       totalShards * len(intent.SegmentRoots),
			AvailableShards:   available,
			MissingShards:     missing,
			MinRequiredShards: intent.Erasure.DataShards * len(intent.SegmentRoots),
			MissingMinerAddrs: missingMiners,
			LastCheckedAtUnix: now,
		}
		s.data.DealHealths[intent.IntentID] = health
		healths = append(healths, health)
	}
	sort.SliceStable(healths, func(i, j int) bool {
		severityRank := map[string]int{wire.DealHealthCritical: 0, wire.DealHealthAtRisk: 1, wire.DealHealthOK: 2}
		if severityRank[healths[i].Status] != severityRank[healths[j].Status] {
			return severityRank[healths[i].Status] < severityRank[healths[j].Status]
		}
		return healths[i].IntentID < healths[j].IntentID
	})
	return healths
}

func (s *Store) DealHealth(intentID string) (wire.DealHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data.DealHealths != nil {
		if health, ok := s.data.DealHealths[intentID]; ok {
			return health, nil
		}
	}
	_, ok := s.data.Intents[intentID]
	if !ok {
		return wire.DealHealth{}, nil
	}
	healths := s.checkDealHealthAllLocked()
	for _, h := range healths {
		if h.IntentID == intentID {
			return h, nil
		}
	}
	return wire.DealHealth{IntentID: intentID, Status: wire.DealHealthOK}, nil
}

func (s *Store) StartDealHealthChecker(interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			healths, err := s.CheckDealHealthAll()
			if err != nil {
				log.Printf("deal health check failed: %v", err)
				continue
			}
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
			if atRisk+critical > 0 {
				log.Printf("deal health check: at_risk=%d critical=%d", atRisk, critical)
			}
		}
	}()
}

func missingShardLabel(intentID string, segmentID int, shardIndex int) string {
	return intentID + ":" + itoa(segmentID) + ":" + itoa(shardIndex)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	result := ""
	for value > 0 {
		result = string(rune('0'+value%10)) + result
		value /= 10
	}
	return result
}
