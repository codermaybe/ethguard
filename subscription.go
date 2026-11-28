package ethguard

import (
	"context"
	"fmt"

	"github.com/codermaybe/ethguard/ethapi"
	sub "github.com/codermaybe/ethguard/internal/subscription"
	"github.com/codermaybe/ethguard/rpc"
)

// subscriptionSources captures the fixed bounded-subscription membership used by one
// subscription. The returned identities are stable for the pool lifetime;
// source health never changes this denominator.
func (c *Client) subscriptionSources() ([]sub.Source, error) {
	if c.isClosed() {
		return nil, ErrPoolClosed
	}
	sources, err := c.pool.SubscriptionSources()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrInvalidRequest, err)
	}
	out := make([]sub.Source, len(sources))
	for i, source := range sources {
		out[i] = source
	}
	return out, nil
}

// SubscribeNewHead opens a typed stream of atomic path changes.
func (c *Client) SubscribeNewHead(ctx context.Context, policy HeadSubscriptionPolicy) (*HeadSubscription, error) {
	return sub.NewHead(c.lifeCtx, ctx, c.subscriptionSources, policy, c.Mode())
}

// SubscribeFilterLogs follows the committed Head path from its birth anchor.
// Each Next result is a complete path delta, including zero-match blocks.
// Numeric/hash filters select that live path without replaying old history;
// crossing toBlock does not stop reorg tracking. latest is live, while safe,
// finalized, pending and unknown filter extensions are rejected locally.
func (c *Client) SubscribeFilterLogs(ctx context.Context, q ethapi.FilterQuery, policy LogsSubscriptionPolicy) (*LogsSubscription, error) {
	return sub.NewLogs(c.lifeCtx, ctx, c.subscriptionSources, q, policy, c.Mode())
}

// SubscribeTransactionReceipts follows the committed Head path from birth.
// nil, unset, and empty transaction hashes select all live receipts. A nonempty
// filter selects complete, proven block receipts locally; no older history is
// replayed. Each Next result atomically includes retractions and empty blocks.
func (c *Client) SubscribeTransactionReceipts(ctx context.Context, q *ethapi.TransactionReceiptsQuery, policy ReceiptsSubscriptionPolicy) (*ReceiptsSubscription, error) {
	return sub.NewReceipts(c.lifeCtx, ctx, c.subscriptionSources, q, policy, c.Mode())
}
