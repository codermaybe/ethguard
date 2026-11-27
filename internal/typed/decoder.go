// Package typed owns the single production projection from canonical RPC
// values to public business values. Every business family projects onto the
// generated ethapi models; the object owners keep structure, integrity and
// request binding, and this package re-validates and freezes the public
// values at the typed boundary. Facades share these decoders rather than
// maintaining parallel implementations.
package typed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	blockobject "github.com/codermaybe/ethguard/internal/ethobject/block"
	headerobject "github.com/codermaybe/ethguard/internal/ethobject/header"
	"github.com/codermaybe/ethguard/internal/ethobject/hexcompat"
	receiptobject "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/rpc"
)

// DecodeBlock projects one caller-supplied raw block response onto the
// official generated Block. This is the direct raw entry: the bytes have not
// been through the object admission, so the checks this entry has always
// owned ride here — the declared wire hash against the recomputed header
// hash (through the shared header kernel), the hash-only transaction-list
// rejection, the empty/non-empty transaction-root consistency, the
// uncle-list consistency and, when uncle header responses are supplied as
// same-source evidence, their per-uncle hash and root commitments. This
// entry does not independently re-verify the whole transaction trie; that
// proof belongs to the admission owner.
func DecodeBlock(raw json.RawMessage) (*ethapi.Block, error) {
	return DecodeBlockWithEvidence(raw, nil)
}

// DecodeBlockWithEvidence is the direct raw entry with same-source uncle
// evidence supplied by the caller. Evidence carries the admitted uncle
// header responses; a block whose uncle list is non-empty without evidence
// is rejected rather than accepted on faith.
//
// The order is fixed by the frozen input contract: the generated model
// re-validates and normalizes FIRST (composed exactly like the protected
// block encoder), and only then does the private per-transaction payload
// bridge consume the canonical transaction bytes re-marshalled from that
// re-validated value — never the original spelling, so generated-accepted
// equivalent JSON spellings are not re-judged by a narrower wire grammar.
func DecodeBlockWithEvidence(raw json.RawMessage, evidence json.RawMessage) (*ethapi.Block, error) {
	if jsonNull(raw) {
		return nil, ethereum.NotFound
	}
	value, err := blockobject.DecodeWithEvidence(raw, evidence)
	if err != nil && !errors.Is(err, rpc.ErrUnsupportedTypedObject) {
		return nil, fmt.Errorf("%w: %w", rpc.ErrMalformedResponse, err)
	}
	return value, err
}

// DecodeBlockAdmitted projects a value that has already been through the
// object admission onto the public Block. The owner verified the header
// self-hash, the transaction trie, withdrawals and the same-source uncle
// evidence, and the canonical committed bytes carry the declared members
// only — uncle header bodies are intentionally not part of the value, so
// re-running the direct-entry checks here would demand material the admitted
// projection does not carry and would reject legitimate admitted historical
// blocks. The generated constructor is the remaining boundary work.
func DecodeBlockAdmitted(raw json.RawMessage) (*ethapi.Block, error) {
	if jsonNull(raw) {
		return nil, ethereum.NotFound
	}
	value, err := ethapi.NewBlock(raw)
	if err != nil {
		return nil, wrapMalformed("decode block", err)
	}
	return &value, nil
}

// DecodeBlockHeaderOnly decodes the header returned by a fullTx=false block
// query. The header kernel checks the declared wire hash against the
// recomputed one before any geth representation exists; the official Header
// members are then projected explicitly and parsed by the generated
// constructor. Transaction and uncle bodies are intentionally absent;
// callers must not present this value as a fully content-corroborated block.
func DecodeBlockHeaderOnly(raw json.RawMessage) (*ethapi.Header, error) {
	if jsonNull(raw) {
		return nil, ethereum.NotFound
	}
	canonical, err := headerobject.Normalize(raw)
	if err != nil {
		return nil, wrapMalformed("decode block header", err)
	}
	header, err := ethapi.NewHeader(canonical)
	if err != nil {
		return nil, wrapMalformed("decode block header", err)
	}
	return &header, nil
}

// DecodeReceipt projects the shared checked receipt representation onto the
// official generated Receipt. The object owner keeps requiredness, status
// and root presence, log bindings and bloom consistency; the projection
// re-parses the canonical bytes so absence and explicit null stay distinct.
func DecodeReceipt(raw json.RawMessage) (*ethapi.Receipt, error) {
	if jsonNull(raw) {
		return nil, ethereum.NotFound
	}
	canonical, err := receiptobject.Encode(raw)
	if err != nil {
		return nil, wrapMalformed("decode receipt", err)
	}
	value, err := ethapi.NewReceipt(canonical)
	if err != nil {
		return nil, wrapMalformed("decode receipt", err)
	}
	return &value, nil
}

// DecodeReceiptAdmitted projects only a successful receipt read's committed
// canonical value. Its owner already checked log bindings, bloom, status and
// request identity. Field aggregation adds diagnostics without rebuilding this
// value. Check the final bytes' safety budget because encoding cannot inherit
// the original response's byte budget, then construct the public model.
func DecodeReceiptAdmitted(raw json.RawMessage) (*ethapi.Receipt, error) {
	if jsonNull(raw) {
		return nil, ethereum.NotFound
	}
	if err := jsonsafe.Validate(raw); err != nil {
		return nil, wrapMalformed("decode receipt", fmt.Errorf("receipt JSON: %w", err))
	}
	value, err := ethapi.NewReceipt(raw)
	if err != nil {
		return nil, wrapMalformed("decode receipt", err)
	}
	return &value, nil
}

// DecodeReceiptsAdmitted is the batch counterpart of DecodeReceiptAdmitted.
// Only a successful block-receipt read may use it; raw and subscription
// payloads still require DecodeReceipts or DecodeReceiptBatch.
func DecodeReceiptsAdmitted(raw json.RawMessage) ([]ethapi.Receipt, error) {
	if jsonNull(raw) {
		return nil, ethereum.NotFound
	}
	if err := jsonsafe.Validate(raw); err != nil {
		return nil, wrapMalformed("decode receipts", fmt.Errorf("receipt JSON: %w", err))
	}
	return projectReceiptRows(raw)
}

// DecodeReceipts keeps null/NotFound distinct from a non-nil empty array.
// Each element uses the same object checks as queries and subscription events.
func DecodeReceipts(raw json.RawMessage) ([]ethapi.Receipt, error) {
	values, _, err := DecodeReceiptBatch(raw)
	return values, err
}

// DecodeReceiptBatch retains the checked official bytes alongside the public
// projection. Subscription keys and Pull filtering must use those bytes:
// encoding a projection back to JSON would discard required sender/context
// information.
func DecodeReceiptBatch(raw json.RawMessage) ([]ethapi.Receipt, json.RawMessage, error) {
	if jsonNull(raw) {
		return nil, nil, ethereum.NotFound
	}
	canonical, err := receiptobject.EncodeBlock(raw)
	if err != nil {
		return nil, nil, wrapMalformed("decode receipts", err)
	}
	values, err := projectReceiptRows(canonical)
	if err != nil {
		return nil, nil, err
	}
	return values, canonical, nil
}

func projectReceiptRows(canonical json.RawMessage) ([]ethapi.Receipt, error) {
	var rows []json.RawMessage
	if err := json.Unmarshal(canonical, &rows); err != nil {
		return nil, wrapMalformed("decode receipts", err)
	}
	values := make([]ethapi.Receipt, 0, len(rows))
	for i, row := range rows {
		value, err := ethapi.NewReceipt(row)
		if err != nil {
			return nil, wrapMalformed(fmt.Sprintf("decode receipts[%d]", i), err)
		}
		values = append(values, value)
	}
	return values, nil
}

// DecodeTransaction shapes one corroborated eth_getTransactionByHash response
// into the official generated transaction union, reporting whether the fleet
// saw it as still pending (no block number on the wire). The generated
// constructor owns the official branch schema; the private payload bridge
// then re-checks, on the constructor's canonical copy, that the declared hash
// equals the hash recomputed from the decoded signature (the
// transaction-side analogue of the block wire-hash check) and that an
// unsigned payload is rejected — honest sources only hold signed
// transactions.
func DecodeTransaction(raw json.RawMessage) (*ethapi.Transaction, bool, error) {
	if jsonNull(raw) {
		return nil, false, ethereum.NotFound
	}
	candidate, err := hexcompat.Transaction(raw)
	if err != nil {
		return nil, false, wrapMalformed("decode transaction", err)
	}
	union, err := ethapi.NewTransaction(candidate)
	if err != nil {
		return nil, false, wrapMalformed("decode transaction", err)
	}
	declared, err := TransactionHash(&union)
	if err != nil {
		return nil, false, wrapMalformed("decode transaction", err)
	}
	canonical, err := ethcodec.MarshalNoEscape(union)
	if err != nil {
		return nil, false, wrapMalformed("decode transaction", err)
	}
	if _, err := transactionWire(canonical, declared); err != nil {
		return nil, false, err
	}
	var context struct {
		BlockHash        *common.Hash    `json:"blockHash"`
		BlockNumber      json.RawMessage `json:"blockNumber"`
		TransactionIndex json.RawMessage `json:"transactionIndex"`
	}
	if err := json.Unmarshal(canonical, &context); err != nil {
		return nil, false, wrapMalformed("decode transaction context", err)
	}
	blockNumberSet := len(bytes.TrimSpace(context.BlockNumber)) > 0 && string(bytes.TrimSpace(context.BlockNumber)) != "null"
	transactionIndexSet := len(bytes.TrimSpace(context.TransactionIndex)) > 0 && string(bytes.TrimSpace(context.TransactionIndex)) != "null"
	if context.BlockHash != nil && (!blockNumberSet || !transactionIndexSet) {
		return nil, false, wrapMalformed("transaction incomplete inclusion context", nil)
	}
	return &union, !blockNumberSet, nil
}

// DecodedTransaction preserves the inclusion metadata needed by
// TransactionInBlock and TransactionSender. The public transaction value is
// the generated union; metadata stays inside this typed boundary so it can
// be validated before projection.
type DecodedTransaction struct {
	Tx               *ethapi.Transaction
	TxHash           ethapi.Hash
	From             ethapi.Address
	HasFrom          bool
	BlockHash        ethapi.Hash
	HasBlockHash     bool
	TransactionIndex uint64
	HasIndex         bool
}

// DecodeTransactionInBlock decodes one transaction returned by
// eth_getTransactionByBlockHashAndIndex and binds its inclusion context to
// the requested block and index. The by-block/index contract keeps its own
// non-pending admission; the generated union is only the public projection.
func DecodeTransactionInBlock(raw json.RawMessage, blockHash ethapi.Hash, index uint64) (*DecodedTransaction, error) {
	if jsonNull(raw) {
		return nil, ethereum.NotFound
	}
	tx, _, err := DecodeTransaction(raw)
	if err != nil {
		return nil, err
	}
	declared, err := TransactionHash(tx)
	if err != nil {
		return nil, wrapMalformed("decode transaction inclusion context", err)
	}
	var wire struct {
		From             *string         `json:"from"`
		BlockHash        *string         `json:"blockHash"`
		TransactionIndex json.RawMessage `json:"transactionIndex"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, wrapMalformed("decode transaction inclusion context", err)
	}
	if wire.From == nil {
		return nil, wrapMalformed("transaction missing sender", nil)
	}
	fromRaw, err := json.Marshal(*wire.From)
	if err != nil {
		return nil, wrapMalformed("transaction sender", err)
	}
	from, err := ethapi.NewAddress(ethcodec.FoldHexSpelling(fromRaw))
	if err != nil {
		return nil, wrapMalformed("transaction sender", err)
	}
	if wire.BlockHash == nil || *wire.BlockHash != string(blockHash) {
		return nil, wrapMalformed("transaction block hash does not match request", nil)
	}
	if len(bytes.TrimSpace(wire.TransactionIndex)) == 0 || string(bytes.TrimSpace(wire.TransactionIndex)) == "null" {
		return nil, wrapMalformed("transaction missing index", nil)
	}
	canonical, err := normalizeQuantity(wire.TransactionIndex)
	if err != nil {
		return nil, wrapMalformed("decode transaction index", err)
	}
	got, ok := new(big.Int).SetString(canonical, 10)
	if !ok || !got.IsUint64() || got.Uint64() != index {
		return nil, wrapMalformed("transaction index does not match request", nil)
	}
	return &DecodedTransaction{
		Tx:               tx,
		TxHash:           declared,
		From:             from,
		HasFrom:          true,
		BlockHash:        blockHash,
		HasBlockHash:     true,
		TransactionIndex: index,
		HasIndex:         true,
	}, nil
}

// TransactionHash returns the declared hash of the selected public union
// branch. Every branch carries exactly one hash member; a zero-selected or
// multi-selected directly constructed value is rejected here instead of
// silently projecting the first branch.
func TransactionHash(tx *ethapi.Transaction) (ethapi.Hash, error) {
	if tx == nil {
		return "", fmt.Errorf("nil transaction")
	}
	selected := 0
	var declared ethapi.Hash
	if tx.Signed7702Transaction != nil {
		selected++
		declared = tx.Signed7702Transaction.Hash
	}
	if tx.Signed4844Transaction != nil {
		selected++
		declared = tx.Signed4844Transaction.Hash
	}
	if tx.Signed1559Transaction != nil {
		selected++
		declared = tx.Signed1559Transaction.Hash
	}
	if tx.Signed2930Transaction != nil {
		selected++
		declared = tx.Signed2930Transaction.Hash
	}
	if tx.SignedLegacyTransaction != nil {
		selected++
		declared = tx.SignedLegacyTransaction.Hash
	}
	if selected != 1 {
		return "", fmt.Errorf("transaction union has %d selected branches", selected)
	}
	return declared, nil
}

// TransactionPayload re-validates a caller-constructed public transaction
// and returns its decoded geth payload for hash computation and sender
// binding. The constructor's canonical copy is what gets bridged: a directly
// constructed value is re-parsed through the generated schema, re-marshalled
// to the canonical spelling, and the declared hash of that checked copy is
// compared against the recomputed signed-payload hash — so a
// case-equivalent spelling succeeds and a genuinely mismatched hash fails.
// nil, a zero-selected or multi-selected union, an illegal branch structure,
// or any payload violation is an input error (ErrInvalidRequest) rejected
// before any network contact; the response decoders keep their own
// response-error classification.
func TransactionPayload(tx *ethapi.Transaction) (*types.Transaction, error) {
	if tx == nil {
		return nil, fmt.Errorf("ethguard: nil transaction: %w", rpc.ErrInvalidRequest)
	}
	raw, err := ethcodec.MarshalNoEscape(*tx)
	if err != nil {
		return nil, fmt.Errorf("ethguard: transaction union: %v: %w", err, rpc.ErrInvalidRequest)
	}
	candidate, err := hexcompat.Transaction(raw)
	if err != nil {
		return nil, fmt.Errorf("ethguard: transaction candidate: %v: %w", err, rpc.ErrInvalidRequest)
	}
	checked, err := ethapi.NewTransaction(candidate)
	if err != nil {
		return nil, fmt.Errorf("ethguard: transaction structure: %v: %w", err, rpc.ErrInvalidRequest)
	}
	declared, err := TransactionHash(&checked)
	if err != nil {
		return nil, fmt.Errorf("ethguard: %v: %w", err, rpc.ErrInvalidRequest)
	}
	canonical, err := ethcodec.MarshalNoEscape(checked)
	if err != nil {
		return nil, fmt.Errorf("ethguard: transaction canonicalization: %v: %w", err, rpc.ErrInvalidRequest)
	}
	payload, err := transactionWire(canonical, declared)
	if err != nil {
		// The caller's bytes are the input here, not a provider response:
		// re-attribute the failure to the input-error class while keeping
		// the diagnostics.
		return nil, fmt.Errorf("ethguard: invalid transaction input: %v: %w", err, rpc.ErrInvalidRequest)
	}
	return payload, nil
}

// transactionWire is the private geth payload bridge: it decodes the wire
// transaction, requires a signature, and verifies the declared hash against
// the hash recomputed from the decoded signature. Callers pass the
// constructor's canonical copy and its declared hash; the bridge never
// manufactures a value. Failures are classified as malformed responses —
// response decoders surface them as such, and the caller-input path
// re-attributes them to ErrInvalidRequest.
func transactionWire(raw json.RawMessage, declared ethapi.Hash) (*types.Transaction, error) {
	var tx *types.Transaction
	if err := json.Unmarshal(raw, &tx); err != nil {
		if errors.Is(err, types.ErrTxTypeNotSupported) {
			return nil, errors.Join(rpc.ErrUnsupportedTypedObject, err)
		}
		return nil, wrapMalformed("decode transaction", err)
	}
	if tx == nil {
		return nil, wrapMalformed("decode transaction: JSON null", nil)
	}
	if _, r, s := tx.RawSignatureValues(); r == nil || s == nil || r.Sign() == 0 || s.Sign() == 0 {
		return nil, wrapMalformed("transaction without signature", nil)
	}
	if tx.Hash().Hex() != string(declared) {
		return nil, wrapMalformed(fmt.Sprintf("wire hash %s disagrees with computed transaction hash %s",
			declared, tx.Hash()), nil)
	}
	return tx, nil
}

func normalizeQuantity(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if len(value) < 3 || !strings.EqualFold(value[:2], "0x") {
		return "", fmt.Errorf("invalid quantity %q", value)
	}
	number, ok := new(big.Int).SetString(value[2:], 16)
	if !ok {
		return "", fmt.Errorf("invalid quantity %q", value)
	}
	return number.String(), nil
}

// wrapMalformed keeps every decode failure inside the malformed response
// class with its cause attached.
func wrapMalformed(what string, cause error) error {
	if errors.Is(cause, rpc.ErrUnsupportedTypedObject) {
		return errors.Join(rpc.ErrMalformedResponse, cause, fmt.Errorf("%s", what))
	}
	if cause == nil {
		return fmt.Errorf("%w: %s", rpc.ErrMalformedResponse, what)
	}
	return fmt.Errorf("%w: %s: %w", rpc.ErrMalformedResponse, what, cause)
}
