package ethguard

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/typed"
)

// Call is the generic decision entry for declared executable reads. The
// allow-list is the existing catalog / read-plan registry — the single
// source of truth the typed methods drive — and the route check completes
// before any network contact: write methods (with a pointer to
// SendRawTransaction for eth_sendRawTransaction), unregistered, unimplemented,
// and subscription methods fail locally with ErrInvalidRequest. A declared
// read whose plan failed to compile is absent from the registry and fails
// closed the same way; the pool's Forward stays an internal execution
// capability, not a public bypass for unknown methods.
//
// A registered read keeps its own method contract through Call: selection,
// pending-local, and explicit single-source Forwarded semantics are the
// method's, and Call never upgrades them. The success JSON rides
// result.Decision.Value — Call adds no second business-value return — and
// CallResult.Err equals the trailing error. Call is not the typed safety
// entry: it applies no typed object decoding and makes no corroboration
// demand beyond the method's own contract.
func (c *Client) Call(ctx context.Context, method string, params ...any) (CallResult, error) {
	_, result, err := operation(c, ctx, false, func(opCtx context.Context) (struct{}, CallResult, error) {
		if err := c.checkCallRoute(method); err != nil {
			return struct{}{}, CallResult{}, err
		}
		decision, err := c.aggregator.Aggregate(opCtx, rpc.Request{Method: method, Params: params})
		if err != nil {
			// A failed aggregation clears the public value; every formed
			// fact stays.
			decision.Value = nil
		}
		return struct{}{}, CallResult{Decision: decisionOf(decision)}, err
	})
	return result, err
}

// checkCallRoute is the pre-network route check of Call.
func (c *Client) checkCallRoute(method string) error {
	if _, planned := c.plans[method]; planned {
		return nil
	}
	if method == rpc.WriteMethodEthSendRawTransaction {
		return fmt.Errorf("ethguard: %s is a write method; use SendRawTransaction: %w", method, rpc.ErrInvalidRequest)
	}
	return fmt.Errorf("ethguard: %s is not a registered executable read: %w", method, rpc.ErrInvalidRequest)
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
