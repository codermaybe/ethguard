package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/refcontext"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The plan-execution fixtures. probeMethod is a synthetic default-block
// state read with the full reference and pending shape: hash anchored at
// position 1, the EIP-1898 object form at the same position, and pending
// observed node-locally. The plan and the chain are the same method
// assembled twice — once as an explicit execution plan, once as the
// compiled decorator chain — over the same scripted nodes.
const probeMethod = "eth_planProbe"

// probeNormalize and probeEncode are the probe's synthetic value domain: a
// JSON string compared by its own spelling, and committed as delivered.
func probeNormalize(value json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return "", err
	}
	return s, nil
}

func probeEncode(value json.RawMessage) (json.RawMessage, error) {
	return value, nil
}

func probeChainPolicy(anchor rpc.AnchorFunc) rpc.Policy {
	return methods.PendingLocal(1)(methods.EIP1898BlockRef(anchor, 1)(rpc.WithAnchor(anchor, 1)(
		rpc.Strict(probeNormalize,
			rpc.WithCanonicalValue(probeEncode),
			rpc.WithRuleCompareKind(rpc.RuleCompareQuantity)))))
}

func probeSpec() methods.MethodSpec {
	return methods.MethodSpec{
		Method:      probeMethod,
		Resolver:    methods.ResolverStrict,
		Normalize:   probeNormalize,
		Encode:      probeEncode,
		KeySource:   rpc.RuleKeyCanonicalBytes,
		CompareKind: rpc.RuleCompareQuantity,
		Reference:   methods.ReferenceSpec{Kind: methods.ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
		Pending:     methods.PendingSpec{Mode: methods.PendingObserve, Pos: 1},
	}
}

// contextAnchor is the consumption-only anchor reader the chain side binds:
// it reads an already-established reference from the in-flight invocation
// and never establishes anything itself, exactly like the composition
// root's stage readers.
func contextAnchor() rpc.AnchorFunc {
	return func(ctx context.Context) (string, error) {
		invocation, ok := refcontext.Invocation(ctx)
		if !ok {
			return "", rpc.ErrReferenceRequired
		}
		ref, ok := invocation.Reference()
		if !ok || !rpc.EvidenceServesTag(ref, rpc.AnchorTag(ctx)) {
			return "", rpc.ErrReferenceRequired
		}
		if ref.Status != rpc.Corroborated {
			return "", rpc.ErrConflict
		}
		return ref.Hash, nil
	}
}

// scriptedStage is the establishment double: one fixed, corroborated
// evidence value served once per Establish call, with the rounds recorded
// for the zero-fan-out assertions.
type scriptedStage struct {
	ref    rpc.ReferenceEvidence
	err    error
	rounds []rpc.ReferenceRequest
}

func (s *scriptedStage) Establish(ctx context.Context, invocation rpc.Invocation, request rpc.ReferenceRequest) (rpc.Invocation, error) {
	s.rounds = append(s.rounds, request)
	if s.err != nil {
		return invocation, s.err
	}
	ref := s.ref
	if ref.Tag == "" && request.Kind != rpc.ReferenceLatest {
		ref.Tag = string(request.Kind)
	}
	completed, err := invocation.WithReference(ref)
	if err != nil {
		return invocation, err
	}
	return completed, nil
}

func probeNodes(t *testing.T, value string, n int) ([]*rpctest.Node, *pool.Pool) {
	t.Helper()
	nodes := make([]*rpctest.Node, n)
	members := make([]pool.Member, 0, n)
	for i := range nodes {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		node.Return(probeMethod, value)
		nodes[i] = node
		members = append(members, pool.Member{Name: string(rune('a' + i)), URL: node.URL()})
	}
	p := fixedPoolWith(t, 1, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	return nodes, p
}

func probeAggregator(t *testing.T, p *pool.Pool, stage *scriptedStage, planned bool) *Aggregator {
	t.Helper()
	shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 2)
	if err != nil {
		t.Fatal(err)
	}
	opts := []Option{
		WithCorroboration(shared),
		WithCollectTimeout(2 * time.Second),
		WithReferenceStage(stage),
	}
	registry := rpc.Registry{}
	if planned {
		plan, err := methods.CompilePlan(probeSpec())
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
	} else {
		registry[probeMethod] = probeChainPolicy(contextAnchor())
	}
	return New(p, registry, opts...)
}

// TestPlannedReadMatchesTheChainPath drives the same probe method through
// both routes over the same scripted fleet: the plan path and the compiled
// chain path must agree on the decision, the prepared request, and the
// recorded block pin, and both must establish exactly once.
func TestPlannedReadMatchesTheChainPath(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 3)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated}}
	plannedDecision, plannedErr := probeAggregator(t, p, stage, true).Aggregate(context.Background(),
		rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if plannedErr != nil {
		t.Fatalf("planned read failed: %v", plannedErr)
	}

	nodes, p = probeNodes(t, "0x64", 3)
	chainStage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated}}
	chainDecision, chainErr := probeAggregator(t, p, chainStage, false).Aggregate(context.Background(),
		rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if chainErr != nil {
		t.Fatalf("chain read failed: %v", chainErr)
	}

	if plannedDecision.Status != chainDecision.Status || plannedDecision.Status != rpc.Corroborated {
		t.Fatalf("status mismatch: plan %v chain %v", plannedDecision.Status, chainDecision.Status)
	}
	if string(plannedDecision.Value) != string(chainDecision.Value) {
		t.Fatalf("value mismatch: plan %s chain %s", plannedDecision.Value, chainDecision.Value)
	}
	if plannedDecision.BlockRef != chainDecision.BlockRef || plannedDecision.BlockRef != "0xabc" {
		t.Fatalf("block pin mismatch: plan %q chain %q", plannedDecision.BlockRef, chainDecision.BlockRef)
	}
	if len(plannedDecision.Support) != len(chainDecision.Support) {
		t.Fatalf("support mismatch: plan %v chain %v", plannedDecision.Support, chainDecision.Support)
	}
	// The prepared request is pinned to the established hash on both paths.
	if plannedDecision.Prepared.Params[1] != "0xabc" || chainDecision.Prepared.Params[1] != "0xabc" {
		t.Fatalf("prepared request mismatch: plan %v chain %v", plannedDecision.Prepared.Params, chainDecision.Prepared.Params)
	}
	// One establishment round per read, on both routes.
	if len(stage.rounds) != 1 || len(chainStage.rounds) != 1 {
		t.Fatalf("establishment rounds: plan %d chain %d, want one each", len(stage.rounds), len(chainStage.rounds))
	}
	// Every source answered for the same rewritten request: exactly one
	// probe call per node (the pool's own chain-id handshake does not count).
	for i, node := range nodes {
		probeCalls := 0
		for _, call := range node.Calls() {
			if call.Method == probeMethod {
				probeCalls++
				params := call.Params
				if len(params) != 2 || string(params[1]) != `"0xabc"` {
					t.Fatalf("node %d was asked %v, want the established hash", i, params)
				}
			}
		}
		if probeCalls != 1 {
			t.Fatalf("node %d answered %d probe calls, want one", i, probeCalls)
		}
	}
}

// TestPlannedValidationFailsBeforeAnyFanOut pins the Validate phase's
// contract: a method-owned rejection fails the read locally, no source is
// contacted, and the decision is Unknown with the failure.
func TestPlannedValidationFailsBeforeAnyFanOut(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Status: rpc.Corroborated}}
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	plan.Validate = func(context.Context, rpc.Request) error {
		return errors.New("rpc: probe rejects everything: rpc invalid request")
	}
	agg := New(p, nil, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
		WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if aggErr == nil || decision.Status != rpc.Unknown {
		t.Fatalf("validation failure did not fail the read: %+v %v", decision, aggErr)
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("rejected read fanned out: %+v", node.Calls())
		}
	}
	if len(stage.rounds) != 0 {
		t.Fatalf("rejected read established a reference: %+v", stage.rounds)
	}
}

// TestPlannedPendingRejectFailsBeforeEstablishment pins the pending-reject
// phase: the declared local rejection fires before the reference stage, so a
// pending read contacts nothing and establishes nothing.
func TestPlannedPendingRejectFailsBeforeEstablishment(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Status: rpc.Corroborated}}
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	// The probe switches to a method-owned rejection only by declaration;
	// without a validate hook the pipeline owns the rejection.
	plan.Pending.Mode = methods.PendingRefuse
	plan.PendingReject = rpc.PendingRejectAt(plan.Pending.Pos, plan.Method)
	agg := New(p, nil, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
		WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "pending"}})
	if aggErr == nil || decision.Status != rpc.Unknown {
		t.Fatalf("pending rejection did not fail the read: %+v %v", decision, aggErr)
	}
	if !errors.Is(aggErr, rpc.ErrInvalidRequest) {
		t.Fatalf("pending rejection error is not ErrInvalidRequest: %v", aggErr)
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("rejected pending read fanned out: %+v", node.Calls())
		}
	}
	if len(stage.rounds) != 0 {
		t.Fatalf("rejected pending read established a reference: %+v", stage.rounds)
	}
}

// TestPlannedPendingObservationStaysSelected pins the pending-local route: a
// pending request resolves as a node-local observation with Selected status,
// never Corroborated, even under unanimous agreement.
func TestPlannedPendingObservationStaysSelected(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 3)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Status: rpc.Corroborated}}
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	agg := New(p, nil, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
		WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "pending"}})
	if aggErr != nil {
		t.Fatalf("pending observation failed: %v", aggErr)
	}
	if decision.Status != rpc.Selected {
		t.Fatalf("pending observation claimed %v, want Selected", decision.Status)
	}
	if string(decision.Value) != `"0x64"` || len(decision.Support) != 1 {
		t.Fatalf("pending observation did not stand on the first usable source: %+v", decision)
	}
	// Pending rides the wire untouched: no establishment, no rewrite.
	if len(stage.rounds) != 0 {
		t.Fatalf("pending observation established a reference: %+v", stage.rounds)
	}
	if decision.Prepared.Params[1] != "pending" {
		t.Fatalf("pending request was rewritten: %+v", decision.Prepared.Params)
	}
	// The pending shape still fans out under the collection mode; resolution
	// stands on the first usable source.
	for i, node := range nodes {
		probeCalls := 0
		for _, call := range node.Calls() {
			if call.Method == probeMethod {
				probeCalls++
			}
		}
		if probeCalls != 1 {
			t.Fatalf("node %d answered %d probe calls, want one", i, probeCalls)
		}
	}
}

// TestPlannedEstablishmentFailureKeepsZeroBusinessFanOut pins the
// establishment contract: a failed establishment fails the read with the
// anchor cause, the partial evidence stays visible, and the business round
// never starts.
func TestPlannedEstablishmentFailureKeepsZeroBusinessFanOut(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	stage := &scriptedStage{err: errors.New("reference: fleet disagreed")}
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	agg := New(p, nil, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
		WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if aggErr == nil || decision.Status != rpc.Unknown {
		t.Fatalf("failed establishment did not fail the read: %+v %v", decision, aggErr)
	}
	if !errors.Is(aggErr, errors.New("reference: fleet disagreed")) && aggErr.Error() != "anchor: reference: fleet disagreed" {
		t.Fatalf("establishment cause lost: %v", aggErr)
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("failed establishment still fanned out: %+v", node.Calls())
		}
	}
}

// TestPlannedReadRequiresAnEstablishmentStage pins the no-stage rule: a read
// that declares a moving-tag position fails before any fan-out when no
// establishment seam is installed.
func TestPlannedReadRequiresAnEstablishmentStage(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	agg := New(p, nil, WithCorroboration(mustShared(t)), WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if aggErr == nil || decision.Status != rpc.Unknown {
		t.Fatalf("missing stage did not fail the read: %+v %v", decision, aggErr)
	}
	if aggErr.Error() != "reference: no establishment stage for a read that requires one: rpc: reference required for moving-tag read" {
		t.Fatalf("unexpected no-stage error: %v", aggErr)
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("stageless read fanned out: %+v", node.Calls())
		}
	}
}

// TestUnplannedMethodKeepsTheLegacyPath pins the fallback: a method with a
// compiled policy and no plan resolves through the legacy policy path, on
// the same aggregator that drives other methods from plans.
func TestUnplannedMethodKeepsTheLegacyPath(t *testing.T) {
	a, b := rpctest.NewNode(1), rpctest.NewNode(1)
	defer a.Close()
	defer b.Close()
	a.Return("eth_other", "0x64")
	b.Return("eth_other", "0x64")
	p := fixedPoolWith(t, 1, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())},
		pool.Member{Name: "a", URL: a.URL()},
		pool.Member{Name: "b", URL: b.URL()},
	)
	shared := mustShared(t)
	planAgg := New(p, rpc.Registry{"eth_other": rpc.Strict(probeNormalize, rpc.WithCanonicalValue(probeEncode))},
		WithCorroboration(shared), WithPlans(nil))
	decision, aggErr := planAgg.Aggregate(context.Background(), rpc.Request{Method: "eth_other"})
	if aggErr != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("legacy path broken beside plans: %+v %v", decision, aggErr)
	}
	// An unknown method still forwards transparently: no plan, no policy.
	dead := fixedPool(t, pool.Member{Name: "dead", URL: "http://127.0.0.1:1"})
	forwardAgg := New(dead, nil, WithCorroboration(shared), WithReferenceStage(&scriptedStage{}))
	_, forwardErr := forwardAgg.Aggregate(context.Background(), rpc.Request{Method: "eth_unregistered"})
	if forwardErr == nil {
		t.Fatal("unregistered method should not silently corroborate")
	}
}

func mustShared(t *testing.T) *rpc.CorroborationShared {
	t.Helper()
	shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 2)
	if err != nil {
		t.Fatal(err)
	}
	return shared
}
