package receipt

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/trie"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

// VerifyBlockCommitment checks a complete receipt list against a previously
// verified header and its complete, ordered, transaction-root-verified txs.
// The caller owns those prerequisites: this function does not verify the
// header, transaction root, signatures, fork rules, or chain validity again.
// No input is mutated. Failure returns no canonical value.
//
// Success binds receipt count, order, block/transaction identity and type,
// and verifies the consensus receipt encoding against header.ReceiptHash.
// The returned official JSON retains its original optional-field presence.
// An omitted type is supplied from txs only to the private root calculation;
// an explicitly supplied type must match the corresponding transaction.
//
// This is not full official-field verification. Sender, gasUsed, prices,
// recipient/contract addresses, blob fields and log location/removal metadata
// still need their own evidence. Receipt consensus encoding selects root when
// present, otherwise status; when both are supplied, the extra status remains
// in the canonical JSON but is not proved by this commitment. Existing Encode
// checks still apply, including log/bloom consistency and field shapes.
func VerifyBlockCommitment(header *types.Header, txs types.Transactions, raw json.RawMessage) (json.RawMessage, error) {
	rows, err := VerifyBlockRows(header, txs, raw)
	if err != nil {
		return nil, err
	}
	return ethcodec.MarshalNoEscape(rows)
}

// VerifyBlockRows has the same proof contract as VerifyBlockCommitment. It
// transfers individually canonical rows to content consumers that must check
// locations or filter before assembling their final batch, avoiding an encode
// and immediate reparse of the complete array.
func VerifyBlockRows(header *types.Header, txs types.Transactions, raw json.RawMessage) ([]json.RawMessage, error) {
	if header == nil || header.Number == nil || header.Number.Sign() < 0 {
		return nil, fmt.Errorf("receipt commitment: missing or invalid header")
	}
	if err := jsonsafe.Validate(raw); err != nil {
		return nil, fmt.Errorf("receipt commitment JSON: %w", err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("receipt commitment: expected an array, not null")
	}
	if len(rows) != len(txs) {
		return nil, fmt.Errorf("receipt commitment: receipt count %d does not match transaction count %d", len(rows), len(txs))
	}
	blockHash := header.Hash()
	canonical := make([]json.RawMessage, len(rows))
	receipts := make(types.Receipts, len(rows))
	for i, row := range rows {
		if txs[i] == nil {
			return nil, fmt.Errorf("receipt commitment: transaction %d is nil", i)
		}
		checked, r, err := DecodeWithCanonical(row)
		if err != nil {
			return nil, fmt.Errorf("receipt commitment: receipt %d: %w", i, err)
		}
		if r.BlockHash != blockHash || r.BlockNumber == nil || r.BlockNumber.Cmp(header.Number) != 0 {
			return nil, fmt.Errorf("receipt commitment: receipt %d block identity mismatch", i)
		}
		if r.TxHash != txs[i].Hash() || r.TransactionIndex != uint(i) {
			return nil, fmt.Errorf("receipt commitment: receipt %d transaction identity or order mismatch", i)
		}
		var presence struct {
			Type json.RawMessage `json:"type"`
		}
		if err := json.Unmarshal(checked, &presence); err != nil {
			return nil, fmt.Errorf("receipt commitment: receipt %d type presence: %w", i, err)
		}
		if len(presence.Type) > 0 && r.Type != txs[i].Type() {
			return nil, fmt.Errorf("receipt commitment: receipt %d transaction type mismatch", i)
		}
		r.Type = txs[i].Type()
		canonical[i], receipts[i] = checked, r
	}
	if root := types.DeriveSha(receipts, trie.NewStackTrie(nil)); root != header.ReceiptHash {
		return nil, fmt.Errorf("receipt commitment: receipts root mismatch")
	}
	return canonical, nil
}
