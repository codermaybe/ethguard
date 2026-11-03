package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The plan-execution fixtures. probeMethod is a synthetic default-block
// state read with the full reference and pending shape: hash anchored at
// position 1, the EIP-1898 object form at the same position, and pending
// observed node-locally. The plan is the only assembly under test.
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

func probeAggregator(t *testing.T, p *pool.Pool, stage *scriptedStage) *Aggregator {
	t.Helper()
	shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 2)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	return New(p,
		WithCorroboration(shared),
		WithCollectTimeout(2*time.Second),
		WithReferenceStage(stage),
		WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
}

// TestPlannedReadExecutesTheFleetFixture drives the probe method through
// the plan path over a scripted fleet and verifies the outcome directly:
// the decision corroborates, the prepared request is pinned to the
// established hash, establishment runs exactly once, and every source
// answered for the same rewritten request.
func TestPlannedReadExecutesTheFleetFixture(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 3)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated}}
	decision, aggErr := probeAggregator(t, p, stage).Aggregate(context.Background(),
		rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if aggErr != nil {
		t.Fatalf("planned read failed: %v", aggErr)
	}

	if decision.Status != rpc.Corroborated {
		t.Fatalf("status = %v, want Corroborated", decision.Status)
	}
	if string(decision.Value) != `"0x64"` {
		t.Fatalf("value = %s, want the fleet's unanimous answer", decision.Value)
	}
	if decision.BlockRef != "0xabc" {
		t.Fatalf("block pin = %q, want the established hash", decision.BlockRef)
	}
	if len(decision.Support) != 3 {
		t.Fatalf("support = %v, want the whole corroborating fleet", decision.Support)
	}
	if decision.Prepared.Params[1] != "0xabc" {
		t.Fatalf("prepared request = %v, want the established hash", decision.Prepared.Params)
	}
	// One establishment round per read.
	if len(stage.rounds) != 1 {
		t.Fatalf("establishment rounds = %d, want one", len(stage.rounds))
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
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
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
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
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
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
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
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
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
	agg := New(p, WithCorroboration(mustShared(t)), WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
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

// TestUnregisteredMethodForwardsBesidePlans pins the explicit forward
// branch beside installed plans: a method outside the registry rides the
// forward plan — one bounded attempt, no agreement claim — while the
// registered plans keep driving their own methods.
func TestUnregisteredMethodForwardsBesidePlans(t *testing.T) {
	a, b := rpctest.NewNode(1), rpctest.NewNode(1)
	defer a.Close()
	defer b.Close()
	a.Return(probeMethod, "0x64")
	b.Return("eth_other", "0x64")
	p := fixedPoolWith(t, 1, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())},
		pool.Member{Name: "a", URL: a.URL()},
		pool.Member{Name: "b", URL: b.URL()},
	)
	shared := mustShared(t)
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	agg := New(p, WithCorroboration(shared),
		WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "pending"}})
	if aggErr != nil || decision.Status != rpc.Selected {
		t.Fatalf("planned read broken beside the forward branch: %+v %v", decision, aggErr)
	}
	forwarded, forwardErr := agg.Aggregate(context.Background(), rpc.Request{Method: "eth_other"})
	if forwardErr == nil || forwarded.Status == rpc.Corroborated {
		t.Fatalf("unregistered method claimed agreement: %+v %v", forwarded, forwardErr)
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
