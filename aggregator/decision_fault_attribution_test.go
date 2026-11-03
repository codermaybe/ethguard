package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The fault-table contract: every pipeline exit assembles Faults,
// SourceFaults, CollectionFaults and Diagnostics through one seam
// (decision_faults.go). These tests pin what each exit owes — request-side
// failures keep the tables empty and ride the returned error, fan-out
// failures attribute every source exactly once, and a resolved decision
// never duplicates a fault across the human-readable and structured tables.

type failingProjector struct{}

func (failingProjector) ProjectObservation(json.RawMessage, rpc.Evaluation) (rpc.ExtensionSet, error) {
	return rpc.ExtensionSet{}, errors.New("projection: no extension accepted")
}

// faultAggregator builds an aggregator over scripted in-process nodes on
// method "m", with an optional corroboration snapshot.
func faultAggregator(t *testing.T, policy rpc.Policy, corroborated bool, script func(i int, node *rpctest.Node), opts ...Option) (*Aggregator, []*rpctest.Node) {
	t.Helper()
	nodes := make([]*rpctest.Node, 2)
	members := make([]pool.Member, 0, len(nodes))
	for i := range nodes {
		node := rpctest.NewNode(0)
		t.Cleanup(node.Close)
		if script != nil {
			script(i, node)
		}
		members = append(members, pool.Member{Name: fmt.Sprintf("node-%c", 'a'+i), URL: node.URL()})
		nodes[i] = node
	}
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	opts = append([]Option{WithCollectTimeout(5 * time.Second)}, opts...)
	if corroborated {
		shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 2)
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, WithCorroboration(shared))
	}
	return New(p, append([]Option{WithPlans(map[string]rpc.ReadPlan{"m": {Method: "m", Resolver: policy}})}, opts...)...), nodes
}

func faultTables(t *testing.T, decision rpc.Decision) (faults, sourceFaults, collectionFaults string) {
	t.Helper()
	for _, fault := range decision.Faults {
		faults += fault.Error() + "\n"
	}
	for _, fault := range decision.SourceFaults {
		sourceFaults += fmt.Sprintf("%s: %v\n", fault.Source.Name, fault.Err)
	}
	for _, fault := range decision.CollectionFaults {
		collectionFaults += fault.Error() + "\n"
	}
	return faults, sourceFaults, collectionFaults
}

func assertNoDuplicateFaults(t *testing.T, decision rpc.Decision) {
	t.Helper()
	seen := map[string]int{}
	for _, fault := range decision.Faults {
		seen[fault.Error()]++
		if seen[fault.Error()] > 1 {
			t.Fatalf("duplicate fault %q in Faults: %+v", fault.Error(), decision.Faults)
		}
	}
}

// Every source failed at the transport level and nothing usable arrived: the
// collection failure is stated once, every source is attributed once, and the
// pool sentinel stays unwrapped by any policy error.
func TestAllSourcesFailedAttribution(t *testing.T) {
	agg, _ := faultAggregator(t, eventPolicy(), false, func(i int, node *rpctest.Node) {
		node.FailHTTP("m", 503)
	})
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if !errors.Is(err, pool.ErrAllSourcesDown) {
		t.Fatalf("err = %v, want ErrAllSourcesDown", err)
	}
	if len(decision.CollectionFaults) != 1 || !errors.Is(decision.CollectionFaults[0], pool.ErrAllSourcesDown) {
		t.Fatalf("collection fault must state the pool failure once: %+v", decision.CollectionFaults)
	}
	assertNoDuplicateFaults(t, decision)
	if len(decision.SourceFaults) != 2 {
		t.Fatalf("both failed sources must be attributed: %+v", decision.SourceFaults)
	}
	for i, fault := range decision.SourceFaults {
		if fault.Source.Name != fmt.Sprintf("node-%c", 'a'+i) || fault.Err == nil {
			t.Fatalf("source fault %d lost its attribution: %+v", i, fault)
		}
	}
	collectFaults := 0
	for _, fault := range decision.Faults {
		if errors.Is(fault, pool.ErrAllSourcesDown) {
			collectFaults++
		}
	}
	if collectFaults != 1 {
		t.Fatalf("collection failure must appear in Faults exactly once: %+v", decision.Faults)
	}
}

// Every delivered response was malformed: the malformed class stays visible
// with per-source attribution, and no collection failure is claimed. The
// payload is a valid JSON-RPC envelope carrying a too-deeply-nested result,
// so the response is delivered and then rejected by admission.
func TestAllMalformedAttribution(t *testing.T) {
	tooDeep := json.RawMessage(strings.Repeat("[", 40) + strings.Repeat("]", 40))
	agg, _ := faultAggregator(t, eventPolicy(), false, func(i int, node *rpctest.Node) {
		node.ReturnRaw("m", tooDeep)
	})
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("err = %v, want ErrMalformedResponse", err)
	}
	if len(decision.CollectionFaults) != 0 {
		t.Fatalf("malformed responses are not collection failures: %+v", decision.CollectionFaults)
	}
	assertNoDuplicateFaults(t, decision)
	if len(decision.Faults) != 2 || len(decision.SourceFaults) != 2 {
		t.Fatalf("both malformed sources must be attributed: %+v / %+v", decision.Faults, decision.SourceFaults)
	}
	for i, fault := range decision.Faults {
		if !errors.Is(fault, rpc.ErrMalformedResponse) {
			t.Fatalf("fault %d lost the malformed class: %v", i, fault)
		}
	}
}

// A partial fan-out resolves: the failed source keeps exactly one
// human-readable fault and exactly one structured attribution, and no
// collection fault is claimed.
func TestPartialSuccessAttribution(t *testing.T) {
	agg, _ := faultAggregator(t, eventPolicy(), false, func(i int, node *rpctest.Node) {
		if i == 0 {
			node.Return("m", "v1")
			return
		}
		node.FailHTTP("m", 503)
	})
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if err != nil || decision.Status != rpc.Selected {
		t.Fatalf("expected a resolved decision, got %+v %v", decision.Status, err)
	}
	assertNoDuplicateFaults(t, decision)
	if len(decision.CollectionFaults) != 0 {
		t.Fatalf("a resolved read must not claim a collection failure: %+v", decision.CollectionFaults)
	}
	if len(decision.SourceFaults) != 1 || decision.SourceFaults[0].Source.Name != "node-b" {
		t.Fatalf("the failed source must be attributed once: %+v", decision.SourceFaults)
	}
	if len(decision.Faults) != 1 || !errors.Is(decision.Faults[0], decision.SourceFaults[0].Err) {
		t.Fatalf("Faults must carry the attributed source cause once: %+v / %+v", decision.Faults, decision.SourceFaults)
	}
}

// A clean corroborated resolve carries no faults and keeps the field
// diagnostics the aggregation produced.
func TestCorroboratedResolveCarriesDiagnosticsWithoutFaults(t *testing.T) {
	agg, _ := faultAggregator(t, eventPolicy(rpc.WithRPCErrorBodies()), true, func(i int, node *rpctest.Node) {
		node.Return("m", "v1")
	})
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("expected a corroborated decision, got %+v %v", decision.Status, err)
	}
	faults, sourceFaults, collectionFaults := faultTables(t, decision)
	if faults != "" || sourceFaults != "" || collectionFaults != "" {
		t.Fatalf("a clean resolve must not report faults: %q %q %q", faults, sourceFaults, collectionFaults)
	}
	if decision.Diagnostics == nil {
		t.Fatal("a corroborated resolve must keep the field diagnostics")
	}
}

// A projection failure is stated once on the resolved decision without
// touching the source fault tables or the field diagnostics.
func TestProjectionErrorAttribution(t *testing.T) {
	a, b := rpctest.NewNode(1), rpctest.NewNode(1)
	defer a.Close()
	defer b.Close()
	a.Return("m", "v1")
	b.Return("m", "v1")
	p := fixedPoolWith(t, 1, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())},
		pool.Member{Name: "a", URL: a.URL()},
		pool.Member{Name: "b", URL: b.URL()},
	)
	plan := rpc.ReadPlan{Method: "m", Resolver: eventPolicy(rpc.WithRPCErrorBodies()), ResolverKind: methods.ResolverStrict, ProjectorID: "m"}
	agg := New(p, WithCorroboration(mustShared(t)),
		WithProjectors(map[string]rpc.ObservationProjector{"m": failingProjector{}}),
		WithPlans(map[string]rpc.ReadPlan{"m": plan}))
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("a projection failure must not fail the read: %+v %v", decision.Status, err)
	}
	if len(decision.Faults) != 1 {
		t.Fatalf("expected exactly the projection fault, got %+v", decision.Faults)
	}
	if _, sourceFaults, collectionFaults := faultTables(t, decision); sourceFaults != "" || collectionFaults != "" {
		t.Fatalf("projection failure must not populate source tables: %q %q", sourceFaults, collectionFaults)
	}
	if decision.Diagnostics == nil {
		t.Fatal("projection failure must not drop the field diagnostics")
	}
}

// plannedFaultAggregator builds a plan-driven aggregator whose Validate hook
// and establishment stage are under the test's control. The pool member is
// unreachable on purpose: the fault exits under test happen before any
// fan-out.
func plannedFaultAggregator(t *testing.T, mutate func(*rpc.ReadPlan), stage ReferenceStage) *Aggregator {
	t.Helper()
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(&plan)
	}
	p := fixedPool(t, pool.Member{Name: "node-a", URL: "http://127.0.0.1:1"})
	opts := []Option{WithPlans(map[string]rpc.ReadPlan{probeMethod: plan})}
	if stage != nil {
		opts = append(opts, WithReferenceStage(stage))
	}
	return New(p, opts...)
}

// The planned pipeline's two local exits must meet the same fault contract
// as the legacy path: a request-side failure rides the returned error and
// keeps every fault table empty, with no diagnostics.
func TestPlannedPrepareFailureKeepsFaultTablesEmpty(t *testing.T) {
	validateErr := errors.New("planned validate: request rejected before any source was contacted")
	agg := plannedFaultAggregator(t, func(plan *rpc.ReadPlan) {
		plan.Validate = func(context.Context, rpc.Request) error { return validateErr }
	}, nil)
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if !errors.Is(err, validateErr) {
		t.Fatalf("err = %v, want the validate failure", err)
	}
	faults, sourceFaults, collectionFaults := faultTables(t, decision)
	if faults != "" || sourceFaults != "" || collectionFaults != "" {
		t.Fatalf("request-side failure must not populate fault tables: %q %q %q", faults, sourceFaults, collectionFaults)
	}
	if decision.Diagnostics != nil {
		t.Fatalf("request-side failure must not carry diagnostics: %+v", decision.Diagnostics)
	}
}

func TestPlannedReferenceFailureKeepsFaultTablesEmpty(t *testing.T) {
	agg := plannedFaultAggregator(t, nil, nil)
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if !errors.Is(err, rpc.ErrReferenceRequired) {
		t.Fatalf("err = %v, want ErrReferenceRequired", err)
	}
	faults, sourceFaults, collectionFaults := faultTables(t, decision)
	if faults != "" || sourceFaults != "" || collectionFaults != "" {
		t.Fatalf("request-side failure must not populate fault tables: %q %q %q", faults, sourceFaults, collectionFaults)
	}
	if decision.Diagnostics != nil {
		t.Fatalf("request-side failure must not carry diagnostics: %+v", decision.Diagnostics)
	}
}

// The seam unit contract: an aggregation error and per-source abstentions are
// each stated once, the aggregation error keeps its errors.Is chain, and the
// degraded tables carry them together with the collection failure.
func TestFaultSeamAttachesEachStatementOnce(t *testing.T) {
	aggregateErr := fmt.Errorf("aggregate: %w", rpc.ErrNoValidResponse)
	forward := forwardPlan("m")
	state := &invocationState{
		plan:            &forward,
		aggregateErr:    aggregateErr,
		aggregateFaults: []error{errors.New("aggregate fields: source \"node-b\": malformed response: bad json")},
		collectErr:      pool.ErrAllSourcesDown,
	}
	decision := rpc.Decision{}
	state.attachFaults(&decision, nil)

	if len(decision.Faults) != 3 {
		t.Fatalf("aggregate error, abstention and collection failure expected, got %+v", decision.Faults)
	}
	assertNoDuplicateFaults(t, decision)
	if !errors.Is(decision.Faults[0], aggregateErr) {
		t.Fatalf("aggregation error lost its chain: %v", decision.Faults[0])
	}
	if !errors.Is(decision.Faults[2], pool.ErrAllSourcesDown) {
		t.Fatalf("collection fault lost its chain: %v", decision.Faults[2])
	}
	if len(decision.CollectionFaults) != 1 || !errors.Is(decision.CollectionFaults[0], pool.ErrAllSourcesDown) {
		t.Fatalf("collection fault table must repeat the pool failure once: %+v", decision.CollectionFaults)
	}
	if decision.SourceFaults != nil {
		t.Fatalf("no emission means no source attribution: %+v", decision.SourceFaults)
	}
}
