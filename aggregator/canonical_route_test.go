package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/pool"
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

// processingPlan assembles the same shape the two pilot methods compile: a
// strict resolver with canonical-bytes clustering, intact raw hooks, and the
// declared processor.
func processingPlan(hooks *processingHooks, process rpc.CanonicalProcessor) rpc.ReadPlan {
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
	return rpc.ReadPlan{
		Method:           "m",
		Resolver:         rpc.Strict(normalize, rpc.WithCanonicalValue(encode), rpc.WithQuorum(2)),
		ResolverKind:     rpc.ResolverStrict,
		ProcessCanonical: process,
	}
}

// echoProcessor is the minimal well-formed processor: the raw response is
// already canonical, evidence absent.
func echoProcessor(hooks *processingHooks) rpc.CanonicalProcessor {
	return func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpc.CanonicalResult, error) {
		hooks.mu.Lock()
		hooks.process++
		hooks.mu.Unlock()
		return rpc.NewCanonicalResult(raw, nil)
	}
}

// canonicalAggregator installs the processing plan over a scripted fleet; the
// bodies apply to all members.
func canonicalAggregator(t *testing.T, plan rpc.ReadPlan, bodies ...string) (*Aggregator, []*rpctest.Node) {
	t.Helper()
	var members []pool.Member
	var nodes []*rpctest.Node
	for i, body := range bodies {
		node := rpctest.NewNode(0)
		t.Cleanup(node.Close)
		if body == "" {
			node.Fail("m", -32000, "transport refused")
		} else {
			node.ReturnRaw("m", json.RawMessage(body))
		}
		nodes = append(nodes, node)
		members = append(members, pool.Member{Name: fmt.Sprintf("proc-%d", i), URL: node.URL()})
	}
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	opts := []Option{WithPlans(map[string]rpc.ReadPlan{"m": plan})}
	shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 2)
	if err != nil {
		t.Fatal(err)
	}
	opts = append(opts, WithCorroboration(shared))
	return New(p, opts...), nodes
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
		rejecting := func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpc.CanonicalResult, error) {
			hooks.mu.Lock()
			hooks.process++
			hooks.mu.Unlock()
			if strings.Contains(string(raw), "bad") {
				return rpc.CanonicalResult{}, fmt.Errorf("required field: %w", rpc.ErrMalformedResponse)
			}
			return rpc.NewCanonicalResult(raw, nil)
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
	internalProcessor := func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpc.CanonicalResult, error) {
		hooks.mu.Lock()
		hooks.process++
		hooks.mu.Unlock()
		if strings.Contains(string(raw), "internal") {
			return rpc.CanonicalResult{}, rpc.ErrInvalidCanonicalResult
		}
		return rpc.NewCanonicalResult(raw, nil)
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
	agg, _ := canonicalAggregator(t, processingPlan(hooks, nil), `"same"`, `"same"`)
	agg.plans["m"] = func() rpc.ReadPlan {
		plan := processingPlan(hooks, nil)
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
