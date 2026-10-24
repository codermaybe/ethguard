package reference

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
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
// policy binding and quorum decisions remain in the reference/runtime layers.
type HeadReader interface {
	Read(ctx context.Context, tag string, timeout time.Duration) ([]HeadObservation, error)
	Confirm(ctx context.Context, height uint64, sourceIDs []string, timeout time.Duration) ([]HeadObservation, error)
}

// PoolHeadReader adapts the current pool to HeadReader. It is the only place
// in this package that knows the pool's collection API. A reader is created
// per reference invocation so it can retain the first-round member snapshot
// for confirmation, even if the pool changes concurrently.
type PoolHeadReader struct {
	Pool    *pool.Pool
	members []*pool.Source
}

func (r *PoolHeadReader) Read(ctx context.Context, tag string, timeout time.Duration) ([]HeadObservation, error) {
	if r.Pool == nil {
		return nil, fmt.Errorf("reference: nil head pool")
	}
	opts := pool.CollectOptions{Timeout: timeout}
	if value, ok := refcontext.Members(ctx); ok {
		opts.Sources, _ = value.([]*pool.Source)
	}
	if opts.Sources != nil {
		r.members = append([]*pool.Source(nil), opts.Sources...)
	}
	emissions, err := r.Pool.Collect(ctx, methods.MethodGetBlockByNumber, []any{tag, false}, opts)
	return observations(emissions), err
}

func (r *PoolHeadReader) Confirm(ctx context.Context, height uint64, sourceIDs []string, timeout time.Duration) ([]HeadObservation, error) {
	if r.Pool == nil {
		return nil, fmt.Errorf("reference: nil head pool")
	}
	opts := pool.CollectOptions{Timeout: timeout, SourceIDs: sourceIDs}
	if r.members != nil {
		byID := make(map[string]*pool.Source, len(r.members))
		for _, source := range r.members {
			byID[source.ID()] = source
		}
		opts.SourceIDs = nil
		for _, id := range sourceIDs {
			if source := byID[id]; source != nil {
				opts.Sources = append(opts.Sources, source)
			}
		}
	}
	emissions, err := r.Pool.Collect(ctx, methods.MethodGetBlockByNumber,
		[]any{fmt.Sprintf("0x%x", height), false}, opts)
	return observations(emissions), err
}

func observations(emissions []pool.Emission) []HeadObservation {
	out := make([]HeadObservation, 0, len(emissions))
	for _, emission := range emissions {
		var source rpc.SourceRef
		if emission.Src != nil {
			source = rpc.SourceRef{Name: emission.Src.Name(), URL: emission.Src.URL(), ID: emission.Src.ID()}
		}
		out = append(out, HeadObservation{
			Source:    source,
			Value:     emission.Val,
			Err:       emission.Err,
			RTT:       emission.RTT,
			Attempted: emission.Attempted,
		})
	}
	return out
}
