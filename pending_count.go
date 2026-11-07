package ethguard

import (
	"context"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/rpc/methods"
)

// pendingTransactionCountExec returns the selected local observation of the
// pending transaction pool. The method plan marks the pending argument as
// PendingObserve, so the decision is Selected rather than Corroborated and no
// moving reference is established.
func (c *Client) pendingTransactionCountExec(ctx context.Context) (uint, CallResult, error) {
	value, decision, err := aggregateInto[hexutil.Uint](ctx, c, methods.MethodGetBlockTransactionCountByNumber, "pending")
	if err != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	return uint(*value), CallResult{Decision: decisionOf(decision)}, nil
}

// PendingTransactionCount returns a node-local observation of the pending
// transaction count. Pending mempools are not a network-wide consensus value.
func (c *Client) PendingTransactionCount(ctx context.Context) (uint, CallResult, error) {
	return operation(c, ctx, false, c.pendingTransactionCountExec)
}

// PendingTransactionCount returns the node-local pending transaction count
// through the go-ethereum-shaped view.
func (ec *EthClient) PendingTransactionCount(ctx context.Context) (uint, error) {
	value, _, err := operation(ec.owner, ctx, true, ec.owner.pendingTransactionCountExec)
	return value, err
}
