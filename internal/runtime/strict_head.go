package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/internal/aggregator"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
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
// identity under a corroboration snapshot, and the purpose's reference gate.
//
// It is the one implementation for strict head references. Both the
// establishment stage (reference rounds inside a business invocation) and the
// facade's ReferenceService (direct queries) drive it, so reference
// rounds and business rounds share members, thresholds, and error assembly.
// The engine owns the round: collection binding, canonical head encoding, and
// evidence projection; it deliberately does not own an invocation or a
// registry — there is no second aggregation coordinator here.
type StrictHeadEstablishment struct {
	pool    *pool.Pool
	rec     observe.Recorder
	timeout time.Duration
	// head is the getBlockByNumber resolver the round's head identity rides:
	// the plan compiler's own assembly. The invocation supplies its rule.
	head rpcengine.CorroboratingResolver
}

// NewStrictHeadEstablishment builds the strict head establishment engine over
// one pool. A zero timeout selects the pool's conservative collect default.
func NewStrictHeadEstablishment(p *pool.Pool, rec observe.Recorder, timeout time.Duration) *StrictHeadEstablishment {
	spec, _ := methods.SpecFor(methods.MethodGetBlockByNumber)
	head, err := methods.CompileResolver(spec, nil)
	if err != nil {
		// The registered spec always compiles; a failure here is a
		// programmer error, not a runtime condition.
		panic("runtime: compile the strict head resolver: " + err.Error())
	}
	corroborating, ok := head.(rpcengine.CorroboratingResolver)
	if !ok {
		panic("runtime: strict head resolver lacks corroboration")
	}
	return &StrictHeadEstablishment{pool: p, rec: rec, timeout: timeout, head: corroborating}
}

// strictHeadRound carries the outcome of one establishment round: the
// reference evidence, every emission the round produced, the admission-time
// original response facts, and the pool-level collection verdict, so a
// caller with an invocation records the exact raw trail against it.
type strictHeadRound struct {
	reference  rpc.ReferenceEvidence
	emissions  []pool.Emission
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
// Every round uses fixed-basis voting with the caller's construction rule.
// A failed round returns partial evidence alongside the error: the faults stay
// on the reference the caller sees.
func (e *StrictHeadEstablishment) establish(ctx context.Context, members []*pool.Source, snap rpcengine.Corroboration, tag string, purpose rpcengine.ReferencePurpose) (strictHeadRound, error) {
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
	emissions, collectErr := e.pool.CollectVoting(ctx, methods.MethodGetBlockByNumber, []any{tag, false}, collectOptions)
	// Admission's shape-reservation stage runs before resolution, exactly as
	// the pipeline's does: a malformed body can never win a cluster. The raw
	// response keeps its original bytes while admission records its judgment.
	aggregator.RejectUnsafeJSON(emissions)
	observations := make([]rpc.Observation, len(emissions))
	participants := make([]rpc.SourceRef, 0, len(emissions))
	for i, emission := range emissions {
		observations[i] = aggregator.ObservationFromEmission(emission)
		participants = append(participants, observations[i].Source)
	}

	decision := rpc.Decision{Status: rpc.Unknown, Participants: participants}
	slotOutcome := aggregator.RoundOutcome(emissions)
	decision.Sources, decision.SourceFaults, decision.Faults = slotOutcome.Decision.Sources, slotOutcome.Decision.SourceFaults, slotOutcome.Decision.Faults
	decision.Adjudication = slotOutcome.Decision.Adjudication
	decision.Adjudication.Required = snap.Required(len(members))
	if collectErr != nil && !aggregator.RoundHasUsable(emissions) {
		// A terminal collection failure with nothing usable returns the pool's
		// own sentinel, with the round's faults attached.
		decision = attachRoundFaults(decision, collectErr)
		e.record(started, decision, collectErr, emissions, slotOutcome.Verdicts)
		return strictHeadRound{reference: evidenceFromDecision(decision, tag), emissions: emissions, collectErr: collectErr}, collectErr
	}
	if collectErr == nil && !aggregator.RoundHasUsable(emissions) {
		if malformed, malformedErr := aggregator.RoundMalformedFault(emissions); malformed {
			err := fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, malformedErr)
			e.record(started, decision, err, emissions, slotOutcome.Verdicts)
			return strictHeadRound{reference: evidenceFromDecision(decision, tag), emissions: emissions, collectErr: collectErr}, err
		}
	}

	outcome, resolveErr := policy.ResolveCorroborated(snap, observations)
	// The resolution's own decision does not carry the fan-out's membership;
	// every emission keeps the participant slot the pipeline's project phase
	// gives it.
	if contractErr := outcome.Validate(observations); contractErr != nil {
		resolveErr = errors.Join(contractErr, resolveErr)
		outcome = slotOutcome
		outcome.Decision.Adjudication.Reason = rpc.ReasonInternalFailure
	}
	decision = outcome.Decision
	verdicts := outcome.Verdicts
	decision.Participants = participants
	decision = attachRoundFaults(decision, collectErr)
	reference := evidenceFromDecision(decision, tag)
	if resolveErr != nil {
		e.record(started, decision, resolveErr, emissions, verdicts)
		return strictHeadRound{reference: reference, emissions: emissions, collectErr: collectErr}, resolveErr
	}
	hash, height, err := blockRefFromDecision(decision)
	if err == nil {
		candidate := reference
		candidate.Hash, candidate.Height = hash, height
		refs := make([]rpc.SourceRef, len(members))
		for i, member := range members {
			refs[i] = aggregator.SourceRefOf(member)
		}
		err = rpcengine.ValidateReferenceEvidence(purpose, rpcengine.FreezeBasis(refs, snap), tag, candidate)
		if err == nil {
			reference = candidate
		} else if errors.Is(err, rpc.ErrGuaranteeNotMet) {
			decision.Adjudication.Reason = rpc.ReasonGuaranteeNotMet
			reference.Adjudication.Reason = rpc.ReasonGuaranteeNotMet
		}
	}
	e.record(started, decision, err, emissions, verdicts)
	return strictHeadRound{reference: reference, emissions: emissions, collectErr: collectErr}, err
}

// attachRoundFaults folds one completed round's fault tables into the
// decision, the way the pipeline's project phase does: collection failures
// ride both fault lists, each failed attempted source keeps its
// human-readable fault and its structured attribution.
func attachRoundFaults(decision rpc.Decision, collectErr error) rpc.Decision {
	if collectErr != nil {
		decision.Faults = append(decision.Faults, aggregator.CollectFault(collectErr))
		decision.CollectionFaults = append(decision.CollectionFaults, aggregator.CollectFault(collectErr))
	}
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
		Sources: decision.Sources, Adjudication: decision.Adjudication,
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
func (e *StrictHeadEstablishment) record(started time.Time, decision rpc.Decision, resolveErr error, emissions []pool.Emission, verdicts []rpcengine.SourceVerdict) {
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
	class := aggregator.RoundAttempts(verdicts)
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
