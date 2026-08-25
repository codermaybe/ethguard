package ethguard

import (
	"context"
	"encoding/json"
	"fmt"

	"ethguard/aggregator"
	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// anchorMethod labels observability events for anchored reads establishing
// their block reference. A fixed literal keeps label cardinality bounded
// (decision 0008); it doubles as the internal registry key under which the
// anchor pipeline resolves, never reaching the wire.
const anchorMethod = "anchor"

// AnchorResolver establishes verified block references behind moving-tag
// reads (latest/safe/finalized). It is the replaceable anchoring strategy:
// implementations decide how a reference is chosen and how stale it may be,
// while call orchestration stays fixed. The default is StrictHeadAnchor;
// conservative selection over divergent heads and TTL caching are future
// implementations of this same seam.
type AnchorResolver interface {
	// AnchorBlockHash returns one block hash every source will be asked
	// about, or an error explaining why no trusted reference was reached.
	AnchorBlockHash(ctx context.Context) (string, error)
}

// StrictHeadAnchor is the default AnchorResolver: fan out a latest-block
// query through the pool and accept it only when sources agree under the
// block-head policy's strict consensus. If sources sit on different heads
// the resolution fails with conflict evidence rather than guessing which
// head is right.
//
// The pipeline rides the ordinary aggregation flow — collect, resolve,
// record — under the bounded "anchor" label, and its registry holds only
// the unanchored head policy, so establishment cannot recurse.
type StrictHeadAnchor struct {
	aggregate *aggregator.Aggregator
}

// NewStrictHeadAnchor builds the default resolver over p, reporting one
// decision event per establishment into rec.
func NewStrictHeadAnchor(p *pool.Pool, rec observe.Recorder) *StrictHeadAnchor {
	if rec == nil {
		rec = observe.Nop
	}
	return &StrictHeadAnchor{
		aggregate: aggregator.New(p, rpc.Registry{
			anchorMethod: anchorHeadPolicy{methods.BlockByNumberPolicy()},
		}, aggregator.WithRecorder(rec)),
	}
}

// AnchorBlockHash implements AnchorResolver.
func (a *StrictHeadAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	decision, err := a.aggregate.Aggregate(ctx, rpc.Request{
		Method: anchorMethod,
		Params: []any{"latest", false},
	})
	if err != nil {
		return "", err
	}
	var head struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(decision.Value, &head); err != nil || len(head.Hash) < 3 {
		return "", fmt.Errorf("%w: winning anchor block carries no hash", rpc.ErrMalformedResponse)
	}
	return head.Hash, nil
}

// anchorHeadPolicy adapts the unanchored block-head policy to the anchor
// pipeline: events report under "anchor", while the fleet is asked the real
// wire method.
type anchorHeadPolicy struct {
	rpc.Policy
}

func (p anchorHeadPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	prepared, err := p.Policy.Prepare(ctx, req)
	if err != nil {
		return rpc.Request{}, err
	}
	prepared.Method = methods.MethodGetBlockByNumber
	return prepared, nil
}
