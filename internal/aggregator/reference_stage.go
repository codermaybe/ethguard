package aggregator

import (
	"context"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

// ReferenceStage is the consumer-owned seam for reference establishment. The
// aggregator declares what it needs — the evidence one establishment formed —
// and the composition root supplies the implementation, which owns the head
// reader, the head codec, and the establishment algorithms. The stage is the
// only place a reference round touches the network during a business read:
// one invocation, one basis, and the establishment rounds ride the same
// invocation instead of a second aggregate.
type ReferenceStage interface {
	// Establish performs the reference rounds for request and returns the
	// evidence those rounds formed, whether or not the establishment
	// succeeded. A nil evidence means no round formed anything; a non-nil
	// evidence beside an error is the failure's partial evidence. Either
	// way, the evidence records the requested tag. The stage transfers the
	// evidence it created and must not modify or retain it afterwards; the
	// caller attaches success evidence to the invocation exactly once, keeps
	// failure evidence visible to the decision, and never starts the
	// business fan-out without its reference.
	Establish(ctx context.Context, invocation rpcengine.Invocation, request rpc.ReferenceRequest) (*rpc.ReferenceEvidence, error)
}
