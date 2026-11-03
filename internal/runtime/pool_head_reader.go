package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/internal/reference"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// PoolHeadReader adapts the pool to the reference HeadReader seam. It is the
// only place outside the pool that knows how a head is requested over the
// wire: the block-by-number read with its header-only shape. A reader is
// created per reference invocation; an explicit member snapshot pins both
// rounds to the invocation's voting basis even if the pool changes
// concurrently. A nil snapshot keeps the pool's own eligibility selection,
// which is the pre-invocation behavior of direct reference calls.
type PoolHeadReader struct {
	pool    *pool.Pool
	members []*pool.Source
}

// NewPoolHeadReader binds one reference invocation to the pool. The member
// snapshot is explicit: callers pass the invocation's frozen basis members,
// so the reference rounds address exactly the voting basis and nothing else.
func NewPoolHeadReader(p *pool.Pool, members []*pool.Source) *PoolHeadReader {
	return &PoolHeadReader{pool: p, members: members}
}

func (r *PoolHeadReader) Read(ctx context.Context, tag string, timeout time.Duration) ([]reference.HeadObservation, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("reference: nil head pool")
	}
	opts := pool.CollectOptions{
		Timeout:      timeout,
		Sources:      r.members,
		TipFreshness: referenceTipFreshness(tag),
	}
	emissions, err := r.pool.Collect(ctx, methods.MethodGetBlockByNumber, []any{tag, false}, opts)
	return headObservations(emissions), err
}

func (r *PoolHeadReader) Confirm(ctx context.Context, height uint64, sourceIDs []string, timeout time.Duration) ([]reference.HeadObservation, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("reference: nil head pool")
	}
	opts := pool.CollectOptions{
		Timeout:      timeout,
		SourceIDs:    sourceIDs,
		TipFreshness: pool.TipFreshnessExempt,
	}
	members := make(map[string]*pool.Source, len(r.members))
	for _, source := range r.members {
		members[source.ID()] = source
	}
	if r.members != nil {
		opts.SourceIDs = nil
		for _, id := range sourceIDs {
			if source := members[id]; source != nil {
				opts.Sources = append(opts.Sources, source)
			}
		}
	}
	emissions, err := r.pool.Collect(ctx, methods.MethodGetBlockByNumber,
		[]any{fmt.Sprintf("0x%x", height), false}, opts)
	out := headObservations(emissions)
	// An ID the snapshot cannot resolve is never silently dropped from the
	// confirmation denominator: it reports fail-closed, in the order it was
	// requested, so the caller sees a member that could not answer rather
	// than a member that vanished. The fixed-ID path keeps the pool's own
	// resolution, which already rejects unknown identities.
	if r.members != nil {
		for _, id := range sourceIDs {
			if _, known := members[id]; known {
				continue
			}
			out = append(out, reference.HeadObservation{
				Source: rpc.SourceRef{ID: id},
				Err:    fmt.Errorf("confirm: member %q is not in the frozen voting basis: fail closed", id),
			})
		}
	}
	return out, err
}

func headObservations(emissions []pool.Emission) []reference.HeadObservation {
	out := make([]reference.HeadObservation, 0, len(emissions))
	for _, emission := range emissions {
		var source rpc.SourceRef
		if emission.Src != nil {
			source = rpc.SourceRef{Name: emission.Src.Name(), URL: emission.Src.URL(), ID: emission.Src.ID()}
		}
		out = append(out, reference.HeadObservation{
			Source:    source,
			Value:     emission.Val,
			Err:       emission.Err,
			RTT:       emission.RTT,
			Attempted: emission.Attempted,
		})
	}
	return out
}
