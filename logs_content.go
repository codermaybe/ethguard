package ethguard

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	objectlog "github.com/codermaybe/ethguard/internal/ethobject/log"
	objectreceipt "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// verifyLogsBlock uses the existing full-block admission, including the
// transaction root and original declarations. The head is already selected by
// the Head stream; a supplier only proves content beneath that fixed hash.
func verifyLogsBlock(ctx context.Context, head *types.Header, raw json.RawMessage, call rpc.AdmissionCaller) (types.Transactions, error) {
	spec, ok := methods.SpecFor(methods.MethodGetBlockByHash)
	if !ok || spec.Admit == nil || spec.Encode == nil {
		return nil, fmt.Errorf("ethguard: full block admission unavailable")
	}
	if head == nil || head.Number == nil || !head.Number.IsUint64() {
		return nil, fmt.Errorf("ethguard: invalid logs header")
	}
	req := rpc.Request{Method: methods.MethodGetBlockByHash, Params: []any{head.Hash().Hex(), true}}
	if _, err := spec.Admit(ctx, req, raw, call); err != nil {
		return nil, err
	}
	canonical, err := spec.Encode(raw)
	if err != nil {
		return nil, err
	}
	var body struct {
		Transactions types.Transactions `json:"transactions"`
	}
	if err := json.Unmarshal(canonical, &body); err != nil || body.Transactions == nil {
		return nil, fmt.Errorf("ethguard: full logs block has no transaction list: %v", err)
	}
	return body.Transactions, nil
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
		return nil, fmt.Errorf("ethguard: invalid logs header")
	}
	canonical, err := objectreceipt.VerifyBlockCommitment(head, txs, raw)
	if err != nil {
		return nil, err
	}
	var receipts []struct {
		Logs []objectlog.CanonicalLog `json:"logs"`
	}
	if err := json.Unmarshal(canonical, &receipts); err != nil {
		return nil, err
	}
	blockHash := ethapi.Hash(head.Hash().Hex())
	blockNumber := ethapi.Quantity(fmt.Sprintf("0x%x", head.Number.Uint64()))
	timestamp := ethapi.Quantity(fmt.Sprintf("0x%x", head.Time))
	logs := make([]ethapi.Log, 0)
	for i, receipt := range receipts {
		txHash := ethapi.Hash(txs[i].Hash().Hex())
		txIndex := ethapi.Quantity(fmt.Sprintf("0x%x", i))
		for _, entry := range receipt.Logs {
			index := ethapi.Quantity(fmt.Sprintf("0x%x", len(logs)))
			if entry.BlockHash != string(blockHash) || entry.BlockNumber != string(blockNumber) || entry.TransactionHash != string(txHash) || entry.TransactionIndex != string(txIndex) || entry.LogIndex != string(index) || entry.Removed || (entry.BlockTimestamp != nil && *entry.BlockTimestamp != string(timestamp)) {
				return nil, fmt.Errorf("ethguard: receipt %d log %d disagrees with proven block location", i, len(logs))
			}
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
