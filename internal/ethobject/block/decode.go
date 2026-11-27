package block

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	objectheader "github.com/codermaybe/ethguard/internal/ethobject/header"
	"github.com/codermaybe/ethguard/rpc"
)

// DecodeWithEvidence checks a direct raw block and constructs its official
// value. It verifies the header hash, private transaction payload grammar,
// empty/non-empty transaction and uncle roots, and supplied uncle evidence.
// It does not prove the complete transaction trie or bind a method request;
// those stronger guarantees belong to AdmitBlock. Root absence and delivery
// error classes remain with the typed caller.
func DecodeWithEvidence(raw, evidence json.RawMessage) (*ethapi.Block, error) {
	head, err := objectheader.Decode(raw)
	if err != nil {
		return nil, decodeError("decode block header", err)
	}
	fields, err := projectHeader(raw)
	if err != nil {
		return nil, decodeError("decode block header", err)
	}
	if err := projectBody(raw, fields); err != nil {
		return nil, decodeError("decode block body", err)
	}
	value, err := parseBlock(fields)
	if err != nil {
		return nil, decodeError("decode block", err)
	}
	// The canonical copy is the re-marshalled re-validated value: the
	// generated constructor resolved equivalent JSON spellings, so the
	// payload bridge consumes normalized transaction bytes — never the
	// original raw spelling.
	normalized, err := ethcodec.MarshalNoEscape(value)
	if err != nil {
		return nil, decodeError("decode block", err)
	}
	var body struct {
		Transactions []json.RawMessage `json:"transactions"`
		UncleHashes  []common.Hash     `json:"uncles"`
	}
	if err := json.Unmarshal(normalized, &body); err != nil {
		return nil, decodeError("decode block body", err)
	}
	// The direct entry validates every private transaction payload,
	// now over the canonical transaction bytes: geth's decoder rejects
	// unsupported transaction types, quantities beyond their width, and
	// malformed signatures — checks the generated schema cannot express.
	// The failure class stays the response class; the trie proof stays with
	// the admission owner.
	for i, item := range body.Transactions {
		if len(item) > 0 && item[0] == '"' {
			return nil, decodeError("hash-only transaction list; request the block with full transactions", nil)
		}
		tx := new(types.Transaction)
		if err := json.Unmarshal(item, tx); err != nil {
			if errors.Is(err, types.ErrTxTypeNotSupported) {
				return nil, errors.Join(rpc.ErrUnsupportedTypedObject, err)
			}
			return nil, decodeError(fmt.Sprintf("decode transaction %d", i), err)
		}
	}
	if head.TxHash == types.EmptyTxsHash && len(body.Transactions) > 0 {
		return nil, decodeError("non-empty transactions for an empty transaction root", nil)
	}
	if head.TxHash != types.EmptyTxsHash && len(body.Transactions) == 0 {
		return nil, decodeError("empty transactions for a non-empty transaction root", nil)
	}
	if head.UncleHash == types.EmptyUncleHash && len(body.UncleHashes) > 0 {
		return nil, decodeError("server returned uncles for an uncle-free header", nil)
	}
	if head.UncleHash != types.EmptyUncleHash && len(body.UncleHashes) == 0 {
		return nil, decodeError("server returned no uncles for a non-empty uncle root", nil)
	}
	if len(body.UncleHashes) > 0 {
		if len(evidence) == 0 {
			return nil, decodeError("uncle headers were not admitted", nil)
		}
		var uncleRaw []json.RawMessage
		if err := json.Unmarshal(evidence, &uncleRaw); err != nil || len(uncleRaw) != len(body.UncleHashes) {
			return nil, decodeError("decode admitted uncle headers", err)
		}
		uncles := make([]*types.Header, len(uncleRaw))
		for i, item := range uncleRaw {
			if err := json.Unmarshal(item, &uncles[i]); err != nil || uncles[i] == nil {
				return nil, decodeError(fmt.Sprintf("decode uncle %d", i), err)
			}
			if uncles[i].Hash() != body.UncleHashes[i] {
				return nil, decodeError(fmt.Sprintf("uncle %d hash mismatch", i), nil)
			}
		}
		if types.CalcUncleHash(uncles) != head.UncleHash {
			return nil, decodeError("uncle root mismatch", nil)
		}
	}
	return &value, nil
}

func decodeError(what string, cause error) error {
	if cause == nil {
		return errors.New(what)
	}
	return fmt.Errorf("%s: %w", what, cause)
}
