package ethguard

import (
	"context"
	"encoding/json"
	"fmt"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard/rpc"
)

// Call forwards or aggregates an arbitrary JSON-RPC method according to the
// registered method policy.
func (c *Client) Call(ctx context.Context, method string, params ...any) (rpc.Decision, error) {
	return c.aggregator.Aggregate(ctx, rpc.Request{Method: method, Params: params})
}

// CallInto aggregates one request and decodes the resolved value into T.
// The returned pointer is nil whenever the error is non-nil.
func CallInto[T any](ctx context.Context, c *Client, method string, params ...any) (*T, rpc.Decision, error) {
	decision, err := c.Call(ctx, method, params...)
	if err != nil {
		return nil, decision, err
	}
	var out T
	if err := json.Unmarshal(decision.Value, &out); err != nil {
		return nil, decision, fmt.Errorf("%w: decode %s: %w", rpc.ErrMalformedResponse, method, err)
	}
	return &out, decision, nil
}

// DecodeBlock shapes one verified eth_getBlockByNumber / eth_getBlockByHash
// response into a block value — the same header-plus-body reconstruction
// go-ethereum's own client performs, since types.Block deliberately has no
// JSON unmarshaller. Responses carrying a hash-only transaction list (the
// fullTx=false wire form) are rejected instead of silently yielding a block
// without its transactions, as are responses whose wire hash disagrees with
// the header's computed hash — a free integrity check once sources agree.
func DecodeBlock(raw json.RawMessage) (*types.Block, error) {
	var head *types.Header
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, wrapMalformed("decode block header", err)
	}
	if head == nil { // upstream "block not found"
		return nil, ethereum.NotFound
	}

	var body struct {
		Hash         *common.Hash      `json:"hash"`
		Transactions []json.RawMessage `json:"transactions"`
		UncleHashes  []common.Hash     `json:"uncles"`
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
	if head.UncleHash == types.EmptyUncleHash && len(body.UncleHashes) > 0 {
		return nil, wrapMalformed("server returned uncles for an uncle-free header", nil)
	}
	if body.Hash != nil && *body.Hash != head.Hash() {
		return nil, fmt.Errorf("%w: wire hash %s disagrees with computed header hash %s",
			rpc.ErrMalformedResponse, body.Hash, head.Hash())
	}
	return types.NewBlockWithHeader(head).WithBody(types.Body{Transactions: txs}), nil
}

// DecodeReceipt shapes one verified eth_getTransactionReceipt response into
// a receipt value. go-ethereum's generated Receipt unmarshaller owns the
// wire schema, so a unanimous payload missing one of its required fields
// (cumulativeGasUsed, logsBloom, logs, transactionHash, gasUsed) dies here
// as malformed instead of silently zero-valuing — the malformed-but-unanimous
// class of decision 0011. As with DecodeBlock's wire-hash check, a free
// integrity check rides along once sources agree: logsBloom is derived from
// the receipt's logs by consensus rule, so a payload whose bloom contradicts
// its own logs cannot be honest, however unanimous the fleet.
func DecodeReceipt(raw json.RawMessage) (*types.Receipt, error) {
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
	if receipt.Bloom != types.CreateBloom(receipt) {
		return wrapMalformed("logsBloom disagrees with the receipt's logs", nil)
	}
	return nil
}

// DecodeReceipts shapes one verified eth_getBlockReceipts response into
// receipts. JSON null means the block is unknown to the fleet and maps to
// ethereum.NotFound; an empty array is a real answer for an empty block.
// Every element passes the single-receipt integrity checks.
func DecodeReceipts(raw json.RawMessage) ([]*types.Receipt, error) {
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

// DecodeTransaction shapes one verified eth_getTransactionByHash response
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
		return nil, false, wrapMalformed("decode transaction", err)
	}
	if tx == nil { // upstream "no such transaction"
		return nil, false, ethereum.NotFound
	}
	var wire struct {
		Hash        *common.Hash `json:"hash"`
		BlockNumber *hexutil.Big `json:"blockNumber"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, false, wrapMalformed("decode transaction context", err)
	}
	if _, r, _ := tx.RawSignatureValues(); r == nil {
		return nil, false, wrapMalformed("transaction without signature", nil)
	}
	if wire.Hash != nil && *wire.Hash != tx.Hash() {
		return nil, false, wrapMalformed(fmt.Sprintf("wire hash %s disagrees with computed transaction hash %s",
			wire.Hash, tx.Hash()), nil)
	}
	return tx, wire.BlockNumber == nil, nil
}

// wrapMalformed keeps every block-decode failure inside the malformed
// response class with its cause attached.
func wrapMalformed(what string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", rpc.ErrMalformedResponse, what)
	}
	return fmt.Errorf("%w: %s: %w", rpc.ErrMalformedResponse, what, cause)
}
