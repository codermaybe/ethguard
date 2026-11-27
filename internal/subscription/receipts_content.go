package subscription

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
)

type receiptsFilter map[ethapi.Hash]struct{}

func compileReceiptsFilter(query *ethapi.TransactionReceiptsQuery) (receiptsFilter, error) {
	filter := make(receiptsFilter)
	if query != nil && query.TransactionHashes != nil {
		for _, hash := range *query.TransactionHashes {
			quoted, err := json.Marshal(string(hash))
			if err != nil {
				return nil, fmt.Errorf("%w: invalid receipt transaction hash %q", rpc.ErrInvalidRequest, hash)
			}
			canonical, err := ethapi.NewHash(ethcodec.FoldHexSpelling(quoted))
			if err != nil {
				return nil, fmt.Errorf("%w: invalid receipt transaction hash %q", rpc.ErrInvalidRequest, hash)
			}
			filter[canonical] = struct{}{}
		}
	}
	return filter, nil
}

func (filter receiptsFilter) indices(txs types.Transactions) []int {
	indices := make([]int, 0, len(txs))
	for i, tx := range txs {
		_, matches := filter[ethapi.Hash(tx.Hash().Hex())]
		if len(filter) == 0 || matches {
			indices = append(indices, i)
		}
	}
	return indices
}

// proveSelectedReceipts validates the whole committed block before filtering.
// It compares the original canonical official rows, preserving optional and
// nullable fields according to the existing receipt/log encoders.
func proveSelectedReceipts(head *types.Header, txs types.Transactions, indices []int, raw json.RawMessage) (json.RawMessage, error) {
	rows, _, err := proveBlockReceiptRows(head, txs, raw)
	if err != nil {
		return nil, err
	}
	selected := make([]json.RawMessage, len(indices))
	for i, index := range indices {
		selected[i] = rows[index]
	}
	return ethcodec.MarshalNoEscape(selected)
}

func decodeCommittedReceipts(canonical json.RawMessage) ([]ethapi.Receipt, error) {
	var rows []json.RawMessage
	if err := json.Unmarshal(canonical, &rows); err != nil {
		return nil, err
	}
	values := make([]ethapi.Receipt, len(rows))
	for i, row := range rows {
		value, err := ethapi.NewReceipt(row)
		if err != nil {
			return nil, err
		}
		values[i] = value
	}
	return values, nil
}
