package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

// The invocation's basis is frozen at entry: later health or scheduling state
// cannot fold a new member into the denominator, and the aggregator's phases
// all run on that one frozen value.
func TestInvocationBasisStaysFrozenAcrossRounds(t *testing.T) {
	sources := namedSources(t, "a", "b", "c")
	snap := rpcengine.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	state := &invocationState{
		snap:   snap,
		snapOK: true,
	}
	state.invocation = beginInvocation(snap, true, sources, nil, false)

	basis := state.invocation.Basis()
	if basis.Size() != 3 {
		t.Fatalf("basis size = %d, want the three captured members", basis.Size())
	}
	if basis.Required() != 2 {
		t.Fatalf("basis required = %d, want the frozen threshold", basis.Required())
	}
	seen := map[string]bool{}
	for _, ref := range basis.Members {
		seen[ref.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("basis members = %v, want exactly the captured three", seen)
	}
	if state.invocation.ID() == "" {
		t.Fatal("the invocation lost its identity")
	}
}

func TestAdmissionDoesNotRejectCanceledOrUnscheduledSources(t *testing.T) {
	plan := rpcengine.ReadPlan{Admit: func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error) {
		t.Fatal("admission ran without an available response")
		return nil, nil
	}}
	state := &invocationState{ctx: context.Background(), plan: &plan, emissions: []pool.Emission{
		{Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeNotStarted, Cause: context.Canceled}}},
		{Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: context.DeadlineExceeded}}},
	}}
	(&Aggregator{}).admitPhase(state)
	for _, emission := range state.emissions {
		if emission.Response.Answer.Interpretation.State != rpc.InterpretationUnassessed || emission.Response.Answer.Interpretation.Cause != nil {
			t.Fatalf("absence became method rejection: %+v", emission.Response)
		}
	}
}

func TestCanonicalInternalFailureRetainsProviderAnswer(t *testing.T) {
	response := rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`null`)}}
	state := &invocationState{ctx: context.Background(), emissions: []pool.Emission{{Attempted: true, Response: response}},
		processorRoute: func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
			return rpcengine.CanonicalResult{}, rpc.ErrInvalidCanonicalResult
		},
	}
	(&Aggregator{}).admitPhase(state)
	got := state.emissions[0].Response
	if !errors.Is(state.internalFault, rpc.ErrInvalidCanonicalResult) || got.Answer.Interpretation.State != rpc.InterpretationInternalFailure || got.Exchange != response.Exchange || string(got.Answer.Result) != "null" {
		t.Fatalf("internal failure lost attribution/facts: %+v, %v", got, state.internalFault)
	}
	if !attemptClassifier(nil)(state.emissions[0]) {
		t.Fatal("internal canonical failure blamed the provider")
	}
}

func TestResolutionRetainsItsThresholdWithoutSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		quorum, available, required int
		status                      rpc.Status
		err                         error
	}{
		{name: "classic partial response", available: 1, required: 1, status: rpc.Selected},
		{name: "constructor quorum", quorum: 3, available: 2, required: 3, status: rpc.Unknown, err: rpc.ErrInsufficientSupport},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sources := namedSources(t, "a", "b", "c")
			var opts []rpcengine.StrictOption
			if tc.quorum != 0 {
				opts = append(opts, rpcengine.WithQuorum(tc.quorum))
			}
			state := invocationState{plan: &rpcengine.ReadPlan{Resolver: rpcengine.Strict(rpc.CanonicalJSON, opts...)},
				invocation: beginInvocation(rpcengine.Corroboration{}, false, sources, nil, false)}
			for i, source := range sources {
				response := rpc.Response{Exchange: rpc.Exchange{Cause: errors.New("dial failed")}}
				if i < tc.available {
					response = rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`1`)}}
				}
				emission := pool.Emission{Src: source, Attempted: true, Response: response}
				state.emissions = append(state.emissions, emission)
				state.observations = append(state.observations, ObservationFromEmission(emission))
			}
			decision, err := state.resolveValue()
			if !errors.Is(err, tc.err) || decision.Status != tc.status || decision.Adjudication.Required != tc.required || decision.Adjudication.Basis != 3 {
				t.Fatalf("selected resolution contract overwritten: %+v, %v", decision, err)
			}
			if err == nil && (decision.Adjudication.Reason != rpc.ReasonAccepted || len(decision.Support) < decision.Adjudication.Required) {
				t.Fatalf("contradictory acceptance: %+v", decision)
			}
		})
	}
}

// Closing the pool after the invocation freezes membership is an operation
// failure. Every frozen member remains visible without an invented attempt.
func TestPoolCloseAfterFreezePreservesUnattemptedMembers(t *testing.T) {
	for _, route := range []string{"symmetric", "voting", "forward mode", "forward policy"} {
		t.Run(route, func(t *testing.T) {
			p := fixedPool(t,
				pool.Member{Name: "a", URL: "http://127.0.0.1:9100"},
				pool.Member{Name: "b", URL: "http://127.0.0.1:9101"},
			)
			members := p.VotingMembers()
			plan := rpcengine.ReadPlan{Method: "m", Resolver: rpcengine.Strict(rpc.CanonicalJSON), ResolverKind: rpcengine.ResolverStrict,
				Prepare: func(_ context.Context, req rpc.Request) (rpc.Request, error) {
					p.Close()
					return req, nil
				},
			}
			if route == "forward policy" {
				plan.Resolver = rpcengine.ForwardResolver{}
			}
			recorder := &captureRecorder{}
			opts := []Option{WithPlans(map[string]rpcengine.ReadPlan{"m": plan}), WithRecorder(recorder)}
			if route == "voting" || route == "forward mode" {
				mode, floor := rpc.CorroborationMajority, 2
				if route == "forward mode" {
					mode, floor = rpc.CorroborationForwarded, 0
				}
				opts = append(opts, WithCorroboration(mustCorroboration(t, mode, floor)))
			}
			decision, err := New(p, opts...).Aggregate(context.Background(), rpc.Request{Method: "m"})
			if !errors.Is(err, pool.ErrClosed) || errors.Is(err, rpc.ErrInvalidOutcome) || decision.Status != rpc.Unknown || decision.Adjudication.Reason != rpc.ReasonUnassessed {
				t.Fatalf("close became a resolver failure: %+v, %v", decision, err)
			}
			if decision.Adjudication.Basis != len(members) || len(decision.Sources) != len(members) || len(decision.Participants) != len(members) {
				t.Fatalf("frozen membership lost: %+v", decision)
			}
			for i, source := range decision.Sources {
				if source.Source != SourceRefOf(members[i]) || source.Attempted || source.Relation != rpc.VerdictAbsent || source.Exchange != (rpc.Exchange{}) || source.Answer != (rpc.AnswerSummary{}) {
					t.Fatalf("unvisited member acquired response facts: %+v", source)
				}
				if health := members[i].Health(); health.Successes != 0 || health.Failures != 0 {
					t.Fatalf("unattempted member accounted: %+v", health)
				}
			}
			if len(decision.SourceFaults) != 0 || len(decision.CollectionFaults) != 1 || !errors.Is(decision.CollectionFaults[0], pool.ErrClosed) || len(decision.Faults) != 1 || !errors.Is(decision.Faults[0], pool.ErrClosed) {
				t.Fatalf("collection error lost or assigned to sources: %+v", decision)
			}
			if len(recorder.events) != 1 || len(recorder.events[0].Attempts) != 0 || recorder.events[0].Faults != 1 {
				t.Fatalf("invented source attempts: %+v", recorder.events)
			}
		})
	}
}
