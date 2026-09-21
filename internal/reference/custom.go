package reference

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ethguard/aggregator"
	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// Custom confirms a legacy resolver's hash without requiring it to implement
// a new interface. Its selection semantics remain caller-owned, not finalized.
func Custom(ctx context.Context, p *pool.Pool, hash string, quorum int, timeout time.Duration, rec observe.Recorder) (rpc.ReferenceEvidence, error) {
	var opts []rpc.StrictOption
	if quorum > 0 {
		opts = append(opts, rpc.WithQuorum(quorum))
	}
	policy := customPolicy{rpc.Strict(func(raw json.RawMessage) (string, error) {
		v, err := decode(raw)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(v.hash, hash) {
			return "", fmt.Errorf("custom reference hash mismatch: %w", rpc.ErrMalformedResponse)
		}
		return methods.BlockByNumberPolicy().Normalize(raw)
	}, opts...)}
	started := time.Now()
	a := aggregator.New(p, rpc.Registry{"anchor": policy}, aggregator.WithRecorder(rec), aggregator.WithCollectTimeout(timeout))
	d, err := a.Aggregate(ctx, rpc.Request{Method: "anchor", Params: []any{hash, false}})
	ref := rpc.ReferenceEvidence{Status: d.Status, Origin: "custom-reference", ObservedAt: started, Participants: d.Participants, Support: d.Support, Reject: d.Reject, Faults: d.Faults}
	if err != nil {
		return ref, err
	}
	v, err := decode(d.Value)
	ref.Hash, ref.Height = v.hash, v.height
	return ref, err
}

type customPolicy struct{ rpc.Policy }

func (p customPolicy) Prepare(_ context.Context, req rpc.Request) (rpc.Request, error) {
	req.Method = methods.MethodGetBlockByHash
	return req, nil
}
func (p customPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	d, err := p.Policy.Resolve(obs)
	if err == nil && d.Status != rpc.Corroborated {
		d.Status = rpc.Unknown
		err = fmt.Errorf("uncorroborated custom reference: %w", rpc.ErrConflict)
	}
	return d, err
}
