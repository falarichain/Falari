package chain

import (
	"encoding/json"
	"sort"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

func (s *Store) TransactionReceipt(hash string) (wire.TransactionReceipt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.data.Receipts[hash]
	return receipt, ok
}

func (s *Store) prepareReceiptsForBlockLocked(block *wire.Block) {
	for txIndex, tx := range block.Transactions {
		hash := tx.TxID
		receipt := s.data.Receipts[hash]
		if receipt.TransactionHash == "" {
			receipt = defaultReceiptForTransaction(tx)
		}
		receipt.TransactionHash = hash
		receipt.TransactionIndex = uint64(txIndex)
		receipt.BlockNumber = block.Height
		receipt.BlockHash = block.Hash
		s.data.Receipts[hash] = receipt
	}
}

func defaultReceiptForTransaction(tx wire.Transaction) wire.TransactionReceipt {
	return wire.TransactionReceipt{
		TransactionHash: tx.TxID,
		From:            tx.From,
	}
}

func (s *Store) receiptsRootForBlockLocked(block wire.Block) string {
	leaves := make([]string, 0, len(block.Transactions))
	for _, tx := range block.Transactions {
		receipt := s.data.Receipts[tx.TxID]
		leaves = append(leaves, receiptLeaf(receipt))
	}
	return chaincrypto.MerkleRoot(leaves)
}

func receiptLeaf(receipt wire.TransactionReceipt) string {
	normalized := struct {
		TransactionHash  string `json:"transaction_hash"`
		TransactionIndex uint64 `json:"transaction_index"`
		From             string `json:"from,omitempty"`
	}{
		TransactionHash:  receipt.TransactionHash,
		TransactionIndex: receipt.TransactionIndex,
		From:             receipt.From,
	}
	raw, _ := json.Marshal(normalized)
	return chaincrypto.HashBytes(raw)
}

func sortedNormalizedAddresses(accounts map[string]wire.Account) []string {
	addresses := make([]string, 0, len(accounts))
	for address := range accounts {
		addresses = append(addresses, wire.NormalizeAddress(address))
	}
	sort.Strings(addresses)
	return addresses
}
