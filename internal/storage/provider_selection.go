package storage

import (
	"sort"
	"strings"
	"time"

	"chain/internal/wire"
)

const MinPreferredProviderHealthBPS uint64 = 3000

func RankProvidersForBlockFetch(providers []wire.StorageProviderRecord) []wire.StorageProviderRecord {
	ranked := append([]wire.StorageProviderRecord(nil), providers...)
	now := time.Now().Unix()
	sort.SliceStable(ranked, func(i, j int) bool {
		leftMemory, leftHasMemory := providerTransportMemory(ranked[i])
		rightMemory, rightHasMemory := providerTransportMemory(ranked[j])
		leftCooldown := leftHasMemory && leftMemory.CooldownUntilUnix > now
		rightCooldown := rightHasMemory && rightMemory.CooldownUntilUnix > now
		if leftCooldown != rightCooldown {
			return !leftCooldown
		}
		if leftCooldown && rightCooldown && leftMemory.CooldownUntilUnix != rightMemory.CooldownUntilUnix {
			return leftMemory.CooldownUntilUnix < rightMemory.CooldownUntilUnix
		}
		leftSuccess := leftHasMemory && leftMemory.LastOutcome == "success" && leftMemory.LastSuccessUnix > 0
		rightSuccess := rightHasMemory && rightMemory.LastOutcome == "success" && rightMemory.LastSuccessUnix > 0
		if leftSuccess != rightSuccess {
			return leftSuccess
		}
		if leftSuccess && rightSuccess && leftMemory.LastSuccessUnix != rightMemory.LastSuccessUnix {
			return leftMemory.LastSuccessUnix > rightMemory.LastSuccessUnix
		}
		if leftMemory.ConsecutiveFailures != rightMemory.ConsecutiveFailures {
			return leftMemory.ConsecutiveFailures < rightMemory.ConsecutiveFailures
		}
		leftPreferred := providerMeetsPreferredHealth(ranked[i])
		rightPreferred := providerMeetsPreferredHealth(ranked[j])
		if leftPreferred != rightPreferred {
			return leftPreferred
		}
		leftP2P := providerSupportsP2P(ranked[i])
		rightP2P := providerSupportsP2P(ranked[j])
		if leftP2P != rightP2P {
			return leftP2P
		}
		if ranked[i].HealthScoreBPS != ranked[j].HealthScoreBPS {
			return ranked[i].HealthScoreBPS > ranked[j].HealthScoreBPS
		}
		if ranked[i].ProviderRecordLive != ranked[j].ProviderRecordLive {
			return ranked[i].ProviderRecordLive
		}
		leftHTTP := ranked[i].Endpoint != ""
		rightHTTP := ranked[j].Endpoint != ""
		if leftHTTP != rightHTTP {
			return leftHTTP
		}
		return ranked[i].MinerAddress < ranked[j].MinerAddress
	})
	return ranked
}

func ResolveProviderRecordForEndpoint(endpoint string, minerAddress string, providers []wire.StorageProviderRecord) wire.StorageProviderRecord {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	for _, provider := range providers {
		if strings.TrimRight(provider.Endpoint, "/") == endpoint && endpoint != "" {
			return provider
		}
	}
	for _, provider := range providers {
		if minerAddress != "" && provider.MinerAddress == minerAddress {
			if endpoint != "" && provider.Endpoint == "" {
				provider.Endpoint = endpoint
			}
			return provider
		}
	}
	return wire.StorageProviderRecord{
		MinerAddress: minerAddress,
		Endpoint:     endpoint,
	}
}

func PreferredProviderEndpoints(primaryEndpoint string, providers []wire.StorageProviderRecord) []string {
	seen := map[string]bool{}
	endpoints := make([]string, 0, len(providers)+1)
	if endpoint := strings.TrimRight(primaryEndpoint, "/"); endpoint != "" {
		endpoints = append(endpoints, endpoint)
		seen[endpoint] = true
	}
	for _, provider := range RankProvidersForBlockFetch(providers) {
		endpoint := strings.TrimRight(provider.Endpoint, "/")
		if endpoint == "" || seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func providerSupportsP2P(provider wire.StorageProviderRecord) bool {
	return provider.PeerID != "" && len(provider.PeerAddrs) > 0
}

func providerMeetsPreferredHealth(provider wire.StorageProviderRecord) bool {
	return provider.HealthScoreBPS == 0 || provider.HealthScoreBPS >= MinPreferredProviderHealthBPS
}
