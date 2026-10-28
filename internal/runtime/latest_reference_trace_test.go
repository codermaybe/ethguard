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
	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// invocationRecordingPolicy records the invocation every Prepare observes, so
// the trace fixture can prove that the reference stage and the business
// phases share one invocation.
type invocationRecordingPolicy struct {
	rpc.Policy

	mu          sync.Mutex
	invocations []rpc.Invocation
}

func (p *invocationRecordingPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	// The prepare-time invocation carries the frozen basis and no reference
	// yet: establishment runs inside this Prepare through the stage.
	if invocation, ok := refcontext.Invocation(ctx); ok {
		p.mu.Lock()
		p.invocations = append(p.invocations, invocation)
		p.mu.Unlock()
	}
	return p.Policy.Prepare(ctx, req)
}

// Resolve observes the completed invocation: the reference established during
// Prepare is attached by then.
func (p *invocationRecordingPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	return p.Policy.Resolve(obs)
}

func (p *invocationRecordingPolicy) snapshot() []rpc.Invocation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]rpc.Invocation(nil), p.invocations...)
}

// recordingStage records every establishment round the aggregator runs.
type recordingStage struct {
	inner aggregator.ReferenceStage

	mu    sync.Mutex
	ids   []rpc.InvocationID
	kinds []rpc.ReferenceKind
}

func (s *recordingStage) Establish(ctx context.Context, invocation rpc.Invocation, request rpc.ReferenceRequest) (rpc.Invocation, error) {
	s.mu.Lock()
	s.ids = append(s.ids, invocation.ID())
	s.kinds = append(s.kinds, request.Kind)
	s.mu.Unlock()
	return s.inner.Establish(ctx, invocation, request)
}

func (s *recordingStage) rounds() ([]rpc.InvocationID, []rpc.ReferenceKind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rpc.InvocationID(nil), s.ids...), append([]rpc.ReferenceKind(nil), s.kinds...)
}

// traceAggregator builds the composed read pipeline with the business policy
// wrapped for invocation recording.
func mustCorroboration(t *testing.T, mode rpc.CorroborationMode, floor int) *rpc.CorroborationShared {
	t.Helper()
	shared, err := rpc.NewCorroborationShared(mode, floor)
	if err != nil {
		t.Fatal(err)
	}
	return shared
}

func traceAggregator(t *testing.T, p *pool.Pool, anchoring *AnchoringStrategy) (*aggregator.Aggregator, *invocationRecordingPolicy, *recordingStage) {
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
	recorded := &invocationRecordingPolicy{Policy: bundle.Policies["eth_getBalance"]}
	registry := rpc.Registry{}
	for method, policy := range bundle.Policies {
		registry[method] = policy
	}
	registry["eth_getBalance"] = recorded
	stage := &recordingStage{inner: bundle.Stage}
	return aggregator.New(p, registry,
		aggregator.WithCorroboration(mustCorroboration(t, rpc.CorroborationMajority, 2)),
		aggregator.WithCollectTimeout(2*time.Second),
		aggregator.WithReferenceStage(stage)), recorded, stage
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
	p := pool.NewPoolWithOptions(0, pool.WithDialer(nativehttp.NewDialer()))
	for i, node := range nodes {
		if _, err := p.Join(fmt.Sprintf("node-%d", i), node.URL()); err != nil {
			t.Fatal(err)
		}
	}
	aggregate, recorded, stage := traceAggregator(t, p, nil)
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

	ids, kinds := stage.rounds()
	if len(ids) != 1 || kinds[0] != rpc.ReferenceLatest {
		t.Fatalf("establishment rounds = %v/%v, want one latest round", ids, kinds)
	}
	// Prepare runs twice for a moving-tag read: once to validate and detect
	// the tag (the decorator reports the pipeline sentinel), once after the
	// stage established the reference, to rewrite. Both observations share
	// the invocation; the second carries the established reference.
	seen := recorded.snapshot()
	if len(seen) != 2 {
		t.Fatalf("Prepare observed %d invocations, want the prepare and prepare-with-reference pair", len(seen))
	}
	if seen[0].ID() != ids[0] || seen[1].ID() != ids[0] {
		t.Fatalf("business invocations %v/%v != establishment invocation %s", seen[0].ID(), seen[1].ID(), ids[0])
	}
	// One frozen basis, captured before any request: every member occupies it.
	if seen[0].Basis().Size() != 3 {
		t.Fatalf("basis size = %d, want the three voting members", seen[0].Basis().Size())
	}
	if seen[0].Basis().Required() != 2 {
		t.Fatalf("basis required = %d, want the snapshot floor", seen[0].Basis().Required())
	}
	// The first Prepare observes no reference; the second observes the
	// established one.
	if seen[0].HasReference() {
		t.Fatal("the prepare-time invocation already carries a reference")
	}
	completed, ok := seen[1].Reference()
	if !ok || completed.Hash != headHash {
		t.Fatalf("prepare-with-reference invocation reference = %+v, want the established head", completed)
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
	p := pool.NewPoolWithOptions(0, pool.WithDialer(nativehttp.NewDialer()))
	for i, node := range nodes {
		if _, err := p.Join(fmt.Sprintf("node-%d", i), node.URL()); err != nil {
			t.Fatal(err)
		}
	}
	aggregate, recorded, stage := traceAggregator(t, p, &AnchoringStrategy{Finalized: true, MaxLag: 32})
	decision, err := aggregate.Aggregate(context.Background(), rpc.Request{Method: "eth_getBalance", Params: []any{"0x1", "latest"}})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if decision.Status != rpc.Corroborated || decision.Reference == nil || decision.Reference.Height != 10000 {
		t.Fatalf("decision = %+v, want corroboration at height 10000", decision)
	}
	ids, kinds := stage.rounds()
	if len(ids) != 1 || kinds[0] != rpc.ReferenceLatest {
		// The method declared the default reference; the finalized strategy
		// implements it through the candidate-selection algorithm.
		t.Fatalf("establishment rounds = %v/%v, want one declared-latest round", ids, kinds)
	}
	if decision.Reference.Origin != "reported-finalized" {
		t.Fatalf("origin = %q, want the finalized algorithm's evidence", decision.Reference.Origin)
	}
	if seen := recorded.snapshot(); len(seen) != 2 || seen[0].ID() != ids[0] || seen[1].ID() != ids[0] {
		t.Fatalf("business invocations %v do not share the establishment ID %s", seen, ids[0])
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
	p := pool.NewPoolWithOptions(0, pool.WithDialer(nativehttp.NewDialer()))
	for i, node := range nodes {
		if _, err := p.Join(fmt.Sprintf("node-%d", i), node.URL()); err != nil {
			t.Fatal(err)
		}
	}
	aggregate, recorded, stage := traceAggregator(t, p, nil)
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
	if ids, _ := stage.rounds(); len(ids) == 0 {
		t.Fatal("the establishment round was not observable")
	}
	if seen := recorded.snapshot(); len(seen) != 1 || seen[0].HasReference() {
		// A failed establishment never reaches the second Prepare, so the
		// only observation is the reference-less prepare-time invocation.
		t.Fatalf("a failed establishment must not leave a reference on the invocation: %+v", seen)
	}
	for i, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == "eth_getBalance" {
				t.Fatalf("node %d served the business read after a failed reference", i)
			}
		}
	}
}
