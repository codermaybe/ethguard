package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// taggedOriginTag keeps the establishment evidence's tag contract: latest
// rides as the default reference and records no tag.
func taggedOriginTag(tag string) string {
	if tag == string(rpc.ReferenceLatest) {
		return ""
	}
	return tag
}

// StrictHeadEstablishment runs the strict head establishment algorithm: one
// voting fan-out over a frozen member snapshot, strict clustering on the head
// identity under a corroboration snapshot, and the Corroborated gate.
//
// It is the one implementation for strict head references. Both the
// establishment stage (reference rounds inside a business invocation) and the
// facade's StrictHeadService (direct resolver calls) drive it, so reference
// rounds and business rounds share members, thresholds, and error assembly.
// The engine owns the round: collection binding, canonical head encoding, and
// evidence projection; it deliberately does not own an invocation or a
// registry — there is no second aggregation coordinator here.
type StrictHeadEstablishment struct {
	pool    *pool.Pool
	rec     observe.Recorder
	timeout time.Duration
	// head is the getBlockByNumber resolver the round's head identity rides:
	// the plan compiler's own assembly, with the establishment's strict
	// options.
	head rpc.Policy
}

// NewStrictHeadEstablishment builds the strict head establishment engine over
// one pool. A zero timeout selects the pool's conservative collect default.
func NewStrictHeadEstablishment(p *pool.Pool, rec observe.Recorder, timeout time.Duration, opts ...rpc.StrictOption) *StrictHeadEstablishment {
	spec, _ := methods.SpecFor(methods.MethodGetBlockByNumber)
	head, err := methods.CompileResolver(spec, opts)
	if err != nil {
		// The registered spec always compiles; a failure here is a
		// programmer error, not a runtime condition.
		panic("runtime: compile the strict head resolver: " + err.Error())
	}
	return &StrictHeadEstablishment{pool: p, rec: rec, timeout: timeout, head: head}
}

// strictHeadRound carries the outcome of one establishment round: the
// reference evidence, every emission the round produced, the admission-time
// raw copy of those emissions, and the pool-level collection verdict, so a
// caller with an invocation records the exact raw trail against it.
type strictHeadRound struct {
	reference  rpc.ReferenceEvidence
	emissions  []pool.Emission
	raw        []pool.Emission
	collectErr error
}

// referenceTipFreshness maps an establishment tag to the scheduling scope the
// pool applies to that round. The sampler observes latest block numbers, so it
// can gate latest reads; safe and finalized views are not described by those
// samples and must not be paused as if latest were their freshness level.
func referenceTipFreshness(tag string) pool.TipFreshnessScope {
	if rpc.ReferenceKindForTag(tag) == rpc.ReferenceLatest {
		return pool.TipFreshnessLatest
	}
	return pool.TipFreshnessExempt
}

// establish runs one round for the requested tag. A nil member snapshot
// selects the pool's current voting members (direct facade calls); an
// explicit snapshot is the invocation's frozen basis (establishment stage).
// When the caller carries a corroboration snapshot the fan-out runs in the
// fixed-basis voting mode with the snapshot adjudicating; without one the
// round keeps the collection and the agreement semantics of direct
// construction. A failed round returns the partial evidence alongside the
// error: the faults stay on the reference the caller sees.
func (e *StrictHeadEstablishment) establish(ctx context.Context, members []*pool.Source, snap rpc.Corroboration, snapOK bool, tag string) (strictHeadRound, error) {
	started := time.Now()
	policy := headAnchorPolicy{e.head}
	if members == nil {
		members = e.pool.VotingMembers()
	}
	collectOptions := pool.CollectOptions{
		Timeout:      e.timeout,
		Sources:      members,
		TipFreshness: referenceTipFreshness(tag),
	}
	var (
		emissions  []pool.Emission
		collectErr error
	)
	if snapOK {
		// A corroborated read collects over the fixed voting basis — every
		// member occupies the denominator even when a scheduling gate skipped
		// its request.
		emissions, collectErr = e.pool.CollectVoting(ctx, methods.MethodGetBlockByNumber, []any{tag, false}, collectOptions)
	} else {
		emissions, collectErr = e.pool.Collect(ctx, methods.MethodGetBlockByNumber, []any{tag, false}, collectOptions)
	}
	// Admission's shape-reservation stage runs before resolution, exactly as
	// the pipeline's does: a malformed body can never win a cluster. The raw
	// values are preserved first so the invocation's capture trail keeps the
	// bytes admission is about to clear.
	rawValues := preserveRawValues(emissions)
	for i := range emissions {
		if emissions[i].Err != nil || emissions[i].Val == nil {
			continue
		}
		if err := jsonsafe.Validate(emissions[i].Val); err != nil {
			emissions[i].Err = fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
			emissions[i].Val = nil
		}
	}
	raw := rawEmissions(emissions, rawValues)
	observations := make([]rpc.Observation, len(emissions))
	participants := make([]rpc.SourceRef, 0, len(emissions))
	for i, emission := range emissions {
		observations[i] = aggregator.ObservationFromEmission(emission)
		participants = append(participants, observations[i].Source)
	}

	decision := rpc.Decision{Status: rpc.Unknown, Participants: participants}
	if collectErr != nil && !aggregator.RoundHasUsable(emissions) {
		// A terminal collection failure with nothing usable returns the pool's
		// own sentinel, with the round's faults attached.
		decision = attachRoundFaults(decision, collectErr, nil, policy, emissions)
		e.record(started, policy, decision, collectErr, emissions, nil)
		return strictHeadRound{reference: evidenceFromDecision(decision, tag), emissions: emissions, raw: raw, collectErr: collectErr}, collectErr
	}
	if collectErr == nil && !aggregator.RoundHasUsable(emissions) {
		if malformed, malformedErr := aggregator.RoundMalformedFault(emissions); malformed {
			err := fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, malformedErr)
			decision.Faults, decision.SourceFaults = aggregator.RoundFaults(policy, emissions, nil)
			e.record(started, policy, decision, err, emissions, nil)
			return strictHeadRound{reference: evidenceFromDecision(decision, tag), emissions: emissions, raw: raw, collectErr: collectErr}, err
		}
	}

	var resolveErr error
	var verdicts []rpc.SourceVerdict
	var resolution rpc.Policy = policy
	if snapOK {
		if corroborating, ok := resolution.(rpc.CorroboratingPolicy); ok {
			decision, resolveErr = corroborating.ResolveCorroborated(snap, observations)
		} else {
			decision, resolveErr = resolution.Resolve(observations)
		}
	} else {
		decision, resolveErr = resolution.Resolve(observations)
	}
	// The resolution's own decision does not carry the fan-out's membership;
	// every emission keeps the participant slot the pipeline's project phase
	// gives it.
	decision.Participants = participants
	// The head policy does not speak per-source verdicts through this
	// resolution, so classification keeps the policy's validity contract —
	// the same statement the pipeline projects for the facade service.
	decision = attachRoundFaults(decision, collectErr, verdicts, policy, emissions)
	e.record(started, policy, decision, resolveErr, emissions, verdicts)
	reference := evidenceFromDecision(decision, tag)
	if resolveErr != nil {
		return strictHeadRound{reference: reference, emissions: emissions, raw: raw, collectErr: collectErr}, resolveErr
	}
	hash, height, err := blockRefFromDecision(decision)
	reference.Hash, reference.Height = hash, height
	return strictHeadRound{reference: reference, emissions: emissions, raw: raw, collectErr: collectErr}, err
}

// preserveRawValues copies the round's delivered values before admission can
// clear a malformed one. It mirrors the pipeline's admission ordering: the
// capture trail keeps the wire bytes; the working emissions keep the
// cleared-value rule.
func preserveRawValues(emissions []pool.Emission) [][]byte {
	if emissions == nil {
		return nil
	}
	values := make([][]byte, len(emissions))
	for i, emission := range emissions {
		if emission.Val != nil {
			values[i] = append([]byte(nil), emission.Val...)
		}
	}
	return values
}

// rawEmissions pairs the post-admission faults with the preserved values, so
// a capture record shows both the wire bytes and why admission refused them.
func rawEmissions(emissions []pool.Emission, values [][]byte) []pool.Emission {
	if emissions == nil {
		return nil
	}
	raw := make([]pool.Emission, len(emissions))
	for i, emission := range emissions {
		raw[i] = emission
		raw[i].Val = values[i]
	}
	return raw
}

// attachRoundFaults folds one completed round's fault tables into the
// decision, the way the pipeline's project phase does: collection failures
// ride both fault lists, each failed attempted source keeps its
// human-readable fault and its structured attribution.
func attachRoundFaults(decision rpc.Decision, collectErr error, verdicts []rpc.SourceVerdict, policy rpc.Policy, emissions []pool.Emission) rpc.Decision {
	if collectErr != nil {
		decision.Faults = append(decision.Faults, aggregator.CollectFault(collectErr))
		decision.CollectionFaults = append(decision.CollectionFaults, aggregator.CollectFault(collectErr))
	}
	faults, sourceFaults := aggregator.RoundFaults(policy, emissions, verdicts)
	decision.Faults = append(decision.Faults, faults...)
	decision.SourceFaults = append(decision.SourceFaults, sourceFaults...)
	return decision
}

// evidenceFromDecision maps one assembled round decision into the reference
// evidence every strict head caller sees: the decision's own fields, the
// requested tag contract (latest rides as the default reference with no tag),
// and the observation timestamp at assembly — after the round, before the
// evidence leaves the engine.
func evidenceFromDecision(decision rpc.Decision, tag string) rpc.ReferenceEvidence {
	origin := "strict-head"
	if tag != "" && tag != string(rpc.ReferenceLatest) {
		origin = "tagged-head"
	}
	return rpc.ReferenceEvidence{
		Tag:              taggedOriginTag(tag),
		Status:           decision.Status,
		Origin:           origin,
		ObservedAt:       time.Now(),
		Participants:     decision.Participants,
		Support:          decision.Support,
		Reject:           decision.Reject,
		Faults:           decision.Faults,
		SourceFaults:     decision.SourceFaults,
		CollectionFaults: decision.CollectionFaults,
	}
}

// record folds one completed round into exactly one observability event under
// the anchor method label — the same statement the aggregation pipeline
// records for the facade service's rounds.
func (e *StrictHeadEstablishment) record(started time.Time, policy rpc.Policy, decision rpc.Decision, resolveErr error, emissions []pool.Emission, verdicts []rpc.SourceVerdict) {
	if e.rec == nil || e.rec == observe.Nop {
		return
	}
	event := observe.DecisionEvent{
		Method:   anchorMethodName,
		Duration: time.Since(started),
		Faults:   len(decision.Faults),
	}
	if resolveErr != nil {
		event.Status = "failed"
		event.Conflict = errors.Is(resolveErr, rpc.ErrConflict)
	} else {
		event.Status = decision.Status.String()
	}
	class := aggregator.RoundAttempts(policy, verdicts)
	for _, emission := range emissions {
		if !emission.Attempted {
			continue // never acquired a slot: no attempt, no observation
		}
		event.Attempts = append(event.Attempts, observe.SourceEvent{
			Source: emission.Src.Name(),
			RTT:    emission.RTT,
			Failed: !class(emission),
		})
	}
	e.rec.RecordDecision(event)
}
