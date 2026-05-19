package chain

import (
	"errors"
	"math"
	"math/big"

	"chain/internal/wire"
)

const storageQuoteMonthSeconds = int64(30 * 24 * 60 * 60)
const storageQuoteGiB = uint64(1 << 30)

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
	if pricing.BasePricePerGiBMonth == 0 {
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
	billableGiBMonths, err := billableStorageGiBMonths(redundantBytes, duration)
	if err != nil {
		return wire.StorageQuoteResponse{}, err
	}
	activeCapacity, activeUsed := s.activeStorageCapacityLocked()
	utilizationBPS := utilizationBPS(activeUsed, activeCapacity)
	multiplier := storageUtilizationMultiplier(utilizationBPS)
	requiredFee, err := quoteRequiredFee(billableGiBMonths, pricing.BasePricePerGiBMonth, multiplier, pricing.MinimumFee)
	if err != nil {
		return wire.StorageQuoteResponse{}, err
	}
	return wire.StorageQuoteResponse{
		Pricing:               pricing,
		FileSize:              req.FileSize,
		RedundantBytes:        redundantBytes,
		Duration:              duration,
		BillableGiBMonths:     billableGiBMonths,
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

func billableStorageGiBMonths(redundantBytes uint64, duration int64) (uint64, error) {
	if duration <= 0 {
		return 0, errors.New("storage duration must be positive")
	}
	numerator := new(big.Int).SetUint64(redundantBytes)
	numerator.Mul(numerator, big.NewInt(duration))
	denominator := new(big.Int).SetUint64(storageQuoteGiB)
	denominator.Mul(denominator, big.NewInt(storageQuoteMonthSeconds))
	value := ceilBigDiv(numerator, denominator)
	if value.Sign() == 0 {
		return 1, nil
	}
	if !value.IsUint64() {
		return 0, errors.New("billable storage overflows uint64")
	}
	return value.Uint64(), nil
}

func quoteRequiredFee(billableGiBMonths uint64, basePrice uint64, multiplier uint64, minimum uint64) (uint64, error) {
	value := new(big.Int).SetUint64(billableGiBMonths)
	value.Mul(value, new(big.Int).SetUint64(basePrice))
	value.Mul(value, new(big.Int).SetUint64(multiplier))
	value = ceilBigDiv(value, big.NewInt(10_000))
	if !value.IsUint64() {
		return 0, errors.New("storage fee overflows uint64")
	}
	required := value.Uint64()
	if required < minimum {
		required = minimum
	}
	return required, nil
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
	case utilization >= 9_000:
		return 20_000
	case utilization >= 8_000:
		return 15_000
	case utilization >= 5_000:
		return 10_000
	case utilization > 0:
		return 9_000
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
