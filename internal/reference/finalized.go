// Package reference establishes a corroborated reference before state reads.
package reference

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
)

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
func Finalized(ctx context.Context, reader HeadReader, codec HeadCodec, maxLag uint64, quorum int, mode rpc.CorroborationMode, timeout time.Duration, rec observe.Recorder) (ref rpc.ReferenceEvidence, resultErr error) {
	started := time.Now()
	ref.Origin, ref.ObservedAt, ref.Status = "reported-finalized", started, rpc.Unknown
	var attempts []observe.SourceEvent
	var observations []rpc.Observation
	var resolved *rpcengine.Outcome

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
	defer func() {
		outcome := rpcengine.UnassessedOutcome(observations)
		if resolved != nil {
			outcome = *resolved
		} else {
			outcome.Decision.Adjudication = ref.Adjudication
		}
		ref.Sources, ref.SourceFaults = outcome.Decision.Sources, outcome.Decision.SourceFaults
		ref.Faults = append(append([]error(nil), ref.CollectionFaults...), outcome.Decision.Faults...)
		ref.Adjudication = outcome.Decision.Adjudication
	}()
	emissions, collectErr := reader.Read(ctx, "finalized", timeout)
	if collectErr != nil {
		err := fmt.Errorf("collect: %w", collectErr)
		ref.Faults = append(ref.Faults, err)
		ref.CollectionFaults = append(ref.CollectionFaults, err)
	}
	views := decodeViews(emissions, codec, &ref, &attempts)
	observations = make([]rpc.Observation, len(emissions))
	for i, emission := range emissions {
		observations[i] = rpc.Observation{Source: emission.Source, Attempted: emission.Attempted, Response: emission.Response}
	}
	ref.Adjudication = rpc.Adjudication{Basis: len(emissions), Required: max(2, quorum, len(emissions)/2+1)}
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
		for i := range emissions {
			observations[i].Response = emissions[i].Response
		}
		return ref, err
	}
	observations = sel.observations
	policy, reorgDetected := confirmSelection(ctx, reader, codec, sel, quorum, mode, timeout, &ref, &attempts)
	if ctx.Err() != nil {
		return ref, ctx.Err()
	}
	outcome, err := policy.Resolve(sel.observations)
	resolved = &outcome
	decision := outcome.Decision
	ref.Status = decision.Status
	ref.Sources, ref.Adjudication = decision.Sources, decision.Adjudication
	ref.Support = decision.Support
	ref.Reject = decision.Reject
	if err != nil {
		if reorgDetected {
			return ref, errors.Join(rpc.ErrReorgDetected, err)
		}
		return ref, err
	}
	if len(decision.Support) < sel.required || decision.Status != rpc.Corroborated {
		outcome.Decision.Adjudication.Reason = rpc.ReasonGuaranteeNotMet
		err := fmt.Errorf("common reference needs %d supporters, got %d: %w", sel.required, len(decision.Support), rpc.ErrGuaranteeNotMet)
		if reorgDetected {
			return ref, errors.Join(rpc.ErrReorgDetected, err)
		}
		return ref, err
	}
	winner, err := codec.Decode(decision.Value)
	if err != nil {
		outcome.Decision.Adjudication.Reason = rpc.ReasonInternalFailure
		return ref, fmt.Errorf("winning reference: %w", rpc.ErrInvalidCanonicalResult)
	}
	ref.Hash, ref.Status = winner.Hash, rpc.Corroborated
	return ref, nil
}

// decodeViews turns the first-round emissions into decodable finalized-head
// views, recording every participant and attributing per-source failures.
func decodeViews(emissions []HeadObservation, codec HeadCodec, ref *rpc.ReferenceEvidence, attempts *[]observe.SourceEvent) []HeadView {
	views := make([]HeadView, 0, len(emissions))
	for i := range emissions {
		emission := &emissions[i]
		source := emission.Source
		ref.Participants = append(ref.Participants, source)
		value, err := emission.Response.Candidate(false)
		var v HeadView
		if err == nil {
			v, err = codec.Decode(value)
			judgment := rpc.Interpretation{State: rpc.InterpretationAdmitted}
			if err != nil {
				judgment = rpc.Interpretation{State: rpc.InterpretationRejected, Cause: err}
			}
			emission.Response.Answer.Interpretation = judgment
		}
		if emission.Attempted {
			*attempts = append(*attempts, observe.SourceEvent{Source: source.Name, RTT: emission.RTT, Failed: err != nil})
		}
		if err != nil {
			continue
		}
		v.Source = source
		views = append(views, v)
	}
	return views
}

// selectCandidate reduces the first-round views to one candidate height under
// the lag filter and freezes the voting basis: all first-round participants
// stay in the denominator with their actual response, each source that
// observed the candidate height carries its raw head, and sources above the
// candidate are named for the confirmation round. It fails on insufficient
// support and on the candidate height carrying more than one hash.
func selectCandidate(emissions []HeadObservation, views []HeadView, maxLag uint64, quorum int, ref *rpc.ReferenceEvidence) (selection, error) {
	heights := make([]uint64, 0, len(views))
	for _, v := range views {
		heights = append(heights, v.Height)
	}
	slices.Sort(heights)
	reference := heights[(len(heights)-1)/2]
	fresh := make([]HeadView, 0, len(views))
	for _, v := range views {
		if v.Height < reference && reference-v.Height > maxLag {
			err := fmt.Errorf("finalized height %d trails reference %d beyond %d blocks", v.Height, reference, maxLag)
			for i := range emissions {
				if emissions[i].Source.ID == v.Source.ID {
					emissions[i].Response.Answer.Interpretation = rpc.Interpretation{State: rpc.InterpretationExcluded, Cause: err}
				}
			}
			continue
		}
		fresh = append(fresh, v)
	}
	required := len(fresh)
	if quorum > 0 {
		required = max(2, quorum, len(emissions)/2+1)
	}
	// Before policy resolution, these are decoded height/hash candidates, not
	// votes. Their sources remain Admitted until Normalize completes; a fork
	// can stop establishment here without erasing the compared groups.
	ref.Adjudication.Required = max(2, required)
	groups := make(map[string]int)
	for _, view := range fresh {
		key := fmt.Sprintf("%d:%s", view.Height, strings.ToLower(view.Hash))
		index, exists := groups[key]
		if !exists {
			index = len(ref.Adjudication.Candidates)
			groups[key] = index
			ref.Adjudication.Candidates = append(ref.Adjudication.Candidates, rpc.CandidateGroup{})
		}
		ref.Adjudication.Candidates[index].Sources = append(ref.Adjudication.Candidates[index].Sources, view.Source)
	}
	if required < 2 || len(fresh) < required {
		ref.Adjudication.Reason = rpc.ReasonInsufficientSupport
		ref.Adjudication.Required = max(2, required)
		return selection{}, fmt.Errorf("insufficient finalized support: need %d, have %d: %w", max(2, required), len(fresh), rpc.ErrInsufficientSupport)
	}
	heights = heights[:0]
	for _, v := range fresh {
		heights = append(heights, v.Height)
	}
	slices.Sort(heights)
	candidate := heights[0]
	if quorum > 0 {
		candidate = heights[len(heights)-required]
	}
	ref.Height = candidate
	candidateHashes := make(map[string]struct{})
	for _, view := range fresh {
		if view.Height == candidate {
			candidateHashes[strings.ToLower(view.Hash)] = struct{}{}
		}
	}
	// Only the selected height participates in this early fork comparison.
	candidateGroups := make([]rpc.CandidateGroup, 0, len(candidateHashes))
	for _, view := range fresh {
		if view.Height != candidate {
			continue
		}
		key := fmt.Sprintf("%d:%s", view.Height, strings.ToLower(view.Hash))
		index := groups[key]
		if index < 0 {
			continue
		}
		candidateGroups = append(candidateGroups, ref.Adjudication.Candidates[index])
		groups[key] = -1
	}
	ref.Adjudication.Candidates = candidateGroups
	if len(candidateHashes) > 1 {
		ref.Adjudication.Reason = rpc.ReasonConflictingCandidates
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
		response := emission.Response
		if _, err := response.Candidate(false); err == nil {
			response.Answer.Interpretation = rpc.Interpretation{State: rpc.InterpretationExcluded, Cause: fmt.Errorf("no finalized support for height %d", candidate)}
		}
		sel.observations[i] = rpc.Observation{Attempted: emission.Attempted, Source: source, Response: response}
	}
	for _, view := range fresh {
		if view.Height < candidate {
			continue
		}
		if view.Height == candidate {
			index := sel.indexes[view.Source.ID]
			sel.observations[index].Response = emissions[index].Response
		} else {
			sel.missing = append(sel.missing, view.Source.ID)
		}
	}
	return sel, nil
}

// confirmSelection reuses an already-corroborated candidate, or fans the
// confirmation request to the members that could not support the candidate
// height, detecting a reorg by hash mismatch against the established
// candidate set. It returns the resolve policy the caller's final vote runs
// on and whether any confirmation contradicted the candidate hash.
func confirmSelection(ctx context.Context, reader HeadReader, codec HeadCodec, sel selection, quorum int, mode rpc.CorroborationMode, timeout time.Duration, ref *rpc.ReferenceEvidence, attempts *[]observe.SourceEvent) (rpcengine.Resolver, bool) {
	var opts []rpcengine.StrictOption
	if quorum > 0 {
		opts = append(opts, rpcengine.WithQuorum(quorum))
	}
	if mode == rpc.CorroborationUnanimity {
		opts = append(opts, rpcengine.WithUnanimity())
	}
	policy := rpcengine.Strict(codec.Normalize, opts...)
	support := 0
	for _, item := range sel.observations {
		if item.Response.CandidateErr(false) == nil {
			support++
		}
	}
	reorgDetected := false
	// Selection already decoded these matching height/hash answers. Counting
	// their support avoids running the resolver twice on the same inputs.
	if support < sel.required {
		if len(sel.missing) > 0 {
			confirmations, err := reader.Confirm(ctx, sel.candidate, sel.missing, timeout)
			if err != nil {
				err = fmt.Errorf("confirm: %w", err)
				ref.Faults = append(ref.Faults, err)
				ref.CollectionFaults = append(ref.CollectionFaults, err)
			}
			for _, emission := range confirmations {
				source := emission.Source
				value, decodeErr := emission.Response.Candidate(false)
				var v HeadView
				if decodeErr == nil {
					v, decodeErr = codec.Decode(value)
				}
				if decodeErr == nil && v.Height != sel.candidate {
					decodeErr = fmt.Errorf("confirmation height %d does not match %d: %w", v.Height, sel.candidate, rpc.ErrMalformedResponse)
				}
				if decodeErr == nil {
					if _, same := sel.candidateHashes[strings.ToLower(v.Hash)]; !same {
						reorgDetected = true
						decodeErr = errors.Join(rpc.ErrAnchorMismatch, rpc.ErrReorgDetected,
							fmt.Errorf("confirmation hash %s differs from the established candidate", v.Hash))
					}
				}
				if emission.Response.Err() == nil {
					judgment := rpc.Interpretation{State: rpc.InterpretationAdmitted}
					if decodeErr != nil {
						judgment = rpc.Interpretation{State: rpc.InterpretationRejected, Cause: decodeErr}
					}
					emission.Response.Answer.Interpretation = judgment
				}
				// A confirmation is a new round, including its failures. Never
				// retain an older successful value beside a new failed attempt.
				if index, ok := sel.indexes[source.ID]; ok {
					sel.observations[index] = rpc.Observation{Attempted: emission.Attempted, Source: source, Response: emission.Response}
				}
				if emission.Attempted {
					*attempts = append(*attempts, observe.SourceEvent{Source: source.Name, RTT: emission.RTT, Failed: decodeErr != nil})
				}

			}
		}
	}
	return policy, reorgDetected
}
