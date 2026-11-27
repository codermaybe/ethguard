// Package aggregator resolves raw multi-source observations using method
// policies. It does not own connections or source health.
package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	// defaults.
	corrob *rpc.CorroborationShared
	// stage establishes the plan's required reference within this invocation.
	stage ReferenceStage
	// plans own each registered method's execution declaration. Unknown
	// methods use a forwarding plan in the same pipeline.
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
// coordinator calls it for the plan's declared moving-tag requirement, so
// reference rounds share the aggregator's invocation.
func WithReferenceStage(stage ReferenceStage) Option {
	return func(a *Aggregator) {
		if stage != nil {
			a.stage = stage
		}
	}
}

// WithProjectors binds each plan's declared projector ID to its projector.
// Multiple methods may share one projector ID. Nil projectors are skipped.
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
	return a.aggregate(ctx, req, false)
}

// AggregateStrict is the typed facade's entry point. It records the
// corroboration requirement on the invocation itself — not on the request,
// not on the context — so the resolved value of a determinism-claiming read
// must be Corroborated or the call fails closed. The requirement is
// invocation-scoped: it is set when the invocation is built here and read
// once at resolution; Forwarded mode and the pending-local route keep their
// documented exemptions (see resolveValue).
func (a *Aggregator) AggregateStrict(ctx context.Context, req rpc.Request) (rpc.Decision, error) {
	return a.aggregate(ctx, req, true)
}

func (a *Aggregator) aggregate(ctx context.Context, req rpc.Request, requireCorroboration bool) (rpc.Decision, error) {
	started := time.Now()
	// Material collection rides the context's per-call collector, installed
	// by the operation entry that owns and later snapshots it. Aggregate
	// never creates a collector: one built here lives on a context that
	// never escapes the invocation, so its records would have no consumer —
	// an entry that declines collection (for example a view call without a
	// bound capture) therefore also declines recording here, and a direct
	// caller that wants material installs its own collector. Without a
	// collector no material is recorded and no business path changes.
	collector := uncertain.CollectorFromContext(ctx)
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
		ctx:       ctx,
		plan:      &plan,
		snap:      snap,
		snapOK:    snapOK,
		collector: collector,
	}
	state.invocation = beginInvocation(snap, snapOK, members, collector, requireCorroboration)
	defer state.finalizeHealth()

	if exit := a.planPreparePhase(state, started, req); exit != nil {
		return exit.decision, exit.err
	}
	a.collectPhase(state, members)
	a.admitPhase(state)
	if exit := a.resolvePhase(state, started); exit != nil {
		return exit.decision, exit.err
	}
	decision, resolveErr := state.resolveValue()
	return a.projectPhase(state, started, decision, resolveErr)
}

// unknownDecision builds the shared shape of the two degraded early-return
// decisions — everything the invocation knows without a resolved value. Each
// caller attaches its phase-specific fault lists after it.
func unknownDecision(reference *rpc.ReferenceEvidence, participants []rpc.SourceRef, prepared *rpc.PreparedRequest) rpc.Decision {
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

// preparedRequest snapshots the frozen request fact observed at the policy
// boundary. The bytes come from the one controlled prepare-time
// serialization (rpc.FreezeParams), so the record and the wire never carry
// two marshaler executions of the same parameter.
func preparedRequest(method string, snapshot json.RawMessage) *rpc.PreparedRequest {
	return &rpc.PreparedRequest{Method: method, Params: snapshot}
}

func collectionFaultList(collectErr error) []error {
	if collectErr == nil {
		return nil
	}
	return []error{fmt.Errorf("collect: %w", collectErr)}
}

func hasMalformedAdmission(emissions []pool.Emission) bool {
	for _, emission := range emissions {
		if errors.Is(emission.Response.Err(), rpc.ErrMalformedResponse) {
			return true
		}
	}
	return false
}

func firstMalformedAdmission(emissions []pool.Emission) error {
	for _, emission := range emissions {
		if errors.Is(emission.Response.Err(), rpc.ErrMalformedResponse) {
			return emission.Response.Err()
		}
	}
	return rpc.ErrMalformedResponse
}

// recordDecision folds one completed aggregation into exactly one
// observability event (decision 0008). It lives here — the call site that
// owns policies — so the per-attempt Failed flag reuses the resolution's own
// per-source statement when the policy stated one, instead of a second
// answer classifier living in the observability seam. Policies without the
// outcome capability keep the validity-contract classification. The no-op
// sink skips assembly entirely, keeping the default call path free of
// event-building costs.
func (a *Aggregator) recordDecision(started time.Time, method string, decision rpc.Decision, resolveErr error, emissions []pool.Emission, verdicts []rpc.SourceVerdict) {
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
	attemptClass := attemptClassifier(verdicts)
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

// attemptClassifier projects the recorded source judgment without calling
// policy hooks. Missing judgments are internal failures handled at the boundary.
func attemptClassifier(verdicts []rpc.SourceVerdict) func(pool.Emission) bool {
	index := verdictIndexByID(verdicts)
	return func(emission pool.Emission) bool {
		v, ok := index[SourceRefOf(emission.Src).ID]
		if !ok || v.Method.State == rpc.InterpretationInternalFailure || v.Method.State == rpc.InterpretationExcluded {
			return true
		}
		return v.Kind != rpc.VerdictUnusable
	}
}

func verdictIndexByID(verdicts []rpc.SourceVerdict) map[string]rpc.SourceVerdict {
	index := make(map[string]rpc.SourceVerdict, len(verdicts))
	for _, verdict := range verdicts {
		index[verdict.Source.ID] = verdict
	}
	return index
}

func hasUsableEmission(emissions []pool.Emission) bool {
	for _, emission := range emissions {
		if emission.Response.CandidateErr(true) == nil {
			return true
		}
	}
	return false
}
