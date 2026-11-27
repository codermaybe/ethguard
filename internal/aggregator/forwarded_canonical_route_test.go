package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/projection"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

type forwardedCanonicalFixture struct {
	req rpc.Request
	raw json.RawMessage
}

func forwardedCanonicalFixtures(tb testing.TB) []forwardedCanonicalFixture {
	tb.Helper()
	tx, hash, blockHash, index := minedTransactionVector(tb)
	receipt := receiptRouteFixture(tb)
	return []forwardedCanonicalFixture{
		{rpc.Request{Method: methods.MethodGetTransactionByHash, Params: []any{hash}}, tx},
		{rpc.Request{Method: methods.MethodGetTransactionByBlockHashAndIndex, Params: []any{blockHash, index}}, tx},
		{receiptRouteRequest(receiptRouteHash(tb, receipt)), receipt},
		{blockReceiptsRouteRequest(tb, receipt), blockReceiptsRouteArray(tb, receipt)},
	}
}

func forwardedCanonicalNodes(tb testing.TB, method string, raw json.RawMessage) ([]pool.Member, []*rpctest.Node) {
	tb.Helper()
	members := make([]pool.Member, 3)
	nodes := make([]*rpctest.Node, 3)
	for i := range nodes {
		nodes[i] = rpctest.NewNode(0)
		nodes[i].ReturnRaw(method, raw)
		tb.Cleanup(nodes[i].Close)
		members[i] = pool.Member{Name: fmt.Sprintf("forwarded-%d", i), URL: nodes[i].URL()}
	}
	return members, nodes
}

type forwardedCanonicalRun struct {
	receiptRouteRun
	event observe.DecisionEvent
}

func forwardedCanonicalAggregate(t *testing.T, ctx context.Context, plan rpcengine.ReadPlan, members []pool.Member, req rpc.Request, capture bool) forwardedCanonicalRun {
	t.Helper()
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	shared, err := rpcengine.NewCorroboration(rpc.CorroborationForwarded, 0)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &captureRecorder{}
	opts := []Option{WithRecorder(recorder), WithPlans(map[string]rpcengine.ReadPlan{req.Method: plan})}
	var projector *recordingEvaluationProjector
	if plan.ProjectorID != "" {
		// The runtime binds both transaction methods to the by-hash schema.
		schemaMethod := req.Method
		if plan.ProjectorID == methods.ProjectorTransaction {
			schemaMethod = methods.MethodGetTransactionByHash
		}
		delegate, err := projection.NewSchemaProjector(schemaMethod)
		if err != nil {
			t.Fatal(err)
		}
		projector = &recordingEvaluationProjector{delegate: delegate}
		opts = append(opts, WithProjectors(map[string]rpcengine.ObservationProjector{plan.ProjectorID: projector}))
	}
	collector := uncertain.NewCollector(uncertain.Config{BudgetBytes: 1 << 20})
	if capture {
		ctx = uncertain.WithCollector(ctx, collector)
	}
	d, err := New(p, shared, opts...).Aggregate(ctx, req)
	if projector != nil && projector.calls != 0 {
		t.Fatalf("Forwarded unexpectedly projected field evidence %d times", projector.calls)
	}
	event := oneEvent(t, recorder)
	if event.Faults != len(d.Faults) {
		t.Fatalf("event faults=%d, decision faults=%d", event.Faults, len(d.Faults))
	}
	event.Duration = 0
	for i := range event.Attempts {
		event.Attempts[i].RTT = 0
	}
	run := forwardedCanonicalRun{receiptRouteRun: receiptRouteRun{decision: d, err: err, raw: collector.Snapshot().Raw, health: map[string]receiptRouteHealth{}}, event: event}
	for _, src := range p.VotingMembers() {
		h := src.Health()
		lastError := ""
		if h.LastError != nil {
			lastError = h.LastError.Error()
		}
		run.health[src.ID()] = receiptRouteHealth{Successes: h.Successes, Failures: h.Failures, ConsecutiveFailures: h.ConsecutiveFailures, LastError: lastError}
	}
	return run
}

func assertForwardedCanonicalRequests(t *testing.T, nodes []*rpctest.Node, req rpc.Request, firstCalls int) {
	t.Helper()
	want, err := json.Marshal(req.Params)
	if err != nil {
		t.Fatal(err)
	}
	for i, node := range nodes {
		calls := node.Calls()
		count := 0
		if i == 0 {
			count = firstCalls
		}
		if len(calls) != count {
			t.Fatalf("source %d calls=%d, want %d", i, len(calls), count)
		}
		for _, call := range calls {
			params, err := json.Marshal(call.Params)
			if err != nil || call.Method != req.Method || !bytes.Equal(params, want) {
				t.Fatalf("wire request=%s %s, want %s %s: %v", call.Method, params, req.Method, want, err)
			}
		}
	}
}

func forwardedCanonicalExecute(t *testing.T, ctx context.Context, fixture forwardedCanonicalFixture, capture bool, requests int) forwardedCanonicalRun {
	t.Helper()
	members, nodes := forwardedCanonicalNodes(t, fixture.req.Method, fixture.raw)
	counts := &workCounters{}
	candidatePlan := instrumentedPlan(t, fixture.req.Method, counts)
	candidate := forwardedCanonicalAggregate(t, ctx, candidatePlan, members, fixture.req, capture)
	assertForwardedCanonicalRequests(t, nodes, fixture.req, requests)
	if requests == 1 {
		// An admission exit keeps the invocation's fixed majority bound; only
		// completed Forwarded adjudication states the one-source requirement.
		wantRequired := 2
		if candidate.err == nil {
			wantRequired = 1
		}
		if candidate.decision.Adjudication.Basis != 3 || candidate.decision.Adjudication.Required != wantRequired || len(candidate.decision.Sources) != 3 || len(candidate.event.Attempts) != 1 {
			t.Fatalf("Forwarded changed its three-member basis or one-attempt contract: %+v / %+v", candidate.decision, candidate.event)
		}
		for _, source := range candidate.decision.Sources {
			if source.Source.Name == "forwarded-0" {
				continue
			}
			if source.Attempted || source.Relation != rpc.VerdictAbsent || source.Exchange.Progress != rpc.ExchangeNotStarted || source.Answer.Interpretation.State != rpc.InterpretationUnassessed {
				t.Fatalf("unvisited source was reinterpreted: %+v", source)
			}
		}
	}
	// Forwarded uses the plan's declared processing contract.
	canonical := candidatePlan.Canonical != nil
	if requests > 0 && canonical && counts.process.Load() != 1 {
		t.Fatalf("Forwarded processor calls=%d, want one", counts.process.Load())
	}
	if canonical && (counts.admit.Load() != 0 || counts.encode.Load() != 0 || counts.normalize.Load() != 0) {
		t.Fatalf("canonical Forwarded reran raw hooks: admit=%d encode=%d normalize=%d", counts.admit.Load(), counts.encode.Load(), counts.normalize.Load())
	}
	if candidate.decision.Extensions != nil || candidate.decision.Diagnostics != nil {
		t.Fatal("Forwarded gained agreement evidence")
	}

	if candidate.decision.Reference != nil || candidate.decision.Revert != nil || len(candidate.decision.Dimensions) != 0 || len(candidate.decision.DeletedFields) != 0 {
		t.Fatalf("object read gained unrelated evidence: %+v", candidate.decision)
	}
	wantEvent := "forwarded"
	if candidate.err != nil {
		wantEvent = "failed"
	}
	if candidate.event.Status != wantEvent || candidate.event.Method != fixture.req.Method || candidate.event.Conflict {
		t.Fatalf("event = %+v, want status %s", candidate.event, wantEvent)
	}
	if !capture && len(candidate.raw) != 0 {
		t.Fatal("disabled capture retained material")
	}
	delivered := 0
	for _, raw := range candidate.raw {
		if len(raw.Value) == 0 {
			continue
		}
		delivered++
		if raw.Stage != "response" || raw.Source.Name != "forwarded-0" || raw.Method != fixture.req.Method || !bytes.Equal(bytes.TrimSpace(raw.Value), bytes.TrimSpace(fixture.raw)) || raw.Truncated {
			t.Fatalf("capture lost original source value: %+v", raw)
		}
	}
	wantDelivered := 0
	if capture {
		wantDelivered = requests
	}
	if delivered != wantDelivered {
		t.Fatalf("captured delivered answers=%d, want %d", delivered, wantDelivered)
	}
	var successes, failures uint64
	for _, health := range candidate.health {
		successes += health.Successes
		failures += health.Failures
	}
	wantSuccess, wantFailure := uint64(0), uint64(0)
	if requests == 1 {
		if candidate.err == nil {
			wantSuccess = 1
		} else {
			wantFailure = 1
		}
	}
	if successes != wantSuccess || failures != wantFailure {
		t.Fatalf("health = %d successes/%d failures, want %d/%d", successes, failures, wantSuccess, wantFailure)
	}
	if len(candidate.event.Attempts) != requests {
		t.Fatalf("attempts = %+v", candidate.event.Attempts)
	}
	if requests == 1 && (candidate.event.Attempts[0].Source != "forwarded-0" || candidate.event.Attempts[0].Failed != (candidate.err != nil)) {
		t.Fatalf("attempt attribution = %+v", candidate.event.Attempts)
	}
	return candidate
}

func TestForwardedCanonicalRegisteredRoutes(t *testing.T) {
	for _, fixture := range forwardedCanonicalFixtures(t) {
		t.Run(fixture.req.Method, func(t *testing.T) {
			for _, capture := range []bool{false, true} {
				for _, variant := range []string{"success", "extensions", "null", "malformed", "binding"} {
					t.Run(fmt.Sprintf("%s/capture=%t", variant, capture), func(t *testing.T) {
						tc := fixture
						want := error(nil)
						switch variant {
						case "extensions":
							if tc.req.Method == methods.MethodGetBlockReceipts {
								var rows []json.RawMessage
								if err := json.Unmarshal(tc.raw, &rows); err != nil || len(rows) == 0 {
									t.Fatalf("fixture list: %v", err)
								}
								rows[0] = receiptRouteReplace(t, rows[0], "vendor", `{"fee":"0x1"}`)
								tc.raw = blockReceiptsRouteArray(t, rows...)
							} else {
								tc.raw = receiptRouteReplace(t, tc.raw, "vendor", `{"fee":"0x1"}`)
							}
						case "null":
							tc.raw = json.RawMessage(`null`)
						case "malformed":
							tc.raw, want = json.RawMessage(`{}`), rpc.ErrMalformedResponse
						case "binding":
							tc.req.Params = append([]any(nil), fixture.req.Params...)
							tc.req.Params[0] = "0x" + strings.Repeat("22", 32)
							want = rpc.ErrMalformedResponse
						}
						run := forwardedCanonicalExecute(t, context.Background(), tc, capture, 1)
						if !errors.Is(run.err, want) || (want == nil && run.decision.Status != rpc.Forwarded) {
							t.Fatalf("outcome=%+v, err=%v, want %v", run.decision, run.err, want)
						}
					})
				}
			}
		})
	}
}

func TestForwardedCanonicalCancellationAndPendingRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, fixture := range forwardedCanonicalFixtures(t) {
		t.Run(fixture.req.Method, func(t *testing.T) {
			run := forwardedCanonicalExecute(t, ctx, fixture, true, 0)
			if !errors.Is(run.err, context.Canceled) {
				t.Fatalf("canceled = %v", run.err)
			}
		})
	}
	fixture := forwardedCanonicalFixtures(t)[3]
	fixture.req.Params = []any{"pending"}
	run := forwardedCanonicalExecute(t, context.Background(), fixture, true, 0)
	if !errors.Is(run.err, rpc.ErrInvalidRequest) {
		t.Fatalf("pending block receipts = %v", run.err)
	}
	fixture.req.Params = []any{"latest"}
	run = forwardedCanonicalExecute(t, context.Background(), fixture, true, 0)
	if !errors.Is(run.err, rpc.ErrReferenceRequired) {
		t.Fatalf("moving tag bypassed reference establishment: %v", run.err)
	}
}

func TestForwardedCanonicalPendingTransaction(t *testing.T) {
	data, err := os.ReadFile("../methods/testdata/transaction/anvil-1.7.1-pending.json")
	if err != nil {
		t.Fatal(err)
	}
	var capture struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(capture.Response), &response); err != nil {
		t.Fatal(err)
	}
	var tx struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(response.Result, &tx); err != nil || tx.Hash == "" {
		t.Fatalf("pending capture identity: %v", err)
	}
	fixture := forwardedCanonicalFixture{rpc.Request{Method: methods.MethodGetTransactionByHash, Params: []any{tx.Hash}}, response.Result}
	run := forwardedCanonicalExecute(t, context.Background(), fixture, true, 1)
	if run.err != nil || run.decision.Status != rpc.Forwarded {
		t.Fatalf("pending by-hash = %+v, %v", run.decision, run.err)
	}
	fixture.req = forwardedCanonicalFixtures(t)[1].req
	run = forwardedCanonicalExecute(t, context.Background(), fixture, true, 1)
	if !errors.Is(run.err, rpc.ErrMalformedResponse) {
		t.Fatalf("pending transaction in a mined block = %v", run.err)
	}
}

func TestForwardedCanonicalInternalContractFault(t *testing.T) {
	for _, withResult := range []bool{false, true} {
		t.Run(fmt.Sprintf("result-beside-error=%t", withResult), func(t *testing.T) {
			fixture := forwardedCanonicalFixtures(t)[2]
			counts := &workCounters{}
			plan := instrumentedPlan(t, fixture.req.Method, counts)
			plan.Canonical.Process = func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
				counts.process.Add(1)
				if withResult {
					result, err := rpcengine.NewCanonicalResult(json.RawMessage(`null`), nil)
					if err != nil {
						return rpcengine.CanonicalResult{}, err
					}
					return result, rpc.ErrMalformedResponse
				}
				return rpcengine.CanonicalResult{}, nil
			}
			members, nodes := forwardedCanonicalNodes(t, fixture.req.Method, fixture.raw)
			run := forwardedCanonicalAggregate(t, context.Background(), plan, members, fixture.req, true)
			assertForwardedCanonicalRequests(t, nodes, fixture.req, 1)
			if !errors.Is(run.err, rpc.ErrInvalidCanonicalResult) || run.decision.Status != rpc.Unknown || len(run.decision.Value) != 0 || len(run.decision.SourceFaults) != 0 || len(run.decision.Faults) != 1 || run.event.Faults != 1 {
				t.Fatalf("processor defect outcome=%+v, err=%v, event=%+v", run.decision, run.err, run.event)
			}
			if counts.process.Load() != 1 || counts.admit.Load() != 0 || counts.encode.Load() != 0 || counts.normalize.Load() != 0 {
				t.Fatalf("processor defect reran hooks: process=%d admit=%d encode=%d normalize=%d", counts.process.Load(), counts.admit.Load(), counts.encode.Load(), counts.normalize.Load())
			}
			for _, h := range run.health {
				if h.Failures != 0 || h.ConsecutiveFailures != 0 || h.LastError != "" {
					t.Fatalf("internal defect blamed provider: %+v", run.health)
				}
			}
			if len(run.event.Attempts) != 1 || run.event.Attempts[0].Failed {
				t.Fatalf("internal defect blamed provider event: %+v", run.event)
			}
		})
	}
}

func TestForwardedCanonicalProtocolAnswer(t *testing.T) {
	for _, fixture := range forwardedCanonicalFixtures(t) {
		t.Run(fixture.req.Method, func(t *testing.T) {
			members, nodes := forwardedCanonicalNodes(t, fixture.req.Method, fixture.raw)
			nodes[0].FailWithData(fixture.req.Method, -32000, "not available", json.RawMessage(`{"detail":1}`), true)
			counts := &workCounters{}
			plan := instrumentedPlan(t, fixture.req.Method, counts)
			candidate := forwardedCanonicalAggregate(t, context.Background(), plan, members, fixture.req, true)
			if candidate.decision.Status != rpc.Unknown || candidate.err == nil {
				t.Fatalf("protocol error = %+v, %v", candidate.decision, candidate.err)
			}
			assertForwardedCanonicalRequests(t, nodes, fixture.req, 1)
			if counts.process.Load() != 0 || counts.admit.Load() != 0 || counts.encode.Load() != 0 || counts.normalize.Load() != 0 {
				t.Fatal("protocol error entered a result processor")
			}
			if candidate.event.Status != "failed" || len(candidate.event.Attempts) != 1 || candidate.event.Attempts[0].Failed || len(candidate.decision.SourceFaults) != 1 {
				t.Fatalf("protocol failure attribution = %+v / %+v", candidate.decision, candidate.event)
			}
			var successes, failures uint64
			for _, h := range candidate.health {
				successes += h.Successes
				failures += h.Failures
			}
			if successes != 0 || failures != 1 {
				t.Fatalf("RPC error health attribution: %+v", candidate.health)
			}
			found := false
			for _, raw := range candidate.raw {
				if raw.Stage == "response" && raw.Source.Name == "forwarded-0" {
					found = true
					if raw.Error != "not available" || len(raw.Value) != 0 {
						t.Fatalf("RPC error material = %+v", raw)
					}
				}
			}
			if !found {
				t.Fatal("RPC error source material missing")
			}

		})
	}
}

func BenchmarkForwardedCanonicalAggregate(b *testing.B) {
	for _, fixture := range forwardedCanonicalFixtures(b) {
		b.Run(fixture.req.Method, func(b *testing.B) {
			{
				name := "canonical"
				b.Run(name, func(b *testing.B) {
					counts := &workCounters{}
					plan := instrumentedPlan(b, fixture.req.Method, counts)

					members, nodes := forwardedCanonicalNodes(b, fixture.req.Method, fixture.raw)
					var calls [3]atomic.Int64
					for i, node := range nodes {
						node.Handle(fixture.req.Method, func(rpctest.Call) (any, error) {
							calls[i].Add(1)
							return fixture.raw, nil
						})
					}
					p := receiptRouteBenchPool(b, members)
					shared, err := rpcengine.NewCorroboration(rpc.CorroborationForwarded, 0)
					if err != nil {
						b.Fatal(err)
					}
					agg := New(p, shared, WithPlans(map[string]rpcengine.ReadPlan{fixture.req.Method: plan}))
					if d, err := agg.Aggregate(context.Background(), fixture.req); err != nil || d.Status != rpc.Forwarded || len(d.Support) != 1 {
						b.Fatalf("warm-up: %+v, %v", d, err)
					}
					for i := range calls {
						calls[i].Store(0)
					}
					counts.reset()
					b.ReportAllocs()
					b.SetBytes(int64(len(fixture.raw)))
					b.ResetTimer()
					for range b.N {
						if d, err := agg.Aggregate(context.Background(), fixture.req); err != nil || d.Status != rpc.Forwarded || len(d.Support) != 1 {
							b.Fatalf("aggregate: %+v, %v", d, err)
						}
					}
					b.StopTimer()
					if calls[0].Load() != int64(b.N) || calls[1].Load() != 0 || calls[2].Load() != 0 {
						b.Fatalf("measured requests = %d/%d/%d, want %d/0/0", calls[0].Load(), calls[1].Load(), calls[2].Load(), b.N)
					}
					b.ReportMetric(float64(calls[0].Load())/float64(b.N), "requests/op")
					b.ReportMetric(float64(counts.process.Load())/float64(b.N), "process/op")
					b.ReportMetric(float64(counts.admit.Load())/float64(b.N), "admit/op")
					b.ReportMetric(float64(counts.encode.Load())/float64(b.N), "encode/op")
				})
			}
		})
	}
}
