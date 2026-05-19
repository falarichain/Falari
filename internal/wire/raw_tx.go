package wire

import (
	"crypto/ecdsa"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
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

func EncodeNativeTransferRawTx(req TransferRequest, privateKey *ecdsa.PrivateKey, chainID *big.Int) (string, error) {
	if req.To == "" {
		return "", errors.New("raw transfer requires recipient")
	}
	if !common.IsHexAddress(req.To) {
		return "", errors.New("raw transfer recipient must be an Ethereum address")
	}
	fee := req.Fee
	if fee == 0 {
		fee = 1
	}
	to := common.HexToAddress(req.To)
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    req.Nonce,
		To:       &to,
		Value:    new(big.Int).SetUint64(req.Amount),
		Gas:      21000,
		GasPrice: new(big.Int).SetUint64(fee),
	})
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		return "", err
	}
	raw, err := signedTx.MarshalBinary()
	if err != nil {
		return "", err
	}
	return encodeHex(raw), nil
}

func DecodeNativeTransferRawTx(rawTx string, chainID *big.Int) (TransferRequest, error) {
	raw, err := decodeHex(rawTx)
	if err != nil {
		return TransferRequest{}, err
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return TransferRequest{}, err
	}
	if tx.To() == nil {
		return TransferRequest{}, errors.New("contract creation raw transactions are not supported")
	}
	if len(tx.Data()) != 0 {
		return TransferRequest{}, errors.New("raw transaction data is not supported for native transfer")
	}
	if !tx.Value().IsUint64() {
		return TransferRequest{}, errors.New("raw transaction value overflows uint64")
	}
	if tx.GasPrice() == nil || !tx.GasPrice().IsUint64() {
		return TransferRequest{}, errors.New("raw transaction fee overflows uint64")
	}
	from, err := types.Sender(types.LatestSignerForChainID(chainID), &tx)
	if err != nil {
		return TransferRequest{}, err
	}
	return TransferRequest{
		From:   from.Hex(),
		To:     tx.To().Hex(),
		Amount: tx.Value().Uint64(),
		Nonce:  tx.Nonce(),
		Fee:    tx.GasPrice().Uint64(),
		RawTx:  rawTx,
	}, nil
}

func RawTransactionHash(rawTx string) (string, error) {
	raw, err := decodeHex(rawTx)
	if err != nil {
		return "", err
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return "", err
	}
	return tx.Hash().Hex(), nil
}
