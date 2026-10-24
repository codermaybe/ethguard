package ethguard

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
)

// Call forwards or aggregates an arbitrary JSON-RPC method according to the
// registered method policy.
func (c *Client) Call(ctx context.Context, method string, params ...any) (rpc.Decision, error) {
	return c.aggregator.Aggregate(ctx, rpc.Request{Method: method, Params: params})
}

// unknownDecision enforces the typed-boundary invariant for scheme B: any
// error return traveling with a Decision must carry Status Unknown, so
// ignoring err can never surface Corroborated, Selected, or Forwarded.
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

// DecodeBlock delegates the public root decoding entry point to the shared
// production typed projection module.
func DecodeBlock(raw json.RawMessage) (*types.Block, error) {
	return typed.DecodeBlock(raw)
}

// DecodeBlockHeaderOnly delegates header projection to internal/typed.
func DecodeBlockHeaderOnly(raw json.RawMessage) (*types.Block, error) {
	return typed.DecodeBlockHeaderOnly(raw)
}

// DecodeBlockWithEvidence delegates full block projection to internal/typed.
func DecodeBlockWithEvidence(raw json.RawMessage, evidence json.RawMessage) (*types.Block, error) {
	return typed.DecodeBlockWithEvidence(raw, evidence)
}

// DecodeReceipt delegates receipt projection to internal/typed.
func DecodeReceipt(raw json.RawMessage) (*types.Receipt, error) {
	return typed.DecodeReceipt(raw)
}

// DecodeReceipts delegates receipt-list projection to internal/typed.
func DecodeReceipts(raw json.RawMessage) ([]*types.Receipt, error) {
	return typed.DecodeReceipts(raw)
}

// DecodeTransaction delegates transaction projection to internal/typed.
func DecodeTransaction(raw json.RawMessage) (*types.Transaction, bool, error) {
	return typed.DecodeTransaction(raw)
}
