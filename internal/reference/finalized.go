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

	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

type view struct {
	source rpc.SourceRef
	height uint64
	hash   string
	raw    json.RawMessage
}

// selection is what the first observation round established: one candidate
// height with a single agreed hash, and the frozen voting basis every later
// resolve runs against. Membership and the required support are fixed before
// confirmation; confirmation can only fill the basis, never shrink it.
type selection struct {
	candidate       uint64
	candidateHashes map[string]struct{}
	observations    []rpc.Observation
	indexes         map[string]int
	missing         []string
	required        int
}

// Finalized observes once and, if necessary, confirms one common height once.
// Membership and the required support are fixed before confirmation. It never
// retries successively older candidates or relaxes the caller's quorum.
func Finalized(ctx context.Context, reader HeadReader, maxLag uint64, quorum int, mode rpc.CorroborationMode, timeout time.Duration, rec observe.Recorder) (ref rpc.ReferenceEvidence, resultErr error) {
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
	emissions, collectErr := reader.Read(ctx, "finalized", timeout)
	if collectErr != nil {
		err := fmt.Errorf("collect: %w", collectErr)
		ref.Faults = append(ref.Faults, err)
		ref.CollectionFaults = append(ref.CollectionFaults, err)
	}
	views := decodeViews(emissions, &ref, &attempts)
	if ctx.Err() != nil {
		return ref, ctx.Err()
	}
	if len(views) == 0 {
		if collectErr != nil {
			return ref, collectErr
		}
		return ref, fmt.Errorf("no valid finalized view: %w", rpc.ErrNoValidResponse)
	}

	sel, err := selectCandidate(emissions, views, maxLag, quorum, &ref)
	if err != nil {
		return ref, err
	}
	policy, reorgDetected := confirmSelection(ctx, reader, sel, quorum, mode, timeout, &ref, &attempts)
	if ctx.Err() != nil {
		return ref, ctx.Err()
	}
	decision, err := policy.Resolve(sel.observations)
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
	if len(decision.Support) < sel.required || decision.Status != rpc.Corroborated {
		err := fmt.Errorf("common reference needs %d supporters, got %d: %w", sel.required, len(decision.Support), rpc.ErrConflict)
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

// decodeViews turns the first-round emissions into decodable finalized-head
// views, recording every participant and attributing per-source failures.
func decodeViews(emissions []HeadObservation, ref *rpc.ReferenceEvidence, attempts *[]observe.SourceEvent) []view {
	views := make([]view, 0, len(emissions))
	for _, emission := range emissions {
		source := emission.Source
		ref.Participants = append(ref.Participants, source)
		v, err := decode(emission.Value)
		if emission.Err != nil {
			err = emission.Err
		}
		if emission.Attempted {
			*attempts = append(*attempts, observe.SourceEvent{Source: source.Name, RTT: emission.RTT, Failed: err != nil})
		}
		if err != nil {
			if emission.Attempted {
				ref.Faults = append(ref.Faults, fmt.Errorf("source %q: %w", source.Name, err))
			}
			ref.SourceFaults = append(ref.SourceFaults, rpc.SourceFault{Source: source, Err: err})
			continue
		}
		v.source = source
		views = append(views, v)
	}
	return views
}

// selectCandidate reduces the first-round views to one candidate height under
// the lag filter and freezes the voting basis: all first-round participants
// stay in the denominator with a placeholder rejection, each source that
// observed the candidate height carries its raw head, and sources above the
// candidate are named for the confirmation round. It fails on insufficient
// support and on the candidate height carrying more than one hash.
func selectCandidate(emissions []HeadObservation, views []view, maxLag uint64, quorum int, ref *rpc.ReferenceEvidence) (selection, error) {
	heights := make([]uint64, 0, len(views))
	for _, v := range views {
		heights = append(heights, v.height)
	}
	slices.Sort(heights)
	reference := heights[(len(heights)-1)/2]
	fresh := make([]view, 0, len(views))
	for _, v := range views {
		if v.height < reference && reference-v.height > maxLag {
			err := fmt.Errorf("finalized height %d trails reference %d beyond %d blocks", v.height, reference, maxLag)
			ref.Faults = append(ref.Faults, fmt.Errorf("source %q: %w", v.source.Name, err))
			ref.SourceFaults = append(ref.SourceFaults, rpc.SourceFault{Source: v.source, Err: err})
			continue
		}
		fresh = append(fresh, v)
	}
	required := len(fresh)
	if quorum > 0 {
		required = max(2, quorum, len(emissions)/2+1)
	}
	if required < 2 || len(fresh) < required {
		return selection{}, fmt.Errorf("insufficient finalized support: need %d, have %d: %w", max(2, required), len(fresh), rpc.ErrConflict)
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
		return selection{}, fmt.Errorf("finalized candidate height %d has %d distinct hashes: %w", candidate, len(candidateHashes), rpc.ErrConflict)
	}

	sel := selection{
		candidate:       candidate,
		candidateHashes: candidateHashes,
		observations:    make([]rpc.Observation, len(emissions)),
		indexes:         make(map[string]int, len(emissions)),
		required:        required,
	}
	// Preserve all first-round participants as the voting denominator, including
	// failures and views that cannot support the selected finalized height.
	for i, emission := range emissions {
		source := emission.Source
		sel.indexes[source.ID] = i
		sel.observations[i] = rpc.Observation{Source: source, Err: fmt.Errorf("no finalized support for height %d", candidate)}
	}
	for _, v := range fresh {
		if v.height < candidate {
			continue
		}
		if v.height == candidate {
			sel.observations[sel.indexes[v.source.ID]].Value = v.raw
			sel.observations[sel.indexes[v.source.ID]].Err = nil
		} else {
			sel.missing = append(sel.missing, v.source.ID)
		}
	}
	return sel, nil
}

// confirmSelection reuses an already-corroborated candidate, or fans the
// confirmation request to the members that could not support the candidate
// height, detecting a reorg by hash mismatch against the established
// candidate set. It returns the resolve policy the caller's final vote runs
// on and whether any confirmation contradicted the candidate hash.
func confirmSelection(ctx context.Context, reader HeadReader, sel selection, quorum int, mode rpc.CorroborationMode, timeout time.Duration, ref *rpc.ReferenceEvidence, attempts *[]observe.SourceEvent) (rpc.Policy, bool) {
	var opts []rpc.StrictOption
	if quorum > 0 {
		opts = append(opts, rpc.WithQuorum(quorum))
	}
	if mode == rpc.CorroborationUnanimity {
		opts = append(opts, rpc.WithUnanimity())
	}
	policy := rpc.Strict(methods.BlockByNumberPolicy().Normalize, opts...)
	initial, initialErr := policy.Resolve(sel.observations)
	reorgDetected := false
	// Reuse an already corroborated candidate; no redundant header fan-out.
	if initialErr != nil || len(initial.Support) < sel.required {
		if len(sel.missing) > 0 {
			confirmations, err := reader.Confirm(ctx, sel.candidate, sel.missing, timeout)
			if err != nil {
				err = fmt.Errorf("confirm: %w", err)
				ref.Faults = append(ref.Faults, err)
				ref.CollectionFaults = append(ref.CollectionFaults, err)
			}
			for _, emission := range confirmations {
				source := emission.Source
				v, decodeErr := decode(emission.Value)
				if emission.Err != nil {
					decodeErr = emission.Err
				}
				if decodeErr == nil && v.height != sel.candidate {
					decodeErr = fmt.Errorf("confirmation height %d does not match %d: %w", v.height, sel.candidate, rpc.ErrMalformedResponse)
				}
				if decodeErr == nil {
					if _, same := sel.candidateHashes[strings.ToLower(v.hash)]; !same {
						reorgDetected = true
						decodeErr = errors.Join(rpc.ErrAnchorMismatch, rpc.ErrReorgDetected,
							fmt.Errorf("confirmation hash %s differs from the established candidate", v.hash))
					}
				}
				if emission.Attempted {
					*attempts = append(*attempts, observe.SourceEvent{Source: source.Name, RTT: emission.RTT, Failed: decodeErr != nil})
				}
				if decodeErr != nil {
					if emission.Attempted {
						ref.Faults = append(ref.Faults, fmt.Errorf("source %q confirm: %w", source.Name, decodeErr))
					}
					ref.SourceFaults = append(ref.SourceFaults, rpc.SourceFault{Source: source, Err: decodeErr})
					continue
				}
				sel.observations[sel.indexes[source.ID]] = rpc.Observation{Source: source, Value: v.raw}
			}
		}
	}
	return policy, reorgDetected
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
