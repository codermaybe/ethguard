package ethguard

import (
	"context"
	"encoding/json"
	"fmt"

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
		return nil, decision, fmt.Errorf("ethguard: decode %s: %w", method, err)
	}
	return &out, decision, nil
}
