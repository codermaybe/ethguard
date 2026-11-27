package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	"github.com/codermaybe/ethguard/internal/pool"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// decoratedOutcomePolicy forwards the mandatory Outcome and corroboration
// contract through a decorator without reinterpreting source responses.
type decoratedOutcomePolicy struct{ rpcengine.Resolver }

func (p decoratedOutcomePolicy) ResolveCorroborated(c rpcengine.Corroboration, obs []rpc.Observation) (rpcengine.Outcome, error) {
	return p.Resolver.(rpcengine.CorroboratingResolver).ResolveCorroborated(c, obs)
}

// quantityStrict is a plain strict policy over hex-quantity strings.
func quantityStrict(opts ...rpcengine.StrictOption) rpcengine.Resolver {
	return rpcengine.Strict(func(v json.RawMessage) (string, error) {
		var s string
		err := json.Unmarshal(v, &s)
		return s, err
	}, opts...)
}

// Direct and decorated resolvers preserve the same public evidence and
// per-source events across support, dissent and method-domain exclusion.
func TestDecoratedMandatoryOutcomeParity(t *testing.T) {
	corrob := mustCorroboration(t, rpc.CorroborationMajority, 2)
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		nodes[i].Return("eth_chainId", "0x64")
	}
	nodes[3].Return("eth_chainId", "0x65") // dissent: loses the cluster
	nodes[4].Fail("eth_chainId", -32000, "boom")

	for _, variant := range []struct {
		name      string
		decorated bool
	}{
		{name: "direct resolver"},
		{name: "decorated resolver", decorated: true},
	} {
		t.Run(variant.name, func(t *testing.T) {
			members := make([]pool.Member, 0, len(nodes))
			for i, node := range nodes {
				members = append(members, pool.Member{Name: fmt.Sprintf("node-%d", i), URL: node.URL()})
			}
			p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
			policy := quantityStrict(rpcengine.WithQuorum(2))
			if variant.decorated {
				policy = decoratedOutcomePolicy{Resolver: policy}
			}
			rec := &captureRecorder{}
			aggregate := New(p, corrob, WithPlans(map[string]rpcengine.ReadPlan{"eth_chainId": {Method: "eth_chainId", Resolver: policy}}), WithRecorder(rec), WithCollectTimeout(2*time.Second))
			decision, err := aggregate.Aggregate(context.Background(), rpc.Request{Method: "eth_chainId"})
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			if decision.Status != rpc.Corroborated {
				t.Fatalf("status = %s, want corroborated", decision.Status)
			}
			if len(decision.Support) != 3 || len(decision.Reject) != 1 {
				t.Fatalf("support %v reject %v, want three supporters and one dissenter", decision.Support, decision.Reject)
			}
			if len(rec.events) != 1 {
				t.Fatalf("expected one decision event, got %d", len(rec.events))
			}
			attempts := rec.events[0].Attempts
			if len(attempts) != 5 {
				t.Fatalf("expected five attempted sources, got %+v", attempts)
			}
			failed := map[string]bool{}
			for _, attempt := range attempts {
				failed[attempt.Source] = attempt.Failed
			}
			for _, name := range []string{"node-0", "node-1", "node-2"} {
				if failed[name] {
					t.Fatalf("%s supported but was marked failed", name)
				}
			}
			if failed["node-3"] {
				t.Fatal("a valid dissenting answer must not count as failed")
			}
			if failed["node-4"] {
				t.Fatal("a legal protocol answer excluded by this policy must not blame the source")
			}
		})
	}
}

// Scheduling and communication progress are independent facts.
func TestRoundSeparatesDialFailureFromSchedulingAbsence(t *testing.T) {
	sources := namedSources(t, "dial", "gated")
	dialErr := errors.New("dial failed")
	emissions := []pool.Emission{
		{Src: sources[0], Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Cause: dialErr}}},
		{Src: sources[1], Response: rpc.Response{Exchange: rpc.Exchange{Cause: pool.ErrSourceGated}}},
	}
	outcome := RoundOutcome(emissions)
	if outcome.Verdicts[0].Kind != rpc.VerdictUnusable || outcome.Verdicts[1].Kind != rpc.VerdictAbsent {
		t.Fatalf("relations: %+v", outcome.Verdicts)
	}
	if !outcome.Decision.Sources[0].Attempted || outcome.Decision.Sources[1].Attempted {
		t.Fatalf("attempts: %+v", outcome.Decision.Sources)
	}
	if len(outcome.Decision.SourceFaults) != 2 || len(outcome.Decision.Faults) != 1 {
		t.Fatalf("faults: %+v", outcome.Decision)
	}
}

// When verdicts are stated, the observability seam consumes them instead of
// re-running the policy's normalizer: a second Normalize on the resolve path
// is exactly the double semantics the outcome statement exists to remove.
func TestStatedVerdictsSkipSecondNormalize(t *testing.T) {
	normalizeCalls := 0
	policy := rpcengine.Strict(func(v json.RawMessage) (string, error) {
		normalizeCalls++
		var s string
		err := json.Unmarshal(v, &s)
		return s, err
	})
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())})
	rec := &captureRecorder{}
	a := New(p, rpcengine.Corroboration{Floor: 1}, WithRecorder(rec))
	sources := namedSources(t, "a")
	obs := []rpc.Observation{
		{Attempted: true, Source: rpc.SourceRef{Name: sources[0].Name(), ID: sources[0].ID()}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"1"`)}}},
	}
	outcome, err := policy.Resolve(obs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(outcome.Verdicts) != 1 || outcome.Verdicts[0].Kind != rpc.VerdictSupported {
		t.Fatalf("verdicts = %+v, want the delivered answer supported", outcome.Verdicts)
	}
	before := normalizeCalls
	a.recordDecision(time.Now(), "m", rpc.Decision{Status: rpc.Unknown}, errors.New("failed"),
		[]pool.Emission{{Src: sources[0], Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"1"`)}}}}, outcome.Verdicts)
	if normalizeCalls != before {
		t.Fatalf("observation classification re-ran Normalize %d times", normalizeCalls-before)
	}
	if len(rec.events) != 1 || len(rec.events[0].Attempts) != 1 || rec.events[0].Attempts[0].Failed {
		t.Fatalf("a delivered usable answer must not count as failed: %+v", rec.events)
	}
}

func mustCorroboration(t *testing.T, mode rpc.CorroborationMode, floor int) rpcengine.Corroboration {
	t.Helper()
	shared, err := rpcengine.NewCorroboration(mode, floor)
	if err != nil {
		t.Fatal(err)
	}
	return shared
}

func TestSourceFaultsAssembledOnceFromResolverJudgments(t *testing.T) {
	sources := namedSources(t, "normalize", "transport", "good")
	badMethod, network := errors.New("normalize failed"), errors.New("connection interrupted")
	calls := 0
	policy := rpcengine.Strict(func(raw json.RawMessage) (string, error) {
		calls++
		if string(raw) == `"bad"` {
			return "", badMethod
		}
		return string(raw), nil
	})
	emissions := []pool.Emission{
		{Src: sources[0], Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"bad"`)}}},
		{Src: sources[1], Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: network}}},
		{Src: sources[2], Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"good"`)}}},
	}
	obs := make([]rpc.Observation, len(emissions))
	for i, emission := range emissions {
		obs[i] = ObservationFromEmission(emission)
	}
	outcome, err := policy.Resolve(obs)
	if err != nil {
		t.Fatal(err)
	}
	state := invocationState{emissions: emissions, verdicts: outcome.Verdicts}
	state.attachFaults(&outcome.Decision)
	if calls != 2 || len(outcome.Decision.SourceFaults) != 2 || len(outcome.Decision.Faults) != 2 {
		t.Fatalf("reclassified or duplicated faults: calls=%d %+v", calls, outcome.Decision)
	}
	for i, cause := range []error{badMethod, network} {
		count := 0
		for _, fault := range outcome.Decision.SourceFaults {
			if fault.Source.ID == sources[i].ID() && errors.Is(fault.Err, cause) {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("source %d cause %v counted %d times", i, cause, count)
		}
		count = 0
		for _, fault := range outcome.Decision.Faults {
			if errors.Is(fault, cause) {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("human fault %v counted %d times", cause, count)
		}
	}
}

// A custom resolver owns method judgments but need not pre-render the public
// source/fault views. The coordinator must project them without parsing again.
type judgmentOnlyPolicy struct {
	rpcengine.Resolver
	cause error
	calls *int
}

func (p judgmentOnlyPolicy) Resolve(obs []rpc.Observation) (rpcengine.Outcome, error) {
	*p.calls++
	return rpcengine.Outcome{
		Decision: rpc.Decision{Status: rpc.Selected, Value: obs[2].Response.Answer.Result,
			Support: []rpc.SourceRef{obs[2].Source},
			Adjudication: rpc.Adjudication{Basis: len(obs), Required: 1, Reason: rpc.ReasonAccepted,
				Candidates: []rpc.CandidateGroup{{Sources: []rpc.SourceRef{obs[2].Source}}}}},
		Verdicts: []rpcengine.SourceVerdict{
			{Source: obs[0].Source, Kind: rpc.VerdictUnusable, Method: rpc.Interpretation{State: rpc.InterpretationRejected, Cause: p.cause}},
			{Source: obs[1].Source, Kind: rpc.VerdictUnusable},
			{Source: obs[2].Source, Kind: rpc.VerdictSupported, Method: rpc.Interpretation{State: rpc.InterpretationValidated}},
		},
	}, nil
}

func TestCustomOutcomeGetsIdempotentSourceProjection(t *testing.T) {
	sources := namedSources(t, "method", "transport", "good")
	methodErr, networkErr := errors.New("bad method answer"), errors.New("dial failed")
	emissions := []pool.Emission{
		{Src: sources[0], Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"bad"`)}}},
		{Src: sources[1], Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Cause: networkErr}}},
		{Src: sources[2], Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"ok"`)}}},
	}
	calls := 0
	policy := judgmentOnlyPolicy{Resolver: rpcengine.ForwardResolver{}, cause: methodErr, calls: &calls}
	state := invocationState{plan: &rpcengine.ReadPlan{Resolver: policy}, emissions: emissions,
		invocation: beginInvocation(rpcengine.Corroboration{}, sources, nil, false)}
	for _, emission := range emissions {
		state.observations = append(state.observations, ObservationFromEmission(emission))
	}
	decision, err := state.resolveValue()
	if err != nil || calls != 1 || len(decision.Sources) != 3 || len(decision.Faults) != 2 || len(decision.SourceFaults) != 2 {
		t.Fatalf("custom outcome not projected: %+v, %v, calls=%d", decision, err, calls)
	}
	outcome := rpcengine.Outcome{Decision: decision, Verdicts: state.verdicts}
	outcome.ProjectSources(state.observations)
	outcome.ProjectSources(state.observations)
	if len(outcome.Decision.Faults) != 2 || len(outcome.Decision.SourceFaults) != 2 || calls != 1 {
		t.Fatalf("projection duplicated or re-resolved: %+v", outcome.Decision)
	}
	for i, cause := range []error{methodErr, networkErr} {
		if outcome.Decision.SourceFaults[i].Source.ID != sources[i].ID() || !errors.Is(outcome.Decision.SourceFaults[i].Err, cause) || !errors.Is(outcome.Decision.Faults[i], cause) {
			t.Fatalf("cause/source lost: %+v", outcome.Decision)
		}
	}
}
