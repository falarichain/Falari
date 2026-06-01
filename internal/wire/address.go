package wire

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// FalariMainnetEVMChainID is Falari's EIP-155 chain ID for mainnet EVM-style transaction signing.
const FalariMainnetEVMChainID int64 = 209

// FalariTestnetEVMChainID is Falari's EIP-155 chain ID for testnet EVM-style transaction signing.
const FalariTestnetEVMChainID int64 = 219

const DefaultEVMChainID int64 = FalariMainnetEVMChainID

func DefaultEVMChainIDBig() *big.Int {
	return big.NewInt(DefaultEVMChainID)
}

func NormalizeAddress(address string) string {
	if common.IsHexAddress(address) {
		return common.HexToAddress(address).Hex()
	}
	return address
}

// IsValidAddress returns true if the string is a valid 0x-prefixed hex address.
func IsValidAddress(address string) bool {
	return common.IsHexAddress(address)
}
