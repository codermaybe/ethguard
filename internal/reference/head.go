package reference

import (
	"context"
	"encoding/json"
	"time"

	"github.com/codermaybe/ethguard/rpc"
)

// HeadObservation is the minimal raw result needed by reference establishment.
// It carries no method policy or business decision; the reference module owns
// interpretation of the head shape.
type HeadObservation struct {
	Source    rpc.SourceRef
	Value     json.RawMessage
	Err       error
	RTT       time.Duration
	Attempted bool
}

// HeadReader is the consumer-owned reference seam. Implementations provide
// only finalized-tag reads and confirmations at one selected height; method
// binding (which wire request reads a head), member snapshots, and quorum
// decisions belong to the composition root and the reference algorithm above
// this seam. A reader is created per reference invocation so it can retain
// the first-round member snapshot for confirmation, even if the pool changes
// concurrently.
type HeadReader interface {
	Read(ctx context.Context, tag string, timeout time.Duration) ([]HeadObservation, error)
	Confirm(ctx context.Context, height uint64, sourceIDs []string, timeout time.Duration) ([]HeadObservation, error)
}
