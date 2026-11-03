package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/internal/refcontext"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The planned-read trace fixtures. The production composition drives
// eth_getBalance from its explicit execution plan; these fixtures pin the
// plan pipeline's read invariants: one invocation shared by the validate
// phase and the establishment stage, one frozen basis, one establishment
// round, and zero business fan-out when establishment fails.

// invocationRecorder observes the invocations the plan pipeline carries: the
// validate phase runs inside the prepare phase, before establishment, on the
// invocation whose ID the establishment stage later receives.
type invocationRecorder struct {
	mu          chan struct{}
	invocations []rpc.Invocation
}

func newInvocationRecorder() *invocationRecorder {
	return &invocationRecorder{mu: make(chan struct{}, 1)}
}

func (r *invocationRecorder) record(invocation rpc.Invocation) {
	r.invocations = append(r.invocations, invocation)
}

func (r *invocationRecorder) snapshot() []rpc.Invocation {
	return append([]rpc.Invocation(nil), r.invocations...)
}

// planValidate observes the prepare-time invocation and lets every request
// pass.
func planValidate(recorder *invocationRecorder) func(context.Context, rpc.Request) error {
	return func(ctx context.Context, req rpc.Request) error {
		if invocation, ok := refcontext.Invocation(ctx); ok {
			recorder.record(invocation)
		}
		return nil
	}
}

// planTraceAggregator builds the production composition and rebinds the
// balance plan with the recording validate hook, so the fixture can observe
// the invocation the plan pipeline shares with the establishment stage.
func planTraceAggregator(t *testing.T, p *pool.Pool, anchoring *AnchoringStrategy) (*aggregator.Aggregator, *invocationRecorder, *recordingStage) {
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
	stage := &recordingStage{inner: bundle.Stage}
	recorder := newInvocationRecorder()
	plan := bundle.Plans["eth_getBalance"]
	plan.Validate = planValidate(recorder)
	agg := aggregator.New(p, nil,
		aggregator.WithCorroboration(mustCorroboration(t, rpc.CorroborationMajority, 2)),
		aggregator.WithCollectTimeout(2*time.Second),
		aggregator.WithReferenceStage(stage),
		aggregator.WithPlans(map[string]rpc.ReadPlan{"eth_getBalance": plan}))
	return agg, recorder, stage
}

// The planned trace for a latest moving-tag read: one invocation shared by
// the validate phase and the establishment stage, one frozen basis, one
// establishment round, and the business request pinned to the established
// hash. No nested aggregate runs: the head read is the stage's own round.
func TestPlannedReferenceTraceSharesOneInvocation(t *testing.T) {
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
	p := planPool(t, nodes)
	aggregate, recorder, stage := planTraceAggregator(t, p, nil)
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
	// The validate phase observed the invocation before establishment; the
	// establishment stage received the same invocation. One ID, one frozen
	// basis, no nested aggregate.
	seen := recorder.snapshot()
	if len(seen) != 1 {
		t.Fatalf("validate observed %d invocations, want the one prepare-time invocation", len(seen))
	}
	if seen[0].ID() != ids[0] {
		t.Fatalf("business invocation %s != establishment invocation %s", seen[0].ID(), ids[0])
	}
	if seen[0].Basis().Size() != 3 {
		t.Fatalf("basis size = %d, want the three voting members", seen[0].Basis().Size())
	}
	if seen[0].Basis().Required() != 2 {
		t.Fatalf("basis required = %d, want the snapshot floor", seen[0].Basis().Required())
	}
	if seen[0].HasReference() {
		t.Fatal("the prepare-time invocation already carries a reference")
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

// A failed establishment keeps the planned business fan-out at zero: the
// nodes never see the business method, and the evidence records the failed
// reference.
func TestPlannedReferenceFailureKeepsBusinessFanOutAtZero(t *testing.T) {
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	for _, node := range nodes {
		node.Fail("eth_getBlockByNumber", -32000, "head unavailable")
	}
	p := planPool(t, nodes)
	aggregate, recorder, stage := planTraceAggregator(t, p, nil)
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
	if seen := recorder.snapshot(); len(seen) != 1 || seen[0].HasReference() {
		// A failed establishment never reaches the rewrite, so the only
		// observation is the reference-less prepare-time invocation.
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

// samePreparedRequest compares the prepared requests without assuming the
// two fleets share source identity: both nil (prepare never produced a
// request), or both present with the same method and params.
func samePreparedRequest(a, b *rpc.Request) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.Method == b.Method && fmt.Sprint(a.Params) == fmt.Sprint(b.Params)
}

// sameReferenceFacts compares the establishment evidence's own facts: the
// level, the established head, the status class, the origin, and the
// membership sizes. Participant identity belongs to each fleet.
func sameReferenceFacts(a, b *rpc.ReferenceEvidence) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.Tag == b.Tag && a.Hash == b.Hash && a.Height == b.Height &&
		a.Status == b.Status && a.Origin == b.Origin &&
		len(a.Participants) == len(b.Participants) &&
		len(a.Support) == len(b.Support) &&
		len(a.Reject) == len(b.Reject) &&
		len(a.Faults) == len(b.Faults) &&
		len(a.SourceFaults) == len(b.SourceFaults) &&
		len(a.CollectionFaults) == len(b.CollectionFaults)
}

func planPool(t *testing.T, nodes []*rpctest.Node) *pool.Pool {
	t.Helper()
	members := make([]pool.Member, 0, len(nodes))
	for i, node := range nodes {
		members = append(members, pool.Member{Name: fmt.Sprintf("node-%d", i), URL: node.URL()})
	}
	p, err := pool.NewWithMembers(1, members, pool.WithDialer(nativehttp.NewDialer()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// The plan-and-chain equivalence matrix for eth_getBalance. Every scenario
// runs the same scripted fleet twice — once driven by the explicit plan,
// once by the compiled chain — and the two outcomes must agree on the
// decision, the prepared request, the reference evidence, the block pin,
// the faults, and the error text.
func TestPlannedBalanceMatchesChainOverTheFleet(t *testing.T) {
	latestHash := "0x" + fmt.Sprintf("%064x", 100)
	safeHash := "0x" + fmt.Sprintf("%064x", 99)
	headFor := func(tag string) string {
		if tag == "safe" {
			return safeHash
		}
		return latestHash
	}
	scenarios := []struct {
		name      string
		params    []any
		balance   func(call rpctest.Call) (any, error)
		failHeads bool
	}{
		{
			name:   "latest agreement",
			params: []any{"0x1", "latest"},
			balance: func(call rpctest.Call) (any, error) {
				var got string
				if len(call.Params) == 2 && json.Unmarshal(call.Params[1], &got) == nil && got == latestHash {
					return "0x64", nil
				}
				return "0xdeadbeef", nil
			},
		},
		{
			name:   "safe through the object form",
			params: []any{"0x1", map[string]any{"blockNumber": "safe"}},
			balance: func(call rpctest.Call) (any, error) {
				var got string
				if len(call.Params) == 2 && json.Unmarshal(call.Params[1], &got) == nil && got == safeHash {
					return "0x64", nil
				}
				return "0xdeadbeef", nil
			},
		},
		{
			name:   "explicit height passes through",
			params: []any{"0x1", "0x5"},
			balance: func(call rpctest.Call) (any, error) {
				return "0x64", nil
			},
		},
		{
			name:   "pending is a node-local observation",
			params: []any{"0x1", "pending"},
			balance: func(call rpctest.Call) (any, error) {
				return "0x64", nil
			},
		},
		{
			name:   "dissent is a conflict",
			params: []any{"0x1", "latest"},
			balance: func(call rpctest.Call) (any, error) {
				var got string
				if len(call.Params) == 2 && json.Unmarshal(call.Params[1], &got) == nil && got == latestHash {
					return "0x64", nil
				}
				return "0xdeadbeef", nil
			},
		},
		{
			name:      "establishment failure keeps the fan-out at zero",
			params:    []any{"0x1", "latest"},
			failHeads: true,
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// The dissent scenario rewrites one node's answer, so the fleet
			// is scripted per node index.
			dissentIndex := 2
			build := func(planned bool) (rpc.Decision, []bool, error) {
				nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
				defer func() {
					for _, node := range nodes {
						node.Close()
					}
				}()
				servedBalance := make([]bool, len(nodes))
				for i, node := range nodes {
					if scenario.failHeads {
						node.Fail("eth_getBlockByNumber", -32000, "head unavailable")
					} else {
						node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
							tag := "latest"
							if len(call.Params) > 0 {
								_ = json.Unmarshal(call.Params[0], &tag)
							}
							return map[string]any{
								"number": fmt.Sprintf("0x%x", 100),
								"hash":   headFor(tag),
							}, nil
						})
					}
					answer := scenario.balance
					if scenario.name == "dissent is a conflict" && i == dissentIndex {
						answer = func(call rpctest.Call) (any, error) { return "0x65", nil }
					}
					handler := answer
					node.Handle("eth_getBalance", func(call rpctest.Call) (any, error) {
						servedBalance[i] = true
						return handler(call)
					})
				}
				p := planPool(t, nodes)
				bundle, err := Build(Inputs{
					Pool:           p,
					Recorder:       observe.Nop,
					CollectTimeout: 2 * time.Second,
					Corroboration:  mustCorroboration(t, rpc.CorroborationMajority, 2),
				})
				if err != nil {
					t.Fatal(err)
				}
				var agg *aggregator.Aggregator
				if planned {
					agg = bundle.Aggregator
				} else {
					// The chain shape: the same fleet, the hand-written
					// balance chain bound to the same stage's readers.
					chain := methods.GetBalancePolicy(bundle.Stage.AnchorHashReader())
					agg = aggregator.New(p, rpc.Registry{"eth_getBalance": chain},
						aggregator.WithCorroboration(mustCorroboration(t, rpc.CorroborationMajority, 2)),
						aggregator.WithCollectTimeout(2*time.Second),
						aggregator.WithReferenceStage(bundle.Stage))
				}
				decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: "eth_getBalance", Params: scenario.params})
				return decision, servedBalance, aggErr
			}

			plannedDecision, plannedServed, plannedErr := build(true)
			chainDecision, chainServed, chainErr := build(false)

			if (plannedErr == nil) != (chainErr == nil) {
				t.Fatalf("error mismatch: plan %v chain %v", plannedErr, chainErr)
			}
			if plannedErr != nil && plannedErr.Error() != chainErr.Error() {
				t.Fatalf("error text mismatch: plan %q chain %q", plannedErr, chainErr)
			}
			if plannedDecision.Status != chainDecision.Status {
				t.Fatalf("status mismatch: plan %v chain %v", plannedDecision.Status, chainDecision.Status)
			}
			if string(plannedDecision.Value) != string(chainDecision.Value) {
				t.Fatalf("value mismatch: plan %s chain %s", plannedDecision.Value, chainDecision.Value)
			}
			if plannedDecision.BlockRef != chainDecision.BlockRef {
				t.Fatalf("block pin mismatch: plan %q chain %q", plannedDecision.BlockRef, chainDecision.BlockRef)
			}
			if len(plannedDecision.Support) != len(chainDecision.Support) {
				t.Fatalf("support mismatch: plan %v chain %v", plannedDecision.Support, chainDecision.Support)
			}
			if len(plannedDecision.Faults) != len(chainDecision.Faults) {
				t.Fatalf("faults mismatch: plan %v chain %v", plannedDecision.Faults, chainDecision.Faults)
			}
			// The prepared request and the reference evidence must carry the
			// same facts. The two fleets are separate, so source identity
			// (URLs, ports) differs by construction; only the evidence's
			// own facts and its membership size must agree.
			if !samePreparedRequest(plannedDecision.Prepared, chainDecision.Prepared) {
				t.Fatalf("prepared request mismatch: plan %v chain %v",
					plannedDecision.Prepared, chainDecision.Prepared)
			}
			if !sameReferenceFacts(plannedDecision.Reference, chainDecision.Reference) {
				t.Fatalf("reference mismatch:\nplan  %+v\nchain %+v", plannedDecision.Reference, chainDecision.Reference)
			}
			for i := range plannedServed {
				if plannedServed[i] != chainServed[i] {
					t.Fatalf("business fan-out mismatch on node %d: plan %v chain %v", i, plannedServed[i], chainServed[i])
				}
			}
		})
	}
}
