package aggregator

import (
	"context"

	"github.com/codermaybe/ethguard/rpc"
)

// ReferenceStage is the consumer-owned seam for reference establishment. The
// aggregator declares what it needs — a completed invocation carrying its
// established reference — and the composition root supplies the
// implementation, which owns the head reader, the head codec, and the
// establishment algorithms. The stage is the only place a reference round
// touches the network during a business read: one invocation, one basis, and
// the establishment rounds ride the same invocation instead of a second
// aggregate.
type ReferenceStage interface {
	// Establish performs the reference rounds for request and returns the
	// invocation carrying the evidence. A failed establishment returns the
	// unchanged invocation and the error; the business fan-out never starts
	// without its reference.
	Establish(ctx context.Context, invocation rpc.Invocation, request rpc.ReferenceRequest) (rpc.Invocation, error)
}
