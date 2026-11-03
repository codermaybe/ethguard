// Package aggregator resolves raw multi-source observations using method
// policies. It does not own connections or source health.
package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// Aggregator is the small orchestration seam between pool and method policy.
type Aggregator struct {
	pool           *pool.Pool
	rec            observe.Recorder
	collectTimeout time.Duration
	projectors     map[string]rpc.ObservationProjector
	// corrob is the runtime-mutable adjudication configuration shared by one
	// client's aggregators; nil leaves policies on construction-time
	// defaults (direct construction, legacy tests).
	corrob *rpc.CorroborationShared
	// stage is the reference establishment seam. When set, the anchor readers
	// inside policy Prepare resolve it from the invocation context, so
	// establishment runs on the aggregator's invocation.
	stage ReferenceStage
	// plans are the read methods driven by explicit execution plans. A
	// method with a plan runs the plan pipeline; every other method keeps
	// the compiled policy path.
	plans map[string]rpc.ReadPlan
}

// Option tunes an Aggregator at construction time.
type Option func(*Aggregator)

// WithRecorder routes one observability event per aggregate decision into r.
func WithRecorder(r observe.Recorder) Option {
	return func(a *Aggregator) {
		if r != nil {
			a.rec = r
		}
	}
}

// WithCollectTimeout bounds the pool operation used by each aggregate call.
func WithCollectTimeout(timeout time.Duration) Option {
	return func(a *Aggregator) { a.collectTimeout = timeout }
}

// WithCorroboration shares one runtime-mutable corroboration configuration
// across the client's aggregators. Each invocation snapshots the mode and
// support floor at its start (decision 0025); corroborated reads then collect
// the full voting basis and resolve under the snapshot instead of
// construction-time defaults.
func WithCorroboration(c *rpc.CorroborationShared) Option {
	return func(a *Aggregator) {
		if c != nil {
			a.corrob = c
		}
	}
}

// WithReferenceStage installs the reference establishment seam. The stage is
// what the anchor readers resolve while preparing a moving-tag read, so the
// reference rounds run on the aggregator's invocation.
func WithReferenceStage(stage ReferenceStage) Option {
	return func(a *Aggregator) {
		if stage != nil {
			a.stage = stage
		}
	}
}

// WithProjectors registers method-level semantic projectors separately from
// policies. Keeping this assembly map outside Policy preserves every optional
// policy capability (admission, anchoring, corroboration, forwarding).
//
// The map key is the projection's resolution key, which depends on the
// route: a planned read resolves its projector by the plan's declared
// projector ID (a business identity that may differ from the method name
// and may be shared across reads), while a chained legacy read still
// resolves by method name. Nil projectors are skipped.
func WithProjectors(projectors map[string]rpc.ObservationProjector) Option {
	return func(a *Aggregator) {
		for key, projector := range projectors {
			if projector != nil {
				a.projectors[key] = projector
			}
		}
	}
}

func New(p *pool.Pool, opts ...Option) *Aggregator {
	a := &Aggregator{pool: p, rec: observe.Nop, projectors: make(map[string]rpc.ObservationProjector), plans: make(map[string]rpc.ReadPlan)}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// corroboration captures the shared configuration for one invocation. A nil
// shared configuration yields no snapshot, so the aggregator falls back to
// each policy's construction-time resolution.
func (a *Aggregator) corroboration() (rpc.Corroboration, bool) {
	if a.corrob == nil {
		return rpc.Corroboration{}, false
	}
	return a.corrob.Snapshot(), true
}

// resolve applies the invocation's frozen corroboration snapshot when the
// policy supports it; policies without the capability resolve with their
// construction-time configuration.
// invocationResolution is one resolution's complete statement: the decision
// plus, when the policy stated them, every source's verdict. Nil verdicts
// mean the policy cannot speak per-source outcomes, and classification falls
// back to the policy's own validity contract.
type invocationResolution struct {
	decision rpc.Decision
	verdicts []rpc.SourceVerdict
	err      error
}

// Aggregate collects one request and resolves it according to its policy.
//
// The returned error reports why no classified decision was reached.
// Failures that did not prevent a decision — a partial pool-level collection
// error or individual attempted sources failing or timing out — are attached
// to Decision.Faults instead, keeping partial fan-outs diagnosable. A terminal
// collection error with no usable emission is returned directly so the pool's
// public sentinel is not masked by a policy's empty-input error.
//
// The phases run in fixed order over one invocation (see invocation.go):
// Prepare → ReferenceStage → PrepareWithReference → Collect → Admit →
// Resolve → Project. A moving-tag read's establishment rounds run inside the
// same invocation — one ID, one frozen basis, no nested aggregate.
func (a *Aggregator) Aggregate(ctx context.Context, req rpc.Request) (rpc.Decision, error) {
	started := time.Now()
	ctx, uncertainDone := uncertain.Begin(ctx)
	defer uncertainDone()
	ctx, capture := refcontext.New(ctx)
	// The corroboration mode and support floor are frozen here, before
	// preparation and any request: a concurrent Set only reaches
	// invocations that start later (decision 0025).
	snap, snapOK := a.corroboration()
	members := a.pool.VotingMembers()
	// The plan is the only execution shape. A method outside the registry
	// rides the explicit forward plan: one bounded single-source attempt, the
	// first usable answer wins, and no agreement is claimed (decision 0002).
	plan, planned := a.plans[req.Method]
	if !planned {
		plan = forwardPlan(req.Method)
	}
	state := &invocationState{
		ctx:           ctx,
		plan:          &plan,
		snap:          snap,
		snapOK:        snapOK,
		corroborating: snapOK && plan.Corroborating(),
		members:       members,
		capture:       capture,
	}
	state.ctx, state.invocation = beginInvocation(state.ctx, snap, snapOK, members, uncertain.FromContext(ctx))

	if exit := a.planPreparePhase(state, started, req); exit != nil {
		return exit.decision, exit.err
	}
	a.collectPhase(state)
	a.admitPhase(state)
	if exit := a.resolvePhase(state, started); exit != nil {
		return exit.decision, exit.err
	}
	decision, resolveErr := state.resolveValue()
	return a.projectPhase(state, started, req, decision, resolveErr)
}

// unknownDecision builds the shared shape of the two degraded early-return
// decisions — everything the invocation knows without a resolved value. Each
// caller attaches its phase-specific fault lists after it.
func unknownDecision(reference *rpc.ReferenceEvidence, participants []rpc.SourceRef, prepared *rpc.Request) rpc.Decision {
	return rpc.Decision{
		Status:       rpc.Unknown,
		Reference:    reference,
		Participants: participants,
		Prepared:     prepared,
	}
}

// sourceFaultOf renders one field-aggregation abstention with the source
// identity, matching the resolver faults' attribution shape.
func sourceFaultOf(m rpc.MalformedSource) error {
	id := m.Source.ID
	if id == "" {
		id = m.Source.Name
	}
	return fmt.Errorf("source %q: malformed response: %v", id, m.Err)
}

// preparedRequest copies the request observed at the policy boundary so later
// mutation cannot rewrite recorded evidence.
func preparedRequest(req rpc.Request) *rpc.Request {
	return &rpc.Request{Method: req.Method, Params: append([]any(nil), req.Params...)}
}

func collectionFaultList(collectErr error) []error {
	if collectErr == nil {
		return nil
	}
	return []error{fmt.Errorf("collect: %w", collectErr)}
}

func hasMalformedAdmission(emissions []pool.Emission) bool {
	for _, emission := range emissions {
		if errors.Is(emission.Err, rpc.ErrMalformedResponse) {
			return true
		}
	}
	return false
}

func firstMalformedAdmission(emissions []pool.Emission) error {
	for _, emission := range emissions {
		if errors.Is(emission.Err, rpc.ErrMalformedResponse) {
			return emission.Err
		}
	}
	return rpc.ErrMalformedResponse
}

// finalizeVerdicts reclassifies skipped members from the coordinator's attempt
// bookkeeping. The resolver sees only observations, so a member whose request
// a scheduling gate paused reads as unusable to it; the coordinator knows the
// call never started and marks it absent. Verdicts stay ordered by source ID.
func finalizeVerdicts(verdicts []rpc.SourceVerdict, emissions []pool.Emission) []rpc.SourceVerdict {
	if verdicts == nil {
		return nil
	}
	absent := make(map[string]struct{}, len(emissions))
	for _, emission := range emissions {
		if !emission.Attempted && emission.Src != nil {
			absent[emission.Src.ID()] = struct{}{}
		}
	}
	if len(absent) == 0 {
		return verdicts
	}
	out := make([]rpc.SourceVerdict, len(verdicts))
	copy(out, verdicts)
	for i, verdict := range out {
		if _, skipped := absent[verdict.Source.ID]; skipped && verdict.Kind == rpc.VerdictUnusable {
			out[i].Kind = rpc.VerdictAbsent
		}
	}
	return out
}

// recordDecision folds one completed aggregation into exactly one
// observability event (decision 0008). It lives here — the call site that
// owns policies — so the per-attempt Failed flag reuses the resolution's own
// per-source statement when the policy stated one, instead of a second
// answer classifier living in the observability seam. Policies without the
// outcome capability keep the validity-contract classification. The no-op
// sink skips assembly entirely, keeping the default call path free of
// event-building costs.
func (a *Aggregator) recordDecision(started time.Time, method string, policy rpc.Policy, decision rpc.Decision, resolveErr error, emissions []pool.Emission, verdicts []rpc.SourceVerdict) {
	if a.rec == observe.Nop {
		return
	}
	event := observe.DecisionEvent{
		Method:   method,
		Duration: time.Since(started),
		Faults:   len(decision.Faults),
	}
	if resolveErr != nil {
		// resolveErr is authoritative: a decision carrying evidence but
		// rejected (for example by conflict detection) reports "failed"
		// with Conflict set, never a success class.
		event.Status = "failed"
		event.Conflict = errors.Is(resolveErr, rpc.ErrConflict)
	} else {
		event.Status = decision.Status.String()
	}
	attemptClass := attemptClassifier(policy, verdicts)
	for _, emission := range emissions {
		if !emission.Attempted {
			continue // never acquired a slot: no attempt, no observation
		}
		event.Attempts = append(event.Attempts, observe.SourceEvent{
			Source: emission.Src.Name(),
			RTT:    emission.RTT,
			Failed: !attemptClass(emission),
		})
	}
	a.rec.RecordDecision(event)
}

// attemptClassifier fixes how one attempted emission is judged usable: from
// the resolution's verdicts when the policy stated them, otherwise from the
// policy's validity contract.
func attemptClassifier(policy rpc.Policy, verdicts []rpc.SourceVerdict) func(pool.Emission) bool {
	if verdicts == nil {
		return func(emission pool.Emission) bool { return usableAnswer(policy, emission) }
	}
	index := verdictIndexByID(verdicts)
	return func(emission pool.Emission) bool {
		verdict, stated := index[emission.Src.ID()]
		if !stated {
			return usableAnswer(policy, emission)
		}
		return verdict.Kind == rpc.VerdictSupported || verdict.Kind == rpc.VerdictDissented
	}
}

func verdictIndexByID(verdicts []rpc.SourceVerdict) map[string]rpc.SourceVerdict {
	index := make(map[string]rpc.SourceVerdict, len(verdicts))
	for _, verdict := range verdicts {
		index[verdict.Source.ID] = verdict
	}
	return index
}

// usableAnswer reports whether an attempted call provided an answer the
// policy could use. A delivered value counts when the policy normalizes it
// — a valid answer that simply lost the comparison is dissent, not a
// failure. A structured protocol error counts only for policies that opted
// into error bodies (rpc.RPCErrorPolicy) and accept the body: a corroborated
// revert is an observation that contributed value. Everything else —
// transport faults, timeouts, malformed payloads, protocol errors the
// policy rejects — provided no usable answer.
func usableAnswer(policy rpc.Policy, emission pool.Emission) bool {
	if emission.Err == nil {
		_, err := policy.Normalize(emission.Val)
		return err == nil
	}
	structured := ObservationRPCError(emission.Err)
	if structured == nil {
		return false
	}
	if provider, ok := policy.(rpc.RPCErrorPolicy); ok && provider.IncludeRPCError() {
		_, err := policy.Normalize(structured.Raw())
		return err == nil
	}
	return false
}

func hasUsableEmission(emissions []pool.Emission) bool {
	for _, emission := range emissions {
		if emission.Val != nil {
			return true
		}
		var protocolErr rpc.ProtocolError
		if errors.As(emission.Err, &protocolErr) {
			return true
		}
	}
	return false
}

// sourceFaults preserves rejected protocol answers as diagnostics, without
// changing pool health or blaming calls that never started. A stated verdict
// is authoritative for its source; without one the validity contract judges.
func sourceFaults(policy rpc.Policy, emissions []pool.Emission, verdicts []rpc.SourceVerdict) []error {
	attemptClass := attemptClassifier(policy, verdicts)
	var faults []error
	for _, emission := range emissions {
		if emission.Attempted && emission.Err != nil && !attemptClass(emission) {
			faults = append(faults, fmt.Errorf("source %q: %w", emission.Src.Name(), emission.Err))
		}
	}
	return faults
}

// structuredSourceFaults mirrors sourceFaults with source attribution kept
// outside the error text, and additionally records unattempted members that
// failed a scheduling gate. Gated members keep their voting-basis slot, so a
// report must classify them as availability faults even though Calls never
// started; the human-readable Faults list intentionally stays unchanged.
// Report generation must use this seam rather than parsing error strings.
func structuredSourceFaults(policy rpc.Policy, emissions []pool.Emission, verdicts []rpc.SourceVerdict) []rpc.SourceFault {
	attemptClass := attemptClassifier(policy, verdicts)
	var faults []rpc.SourceFault
	for _, emission := range emissions {
		if emission.Err == nil {
			continue
		}
		if emission.Attempted && attemptClass(emission) {
			continue
		}
		faults = append(faults, rpc.SourceFault{
			Source: SourceRefOf(emission.Src),
			Err:    emission.Err,
		})
	}
	return faults
}

// observationRPCError converts a source-attributed error into the structured
// value policies read, using only the project-owned protocol-error
// capabilities. Presence-preserving Adapters answer through
// rpc.ErrorDataPresence; every other Adapter falls back to the decoded
// rpc.DataError value, where an absent field and a JSON null are
// indistinguishable and stay unknown presence.
func ObservationRPCError(err error) *rpc.RPCError {
	if err == nil {
		return nil
	}
	var protocolErr rpc.ProtocolError
	if !errors.As(err, &protocolErr) {
		return nil
	}
	structured := &rpc.RPCError{Code: protocolErr.ErrorCode(), Message: protocolErr.Error()}
	var preserved rpc.ErrorDataPresence
	if errors.As(err, &preserved) {
		if raw, present := preserved.ErrorDataJSON(); present {
			structured.Data = append(json.RawMessage(nil), raw...)
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				structured.DataPresence = rpc.ErrorDataPresenceNull
			} else {
				structured.DataPresence = rpc.ErrorDataPresenceValue
			}
		} else {
			structured.DataPresence = rpc.ErrorDataPresenceMissing
		}
		return structured
	}
	var dataErr rpc.DataError
	if errors.As(err, &dataErr) {
		if data := dataErr.ErrorData(); data != nil {
			if raw, marshalErr := json.Marshal(data); marshalErr == nil {
				structured.Data = raw
				structured.DataPresence = rpc.ErrorDataPresenceValue
			}
		}
	}
	return structured
}
