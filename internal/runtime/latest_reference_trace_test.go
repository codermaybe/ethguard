package runtime

import (
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"

	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

func tracePool(t *testing.T, nodes []*rpctest.Node) *pool.Pool {
	t.Helper()
	members := make([]pool.Member, 0, len(nodes))
	for i, node := range nodes {
		members = append(members, pool.Member{Name: fmt.Sprintf("node-%d", i), URL: node.URL()})
	}
	p, err := pool.NewWithMembers(0, members, pool.WithDialer(nativehttp.NewDialer()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// traceRecorder records the pipeline's observable events in order: the
// plan's Validate hook and the establishment rounds with the exact
// invocation each receives. The sharing proof rides the stage — Establish
// receives the same pre-establishment invocation the business phases
// continue with — so the recorded invocation carries the frozen basis and
// the no-reference-yet state the validate-time observation used to pin.
type traceRecorder struct {
	inner aggregator.ReferenceStage

	mu          sync.Mutex
	events      []string
	invocations []rpc.Invocation
	kinds       []rpc.ReferenceKind
}

func (r *traceRecorder) observe(ctx context.Context, req rpc.Request) error {
	r.mu.Lock()
	r.events = append(r.events, "validate")
	r.mu.Unlock()
	return nil
}

func (r *traceRecorder) Establish(ctx context.Context, invocation rpc.Invocation, request rpc.ReferenceRequest) (rpc.Invocation, error) {
	r.mu.Lock()
	r.events = append(r.events, "establish")
	r.invocations = append(r.invocations, invocation)
	r.kinds = append(r.kinds, request.Kind)
	r.mu.Unlock()
	return r.inner.Establish(ctx, invocation, request)
}

func (r *traceRecorder) trace() ([]string, []rpc.Invocation, []rpc.ReferenceKind) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...),
		append([]rpc.Invocation(nil), r.invocations...),
		append([]rpc.ReferenceKind(nil), r.kinds...)
}

func mustCorroboration(t *testing.T, mode rpc.CorroborationMode, floor int) *rpc.CorroborationShared {
	t.Helper()
	shared, err := rpc.NewCorroborationShared(mode, floor)
	if err != nil {
		t.Fatal(err)
	}
	return shared
}

// traceAggregator builds the composed read pipeline for the trace fixtures:
// the compiled balance plan with a Validate hook recording the invocation,
// registered beside the recording establishment stage.
func traceAggregator(t *testing.T, p *pool.Pool, anchoring *AnchoringStrategy) (*aggregator.Aggregator, *traceRecorder) {
	t.Helper()
	bundle, err := Build(Inputs{
		Pool:           p,
		Recorder:       observe.Nop,
		CollectTimeout: 2 * time.Second,
		Corroboration:  mustCorroboration(t, rpc.CorroborationMajority, 2),
		Anchoring:      anchoring,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := methods.PlanFor(methods.MethodGetBalance)
	if err != nil {
		t.Fatal(err)
	}
	recorded := &traceRecorder{inner: bundle.Stage}
	plan.Validate = recorded.observe
	return aggregator.New(p, aggregator.WithPlans(map[string]rpc.ReadPlan{
		methods.MethodGetBalance: plan,
	}),
		aggregator.WithCorroboration(mustCorroboration(t, rpc.CorroborationMajority, 2)),
		aggregator.WithCollectTimeout(2*time.Second),
		aggregator.WithReferenceStage(recorded)), recorded
}

// The trace fixture for a latest moving-tag read: one invocation ID shared by
// the reference stage and the business phases, one frozen basis, one
// establishment round through the stage, and the business request pinned to
// the established hash. No nested aggregate runs: the head read is the
// stage's own round, not a second Aggregate.
func TestLatestReferenceTraceSharesOneInvocation(t *testing.T) {
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	const (
		headHeight = 100
		headHash   = "0xabc"
	)
	for _, node := range nodes {
		node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
			return map[string]any{"number": fmt.Sprintf("0x%x", headHeight), "hash": headHash}, nil
		})
		node.Handle("eth_getBalance", func(call rpctest.Call) (any, error) {
			var tag string
			if len(call.Params) == 2 && json.Unmarshal(call.Params[1], &tag) == nil && tag == headHash {
				return "0x64", nil
			}
			return "0xdeadbeef", nil
		})
	}
	p := tracePool(t, nodes)
	aggregate, trace := traceAggregator(t, p, nil)
	decision, err := aggregate.Aggregate(context.Background(), rpc.Request{Method: "eth_getBalance", Params: []any{"0x1", "latest"}})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if decision.Status != rpc.Corroborated {
		t.Fatalf("status = %s, want corroborated", decision.Status)
	}
	if decision.Reference == nil || decision.Reference.Hash != headHash || decision.Reference.Tag != "" {
		t.Fatalf("reference = %+v, want the established latest head", decision.Reference)
	}

	events, invocations, kinds := trace.trace()
	if len(kinds) != 1 || kinds[0] != rpc.ReferenceLatest {
		t.Fatalf("establishment rounds = %v, want one latest round", kinds)
	}
	// The plan pipeline validates once, before establishment: the recorded
	// event order pins that, and the establishment call receives the same
	// pre-establishment invocation the business phases continue with. The
	// rewrite after establishment is the plan's own pure function — the
	// decorator path's prepare pair becomes this identity check plus the
	// decision's attached reference (asserted above).
	if len(events) != 2 || events[0] != "validate" || events[1] != "establish" {
		t.Fatalf("pipeline events = %v, want one validate before one establish", events)
	}
	seen := invocations[0]
	// One frozen basis, captured before any request: every member occupies it.
	if seen.Basis().Size() != 3 {
		t.Fatalf("basis size = %d, want the three voting members", seen.Basis().Size())
	}
	if seen.Basis().Required() != 2 {
		t.Fatalf("basis required = %d, want the snapshot floor", seen.Basis().Required())
	}
	// The establish-time invocation predates WithReference: no reference yet.
	// The established reference rides the decision (asserted above).
	if seen.HasReference() {
		t.Fatal("the pre-establishment invocation already carries a reference")
	}
	// The establishment round is the only head read; the business round is
	// the only balance read. A nested aggregate would multiply the rounds.
	for i, node := range nodes {
		calls := 0
		for _, call := range node.Calls() {
			switch call.Method {
			case "eth_getBlockByNumber":
				calls++
			case "eth_getBalance":
				calls++
			}
		}
		if calls != 2 {
			t.Fatalf("node %d served %d rounds, want exactly head + business", i, calls)
		}
	}
}

// The finalized trace: candidate selection, confirmation of the member above
// the candidate, and the business read pinned to the confirmed height — all
// inside one invocation.
func TestFinalizedReferenceTraceConfirmsAndPins(t *testing.T) {
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	heads := []int{10000, 9999, 10001}
	for i, node := range nodes {
		height := heads[i]
		node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
			tag := ""
			if len(call.Params) > 0 {
				_ = json.Unmarshal(call.Params[0], &tag)
			}
			if tag == "finalized" || tag == "latest" {
				return map[string]any{"number": fmt.Sprintf("0x%x", height), "hash": fmt.Sprintf("0x%064x", height)}, nil
			}
			// A confirmation request names its height as a hex quantity; the
			// member confirms that height under the fleet's shared hash.
			height := uint64(0)
			_, _ = fmt.Sscanf(tag, "0x%x", &height)
			return map[string]any{"number": fmt.Sprintf("0x%x", height), "hash": fmt.Sprintf("0x%064x", height)}, nil
		})
		node.Handle("eth_getBalance", func(call rpctest.Call) (any, error) {
			hash := ""
			if len(call.Params) == 2 {
				_ = json.Unmarshal(call.Params[1], &hash)
			}
			if hash == fmt.Sprintf("0x%064x", 9999) {
				return "0x64", nil
			}
			return "0xdeadbeef", nil
		})
	}
	p := tracePool(t, nodes)
	aggregate, trace := traceAggregator(t, p, &AnchoringStrategy{Finalized: true, MaxLag: 32})
	decision, err := aggregate.Aggregate(context.Background(), rpc.Request{Method: "eth_getBalance", Params: []any{"0x1", "latest"}})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if decision.Status != rpc.Corroborated || decision.Reference == nil || decision.Reference.Height != 10000 {
		t.Fatalf("decision = %+v, want corroboration at height 10000", decision)
	}
	_, invocations, kinds := trace.trace()
	if len(invocations) != 1 || kinds[0] != rpc.ReferenceLatest {
		// The method declared the default reference; the finalized strategy
		// implements it through the candidate-selection algorithm, on the one
		// shared invocation.
		t.Fatalf("establishment rounds = %v/%v, want one declared-latest round", invocations, kinds)
	}
	if decision.Reference.Origin != "reported-finalized" {
		t.Fatalf("origin = %q, want the finalized algorithm's evidence", decision.Reference.Origin)
	}
}

// A failed establishment keeps the business fan-out at zero: the nodes never
// see the business method, and the evidence records the failed reference.
func TestReferenceFailureKeepsBusinessFanOutAtZero(t *testing.T) {
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	for _, node := range nodes {
		node.Fail("eth_getBlockByNumber", -32000, "head unavailable")
	}
	p := tracePool(t, nodes)
	aggregate, trace := traceAggregator(t, p, nil)
	decision, err := aggregate.Aggregate(context.Background(), rpc.Request{Method: "eth_getBalance", Params: []any{"0x1", "latest"}})
	if err == nil {
		t.Fatal("expected the failed establishment to fail the read")
	}
	if decision.Status != rpc.Unknown {
		t.Fatalf("status = %s, want unknown", decision.Status)
	}
	if decision.Reference == nil || len(decision.Reference.Faults) == 0 {
		t.Fatalf("failed reference lost its evidence: %+v", decision.Reference)
	}
	events, invocations, _ := trace.trace()
	if len(events) != 2 || events[0] != "validate" || events[1] != "establish" {
		t.Fatalf("pipeline events = %v, want one validate before one establish", events)
	}
	if len(invocations) != 1 || invocations[0].HasReference() {
		// A failed establishment never reaches the second Prepare, so the
		// only observation is the reference-less pre-establishment invocation.
		t.Fatalf("a failed establishment must not leave a reference on the invocation: %+v", invocations)
	}
	for i, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == "eth_getBalance" {
				t.Fatalf("node %d served the business read after a failed reference", i)
			}
		}
	}
}
