package storage

import (
	"testing"

	"chain/internal/wire"
)

func TestRankProvidersForBlockFetchPrefersP2PAndHealth(t *testing.T) {
	resetProviderTransportMemoryForTests()
	providers := []wire.StorageProviderRecord{
		{
			MinerAddress:       "miner_http_only",
			Endpoint:           "http://http-only",
			HealthScoreBPS:     9000,
			ProviderRecordLive: true,
		},
		{
			MinerAddress:       "miner_p2p_best",
			Endpoint:           "http://p2p-best",
			PeerID:             "12D3KooWbest",
			PeerAddrs:          []string{"/ip4/127.0.0.1/tcp/7000/p2p/12D3KooWbest"},
			HealthScoreBPS:     9200,
			ProviderRecordLive: true,
		},
		{
			MinerAddress:       "miner_p2p_low",
			Endpoint:           "http://p2p-low",
			PeerID:             "12D3KooWlow",
			PeerAddrs:          []string{"/ip4/127.0.0.1/tcp/7001/p2p/12D3KooWlow"},
			HealthScoreBPS:     2000,
			ProviderRecordLive: true,
		},
	}

	ranked := RankProvidersForBlockFetch(providers)
	if len(ranked) != 3 {
		t.Fatalf("unexpected ranked providers: %+v", ranked)
	}
	if ranked[0].MinerAddress != "miner_p2p_best" {
		t.Fatalf("expected p2p healthy provider first, got %+v", ranked)
	}
	if ranked[2].MinerAddress != "miner_p2p_low" {
		t.Fatalf("expected low-health provider last, got %+v", ranked)
	}
}

func TestRankProvidersForBlockFetchPrefersRecentSuccess(t *testing.T) {
	resetProviderTransportMemoryForTests()
	successProvider := wire.StorageProviderRecord{
		MinerAddress:       "miner_recent_success",
		Endpoint:           "http://recent-success",
		PeerID:             "12D3KooWrecent",
		PeerAddrs:          []string{"/ip4/127.0.0.1/tcp/7100/p2p/12D3KooWrecent"},
		HealthScoreBPS:     3500,
		ProviderRecordLive: true,
	}
	healthierProvider := wire.StorageProviderRecord{
		MinerAddress:       "miner_healthier",
		Endpoint:           "http://healthier",
		PeerID:             "12D3KooWhealthier",
		PeerAddrs:          []string{"/ip4/127.0.0.1/tcp/7101/p2p/12D3KooWhealthier"},
		HealthScoreBPS:     9800,
		ProviderRecordLive: true,
	}

	RememberProviderFetchSuccess(successProvider, "libp2p")
	ranked := RankProvidersForBlockFetch([]wire.StorageProviderRecord{healthierProvider, successProvider})
	if ranked[0].MinerAddress != successProvider.MinerAddress {
		t.Fatalf("expected recent success provider first, got %+v", ranked)
	}
}

func TestRankProvidersForBlockFetchDeprioritizesCooldownProviders(t *testing.T) {
	resetProviderTransportMemoryForTests()
	stableProvider := wire.StorageProviderRecord{
		MinerAddress:       "miner_stable",
		Endpoint:           "http://stable",
		PeerID:             "12D3KooWstable",
		PeerAddrs:          []string{"/ip4/127.0.0.1/tcp/7200/p2p/12D3KooWstable"},
		HealthScoreBPS:     4000,
		ProviderRecordLive: true,
	}
	coolingProvider := wire.StorageProviderRecord{
		MinerAddress:       "miner_cooling",
		Endpoint:           "http://cooling",
		PeerID:             "12D3KooWcooling",
		PeerAddrs:          []string{"/ip4/127.0.0.1/tcp/7201/p2p/12D3KooWcooling"},
		HealthScoreBPS:     9900,
		ProviderRecordLive: true,
	}

	RememberProviderFetchFailure(coolingProvider, "libp2p", errTestProviderFetch)
	RememberProviderFetchFailure(coolingProvider, "libp2p", errTestProviderFetch)
	ranked := RankProvidersForBlockFetch([]wire.StorageProviderRecord{coolingProvider, stableProvider})
	if ranked[0].MinerAddress != stableProvider.MinerAddress {
		t.Fatalf("expected cooldown provider to be deprioritized, got %+v", ranked)
	}
}

var errTestProviderFetch = providerSelectionTestError("provider fetch failed")

type providerSelectionTestError string

func (e providerSelectionTestError) Error() string {
	return string(e)
}
