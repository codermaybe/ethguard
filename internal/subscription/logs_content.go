package subscription

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	objectblock "github.com/codermaybe/ethguard/internal/ethobject/block"
	objectlog "github.com/codermaybe/ethguard/internal/ethobject/log"
	objectreceipt "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/rpc"
)

// verifyBlockTransactions uses the existing full-block admission, including the
// transaction root and original declarations. The head is already selected by
// the Head stream; a supplier only proves content beneath that fixed hash.
func verifyBlockTransactions(ctx context.Context, head *types.Header, raw json.RawMessage, call rpc.AdmissionCaller) (types.Transactions, error) {
	if head == nil || head.Number == nil || !head.Number.IsUint64() {
		return nil, fmt.Errorf("ethguard: invalid content header")
	}
	// An exact null response reaches the delivery absence stage; other
	// spellings pass through full-block admission and its header checks.
	if string(raw) == "null" {
		return nil, fmt.Errorf("ethguard: full block has no transaction list: %v", nil)
	}
	req := rpc.Request{Params: []any{head.Hash().Hex(), true}}
	full, err := objectblock.AdmitBlock(ctx, req, raw, true, call)
	if err != nil {
		return nil, err
	}
	txs, err := full.Transactions()
	if err != nil || txs == nil {
		return nil, fmt.Errorf("ethguard: full block has no transaction list: %v", err)
	}
	return txs, nil
}

// proveBlockLogs verifies the complete receipt commitment, then checks every
// log's supplied location against its unique position in that committed block.
// Only log content enters the public result. Receipt sender, fee and other
// uncommitted metadata are never promoted to verified delivery fields.
//
// The returned logs are constructed: timestamp always comes from the header,
// removed is always false, and path retractions carry removal separately.
func proveBlockLogs(head *types.Header, txs types.Transactions, raw json.RawMessage) ([]ethapi.Log, error) {
	if head == nil || head.Number == nil || !head.Number.IsUint64() {
		return nil, fmt.Errorf("ethguard: invalid content header")
	}
	_, receipts, err := proveBlockReceiptRows(head, txs, raw)
	if err != nil {
		return nil, err
	}
	blockHash := ethapi.Hash(head.Hash().Hex())
	blockNumber := ethapi.Quantity(fmt.Sprintf("0x%x", head.Number.Uint64()))
	timestamp := ethapi.Quantity(fmt.Sprintf("0x%x", head.Time))
	logs := make([]ethapi.Log, 0)
	for i, receipt := range receipts {
		txHash := ethapi.Hash(txs[i].Hash().Hex())
		txIndex := ethapi.Quantity(fmt.Sprintf("0x%x", i))
		for _, entry := range receipt {
			index := ethapi.Quantity(fmt.Sprintf("0x%x", len(logs)))
			address, data, removed := ethapi.Address(entry.Address), ethapi.Data(entry.Data), false
			topics := make([]ethapi.Hash, len(entry.Topics))
			for j, topic := range entry.Topics {
				topics[j] = ethapi.Hash(topic)
			}
			// Each delivery owns independent field pointers, even when several
			// logs share a block or transaction.
			bh, bn, ts, ti := blockHash, blockNumber, timestamp, txIndex
			logs = append(logs, ethapi.Log{Address: &address, Topics: &topics, Data: &data, BlockNumber: &bn, TransactionHash: txHash, TransactionIndex: &ti, BlockHash: &bh, LogIndex: &index, Removed: &removed, BlockTimestamp: &ts})
		}
	}
	return logs, nil
}

// proveBlockReceiptRows checks locations across the complete committed batch,
// before either method applies its filter. Canonical rows retain presence.
func proveBlockReceiptRows(head *types.Header, txs types.Transactions, raw json.RawMessage) ([]json.RawMessage, [][]objectlog.CanonicalLog, error) {
	if head == nil || head.Number == nil || !head.Number.IsUint64() {
		return nil, nil, fmt.Errorf("ethguard: invalid content header")
	}
	rows, err := objectreceipt.VerifyBlockRows(head, txs, raw)
	if err != nil {
		return nil, nil, err
	}
	logs := make([][]objectlog.CanonicalLog, len(rows))
	blockHash, blockNumber, timestamp := head.Hash().Hex(), fmt.Sprintf("0x%x", head.Number.Uint64()), fmt.Sprintf("0x%x", head.Time)
	index := 0
	for i, row := range rows {
		txHash, txIndex := txs[i].Hash().Hex(), fmt.Sprintf("0x%x", i)
		var receipt struct {
			Logs []objectlog.CanonicalLog `json:"logs"`
		}
		if err := json.Unmarshal(row, &receipt); err != nil {
			return nil, nil, err
		}
		for _, entry := range receipt.Logs {
			if entry.BlockHash != blockHash || entry.BlockNumber != blockNumber || entry.TransactionHash != txHash || entry.TransactionIndex != txIndex || entry.LogIndex != fmt.Sprintf("0x%x", index) || entry.Removed || (entry.BlockTimestamp != nil && *entry.BlockTimestamp != timestamp) {
				return nil, nil, fmt.Errorf("ethguard: receipt %d log %d disagrees with proven block location", i, index)
			}
			index++
		}
		logs[i] = receipt.Logs
	}
	return rows, logs, nil
}
