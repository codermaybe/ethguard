package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/projection"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// The fault-table contract: every pipeline exit assembles Faults,
// SourceFaults, CollectionFaults and Diagnostics through one seam
// (decision_faults.go). These tests pin what each exit owes — request-side
// failures keep the tables empty and ride the returned error, fan-out
// failures attribute every source exactly once, and a resolved decision
// never duplicates a fault across the human-readable and structured tables.

type failingProjector struct{}

func (failingProjector) ProjectObservation(json.RawMessage, rpcengine.Evaluation) (rpc.ExtensionSet, error) {
	return rpc.ExtensionSet{}, errors.New("projection: no extension accepted")
}

// faultAggregator builds an aggregator over scripted in-process nodes on
// method "m", with an explicit construction rule.
func faultAggregator(t *testing.T, policy rpcengine.Resolver, corroborated bool, script func(i int, node *rpctest.Node), opts ...Option) (*Aggregator, []*rpctest.Node) {
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
	corrob := rpcengine.Corroboration{Floor: 1}
	if corroborated {
		shared, err := rpcengine.NewCorroboration(rpc.CorroborationMajority, 2)
		if err != nil {
			t.Fatal(err)
		}
		corrob = shared
	}
	return New(p, corrob, append([]Option{WithPlans(map[string]rpcengine.ReadPlan{"m": {Method: "m", Resolver: policy}})}, opts...)...), nodes
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
	agg, _ := faultAggregator(t, eventPolicy(rpcengine.WithRPCErrorBodies()), true, func(i int, node *rpctest.Node) {
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
	plan := rpcengine.ReadPlan{Method: "m", Resolver: eventPolicy(rpcengine.WithRPCErrorBodies()), ResolverKind: methods.ResolverStrict, ProjectorID: "m"}
	recorder := &captureRecorder{}
	agg := New(p, mustMajorityRule(t), WithRecorder(recorder), WithProjectors(map[string]rpcengine.ObservationProjector{"m": failingProjector{}}), WithPlans(map[string]rpcengine.ReadPlan{"m": plan}))
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("a projection failure must not fail the read: %+v %v", decision.Status, err)
	}
	if len(decision.Faults) != 1 {
		t.Fatalf("expected exactly the projection fault, got %+v", decision.Faults)
	}
	if decision.Extensions != nil || oneEvent(t, recorder).Faults != 1 {
		t.Fatalf("failed projection must remain absent and record one event fault: %+v / %+v", decision.Extensions, recorder.events)
	}
	if _, sourceFaults, collectionFaults := faultTables(t, decision); sourceFaults != "" || collectionFaults != "" {
		t.Fatalf("projection failure must not populate source tables: %q %q", sourceFaults, collectionFaults)
	}
	if decision.Diagnostics == nil {
		t.Fatal("projection failure must not drop the field diagnostics")
	}
}

func TestProjectionDeliveryStatesAndEventFaults(t *testing.T) {
	for _, tt := range []struct {
		name      string
		projector rpcengine.ObservationProjector
		projected bool
		faults    int
	}{
		{name: "unprojected"},
		{name: "successful empty standard projection", projector: projection.ScalarStandardProjector(), projected: true},
		{name: "failed custom projection", projector: failingProjector{}, faults: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &captureRecorder{}
			plan := rpcengine.ReadPlan{Method: "m", Resolver: eventPolicy(), ResolverKind: methods.ResolverStrict}
			if tt.projector != nil {
				plan.ProjectorID = "projection-state"
			}
			agg, nodes := faultAggregator(t, plan.Resolver, true, func(_ int, node *rpctest.Node) {
				node.Return("m", "0x1")
			}, WithRecorder(recorder), WithPlans(map[string]rpcengine.ReadPlan{"m": plan}),
				WithProjectors(map[string]rpcengine.ObservationProjector{"projection-state": tt.projector}))
			decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
			if err != nil || decision.Status != rpc.Corroborated || string(decision.Value) != `"0x1"` {
				t.Fatalf("projection changed the business result: %+v / %v", decision, err)
			}
			if (decision.Extensions != nil) != tt.projected {
				t.Fatalf("projection presence = %t, want %t", decision.Extensions != nil, tt.projected)
			}
			if decision.Extensions != nil && decision.Extensions.Fields != nil {
				t.Fatalf("successful empty projection fields = %#v, want nil", decision.Extensions.Fields)
			}
			if len(decision.Faults) != tt.faults || len(decision.SourceFaults) != 0 || len(decision.CollectionFaults) != 0 {
				t.Fatalf("projection changed fault attribution: %+v / %+v / %+v", decision.Faults, decision.SourceFaults, decision.CollectionFaults)
			}
			event := oneEvent(t, recorder)
			if event.Faults != tt.faults || event.Status != rpc.Corroborated.String() {
				t.Fatalf("projection event differs from decision: %+v", event)
			}
			for i, node := range nodes {
				if calls := node.Calls(); len(calls) != 1 || calls[0].Method != "m" {
					t.Fatalf("source %d requests = %+v, want one m request", i, calls)
				}
			}
		})
	}
}

type recordingEvaluationProjector struct {
	delegate   rpcengine.ObservationProjector
	evaluation rpcengine.Evaluation
	calls      int
}

func (p *recordingEvaluationProjector) ProjectObservation(value json.RawMessage, evaluation rpcengine.Evaluation) (rpc.ExtensionSet, error) {
	p.calls++
	p.evaluation = evaluation
	return p.delegate.ProjectObservation(value, evaluation)
}

func TestCustomProjectorReceivesCompleteAcceptedEvaluation(t *testing.T) {
	projector := &recordingEvaluationProjector{delegate: projection.NewStandardProjector([]projection.StandardField{{Path: "/known", Required: true}})}
	plan := rpcengine.ReadPlan{Method: "m", Resolver: rpcengine.Strict(rpc.CanonicalJSON), ResolverKind: methods.ResolverStrict, ProjectorID: "custom"}
	agg, _ := faultAggregator(t, plan.Resolver, true, func(_ int, node *rpctest.Node) {
		node.ReturnRaw("m", json.RawMessage(`{"known":1,"vendor":2}`))
	}, WithPlans(map[string]rpcengine.ReadPlan{"m": plan}), WithProjectors(map[string]rpcengine.ObservationProjector{"custom": projector}))
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if err != nil || decision.Status != rpc.Corroborated || projector.calls != 1 {
		t.Fatalf("custom projector call = %d, decision = %+v, error = %v", projector.calls, decision, err)
	}
	if len(projector.evaluation.Accepted) != 2 {
		t.Fatalf("custom projector received filtered Accepted: %+v", projector.evaluation)
	}
	for i, path := range []string{"/known", "/vendor"} {
		field := projector.evaluation.Accepted[i]
		if field.Path != path || string(field.Value) != fmt.Sprint(i+1) || len(field.Sources) != 2 {
			t.Fatalf("custom projector lost complete source-backed field: %+v", field)
		}
	}
	if decision.Extensions == nil || len(decision.Extensions.Fields) != 1 || decision.Extensions.Fields[0].Path != "/vendor" {
		t.Fatalf("delegated standard projection = %+v", decision.Extensions)
	}
}

func TestEmptyLogsRetainsAcceptedRootBeforeStandardProjection(t *testing.T) {
	spec, ok := methods.SpecFor(methods.MethodGetLogs)
	if !ok {
		t.Fatal("logs method missing")
	}
	plan, err := methods.CompilePlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	standard, err := projection.NewSchemaProjector(methods.MethodGetLogs)
	if err != nil {
		t.Fatal(err)
	}
	projector := &recordingEvaluationProjector{delegate: standard}
	members := make([]pool.Member, 3)
	nodes := make([]*rpctest.Node, 3)
	for i := range nodes {
		nodes[i] = rpctest.NewNode(0)
		t.Cleanup(nodes[i].Close)
		nodes[i].ReturnRaw(methods.MethodGetLogs, json.RawMessage(`[]`))
		members[i] = pool.Member{Name: fmt.Sprintf("source-%d", i), URL: nodes[i].URL()}
	}
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	agg := New(p, mustMajorityRule(t), WithPlans(map[string]rpcengine.ReadPlan{methods.MethodGetLogs: plan}), WithProjectors(map[string]rpcengine.ObservationProjector{plan.ProjectorID: projector}))
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: methods.MethodGetLogs, Params: []any{map[string]any{"fromBlock": "0x1", "toBlock": "0x1"}}})
	if err != nil || decision.Status != rpc.Corroborated || string(decision.Value) != `[]` {
		t.Fatalf("empty logs result = %+v / %v", decision, err)
	}
	if projector.calls != 1 || len(projector.evaluation.Accepted) != 1 {
		t.Fatalf("root leaf disappeared before projection: calls=%d evaluation=%+v", projector.calls, projector.evaluation)
	}
	field := projector.evaluation.Accepted[0]
	if field.Path != "" || string(field.Value) != `[]` || len(field.Sources) != 3 {
		t.Fatalf("root leaf = %+v", field)
	}
	// The schema has no literal root declaration, but its /* container pattern
	// matches the empty segment under the existing matcher contract.
	if decision.Extensions == nil || decision.Extensions.Fields != nil {
		t.Fatalf("standard empty logs projection = %+v, want a successful empty set", decision.Extensions)
	}
	for i, node := range nodes {
		if calls := node.Calls(); len(calls) != 1 || calls[0].Method != methods.MethodGetLogs {
			t.Fatalf("source %d requests = %+v, want one eth_getLogs request", i, calls)
		}
	}
}

// plannedFaultAggregator builds a plan-driven aggregator whose Prepare hook
// and establishment stage are under the test's control. The pool member is
// unreachable on purpose: the fault exits under test happen before any
// fan-out.
func plannedFaultAggregator(t *testing.T, mutate func(*rpcengine.ReadPlan), stage ReferenceStage) *Aggregator {
	t.Helper()
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(&plan)
	}
	p := fixedPool(t, pool.Member{Name: "node-a", URL: "http://127.0.0.1:1"})
	opts := []Option{WithPlans(map[string]rpcengine.ReadPlan{probeMethod: plan})}
	if stage != nil {
		opts = append(opts, WithReferenceStage(stage))
	}
	return New(p, rpcengine.Corroboration{Floor: 1}, opts...)
}

// The planned pipeline's two local exits must meet the same fault contract
// as the legacy path: a request-side failure rides the returned error and
// keeps every fault table empty, with no diagnostics.
func TestPlannedPrepareFailureKeepsFaultTablesEmpty(t *testing.T) {
	validateErr := errors.New("planned validate: request rejected before any source was contacted")
	agg := plannedFaultAggregator(t, func(plan *rpcengine.ReadPlan) {
		plan.Prepare = func(_ context.Context, req rpc.Request) (rpc.Request, error) {
			return req, validateErr
		}
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
	state.attachFaults(&decision)

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
