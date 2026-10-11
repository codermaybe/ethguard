// Package reference establishes a corroborated reference before state reads.
package reference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ethguard/internal/refcontext"
	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

type view struct {
	source rpc.SourceRef
	height uint64
	hash   string
	raw    json.RawMessage
}

// Finalized observes once and, if necessary, confirms one common height once.
// Membership and the required support are fixed before confirmation. It never
// retries successively older candidates or relaxes the caller's quorum.
func Finalized(ctx context.Context, p *pool.Pool, maxLag uint64, quorum int, mode rpc.CorroborationMode, timeout time.Duration, rec observe.Recorder) (ref rpc.ReferenceEvidence, resultErr error) {
	started := time.Now()
	ref.Origin, ref.ObservedAt, ref.Status = "reported-finalized", started, rpc.Unknown
	var attempts []observe.SourceEvent
	defer func() {
		if rec == nil || rec == observe.Nop {
			return
		}
		status := ref.Status.String()
		if resultErr != nil {
			status = "failed"
		}
		rec.RecordDecision(observe.DecisionEvent{Method: "anchor", Status: status, Conflict: errors.Is(resultErr, rpc.ErrConflict), Faults: len(ref.Faults), Duration: time.Since(started), Attempts: attempts})
	}()
	firstOpts := pool.CollectOptions{Timeout: timeout}
	if value, ok := refcontext.Members(ctx); ok {
		firstOpts.Sources, _ = value.([]*pool.Source)
	}
	emissions, collectErr := p.Collect(ctx, methods.MethodGetBlockByNumber, []any{"finalized", false}, firstOpts)
	if collectErr != nil {
		ref.Faults = append(ref.Faults, fmt.Errorf("collect: %w", collectErr))
	}
	views := make([]view, 0, len(emissions))
	for _, emission := range emissions {
		source := sourceRef(emission)
		ref.Participants = append(ref.Participants, source)
		v, err := decode(emission.Val)
		if emission.Err != nil {
			err = emission.Err
		}
		if emission.Attempted {
			attempts = append(attempts, observe.SourceEvent{Source: source.Name, RTT: emission.RTT, Failed: err != nil})
		}
		if err != nil {
			if emission.Attempted {
				ref.Faults = append(ref.Faults, fmt.Errorf("source %q: %w", source.Name, err))
			}
			continue
		}
		v.source = source
		views = append(views, v)
	}
	if ctx.Err() != nil {
		return ref, ctx.Err()
	}
	if len(views) == 0 {
		if collectErr != nil {
			return ref, collectErr
		}
		return ref, fmt.Errorf("no valid finalized view: %w", rpc.ErrNoValidResponse)
	}
	heights := make([]uint64, 0, len(views))
	for _, v := range views {
		heights = append(heights, v.height)
	}
	slices.Sort(heights)
	reference := heights[(len(heights)-1)/2]
	fresh := make([]view, 0, len(views))
	for _, v := range views {
		if v.height < reference && reference-v.height > maxLag {
			ref.Faults = append(ref.Faults, fmt.Errorf("source %q: finalized height %d trails reference %d beyond %d blocks", v.source.Name, v.height, reference, maxLag))
			continue
		}
		fresh = append(fresh, v)
	}
	required := len(fresh)
	if quorum > 0 {
		required = max(2, quorum, len(emissions)/2+1)
	}
	if required < 2 || len(fresh) < required {
		return ref, fmt.Errorf("insufficient finalized support: need %d, have %d: %w", max(2, required), len(fresh), rpc.ErrConflict)
	}
	heights = heights[:0]
	for _, v := range fresh {
		heights = append(heights, v.height)
	}
	slices.Sort(heights)
	candidate := heights[0]
	if quorum > 0 {
		candidate = heights[len(heights)-required]
	}
	ref.Height = candidate
	candidateHashes := make(map[string]struct{})
	for _, view := range fresh {
		if view.height == candidate {
			candidateHashes[strings.ToLower(view.hash)] = struct{}{}
		}
	}
	if len(candidateHashes) > 1 {
		return ref, fmt.Errorf("finalized candidate height %d has %d distinct hashes: %w", candidate, len(candidateHashes), rpc.ErrConflict)
	}
	reorgDetected := false

	// Preserve all first-round participants as the voting denominator, including
	// failures and views that cannot support the selected finalized height.
	observations := make([]rpc.Observation, len(emissions))
	indexes := make(map[string]int, len(emissions))
	for i, emission := range emissions {
		source := sourceRef(emission)
		indexes[source.ID] = i
		observations[i] = rpc.Observation{Source: source, Err: fmt.Errorf("no finalized support for height %d", candidate)}
	}
	var missing []string
	for _, v := range fresh {
		if v.height < candidate {
			continue
		}
		if v.height == candidate {
			observations[indexes[v.source.ID]].Value = v.raw
			observations[indexes[v.source.ID]].Err = nil
		} else {
			missing = append(missing, v.source.ID)
		}
	}
	var opts []rpc.StrictOption
	if quorum > 0 {
		opts = append(opts, rpc.WithQuorum(quorum))
	}
	if mode == rpc.CorroborationUnanimity {
		opts = append(opts, rpc.WithUnanimity())
	}
	policy := rpc.Strict(methods.BlockByNumberPolicy().Normalize, opts...)
	initial, initialErr := policy.Resolve(observations)
	// Reuse an already corroborated candidate; no redundant header fan-out.
	if initialErr != nil || len(initial.Support) < required {
		if len(missing) > 0 {
			confirmOpts := pool.CollectOptions{Timeout: timeout, SourceIDs: missing}
			if firstOpts.Sources != nil {
				byID := make(map[string]*pool.Source, len(firstOpts.Sources))
				for _, source := range firstOpts.Sources {
					byID[source.ID()] = source
				}
				confirmOpts.SourceIDs = nil
				for _, id := range missing {
					if source := byID[id]; source != nil {
						confirmOpts.Sources = append(confirmOpts.Sources, source)
					}
				}
			}
			confirmations, err := p.Collect(ctx, methods.MethodGetBlockByNumber, []any{fmt.Sprintf("0x%x", candidate), false}, confirmOpts)
			if err != nil {
				ref.Faults = append(ref.Faults, fmt.Errorf("confirm: %w", err))
			}
			for _, emission := range confirmations {
				source := sourceRef(emission)
				v, decodeErr := decode(emission.Val)
				if emission.Err != nil {
					decodeErr = emission.Err
				}
				if decodeErr == nil && v.height != candidate {
					decodeErr = fmt.Errorf("confirmation height %d does not match %d: %w", v.height, candidate, rpc.ErrMalformedResponse)
				}
				if decodeErr == nil {
					if _, same := candidateHashes[strings.ToLower(v.hash)]; !same {
						reorgDetected = true
						decodeErr = errors.Join(rpc.ErrAnchorMismatch, rpc.ErrReorgDetected,
							fmt.Errorf("confirmation hash %s differs from the established candidate", v.hash))
					}
				}
				if emission.Attempted {
					attempts = append(attempts, observe.SourceEvent{Source: source.Name, RTT: emission.RTT, Failed: decodeErr != nil})
				}
				if decodeErr != nil {
					if emission.Attempted {
						ref.Faults = append(ref.Faults, fmt.Errorf("source %q confirm: %w", source.Name, decodeErr))
					}
					continue
				}
				observations[indexes[source.ID]] = rpc.Observation{Source: source, Value: v.raw}
			}
		}
	}
	if ctx.Err() != nil {
		return ref, ctx.Err()
	}
	decision, err := policy.Resolve(observations)
	ref.Support = decision.Support
	supported := make(map[string]bool, len(ref.Support))
	for _, source := range ref.Support {
		supported[source.ID] = true
	}
	for _, source := range ref.Participants {
		if !supported[source.ID] {
			ref.Reject = append(ref.Reject, source)
		}
	}
	if err != nil {
		if reorgDetected {
			return ref, errors.Join(rpc.ErrReorgDetected, err)
		}
		return ref, err
	}
	if len(decision.Support) < required || decision.Status != rpc.Corroborated {
		err := fmt.Errorf("common reference needs %d supporters, got %d: %w", required, len(decision.Support), rpc.ErrConflict)
		if reorgDetected {
			return ref, errors.Join(rpc.ErrReorgDetected, err)
		}
		return ref, err
	}
	winner, err := decode(decision.Value)
	if err != nil {
		return ref, fmt.Errorf("winning reference: %w", rpc.ErrMalformedResponse)
	}
	ref.Hash, ref.Status = winner.hash, rpc.Corroborated
	return ref, nil
}

func sourceRef(e pool.Emission) rpc.SourceRef {
	return rpc.SourceRef{Name: e.Src.Name(), URL: e.Src.URL(), ID: e.Src.ID()}
}
func decode(raw json.RawMessage) (view, error) {
	if _, err := methods.BlockByNumberPolicy().Normalize(raw); err != nil {
		return view{}, err
	}
	var head struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return view{}, err
	}
	height, err := rpc.ParseHexQuantity(head.Number)
	return view{height: height, hash: strings.ToLower(head.Hash), raw: raw}, err
}
