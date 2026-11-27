package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

type subscriptionSource interface {
	Subscribe(context.Context, string, ...any) (transport.Subscription, error)
}

// subscriptionSources captures the fixed WSS membership used by one
// subscription. The returned identities are stable for the pool lifetime;
// source health never changes this denominator.
func (c *Client) subscriptionSources() ([]identifiedSubscriptionSource, error) {
	if c.isClosed() {
		return nil, ErrPoolClosed
	}
	sources, err := c.pool.SubscriptionSources()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrInvalidRequest, err)
	}
	out := make([]identifiedSubscriptionSource, len(sources))
	for i, source := range sources {
		out[i] = source
	}
	// A single WSS source inside a multi-source client would silently bypass
	// the client's strict corroboration mode because HTTP members cannot emit
	// subscriptions. Keep that setup fail-closed until an explicit
	// SingleSourceSelected subscription option exists.
	if len(out) == 1 && c.Mode() != rpc.CorroborationForwarded {
		return nil, fmt.Errorf("%w: one WSS source cannot satisfy strict subscription corroboration; configure at least two WSS sources", rpc.ErrInvalidRequest)
	}
	return out, nil
}

type identifiedSubscriptionSource interface {
	subscriptionSource
	ID() string
}

type subscriptionUnarySource interface {
	CallContext(context.Context, string, ...any) (json.RawMessage, error)
}

func waitSubscriptionRetry(stop <-chan struct{}, ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-stop:
		return false
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// SubscribeNewHead opens a typed stream of atomic path changes.
func (c *Client) SubscribeNewHead(ctx context.Context, policy HeadSubscriptionPolicy) (*HeadSubscription, error) {
	sources, err := c.subscriptionSources()
	if err != nil {
		return nil, err
	}
	return startNewHeadSubscription(c.lifeCtx, ctx, sources, policy, c.Mode())
}

// SubscribeFilterLogs follows the committed Head path from its birth anchor.
// Each Next result is a complete path delta, including zero-match blocks.
// Numeric/hash filters select that live path without replaying old history;
// crossing toBlock does not stop reorg tracking. latest is live, while safe,
// finalized, pending and unknown filter extensions are rejected locally.
func (c *Client) SubscribeFilterLogs(ctx context.Context, q ethapi.FilterQuery, policy LogsSubscriptionPolicy) (*LogsSubscription, error) {
	filter, err := compileLogsFilter(q)
	if err != nil {
		return nil, err
	}
	if err := validateLogsPolicy(policy); err != nil {
		return nil, err
	}
	sources, err := c.subscriptionSources()
	if err != nil {
		return nil, err
	}
	return startLogsSubscription(c.lifeCtx, ctx, sources, filter, policy, c.Mode())
}

// SubscribeTransactionReceipts follows the committed Head path from birth.
// nil, unset, and empty transaction hashes select all live receipts. A nonempty
// filter selects complete, proven block receipts locally; no older history is
// replayed. Each Next result atomically includes retractions and empty blocks.
func (c *Client) SubscribeTransactionReceipts(ctx context.Context, q *ethapi.TransactionReceiptsQuery, policy ReceiptsSubscriptionPolicy) (*ReceiptsSubscription, error) {
	filter, err := compileReceiptsFilter(q)
	if err != nil {
		return nil, err
	}
	if err := validateReceiptsPolicy(policy); err != nil {
		return nil, err
	}
	sources, err := c.subscriptionSources()
	if err != nil {
		return nil, err
	}
	return startReceiptsSubscription(c.lifeCtx, ctx, sources, filter, policy, c.Mode())
}
