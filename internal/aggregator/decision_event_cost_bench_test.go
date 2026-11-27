package aggregator

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
)

// The event-assembly baseline: the cost of projecting one completed
// three-source aggregation into its observability event. The producer folds
// the resolution's own verdicts, so no answer is re-classified and no
// normalizer re-runs inside the measured loop — this is pure projection of
// delivered facts. The nop row is the monitoring-off state: the silent sink
// short-circuits before any assembly work, which is why wiring nothing costs
// nothing.
// eventBenchFleet builds three named sources whose members never receive
// traffic: the benchmark drives the event producer directly on a scripted
// resolution, not through a live aggregation.
func eventBenchFleet(b *testing.B) []*pool.Source {
	b.Helper()
	return fixedPool(b,
		pool.Member{Name: "a", URL: "http://127.0.0.1:1"},
		pool.Member{Name: "b", URL: "http://127.0.0.1:2"},
		pool.Member{Name: "c", URL: "http://127.0.0.1:3"},
	).VotingMembers()
}

// eventBenchResolution assembles the delivered facts one corroborated
// three-source resolution hands to the observability seam: the outcome's
// verdicts, the per-source emissions with their observed RTTs, and the
// decision those facts resolved to.
func eventBenchResolution(b *testing.B, sources []*pool.Source) (rpcengine.Resolver, rpc.Decision, []pool.Emission, []rpcengine.SourceVerdict) {
	b.Helper()
	policy := rpcengine.Strict(func(v json.RawMessage) (string, error) {
		var s string
		err := json.Unmarshal(v, &s)
		return s, err
	})
	observations := make([]rpc.Observation, 0, len(sources))
	emissions := make([]pool.Emission, 0, len(sources))
	for _, source := range sources {
		ref := rpc.SourceRef{Name: source.Name(), ID: source.ID()}
		observations = append(observations, rpc.Observation{Attempted: true, Source: ref, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x64"`)}}})
		emissions = append(emissions, pool.Emission{
			Src: source, Attempted: true,
			RTT: 5 * time.Millisecond, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x64"`)}},
		})
	}
	outcome, err := policy.Resolve(observations)
	if err != nil {
		b.Fatalf("Resolve: %v", err)
	}
	if len(outcome.Verdicts) != len(sources) {
		b.Fatalf("verdicts = %d, want one per source", len(outcome.Verdicts))
	}
	decision := rpc.Decision{
		Status:      rpc.Corroborated,
		Value:       json.RawMessage(`"0x64"`),
		Support:     []rpc.SourceRef{{Name: sources[0].Name(), ID: sources[0].ID()}, {Name: sources[1].Name(), ID: sources[1].ID()}, {Name: sources[2].Name(), ID: sources[2].ID()}},
		Reference:   &rpc.ReferenceEvidence{Hash: "0x1111111111111111111111111111111111111111111111111111111111111111", Status: rpc.Corroborated},
		Diagnostics: &rpc.FieldDiagnostics{},
	}
	return policy, decision, emissions, outcome.Verdicts
}

func benchmarkDecisionEventAssembly(b *testing.B, rec observe.Recorder) {
	sources := eventBenchFleet(b)
	_, decision, emissions, verdicts := eventBenchResolution(b, sources)
	agg := New(fixedPool(b), WithRecorder(rec))
	started := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agg.recordDecision(started, "eth_getBalance", decision, nil, emissions, verdicts)
	}
}

// BenchmarkDecisionEventAssembly measures the event projection a wired
// recorder receives for every completed aggregation.
func BenchmarkDecisionEventAssembly(b *testing.B) {
	benchmarkDecisionEventAssembly(b, &eventBenchRecorder{})
}

// BenchmarkDecisionEventAssemblySilent is the monitoring-off state: the
// default recorder discards before any assembly, so the measured cost is the
// whole price of not observing.
func BenchmarkDecisionEventAssemblySilent(b *testing.B) {
	benchmarkDecisionEventAssembly(b, observe.Nop)
}

// eventBenchRecorder accepts each event without storing it, so the measured
// cost includes the assembly and the seam delivery but no unbounded growth.
type eventBenchRecorder struct{}

func (eventBenchRecorder) RecordDecision(observe.DecisionEvent) {}
