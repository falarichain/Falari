package chain

import (
	"errors"
	"math"
	"math/big"

	"chain/internal/wire"
)

const storageQuoteBasePeriodDays = int64(300)
const storageQuoteMiB = uint64(1 << 20)

func (s *Store) StorageQuote(req wire.StorageQuoteRequest) (wire.StorageQuoteResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.storageQuoteLocked(req)
}

func (s *Store) storageQuoteForIntentLocked(req wire.CreateIntentRequest) (wire.StorageQuoteResponse, error) {
	return s.storageQuoteLocked(wire.StorageQuoteRequest{
		FileSize: req.FileSize,
		Erasure:  req.Erasure,
		Policy:   req.Policy,
	})
}

func (s *Store) storageQuoteLocked(req wire.StorageQuoteRequest) (wire.StorageQuoteResponse, error) {
	if req.FileSize <= 0 {
		return wire.StorageQuoteResponse{}, errors.New("file size must be positive")
	}
	if req.Erasure.DataShards <= 0 || req.Erasure.ParityShards < 0 {
		return wire.StorageQuoteResponse{}, errors.New("invalid erasure policy")
	}
	pricing := s.data.StoragePricing
	if pricing.BasePrice == 0 {
		pricing = defaultStoragePricing()
	}
	if pricing.MinimumFee == 0 {
		pricing.MinimumFee = defaultStorageMinimumFee
	}
	if pricing.PermanentDuration == 0 {
		pricing.PermanentDuration = defaultPermanentStorageDuration
	}
	duration := req.Policy.Duration
	if duration <= 0 {
		duration = pricing.PermanentDuration
	}
	redundantBytes, err := redundantStorageBytes(req.FileSize, req.Erasure)
	if err != nil {
		return wire.StorageQuoteResponse{}, err
	}
	mibDays, err := totalStorageMiBDays(redundantBytes, duration)
	if err != nil {
		return wire.StorageQuoteResponse{}, err
	}
	storageFee, err := computeStorageFee(redundantBytes, duration, pricing.BasePrice)
	if err != nil {
		return wire.StorageQuoteResponse{}, err
	}
	activeCapacity, activeUsed := s.activeStorageCapacityLocked()
	utilizationBPS := utilizationBPS(activeUsed, activeCapacity)
	multiplier := storageUtilizationMultiplier(utilizationBPS)
	requiredFee := applyMultiplierAndMinimum(storageFee, multiplier, pricing.MinimumFee)
	return wire.StorageQuoteResponse{
		Pricing:               pricing,
		FileSize:              req.FileSize,
		RedundantBytes:        redundantBytes,
		Duration:              duration,
		TotalMiBDays:          mibDays,
		UtilizationBPS:        utilizationBPS,
		UtilizationMultiplier: multiplier,
		RequiredFee:           requiredFee,
		ActiveCapacityBytes:   activeCapacity,
		ActiveUsedBytes:       activeUsed,
	}, nil
}

func redundantStorageBytes(fileSize int64, erasure wire.ErasurePolicy) (uint64, error) {
	if fileSize <= 0 {
		return 0, errors.New("file size must be positive")
	}
	totalShards := erasure.DataShards + erasure.ParityShards
	if erasure.DataShards <= 0 || totalShards <= 0 {
		return 0, errors.New("invalid erasure policy")
	}
	value := new(big.Int).Mul(big.NewInt(fileSize), big.NewInt(int64(totalShards)))
	value = ceilBigDiv(value, big.NewInt(int64(erasure.DataShards)))
	if !value.IsUint64() {
		return 0, errors.New("redundant storage size overflows uint64")
	}
	return value.Uint64(), nil
}

// totalStorageMiBDays computes the total MiB·days for the given redundant
// bytes and duration in seconds.
func totalStorageMiBDays(redundantBytes uint64, duration int64) (uint64, error) {
	if duration <= 0 {
		return 0, errors.New("storage duration must be positive")
	}
	redundantMiB := ceilBigDiv(
		new(big.Int).SetUint64(redundantBytes),
		new(big.Int).SetUint64(storageQuoteMiB),
	)
	total := new(big.Int).Mul(redundantMiB, big.NewInt(duration/(24*60*60)))
	if !total.IsUint64() {
		return 0, errors.New("total MiB-days overflows uint64")
	}
	result := total.Uint64()
	if result == 0 {
		return 1, nil
	}
	return result, nil
}

// computeStorageFee calculates the storage fee using flat-rate pricing.
//
// Base rate: basePrice tokens per MiB per 300 days.
// fee = ceil(redundantMiB × basePrice × totalDays / basePeriodDays)
func computeStorageFee(redundantBytes uint64, duration int64, basePrice uint64) (uint64, error) {
	if duration <= 0 {
		return 0, errors.New("storage duration must be positive")
	}
	totalDays := duration / (24 * 60 * 60)
	if totalDays <= 0 {
		totalDays = 1
	}
	redundantMiB := ceilBigDiv(
		new(big.Int).SetUint64(redundantBytes),
		new(big.Int).SetUint64(storageQuoteMiB),
	).Uint64()
	if redundantMiB == 0 {
		redundantMiB = 1
	}

	num := new(big.Int).SetUint64(redundantMiB)
	num.Mul(num, new(big.Int).SetUint64(basePrice))
	num.Mul(num, new(big.Int).SetInt64(totalDays))
	den := new(big.Int).SetInt64(storageQuoteBasePeriodDays)
	fee := ceilBigDiv(num, den)
	if !fee.IsUint64() {
		return 0, errors.New("storage fee overflows uint64")
	}
	result := fee.Uint64()
	if result == 0 {
		result = 1
	}
	return result, nil
}

// applyMultiplierAndMinimum applies the utilization multiplier and enforces
// the minimum fee floor.
func applyMultiplierAndMinimum(fee uint64, multiplier uint64, minimum uint64) uint64 {
	value := new(big.Int).SetUint64(fee)
	value.Mul(value, new(big.Int).SetUint64(multiplier))
	value = ceilBigDiv(value, big.NewInt(10_000))
	if !value.IsUint64() {
		return math.MaxUint64
	}
	required := value.Uint64()
	if required < minimum {
		required = minimum
	}
	return required
}

func (s *Store) activeStorageCapacityLocked() (uint64, uint64) {
	var capacity uint64
	var used uint64
	for _, miner := range s.data.Miners {
		if miner.Status != "active" {
			continue
		}
		capacity = saturatingAdd(capacity, miner.CapacityBytes)
		used = saturatingAdd(used, saturatingAdd(miner.UsedBytes, miner.ReservedBytes))
	}
	return capacity, used
}

func utilizationBPS(used uint64, capacity uint64) uint64 {
	if capacity == 0 || used == 0 {
		return 0
	}
	value := new(big.Int).SetUint64(used)
	value.Mul(value, big.NewInt(10_000))
	value.Div(value, new(big.Int).SetUint64(capacity))
	if !value.IsUint64() {
		return math.MaxUint64
	}
	return value.Uint64()
}

func storageUtilizationMultiplier(utilization uint64) uint64 {
	switch {
	case utilization >= 7_000:
		return 15_000 // ≥ 70% → 1.5x
	case utilization >= 5_000:
		return 10_000 // 50%~70% → 1.0x
	case utilization >= 3_000:
		return 7_000 // 30%~50% → 0.7x
	case utilization > 0:
		return 5_000 // < 30% → 0.5x
	default:
		return 10_000
	}
}

func ceilBigDiv(numerator *big.Int, denominator *big.Int) *big.Int {
	if denominator.Sign() <= 0 {
		return big.NewInt(0)
	}
	quotient, remainder := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

func saturatingAdd(left uint64, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}
