package chain

import (
	"net"
	"net/url"
	"strings"
	"time"

	"chain/internal/wire"
)

// DHTStalenessSeconds is the maximum age (in seconds) of a miner's last DHT
// publish before it is considered stale. Miners with stale DHT records have
// their RetrievalObligMet flag cleared at epoch finalization.
const DHTStalenessSeconds = int64(EpochIntervalDefault / time.Second) // 1 epoch interval (default 1800s = 30min)

// minerIP extracts the IP address string for a miner from its Endpoint or
// ProviderRecord PeerAddrs. Returns empty string if no IP can be determined.
func (s *Store) minerIP(addr string, stats wire.MinerStats) string {
	// Try Endpoint first (URL format: http://IP:PORT).
	if stats.Endpoint != "" {
		if u, err := url.Parse(stats.Endpoint); err == nil && u.Hostname() != "" {
			if ip := net.ParseIP(u.Hostname()); ip != nil {
				return ip.String()
			}
		}
	}
	// Fallback: extract IP from ProviderRecord PeerAddrs (multiaddr /ip4/X.X.X.X/tcp/PORT).
	if record, ok := s.data.ProviderRecords[addr]; ok {
		for _, ma := range record.PeerAddrs {
			parts := strings.Split(ma, "/")
			for i, p := range parts {
				if p == "ip4" && i+1 < len(parts) {
					if ip := net.ParseIP(parts[i+1]); ip != nil {
						return ip.String()
					}
				}
			}
		}
	}
	return ""
}

// ip16Subnet returns the /16 subnet prefix (first two octets) of an IP string.
// Returns empty string for invalid IPs.
func ip16Subnet(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	ip = ip.To4()
	if ip == nil {
		return "" // IPv6 not handled as /16
	}
	return strings.Join(strings.Split(ip.String(), ".")[:2], ".")
}

// computeIPDispersionScoresLocked computes a per-miner IP dispersion score
// (0-10000) based on how many other miners share the same /16 IP subnet.
// Miners on unique subnets get full score; crowded subnets get penalized.
func (s *Store) computeIPDispersionScoresLocked() map[string]uint64 {
	scores := make(map[string]uint64, len(s.data.Miners))

	// Pass 1: collect /16 subnet → miner addresses.
	subnetMiners := map[string][]string{}
	minerSubnet := map[string]string{}
	for addr, stats := range s.data.Miners {
		if stats.Status == wire.MinerStatusExiting || stats.Status == wire.MinerStatusExited || stats.Status == wire.MinerStatusJailed {
			continue
		}
		if stats.UsedBytes == 0 {
			continue
		}
		ip := s.minerIP(addr, stats)
		if ip == "" {
			scores[addr] = 5000 // unknown IP → neutral score
			continue
		}
		subnet := ip16Subnet(ip)
		if subnet == "" {
			scores[addr] = 5000
			continue
		}
		subnetMiners[subnet] = append(subnetMiners[subnet], addr)
		minerSubnet[addr] = subnet
	}

	// Pass 2: assign score based on subnet crowd size.
	for addr, subnet := range minerSubnet {
		count := len(subnetMiners[subnet])
		switch {
		case count <= 1:
			scores[addr] = 10000
		case count <= 5:
			scores[addr] = 7500
		case count <= 20:
			scores[addr] = 5000
		case count <= 50:
			scores[addr] = 2500
		default:
			scores[addr] = 0
		}
	}

	return scores
}

func (s *Store) computeMinerEffectiveWeightLocked(stats wire.MinerStats, ipDispersionScores map[string]uint64) uint64 {
	if stats.Status == wire.MinerStatusExiting || stats.Status == wire.MinerStatusExited || stats.Status == wire.MinerStatusJailed {
		return 0
	}

	storedBytes := stats.UsedBytes
	if storedBytes == 0 {
		return 0
	}

	proofScore := uint64(5000)
	totalProofs := stats.ProofSuccess + stats.ProofFailure
	if totalProofs > 0 {
		proofScore = stats.ProofSuccess * 10000 / totalProofs
	} else if stats.Status == wire.MinerStatusActive {
		proofScore = 6500
	}

	availabilityScore := uint64(10000)
	if stats.ConsecutiveFailures > 0 {
		divisor := uint64(1) << stats.ConsecutiveFailures
		if divisor == 0 {
			divisor = 1
		}
		if divisor <= 64 {
			availabilityScore = 10000 / divisor
		} else {
			availabilityScore = 0
		}
	}

	// Retrieval speed score (0-10000): reuses existing SpeedScore computed
	// by RecomputeAllAntiSpamScoresLocked. Default 5000 when no retrievals.
	retrievalSpeedScore := stats.SpeedScore

	// IP dispersion score (0-10000): computed globally before this call.
	ipDispersionScore := ipDispersionScores[stats.MinerAddress]

	switch stats.Status {
	case wire.MinerStatusDegraded:
		proofScore /= 2
		availabilityScore /= 2
	case wire.MinerStatusJailed:
		proofScore /= 10
		availabilityScore = 0
	}

	params := s.miningParamsLocked()
	// Use mulDivUint64 (big.Int) to avoid uint64 overflow when
	// storedBytes * score exceeds ~1.8×10¹⁹ (e.g. 10 PB × 10 000).
	const bpsSquare = uint64(10000) * 10000 // 100_000_000
	weight := mulDivUint64(storedBytes, proofScore, params.StoredBytesWeightBPS, bpsSquare)
	weight += mulDivUint64(storedBytes, proofScore, params.ProofScoreWeightBPS, bpsSquare)
	weight += mulDivUint64(storedBytes, availabilityScore, params.AvailabilityWeightBPS, bpsSquare)
	weight += mulDivUint64(storedBytes, retrievalSpeedScore, params.RetrievalSpeedWeightBPS, bpsSquare)
	weight += mulDivUint64(storedBytes, ipDispersionScore, params.IPDispersionWeightBPS, bpsSquare)

	// Retrieval obligation penalty: miners who don't participate in
	// retrieval+DHT lose a portion of their weight.
	if !stats.RetrievalObligMet && stats.DHTLastPublishUnix > 0 && params.RetrievalWeightBPS > 0 {
		weight = mulDivUint64(weight, 10000-params.RetrievalWeightBPS, 1, 10000)
	}

	return weight
}

func (s *Store) RecomputeAllMinerWeightsLocked() {
	ipDispersionScores := s.computeIPDispersionScoresLocked()
	for addr, stats := range s.data.Miners {
		s.accrueStorageRewardForMinerLocked(addr)
		stats = s.data.Miners[addr]
		stats.EffectiveWeight = s.computeMinerEffectiveWeightLocked(stats, ipDispersionScores)
		s.data.Miners[addr] = stats
	}
}

// checkDHTObligationsLocked clears RetrievalObligMet for miners whose DHT
// publish records have gone stale. Called during epoch finalization.
func (s *Store) checkDHTObligationsLocked() {
	cutoff := time.Now().Unix() - DHTStalenessSeconds
	for addr, stats := range s.data.Miners {
		if stats.Status == wire.MinerStatusExiting || stats.Status == wire.MinerStatusExited {
			continue
		}
		if stats.DHTLastPublishUnix > 0 && stats.DHTLastPublishUnix < cutoff {
			s.accrueStorageRewardForMinerLocked(addr)
			stats = s.data.Miners[addr]
			stats.RetrievalObligMet = false
			s.data.Miners[addr] = stats
		}
	}
}
