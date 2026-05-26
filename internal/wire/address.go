package wire

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

const DefaultEVMChainID int64 = 31337

func DefaultEVMChainIDBig() *big.Int {
	return big.NewInt(DefaultEVMChainID)
}

func NormalizeAddress(address string) string {
	if common.IsHexAddress(address) {
		return common.HexToAddress(address).Hex()
	}
	return address
}
