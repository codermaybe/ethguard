package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	"github.com/codermaybe/ethguard/internal/pool"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// The canonical route's production-pipeline witnesses at the coordinator
// level: a processing plan installed through the same seam the registry uses,
// driven through the full phase pipeline, with raw-hook and processor call
// counters proving the single-processing contract on every exit — success,
// partial failure, degraded exits, and the internal contract fault.

// processingHooks carries the call counters of one synthetic processing plan.
type processingHooks struct {
	mu        sync.Mutex
	normalize int
	encode    int
	process   int
}

func (h *processingHooks) snapshot() (normalize, encode, process int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.normalize, h.encode, h.process
}

// processingPlan has the same processed-only contract as registered methods.
func processingPlan(hooks *processingHooks, process rpcengine.CanonicalProcessor) rpcengine.ReadPlan {
	normalize := func(raw json.RawMessage) (string, error) {
		hooks.mu.Lock()
		hooks.normalize++
		hooks.mu.Unlock()
		return string(raw), nil
	}
	return rpcengine.ReadPlan{Method: "m", Normalize: normalize, ResolverKind: rpcengine.ResolverStrict,
		Canonical: &rpcengine.CanonicalRead{Process: process, Resolver: rpcengine.CanonicalStrict(rpcengine.RuleCompareMethodSpecific)}}
}

// echoProcessor is the minimal well-formed processor: the raw response is
// already canonical, evidence absent.
func echoProcessor(hooks *processingHooks) rpcengine.CanonicalProcessor {
	return func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
		hooks.mu.Lock()
		hooks.process++
		hooks.mu.Unlock()
		return rpcengine.NewCanonicalResult(raw, nil)
	}
}

// canonicalAggregator installs the processing plan over a scripted fleet; the
// bodies apply to all members.
func canonicalAggregator(t *testing.T, plan rpcengine.ReadPlan, bodies ...string) (*Aggregator, []*rpctest.Node) {
	t.Helper()
	var members []pool.Member
	var nodes []*rpctest.Node
	for i, body := range bodies {
		node := rpctest.NewNode(0)
		t.Cleanup(node.Close)
		if body == "" {
			node.FailHTTP("m", 503)
		} else {
			node.ReturnRaw("m", json.RawMessage(body))
		}
		nodes = append(nodes, node)
		members = append(members, pool.Member{Name: fmt.Sprintf("proc-%d", i), URL: node.URL()})
	}
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	opts := []Option{WithPlans(map[string]rpcengine.ReadPlan{"m": plan})}
	shared, err := rpcengine.NewCorroboration(rpc.CorroborationMajority, 2)
	if err != nil {
		t.Fatal(err)
	}
	return New(p, shared, opts...), nodes
}

// TestCanonicalRouteProcessesOncePerSource drives the full pipeline: the
// processor runs exactly once per available source, the raw hooks never run,
// and the decision carries the processed bytes with the fault attributed to
// the faulting source alone.
func TestCanonicalRouteProcessesOncePerSource(t *testing.T) {
	hooks := &processingHooks{}
	agg, _ := canonicalAggregator(t, processingPlan(hooks, echoProcessor(hooks)), "", `"same"`, `"same"`)
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("canonical round = %+v, %v", decision, err)
	}
	if string(decision.Value) != `"same"` {
		t.Fatalf("committed value = %s", decision.Value)
	}
	if len(decision.Support) != 2 || decision.Representative.ID == "" {
		t.Fatalf("support facts: %+v", decision)
	}
	normalize, encode, process := hooks.snapshot()
	if process != 2 {
		t.Fatalf("processor ran %d times for two available sources", process)
	}
	if normalize != 0 || encode != 0 {
		t.Fatalf("raw hooks ran on the canonical route: normalize=%d encode=%d", normalize, encode)
	}
	if len(decision.SourceFaults) != 1 || decision.SourceFaults[0].Source.Name != "proc-0" {
		t.Fatalf("fault attribution: %+v", decision.SourceFaults)
	}
}

func TestCanonicalRouteRetainsConfiguredQuorum(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bodies    []string
		available int
		status    rpc.Status
		err       error
	}{
		{name: "insufficient support", bodies: []string{`"same"`, `"same"`, ""}, available: 2, status: rpc.Unknown, err: rpc.ErrInsufficientSupport},
		{name: "quorum reached", bodies: []string{`"same"`, `"same"`, `"same"`}, available: 3, status: rpc.Corroborated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hooks := &processingHooks{}
			plan := processingPlan(hooks, echoProcessor(hooks))

			fixture, _ := canonicalAggregator(t, plan, tc.bodies...)
			shared, err := rpcengine.NewCorroboration(rpc.CorroborationMajority, 3)
			if err != nil {
				t.Fatal(err)
			}
			agg := New(fixture.pool, shared, WithPlans(map[string]rpcengine.ReadPlan{"m": plan}))
			decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
			if !errors.Is(err, tc.err) || decision.Status != tc.status || decision.Adjudication.Required != 3 || decision.Adjudication.Basis != 3 {
				t.Fatalf("constructor quorum was not retained: %+v, %v", decision, err)
			}
			if len(decision.Support) != tc.available {
				t.Fatalf("support = %d, want %d", len(decision.Support), tc.available)
			}
			if err == nil && string(decision.Value) != `"same"` {
				t.Fatalf("committed value = %s", decision.Value)
			}
			_, _, process := hooks.snapshot()
			if process != tc.available {
				t.Fatalf("processor ran %d times for %d available sources", process, tc.available)
			}
		})
	}
}

// TestCanonicalRouteZeroProcessingOnDegradedExits pins the exit contracts:
// jsonsafe-malformed responses never reach the processor, degraded exits
// never re-run raw hooks, and the frozen route is shared by all four phases.
func TestCanonicalRouteZeroProcessingOnDegradedExits(t *testing.T) {
	t.Run("jsonsafe-malformed", func(t *testing.T) {
		hooks := &processingHooks{}
		agg, _ := canonicalAggregator(t, processingPlan(hooks, echoProcessor(hooks)), `{"duplicate":1,"duplicate":2}`)
		_, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
		if !errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("duplicate-key round = %v", err)
		}
		normalize, encode, process := hooks.snapshot()
		if process != 0 || normalize != 0 || encode != 0 {
			t.Fatalf("degraded exit ran hooks: process=%d normalize=%d encode=%d", process, normalize, encode)
		}
	})
	t.Run("transport-fault-only", func(t *testing.T) {
		hooks := &processingHooks{}
		agg, _ := canonicalAggregator(t, processingPlan(hooks, echoProcessor(hooks)), "", "")
		_, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
		if err == nil {
			t.Fatal("all-transport-fault round succeeded")
		}
		normalize, encode, process := hooks.snapshot()
		if process != 0 || normalize != 0 || encode != 0 {
			t.Fatalf("degraded exit ran hooks: process=%d normalize=%d encode=%d", process, normalize, encode)
		}
	})
	t.Run("admission-class-failure", func(t *testing.T) {
		hooks := &processingHooks{}
		rejecting := func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
			hooks.mu.Lock()
			hooks.process++
			hooks.mu.Unlock()
			if strings.Contains(string(raw), "bad") {
				return rpcengine.CanonicalResult{}, fmt.Errorf("required field: %w", rpc.ErrMalformedResponse)
			}
			return rpcengine.NewCanonicalResult(raw, nil)
		}
		agg, _ := canonicalAggregator(t, processingPlan(hooks, rejecting), `"good"`, `"bad"`, `"good"`)
		decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
		if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 2 {
			t.Fatalf("partial failure round = %+v, %v", decision, err)
		}
		if len(decision.SourceFaults) != 1 || !errors.Is(decision.SourceFaults[0].Err, rpc.ErrMalformedResponse) {
			t.Fatalf("admission-class fault attribution: %+v", decision.SourceFaults)
		}
		normalize, encode, process := hooks.snapshot()
		if process != 3 {
			t.Fatalf("processor ran %d times for three available sources", process)
		}
		if normalize != 0 || encode != 0 {
			t.Fatalf("raw hooks ran beside the processor: normalize=%d encode=%d", normalize, encode)
		}
	})
}

// TestCanonicalRouteArrivalOrderDoesNotChangeValue pins the clustering
// invariants on the processing route: identical processed bytes win
// regardless of source arrival order.
func TestCanonicalRouteArrivalOrderDoesNotChangeValue(t *testing.T) {
	run := func(order ...int) string {
		hooks := &processingHooks{}
		bodies := make([]string, 3)
		for i, position := range order {
			if position == 0 {
				bodies[i] = `"beta"`
			} else {
				bodies[i] = `"alpha"`
			}
		}
		agg, _ := canonicalAggregator(t, processingPlan(hooks, echoProcessor(hooks)), bodies...)
		decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
		if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 2 {
			t.Fatalf("majority round = %+v, %v", decision, err)
		}
		return string(decision.Value)
	}
	if run(1, 1, 0) != run(0, 1, 1) {
		t.Fatal("arrival order changed the committed value")
	}
}

// TestCanonicalRouteInternalContractFaultTerminatesRound pins the internal
// sentinel: one processor returning the contract error beside two valid
// sources fails the whole round — no partial success, no provider blame, the
// real transport fault keeps its attribution, and the internal source's
// health is untouched.
func TestCanonicalRouteInternalContractFaultTerminatesRound(t *testing.T) {
	hooks := &processingHooks{}
	internalProcessor := func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
		hooks.mu.Lock()
		hooks.process++
		hooks.mu.Unlock()
		if strings.Contains(string(raw), "internal") {
			return rpcengine.CanonicalResult{}, rpc.ErrInvalidCanonicalResult
		}
		return rpcengine.NewCanonicalResult(raw, nil)
	}
	agg, _ := canonicalAggregator(t, processingPlan(hooks, internalProcessor), `"internal"`, `"same"`, "")
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("internal fault surfaced as %v", err)
	}
	if decision.Status != rpc.Unknown || len(decision.Value) != 0 {
		t.Fatalf("internal fault produced a value: %+v", decision)
	}
	for _, fault := range decision.SourceFaults {
		if fault.Source.Name == "proc-0" {
			t.Fatalf("internal fault blamed the source: %+v", decision.SourceFaults)
		}
	}
	found := false
	for _, fault := range decision.Faults {
		if errors.Is(fault, rpc.ErrInvalidCanonicalResult) {
			found = true
		}
	}
	if !found {
		t.Fatalf("internal fault missing from the decision faults: %v", decision.Faults)
	}
	// The round-level transport fault keeps its attribution.
	attributed := false
	for _, fault := range decision.SourceFaults {
		if fault.Source.Name == "proc-2" && fault.Err != nil {
			attributed = true
		}
	}
	if !attributed {
		t.Fatalf("transport fault lost its attribution: %+v", decision.SourceFaults)
	}
	// Health: the internal source was not penalized.
	member := agg.pool.VotingMembers()[0]
	if h := member.Health(); h.Failures > 0 || h.ConsecutiveFailures > 0 {
		t.Fatalf("internal source health penalized: %+v", h)
	}
}

// TestRawRouteKeepsFullAdmission pins the route boundary: the same method
// without a processor keeps the complete raw route — admission runs, its
// evidence lands on the decision, and the raw hooks classify the answers.
func TestRawRouteKeepsFullAdmission(t *testing.T) {
	hooks := &processingHooks{}
	var mu sync.Mutex
	admitCalls := 0
	admission := func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (json.RawMessage, error) {
		mu.Lock()
		admitCalls++
		mu.Unlock()
		return json.RawMessage(`"evidence"`), nil
	}
	agg, _ := canonicalAggregator(t, rawProcessingPlan(hooks), `"same"`, `"same"`)
	agg.plans["m"] = func() rpcengine.ReadPlan {
		plan := rawProcessingPlan(hooks)
		plan.Admit = admission
		return plan
	}()
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("raw route = %+v, %v", decision, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if admitCalls != 2 {
		t.Fatalf("raw route admission ran %d times for two sources", admitCalls)
	}
	if string(decision.Provenance) != `"evidence"` {
		t.Fatalf("admission evidence lost: %s", decision.Provenance)
	}
	normalize, encode, _ := hooks.snapshot()
	if normalize+encode == 0 {
		t.Fatal("raw route never classified an answer")
	}
}

// TestCanonicalRouteCanceledRound records the cancellation exit: a canceled
// context fails the round without processing raw material again.
func TestCanonicalRouteCanceledRound(t *testing.T) {
	hooks := &processingHooks{}
	agg, _ := canonicalAggregator(t, processingPlan(hooks, echoProcessor(hooks)), `"same"`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := agg.Aggregate(ctx, rpc.Request{Method: "m"})
	if err == nil {
		t.Fatal("canceled round succeeded")
	}
	normalize, encode, _ := hooks.snapshot()
	if normalize != 0 || encode != 0 {
		t.Fatalf("canceled round re-ran raw hooks: normalize=%d encode=%d", normalize, encode)
	}
}

// TestCanonicalRouteProcessesPerInvocation pins the no-cross-request-hit
// rule: the same source answering the same request in two separate
// invocations is processed once per invocation — two processes total — and
// no result survives an invocation to answer the next one.
func TestCanonicalRouteProcessesPerInvocation(t *testing.T) {
	hooks := &processingHooks{}
	agg, _ := canonicalAggregator(t, processingPlan(hooks, echoProcessor(hooks)), `"same"`, `"same"`, `"same"`)
	for i := 1; i <= 2; i++ {
		decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
		if err != nil || decision.Status != rpc.Corroborated {
			t.Fatalf("round %d = %+v, %v", i, decision, err)
		}
		_, _, process := hooks.snapshot()
		if process != 3*i {
			t.Fatalf("round %d: processor ran %d times total, want three per invocation", i, process)
		}
	}
	normalize, encode, _ := hooks.snapshot()
	if normalize != 0 || encode != 0 {
		t.Fatalf("two rounds re-ran raw hooks: normalize=%d encode=%d", normalize, encode)
	}
}

// TestCanonicalRouteGatedMemberStaysAbsentWithGateFault pins the unattempted
// contract on the processing route: a source gated by its own repeated
// malformed answers keeps its voting-basis slot, is never attempted again,
// and surfaces as the gate fault with its slot kept — the round's processing
// never fabricates a response for it.
func TestCanonicalRouteGatedMemberStaysAbsentWithGateFault(t *testing.T) {
	hooks := &processingHooks{}
	// The bad source fails the object's semantic validation three times, the
	// same gating shape the raw route produces; the other two corroborate.
	rejecting := func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
		hooks.mu.Lock()
		hooks.process++
		hooks.mu.Unlock()
		if strings.Contains(string(raw), "bad") {
			return rpcengine.CanonicalResult{}, fmt.Errorf("required field: %w", rpc.ErrMalformedResponse)
		}
		return rpcengine.NewCanonicalResult(raw, nil)
	}
	agg, nodes := canonicalAggregator(t, processingPlan(hooks, rejecting), `"same"`, `"same"`, `"bad"`)
	members := agg.pool.VotingMembers()
	gated := members[2]
	for attempt := uint64(1); attempt <= 3; attempt++ {
		decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
		if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 2 {
			t.Fatalf("attempt %d: decision=%+v err=%v", attempt, decision, err)
		}
		if h := gated.Health(); h.Failures != attempt {
			t.Fatalf("attempt %d: gated health=%+v", attempt, h)
		}
	}
	_, _, processed := hooks.snapshot()
	if processed != 9 {
		t.Fatalf("processor ran %d times, want 3 per round × 3 available", processed)
	}
	for range 2 {
		decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
		if err != nil || len(decision.Participants) != 3 || len(decision.Support) != 2 || len(decision.SourceFaults) != 1 || !errors.Is(decision.SourceFaults[0].Err, pool.ErrSourceGated) {
			t.Fatalf("gated fixed basis: decision=%+v err=%v", decision, err)
		}
	}
	if len(nodes[2].Calls()) != 3 {
		t.Fatalf("gated source was attempted again: %d calls", len(nodes[2].Calls()))
	}
	// The gated member contributed only its first three rounds of processing;
	// the two live sources account for every later one.
	_, _, processedAfter := hooks.snapshot()
	if processedAfter != 13 {
		t.Fatalf("unexpected processing total: %d, want 9 + 2 live sources × 2 rounds", processedAfter)
	}
}

// TestCanonicalProcessorResultBesideErrorAbortsInternally pins the pairing
// defect the CanonicalProcessor contract names: a processor returning a
// non-zero result beside a non-sentinel error is an internal contract fault,
// not a provider admission failure. The round aborts even with quorum in
// reach, no fault or health table blames the source, and the provider's
// transport accounting keeps its success — it answered; the defect is the
// process's.
func TestCanonicalProcessorResultBesideErrorAbortsInternally(t *testing.T) {
	hooks := &processingHooks{}
	mixed := func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
		hooks.mu.Lock()
		hooks.process++
		hooks.mu.Unlock()
		result, buildErr := rpcengine.NewCanonicalResult(raw, nil)
		if buildErr != nil {
			return rpcengine.CanonicalResult{}, buildErr
		}
		if strings.Contains(string(raw), "broken") {
			return result, fmt.Errorf("required field: %w", rpc.ErrMalformedResponse)
		}
		return result, nil
	}
	agg, _ := canonicalAggregator(t, processingPlan(hooks, mixed), `"broken"`, `"same"`, `"same"`)
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("result beside error must abort with the internal sentinel; status=%v err=%v support=%d", decision.Status, err, len(decision.Support))
	}
	if decision.Status != rpc.Unknown || len(decision.Value) != 0 || len(decision.Support) != 0 {
		t.Fatalf("internal fault produced a committed answer: status=%v value=%s support=%d", decision.Status, decision.Value, len(decision.Support))
	}
	named := false
	for _, fault := range decision.Faults {
		if errors.Is(fault, rpc.ErrInvalidCanonicalResult) {
			named = true
		}
	}
	if !named {
		t.Fatalf("pairing defect missing from the decision faults: %v", decision.Faults)
	}
	for _, fault := range decision.SourceFaults {
		if fault.Source.Name == "proc-0" {
			t.Fatalf("pairing defect blamed the source: %+v", decision.SourceFaults)
		}
	}
	normalize, encode, process := hooks.snapshot()
	if process != 3 || normalize != 0 || encode != 0 {
		t.Fatalf("hook counts: process=%d normalize=%d encode=%d", process, normalize, encode)
	}
	// Transport accounting: the provider answered, so the transport level
	// keeps its success and takes no failure. The finalizer's transport
	// record stays; an internal error is never a provider response
	// violation, adds no failure count, and the latency record updates as
	// any successful round trip does.
	health := agg.pool.VotingMembers()[0].Health()
	if health.Failures != 0 || health.ConsecutiveFailures != 0 || health.Successes == 0 {
		t.Fatalf("pairing defect changed provider health accounting: %+v", health)
	}
	if health.LatencyEWMA == 0 {
		t.Fatalf("transport success lost its latency record: %+v", health)
	}
}

// TestCanonicalProcessorPairingOutcomes walks the processor outcome matrix
// the CanonicalProcessor contract fixes: the sentinel beside a result, a
// plain error with a zero result, a nil error with a zero result, and the
// well-formed success. Each must land on its own exit — internal abort,
// provider admission fault, internal abort, corroborated success — never
// collapsed into one failure class.
func TestCanonicalProcessorPairingOutcomes(t *testing.T) {
	build := func(raw json.RawMessage) (rpcengine.CanonicalResult, error) {
		return rpcengine.NewCanonicalResult(raw, nil)
	}
	run := func(process rpcengine.CanonicalProcessor) (rpc.Decision, *Aggregator, error) {
		hooks := &processingHooks{}
		agg, _ := canonicalAggregator(t, processingPlan(hooks, process), `"odd"`, `"same"`, `"same"`)
		decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
		return decision, agg, err
	}
	t.Run("sentinel-beside-result", func(t *testing.T) {
		decision, agg, err := run(func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
			result, _ := build(raw)
			if strings.Contains(string(raw), "odd") {
				return result, rpc.ErrInvalidCanonicalResult
			}
			return result, nil
		})
		if !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
			t.Fatalf("sentinel beside a result = %v", err)
		}
		for _, fault := range decision.SourceFaults {
			if fault.Source.Name == "proc-0" {
				t.Fatalf("sentinel blamed the source: %+v", decision.SourceFaults)
			}
		}
		if h := agg.pool.VotingMembers()[0].Health(); h.Failures != 0 || h.ConsecutiveFailures != 0 {
			t.Fatalf("sentinel penalized provider health: %+v", h)
		}
	})
	t.Run("plain-error-with-zero-result", func(t *testing.T) {
		decision, _, err := run(func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
			if strings.Contains(string(raw), "odd") {
				return rpcengine.CanonicalResult{}, fmt.Errorf("required field: %w", rpc.ErrMalformedResponse)
			}
			return build(raw)
		})
		// A zero result beside a plain error is the source's own admission
		// verdict: the round corroborates on the two usable sources and the
		// fault lands on that source alone.
		if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 2 {
			t.Fatalf("plain error with zero result = %+v, %v", decision, err)
		}
		if len(decision.SourceFaults) != 1 || decision.SourceFaults[0].Source.Name != "proc-0" {
			t.Fatalf("admission fault attribution: %+v", decision.SourceFaults)
		}
	})
	t.Run("nil-error-with-zero-result", func(t *testing.T) {
		decision, agg, err := run(func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
			if strings.Contains(string(raw), "odd") {
				return rpcengine.CanonicalResult{}, nil
			}
			return build(raw)
		})
		if !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
			t.Fatalf("nil error with zero result must abort internally; status=%v err=%v", decision.Status, err)
		}
		for _, fault := range decision.SourceFaults {
			if fault.Source.Name == "proc-0" {
				t.Fatalf("nil-result defect blamed the source: %+v", decision.SourceFaults)
			}
		}
		if h := agg.pool.VotingMembers()[0].Health(); h.Failures != 0 || h.ConsecutiveFailures != 0 {
			t.Fatalf("nil-result defect penalized provider health: %+v", h)
		}
	})
	t.Run("nil-error-with-result", func(t *testing.T) {
		hooks := &processingHooks{}
		agg, _ := canonicalAggregator(t, processingPlan(hooks, echoProcessor(hooks)), `"same"`, `"same"`, `"same"`)
		decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
		if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 3 {
			t.Fatalf("well-formed round = %+v, %v", decision, err)
		}
	})
}

// Contract defects must fail locally before collection or method hooks.
func TestCanonicalExecutionRejectsMissingOrMixedContractsBeforeRequests(t *testing.T) {
	for _, defect := range []string{"missing-contract", "missing-resolver", "missing-processor", "raw-resolver", "raw-admission", "pending-observe"} {
		t.Run(defect, func(t *testing.T) {
			hooks := &processingHooks{}
			plan := processingPlan(hooks, echoProcessor(hooks))
			switch defect {
			case "missing-contract":
				plan.Canonical = nil
			case "missing-resolver":
				plan.Canonical.Resolver = nil
			case "missing-processor":
				plan.Canonical.Process = nil
			case "raw-resolver":
				plan.Resolver = rpcengine.Strict(rpc.CanonicalJSON)
			case "raw-admission":
				plan.Admit = func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error) {
					t.Fatal("raw admission ran")
					return nil, nil
				}
			case "pending-observe":
				plan.Pending = rpcengine.PendingSpec{Mode: rpcengine.PendingObserve}
			}
			agg, nodes := canonicalAggregator(t, plan, `"same"`, `"same"`)
			decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
			if !errors.Is(err, rpc.ErrInvalidReadPlan) || decision.Status != rpc.Unknown {
				t.Fatalf("invalid contract = %+v, %v", decision, err)
			}
			normalize, encode, process := hooks.snapshot()
			if normalize != 0 || encode != 0 || process != 0 {
				t.Fatal("invalid plan ran hooks")
			}
			for _, node := range nodes {
				if len(node.Calls()) != 0 {
					t.Fatal("invalid plan contacted a provider")
				}
			}
		})
	}
}

func rawProcessingPlan(hooks *processingHooks) rpcengine.ReadPlan {
	normalize := func(raw json.RawMessage) (string, error) {
		hooks.mu.Lock()
		hooks.normalize++
		hooks.mu.Unlock()
		return string(raw), nil
	}
	encode := func(raw json.RawMessage) (json.RawMessage, error) {
		hooks.mu.Lock()
		hooks.encode++
		hooks.mu.Unlock()
		return raw, nil
	}
	return rpcengine.ReadPlan{Method: "m", Normalize: normalize, ResolverKind: rpcengine.ResolverStrict, Resolver: rpcengine.Strict(normalize, rpcengine.WithCanonicalValue(encode))}
}
