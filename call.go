package ethguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard/internal/jsonsafe"
	"ethguard/rpc"
)

// Call forwards or aggregates an arbitrary JSON-RPC method according to the
// registered method policy.
func (c *Client) Call(ctx context.Context, method string, params ...any) (rpc.Decision, error) {
	return c.aggregator.Aggregate(ctx, rpc.Request{Method: method, Params: params})
}

// unknownDecision enforces the typed-boundary invariant for scheme B: any
// error return traveling with a Decision must carry Status Unknown, so
// ignoring err can never surface Corroborated, Selected, or Forwarded.
// Decode and projection failures use this path. They are not a business
// adjudication and must not be reported as Corroborated or Selected.
// Aggregation failures already arrive Unknown; this clears a success Status
// when the typed layer (decode, binding, projection) fails after a
// successful aggregate. It is a no-op on the success path by construction:
// callers pass only error returns through it.
func unknownDecision(d rpc.Decision) rpc.Decision {
	d.Status = rpc.Unknown
	return d
}

// CallInto aggregates one request and decodes the resolved value into T.
// The returned pointer is nil whenever the error is non-nil.
func CallInto[T any](ctx context.Context, c *Client, method string, params ...any) (*T, rpc.Decision, error) {
	decision, err := c.Call(ctx, method, params...)
	if err != nil {
		return nil, unknownDecision(decision), err
	}
	var out T
	if err := jsonsafe.Decode(decision.Value, &out); err != nil {
		return nil, unknownDecision(decision), fmt.Errorf("%w: decode %s: %w", rpc.ErrMalformedResponse, method, err)
	}
	return &out, decision, nil
}

// DecodeBlock shapes one corroborated eth_getBlockByNumber / eth_getBlockByHash
// response into a block value — the same header-plus-body reconstruction
// go-ethereum's own client performs, since types.Block deliberately has no
// JSON unmarshaller. Responses carrying a hash-only transaction list (the
// fullTx=false wire form) are rejected instead of silently yielding a block
// without its transactions, as are responses whose wire hash disagrees with
// the header's computed hash — a free integrity check once sources agree.
func DecodeBlock(raw json.RawMessage) (*types.Block, error) {
	return DecodeBlockWithEvidence(raw, nil)
}

// DecodeBlockHeaderOnly decodes the header returned by a fullTx=false block
// query. Transaction and uncle bodies are intentionally absent; callers must
// not present this value as a fully content-corroborated block.
func DecodeBlockHeaderOnly(raw json.RawMessage) (*types.Block, error) {
	var head *types.Header
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, wrapMalformed("decode block header", err)
	}
	if head == nil {
		return nil, ethereum.NotFound
	}
	return types.NewBlockWithHeader(head), nil
}

// DecodeBlockWithEvidence decodes a full block and attaches same-source
// dependent uncle headers supplied by the admission stage. Evidence is kept
// separate from the upstream JSON so vendor fields never affect clustering.
func DecodeBlockWithEvidence(raw json.RawMessage, evidence json.RawMessage) (*types.Block, error) {
	var head *types.Header
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, wrapMalformed("decode block header", err)
	}
	if head == nil { // upstream "block not found"
		return nil, ethereum.NotFound
	}

	var body struct {
		Hash         *common.Hash        `json:"hash"`
		Transactions []json.RawMessage   `json:"transactions"`
		UncleHashes  []common.Hash       `json:"uncles"`
		Withdrawals  []*types.Withdrawal `json:"withdrawals"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, wrapMalformed("decode block body", err)
	}

	txs := make([]*types.Transaction, 0, len(body.Transactions))
	for i, item := range body.Transactions {
		if len(item) > 0 && item[0] == '"' {
			return nil, wrapMalformed("hash-only transaction list; request the block with full transactions", nil)
		}
		tx := new(types.Transaction)
		if err := json.Unmarshal(item, tx); err != nil {
			return nil, wrapMalformed(fmt.Sprintf("decode transaction %d", i), err)
		}
		txs = append(txs, tx)
	}
	if head.TxHash == types.EmptyTxsHash && len(txs) > 0 {
		return nil, wrapMalformed("non-empty transactions for an empty transaction root", nil)
	}
	if head.TxHash != types.EmptyTxsHash && len(txs) == 0 {
		return nil, wrapMalformed("empty transactions for a non-empty transaction root", nil)
	}
	if head.UncleHash == types.EmptyUncleHash && len(body.UncleHashes) > 0 {
		return nil, wrapMalformed("server returned uncles for an uncle-free header", nil)
	}
	if head.UncleHash != types.EmptyUncleHash && len(body.UncleHashes) == 0 {
		return nil, wrapMalformed("server returned no uncles for a non-empty uncle root", nil)
	}
	var uncles []*types.Header
	if len(body.UncleHashes) > 0 {
		if len(evidence) == 0 {
			return nil, wrapMalformed("uncle headers were not admitted", nil)
		}
		var uncleRaw []json.RawMessage
		if err := json.Unmarshal(evidence, &uncleRaw); err != nil || len(uncleRaw) != len(body.UncleHashes) {
			return nil, wrapMalformed("decode admitted uncle headers", err)
		}
		uncles = make([]*types.Header, len(uncleRaw))
		for i, item := range uncleRaw {
			if err := json.Unmarshal(item, &uncles[i]); err != nil || uncles[i] == nil {
				return nil, wrapMalformed(fmt.Sprintf("decode uncle %d", i), err)
			}
			if uncles[i].Hash() != body.UncleHashes[i] {
				return nil, wrapMalformed(fmt.Sprintf("uncle %d hash mismatch", i), nil)
			}
		}
		if types.CalcUncleHash(uncles) != head.UncleHash {
			return nil, wrapMalformed("uncle root mismatch", nil)
		}
	}
	if body.Hash != nil && *body.Hash != head.Hash() {
		return nil, fmt.Errorf("%w: wire hash %s disagrees with computed header hash %s",
			rpc.ErrMalformedResponse, body.Hash, head.Hash())
	}
	return types.NewBlockWithHeader(head).WithBody(types.Body{Transactions: txs, Uncles: uncles, Withdrawals: body.Withdrawals}), nil
}

// DecodeReceipt shapes one corroborated eth_getTransactionReceipt response into
// a receipt value. go-ethereum's generated Receipt unmarshaller owns the
// wire schema, so a unanimous payload missing one of its required fields
// (cumulativeGasUsed, logsBloom, logs, transactionHash, gasUsed) dies here
// as malformed instead of silently zero-valuing — the malformed-but-unanimous
// class of decision 0011. As with DecodeBlock's wire-hash check, a free
// integrity check rides along once sources agree: logsBloom is derived from
// the receipt's logs by consensus rule, so a payload whose bloom contradicts
// its own logs cannot be honest, however unanimous the fleet.
func DecodeReceipt(raw json.RawMessage) (*types.Receipt, error) {
	if string(bytes.TrimSpace(raw)) != "null" {
		if err := validateReceiptEnvelope(raw); err != nil {
			return nil, err
		}
	}
	var receipt *types.Receipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, wrapMalformed("decode receipt", err)
	}
	if receipt == nil { // upstream "no receipt for this transaction"
		return nil, ethereum.NotFound
	}
	if err := validateReceipt(receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

// validateReceipt holds the integrity checks every decoded receipt must
// pass, shared by the single-receipt and block-receipts decode paths: the
// bloom is derived from a receipt's logs by consensus rule, so a payload
// whose bloom contradicts its own logs cannot be honest.
func validateReceipt(receipt *types.Receipt) error {
	for i, log := range receipt.Logs {
		if log == nil {
			return wrapMalformed(fmt.Sprintf("decode receipt log %d: null element", i), nil)
		}
	}
	if receipt.Bloom != types.CreateBloom(receipt) {
		return wrapMalformed("logsBloom disagrees with the receipt's logs", nil)
	}
	return nil
}

// DecodeReceipts shapes one corroborated eth_getBlockReceipts response into
// receipts. JSON null means the block is unknown to the fleet and maps to
// ethereum.NotFound; an empty array is a real answer for an empty block.
// Every element passes the single-receipt integrity checks.
func DecodeReceipts(raw json.RawMessage) ([]*types.Receipt, error) {
	if string(bytes.TrimSpace(raw)) != "null" {
		var rows []json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil || rows == nil {
			return nil, wrapMalformed("decode receipts: expected array", err)
		}
		for i, row := range rows {
			if err := validateReceiptEnvelope(row); err != nil {
				return nil, wrapMalformed(fmt.Sprintf("decode receipt %d", i), err)
			}
		}
	}
	var receipts []*types.Receipt
	if err := json.Unmarshal(raw, &receipts); err != nil {
		return nil, wrapMalformed("decode receipts", err)
	}
	if receipts == nil { // upstream "block not found"
		return nil, ethereum.NotFound
	}
	for i, receipt := range receipts {
		if receipt == nil {
			return nil, wrapMalformed(fmt.Sprintf("decode receipt %d: null element", i), nil)
		}
		if err := validateReceipt(receipt); err != nil {
			return nil, err
		}
	}
	return receipts, nil
}

// validateReceiptEnvelope rejects unanimous but incomplete receipt objects at
// the typed boundary. The generated go-ethereum decoder intentionally leaves
// absent JSON fields at their zero values; these fields are required for a
// receipt's inclusion context and must be distinguished from an explicit
// zero value.
func validateReceiptEnvelope(raw json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return wrapMalformed("receipt is not an object", err)
	}
	for _, field := range []string{"transactionHash", "transactionIndex", "blockHash", "blockNumber", "cumulativeGasUsed", "gasUsed", "logsBloom", "logs"} {
		value, ok := obj[field]
		if !ok || string(bytes.TrimSpace(value)) == "null" || len(bytes.TrimSpace(value)) == 0 {
			return wrapMalformed("receipt missing "+field, nil)
		}
	}
	if status, ok := obj["status"]; !ok || string(bytes.TrimSpace(status)) == "null" {
		if root, rootOK := obj["root"]; !rootOK || string(bytes.TrimSpace(root)) == "null" {
			return wrapMalformed("receipt missing status or root", nil)
		}
	}
	if logs := bytes.TrimSpace(obj["logs"]); len(logs) == 0 || logs[0] != '[' {
		return wrapMalformed("receipt logs must be an array", nil)
	}
	return nil
}

// DecodeTransaction shapes one corroborated eth_getTransactionByHash response
// into a transaction, reporting whether the fleet saw it as still pending
// (no block number on the wire). go-ethereum's generated Transaction
// unmarshaller owns the wire schema. Two integrity checks ride along once
// sources agree: the wire hash must equal the hash recomputed from the
// decoded signature (the transaction-side analogue of DecodeBlock's wire
// hash check), and an unsigned payload is rejected — honest sources only
// hold signed transactions.
func DecodeTransaction(raw json.RawMessage) (*types.Transaction, bool, error) {
	var tx *types.Transaction
	if err := json.Unmarshal(raw, &tx); err != nil {
		if errors.Is(err, types.ErrTxTypeNotSupported) {
			return nil, false, errors.Join(rpc.ErrUnsupportedTypedObject, err)
		}
		return nil, false, wrapMalformed("decode transaction", err)
	}
	if tx == nil { // upstream "no such transaction"
		return nil, false, ethereum.NotFound
	}
	var wire struct {
		Hash             *common.Hash    `json:"hash"`
		From             *common.Address `json:"from"`
		BlockHash        *common.Hash    `json:"blockHash"`
		BlockNumber      json.RawMessage `json:"blockNumber"`
		TransactionIndex json.RawMessage `json:"transactionIndex"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, false, wrapMalformed("decode transaction context", err)
	}
	if wire.Hash == nil {
		return nil, false, wrapMalformed("transaction missing hash", nil)
	}
	if _, r, s := tx.RawSignatureValues(); r == nil || s == nil || r.Sign() == 0 || s.Sign() == 0 {
		return nil, false, wrapMalformed("transaction without signature", nil)
	}
	if *wire.Hash != tx.Hash() {
		return nil, false, wrapMalformed(fmt.Sprintf("wire hash %s disagrees with computed transaction hash %s",
			wire.Hash, tx.Hash()), nil)
	}
	blockNumberSet := len(bytes.TrimSpace(wire.BlockNumber)) > 0 && string(bytes.TrimSpace(wire.BlockNumber)) != "null"
	transactionIndexSet := len(bytes.TrimSpace(wire.TransactionIndex)) > 0 && string(bytes.TrimSpace(wire.TransactionIndex)) != "null"
	if wire.BlockHash != nil && (!blockNumberSet || !transactionIndexSet) {
		return nil, false, wrapMalformed("transaction incomplete inclusion context", nil)
	}
	return tx, !blockNumberSet, nil
}

// wrapMalformed keeps every block-decode failure inside the malformed
// response class with its cause attached.
func wrapMalformed(what string, cause error) error {
	if errors.Is(cause, rpc.ErrUnsupportedTypedObject) {
		return errors.Join(rpc.ErrMalformedResponse, cause, fmt.Errorf("%s", what))
	}
	if cause == nil {
		return fmt.Errorf("%w: %s", rpc.ErrMalformedResponse, what)
	}
	return fmt.Errorf("%w: %s: %w", rpc.ErrMalformedResponse, what, cause)
}
