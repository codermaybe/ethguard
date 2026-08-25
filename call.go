package ethguard

import (
	"context"
	"encoding/json"
	"fmt"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
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

// wrapMalformed keeps every block-decode failure inside the malformed
// response class with its cause attached.
func wrapMalformed(what string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", rpc.ErrMalformedResponse, what)
	}
	return fmt.Errorf("%w: %s: %w", rpc.ErrMalformedResponse, what, cause)
}
