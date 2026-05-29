package chain

import (
	"errors"
	"time"

	"chain/internal/wire"
)

type submitRetrievalReceiptTxPayload struct {
	Request         wire.SubmitRetrievalReceiptRequest  `json:"request"`
	Response        wire.SubmitRetrievalReceiptResponse `json:"response"`
	AlreadyRewarded bool                                `json:"already_rewarded"`
	SubmittedAtUnix int64                               `json:"submitted_at_unix"`
}

func (s *Store) SubmitRetrievalReceipt(req wire.SubmitRetrievalReceiptRequest) (wire.SubmitRetrievalReceiptResponse, error) {
	if err := wire.VerifyRetrievalReceipt(req.Receipt); err != nil {
		return wire.SubmitRetrievalReceiptResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	resp, alreadyRewarded, err := s.submitRetrievalReceiptLocked(req)
	if err != nil {
		return wire.SubmitRetrievalReceiptResponse{}, err
	}
	s.recordTxLocked("submit_retrieval_receipt", req.Receipt.MinerAddress, submitRetrievalReceiptTxPayload{
		Request:         req,
		Response:        resp,
		AlreadyRewarded: alreadyRewarded,
		SubmittedAtUnix: time.Now().Unix(),
	})
	if err := s.saveLocked(); err != nil {
		return wire.SubmitRetrievalReceiptResponse{}, err
	}
	return resp, nil
}

func (s *Store) computeMinerAntiSpamScoreLocked(stats *wire.MinerStats) {
	totalRetrievals := stats.RetrievalSuccess
	if totalRetrievals == 0 {
		stats.SpeedScore = 5000
		stats.AntiSpamScore = 10000
		return
	}
	windowCount := uint64(0)
	windowTotalBytes := uint64(0)
	totalAbuseBytes := uint64(0)
	for _, window := range s.data.RetrievalWindows {
		windowCount++
		windowTotalBytes = saturatingAdd(windowTotalBytes, window.BytesSum)
		if window.SampleCount > 0 && window.SampleBytes > 0 {
			avgPerSample := window.SampleBytes / window.SampleCount
			maxReward := s.miningParamsLocked().MaxRetrievalRewardPerWindow
			if avgPerSample > maxReward/defaultRetrievalAbuseSpeedMultiplier {
				totalAbuseBytes = saturatingAdd(totalAbuseBytes, window.BytesSum)
			}
		}
	}
	speedScore := uint64(10000)
	if stats.RetrievalBytes > 0 && windowTotalBytes > 0 {
		ratio := stats.RetrievalBytes * 10000 / windowTotalBytes
		if ratio > 10000 {
			ratio = 10000
		}
		speedScore = ratio
	}
	stats.SpeedScore = speedScore

	antiSpamScore := uint64(10000)
	if stats.RetrievalBytes > 0 && totalAbuseBytes > 0 {
		abuseRatio := totalAbuseBytes * 10000 / stats.RetrievalBytes
		if abuseRatio > 10000 {
			abuseRatio = 10000
		}
		antiSpamScore = 10000 - abuseRatio
	}
	if stats.Status == wire.MinerStatusDegraded {
		antiSpamScore /= 2
	}
	if stats.Status == wire.MinerStatusJailed {
		antiSpamScore /= 4
	}
	stats.AntiSpamScore = antiSpamScore
}

func (s *Store) RecomputeAllAntiSpamScoresLocked() {
	for addr, stats := range s.data.Miners {
		statsCopy := stats
		s.computeMinerAntiSpamScoreLocked(&statsCopy)
		s.data.Miners[addr] = statsCopy
	}
}

func (s *Store) RetrievalReceipts(intentID string, minerAddress string) wire.RetrievalReceiptResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	receipts := make([]wire.RetrievalReceipt, 0)
	for _, receipt := range s.data.RetrievalReceipts {
		if intentID != "" && receipt.IntentID != intentID {
			continue
		}
		if minerAddress != "" && receipt.MinerAddress != minerAddress {
			continue
		}
		receipts = append(receipts, receipt)
	}
	return wire.RetrievalReceiptResponse{Receipts: receipts}
}

func (s *Store) submitRetrievalReceiptLocked(req wire.SubmitRetrievalReceiptRequest) (wire.SubmitRetrievalReceiptResponse, bool, error) {
	receipt := req.Receipt
	receipt.User = wire.NormalizeAddress(receipt.User)
	receipt.ClientAddress = wire.NormalizeAddress(receipt.ClientAddress)
	if receipt.ServedAtUnix > time.Now().Add(10*time.Minute).Unix() {
		return wire.SubmitRetrievalReceiptResponse{}, false, errors.New("retrieval receipt is from the future")
	}
	if _, err := s.registeredMinerLocked(receipt.MinerAddress, receipt.MinerPublicKey); err != nil {
		return wire.SubmitRetrievalReceiptResponse{}, false, err
	}
	intent, ok := s.data.Intents[receipt.IntentID]
	if !ok {
		return wire.SubmitRetrievalReceiptResponse{}, false, errors.New("retrieval intent not found")
	}
	if intent.User != receipt.User {
		return wire.SubmitRetrievalReceiptResponse{}, false, errors.New("retrieval user mismatch")
	}
	if !intentAllowsProviderDiscovery(intent) {
		return wire.SubmitRetrievalReceiptResponse{}, false, errors.New("retrieval access is not allowed")
	}
	if _, ok := intentReceiptForShard(intent, receipt.ShardHash, ""); !ok {
		return wire.SubmitRetrievalReceiptResponse{}, false, errors.New("retrieval shard is not part of intent")
	}
	if existing, ok := s.data.RetrievalReceipts[receipt.ReceiptID]; ok {
		return wire.SubmitRetrievalReceiptResponse{
			ReceiptID:    existing.ReceiptID,
			IntentID:     existing.IntentID,
			MinerAddress: existing.MinerAddress,
			BytesServed:  existing.BytesServed,
			Status:       "accepted",
		}, true, nil
	}

	stats := s.minerStatsLocked(receipt.MinerAddress)
	stats.RetrievalSuccess++
	stats.RetrievalBytes = saturatingAdd(stats.RetrievalBytes, receipt.BytesServed)
	s.computeMinerAntiSpamScoreLocked(&stats)
	s.data.Miners[receipt.MinerAddress] = stats
	s.data.RetrievalReceipts[receipt.ReceiptID] = receipt
	return wire.SubmitRetrievalReceiptResponse{
		ReceiptID:    receipt.ReceiptID,
		IntentID:     receipt.IntentID,
		MinerAddress: receipt.MinerAddress,
		BytesServed:  receipt.BytesServed,
		Status:       "accepted",
	}, false, nil
}

func (s *Store) EpochRewards(epochID string) (wire.EpochRewardsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	epoch, ok := s.data.Epochs[epochID]
	if !ok {
		return wire.EpochRewardsResponse{}, errors.New("epoch not found")
	}
	resp := wire.EpochRewardsResponse{
		EpochID:              epoch.EpochID,
		EpochRound:           epoch.EpochRound,
		StorageRewardsPaid:   epoch.StorageRewardsPaid,
		RetrievalRewardsPaid: epoch.RetrievalRewardsPaid,
		RepairRewardsPaid:    epoch.RepairRewardsPaid,
		StorageSlashed:       epoch.StorageSlashed,
	}
	for _, bucket := range s.data.MiningRewardVestings {
		if bucket.Total > bucket.Released {
			resp.PendingMiningRewards = saturatingAdd(resp.PendingMiningRewards, bucket.Total-bucket.Released)
		}
	}
	return resp, nil
}
