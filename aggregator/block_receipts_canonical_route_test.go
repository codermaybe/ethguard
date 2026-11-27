package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	objectreceipt "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/internal/transport"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpc/projection"
	"github.com/codermaybe/ethguard/rpctest"
)

const blockReceiptsRouteMethod = methods.MethodGetBlockReceipts

func blockReceiptsRouteArray(tb testing.TB, rows ...json.RawMessage) json.RawMessage {
	tb.Helper()
	if rows == nil {
		return json.RawMessage(`[]`)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

func blockReceiptsRouteRequest(tb testing.TB, receipt json.RawMessage) rpc.Request {
	tb.Helper()
	var fields struct {
		BlockHash string `json:"blockHash"`
	}
	if err := json.Unmarshal(receipt, &fields); err != nil || fields.BlockHash == "" {
		tb.Fatalf("fixture block hash: %v", err)
	}
	return rpc.Request{Method: blockReceiptsRouteMethod, Params: []any{fields.BlockHash}}
}

func blockReceiptsRoutePlan(tb testing.TB, processed bool) rpc.ReadPlan {
	tb.Helper()
	spec, ok := methods.SpecFor(blockReceiptsRouteMethod)
	if !ok {
		tb.Fatal("block receipts spec missing")
	}
	plan, err := methods.CompilePlan(spec)
	if err != nil {
		tb.Fatal(err)
	}
	if !processed {
		plan.ProcessCanonical = nil
	}
	return plan
}

func blockReceiptsRouteNodes(tb testing.TB, bodies []json.RawMessage) ([]pool.Member, []*rpctest.Node) {
	tb.Helper()
	members := make([]pool.Member, len(bodies))
	nodes := make([]*rpctest.Node, len(bodies))
	for i, body := range bodies {
		nodes[i] = rpctest.NewNode(0)
		nodes[i].ReturnRaw(blockReceiptsRouteMethod, body)
		tb.Cleanup(nodes[i].Close)
		members[i] = pool.Member{Name: fmt.Sprintf("block-receipts-%d", i), URL: nodes[i].URL()}
	}
	return members, nodes
}

func assertBlockReceiptsRouteRequests(t *testing.T, nodes []*rpctest.Node, req rpc.Request, perNode int) {
	t.Helper()
	want, err := json.Marshal(req.Params)
	if err != nil {
		t.Fatal(err)
	}
	for i, node := range nodes {
		calls := node.Calls()
		if len(calls) != perNode {
			t.Fatalf("source %d requests = %d, want %d", i, len(calls), perNode)
		}
		for _, call := range calls {
			params, err := json.Marshal(call.Params)
			if err != nil || call.Method != req.Method || !bytes.Equal(params, want) {
				t.Fatalf("source %d request = %s %s, want %s %s: %v", i, call.Method, params, req.Method, want, err)
			}
		}
	}
}

func blockReceiptsRouteProjector(tb testing.TB) rpc.ObservationProjector {
	tb.Helper()
	projector, err := projection.NewSchemaProjector(blockReceiptsRouteMethod)
	if err != nil {
		tb.Fatal(err)
	}
	return projector
}

type blockReceiptsRouteRun struct {
	receiptRouteRun
	evaluation *rpc.Evaluation
}

func blockReceiptsRouteAggregate(t *testing.T, plan rpc.ReadPlan, members []pool.Member, req rpc.Request) blockReceiptsRouteRun {
	t.Helper()
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 2)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &captureRecorder{}
	projector := &recordingEvaluationProjector{delegate: blockReceiptsRouteProjector(t)}
	agg := New(p, WithRecorder(recorder), WithPlans(map[string]rpc.ReadPlan{req.Method: plan}), WithCorroboration(shared),
		WithProjectors(map[string]rpc.ObservationProjector{methods.ProjectorBlockReceipts: projector}))
	collector := uncertain.NewCollector(uncertain.Config{BudgetBytes: 1 << 20})
	decision, err := agg.Aggregate(uncertain.WithCollector(context.Background(), collector), req)
	if event := oneEvent(t, recorder); event.Faults != len(decision.Faults) {
		t.Fatalf("event faults = %d, decision faults = %d", event.Faults, len(decision.Faults))
	}
	run := blockReceiptsRouteRun{receiptRouteRun: receiptRouteRun{decision: decision, err: err, raw: collector.Snapshot().Raw, health: map[string]receiptRouteHealth{}}}
	if projector.calls > 0 {
		run.evaluation = &projector.evaluation
	}
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

func blockReceiptsRouteCompare(t *testing.T, bodies []json.RawMessage, req rpc.Request) blockReceiptsRouteRun {
	t.Helper()
	members, nodes := blockReceiptsRouteNodes(t, bodies)
	baseline := blockReceiptsRouteAggregate(t, blockReceiptsRoutePlan(t, false), members, req)
	assertBlockReceiptsRouteRequests(t, nodes, req, 1)
	candidate := blockReceiptsRouteAggregate(t, blockReceiptsRoutePlan(t, true), members, req)
	assertBlockReceiptsRouteRequests(t, nodes, req, 2)
	compareReceiptRoutes(t, baseline.receiptRouteRun, candidate.receiptRouteRun)
	compareReceiptMaterial(t, baseline.raw, candidate.raw)
	compareReceiptHealth(t, baseline.health, candidate.health)
	if !reflect.DeepEqual(baseline.evaluation, candidate.evaluation) {
		t.Fatalf("full raw evaluation differs:\n%+v\n%+v", baseline.evaluation, candidate.evaluation)
	}
	if !bytes.Equal(jsonOf(t, baseline.decision.Sources), jsonOf(t, candidate.decision.Sources)) || !reflect.DeepEqual(baseline.decision.BlockRef, candidate.decision.BlockRef) {
		t.Fatal("source summaries or block pin changed")
	}
	return candidate
}

// Both plans use the registered method and real HTTP collection. Clearing only
// ProcessCanonical freezes the old route; all raw votes, faults, projection,
// retained material, source health and request counts are compared.
func TestBlockReceiptsCanonicalRoute(t *testing.T) {
	receipt := receiptRouteFixture(t)
	req := blockReceiptsRouteRequest(t, receipt)
	valid := blockReceiptsRouteArray(t, receipt)
	dissent := blockReceiptsRouteArray(t, receiptRouteReplace(t, receipt, "gasUsed", `"0x1"`))
	ordered := blockReceiptsRouteArray(t, receipt, receiptRouteReplace(t, receipt, "gasUsed", `"0x1"`))
	reversed := blockReceiptsRouteArray(t, receiptRouteReplace(t, receipt, "gasUsed", `"0x1"`), receipt)
	bad := blockReceiptsRouteArray(t, receiptRouteDelete(t, receipt, "logs"))
	foreign := receiptRouteReplace(t, receipt, "blockHash", `"0x`+strings.Repeat("22", 32)+`"`)
	withLogs := receiptRouteWithLogs(t, receipt, receiptRouteHash(t, receipt))
	wrongBloom := blockReceiptsRouteArray(t, receiptRouteReplace(t, withLogs, "logsBloom", `"0x`+strings.Repeat("00", 256)+`"`))
	wrongLogBinding := blockReceiptsRouteArray(t, receiptRouteWithLogs(t, receipt, "0x"+strings.Repeat("11", 32)))
	cases := []struct {
		name            string
		bodies          []json.RawMessage
		want            rpc.Status
		class           error
		support, faults int
	}{
		{"identical", []json.RawMessage{valid, valid, valid}, rpc.Corroborated, nil, 3, 0},
		{"spellings", []json.RawMessage{valid, blockReceiptsRouteArray(t, receiptRouteFoldHex(t, receipt)), blockReceiptsRouteArray(t, receiptRouteReorder(t, receipt))}, rpc.Corroborated, nil, 3, 0},
		{"null", []json.RawMessage{json.RawMessage(`null`), json.RawMessage(` null `), json.RawMessage(`null`)}, rpc.Corroborated, nil, 3, 0},
		{"empty", []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`[ ]`), json.RawMessage(`[]`)}, rpc.Corroborated, nil, 3, 0},
		{"null-is-not-empty", []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`[]`)}, rpc.Corroborated, nil, 2, 0},
		{"null-row", []json.RawMessage{valid, valid, json.RawMessage(`[null]`)}, rpc.Corroborated, nil, 2, 1},
		{"second-row-invalid", []json.RawMessage{valid, valid, blockReceiptsRouteArray(t, receipt, json.RawMessage(`null`))}, rpc.Corroborated, nil, 2, 1},
		{"dissent", []json.RawMessage{valid, valid, dissent}, rpc.Corroborated, nil, 2, 0},
		{"array-order", []json.RawMessage{ordered, reversed, ordered}, rpc.Corroborated, nil, 2, 0},
		{"fixed-five-member-basis", []json.RawMessage{valid, valid, bad, bad, bad}, rpc.Unknown, rpc.ErrInsufficientSupport, 2, 3},
		{"all-malformed", []json.RawMessage{bad, bad, bad}, rpc.Unknown, rpc.ErrMalformedResponse, 0, 3},
		{"first-binding-before-next-shape", []json.RawMessage{valid, valid, blockReceiptsRouteArray(t, foreign, receiptRouteDelete(t, receipt, "logs"))}, rpc.Corroborated, nil, 2, 1},
		{"second-binding", []json.RawMessage{valid, valid, blockReceiptsRouteArray(t, receipt, foreign)}, rpc.Corroborated, nil, 2, 1},
		{"bloom-mismatch", []json.RawMessage{valid, valid, wrongBloom}, rpc.Corroborated, nil, 2, 1},
		{"log-binding", []json.RawMessage{valid, valid, wrongLogBinding}, rpc.Corroborated, nil, 2, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := blockReceiptsRouteCompare(t, tc.bodies, req)
			if run.decision.Status != tc.want || !errors.Is(run.err, tc.class) || len(run.decision.Support) != tc.support || len(run.decision.SourceFaults) != tc.faults {
				t.Fatalf("decision = %+v, err=%v", run.decision, run.err)
			}
			if run.decision.Adjudication.Basis != len(tc.bodies) || run.decision.Adjudication.Required != len(tc.bodies)/2+1 {
				t.Fatalf("fixed basis changed: %+v", run.decision.Adjudication)
			}
			if tc.name == "fixed-five-member-basis" {
				if run.decision.Extensions != nil || run.decision.Diagnostics == nil || len(run.decision.Diagnostics.Residuals) == 0 {
					t.Fatalf("minority evidence = %+v", run.decision)
				}
				for _, field := range run.decision.Diagnostics.Residuals {
					if len(field.Sources) != 2 {
						t.Fatalf("minority lost fixed-denominator raw votes: %+v", field)
					}
				}
			}
		})
	}
}

func TestBlockReceiptsCanonicalRouteExtensions(t *testing.T) {
	receipt := receiptRouteFixture(t)
	base := receiptRouteReplace(t, receipt, "vendor", `{"fee":"0x1"}`)
	a := blockReceiptsRouteArray(t, receiptRouteReplace(t, base, "minority", `1`))
	b := blockReceiptsRouteArray(t, receiptRouteFoldHex(t, base))
	c := blockReceiptsRouteArray(t, base)
	run := blockReceiptsRouteCompare(t, []json.RawMessage{a, b, c}, blockReceiptsRouteRequest(t, receipt))
	if run.err != nil || run.evaluation == nil || run.decision.Extensions == nil || len(run.decision.Extensions.Fields) != 1 {
		t.Fatalf("projection = %+v, %v", run.decision, run.err)
	}
	field := run.decision.Extensions.Fields[0]
	wantFingerprint, err := rpc.CoreObjectFingerprint(run.decision.Value)
	if err != nil || field.Path != "/0/vendor/fee" || string(field.Value) != `"0x1"` || len(field.Sources) != 3 || field.CoreObjectFingerprint != wantFingerprint {
		t.Fatalf("extension = %+v, fingerprint=%s, %v", field, wantFingerprint, err)
	}
	var coreAccepted, minorityResidual, spellingResidual bool
	for _, field := range run.evaluation.Accepted {
		coreAccepted = coreAccepted || field.Path == "/0/transactionHash"
	}
	for _, field := range run.decision.Diagnostics.Residuals {
		minorityResidual = minorityResidual || field.Path == "/0/minority"
		spellingResidual = spellingResidual || field.Path == "/0/transactionHash"
	}
	if !coreAccepted || !minorityResidual || !spellingResidual {
		t.Fatalf("raw core votes/residuals lost: accepted=%+v diagnostics=%+v", run.evaluation.Accepted, run.decision.Diagnostics)
	}
}

func TestBlockReceiptsCanonicalRouteHeightBinding(t *testing.T) {
	receipt := receiptRouteFixture(t)
	var fields struct {
		BlockNumber string `json:"blockNumber"`
	}
	if err := json.Unmarshal(receipt, &fields); err != nil || fields.BlockNumber == "" {
		t.Fatalf("fixture block height: %v", err)
	}
	valid := blockReceiptsRouteArray(t, receipt)
	foreign := blockReceiptsRouteArray(t, receipt, receiptRouteReplace(t, receipt, "blockNumber", `"0x1"`))
	for _, reference := range []any{fields.BlockNumber, map[string]any{"blockNumber": fields.BlockNumber}} {
		run := blockReceiptsRouteCompare(t, []json.RawMessage{valid, valid, foreign}, rpc.Request{Method: blockReceiptsRouteMethod, Params: []any{reference}})
		if run.err != nil || len(run.decision.SourceFaults) != 1 || !strings.Contains(run.decision.SourceFaults[0].Err.Error(), "receipt 1 block number does not match requested number") {
			t.Fatalf("height binding = %+v, %v", run.decision, run.err)
		}
	}
}

func TestBlockReceiptsCanonicalRouteWorkCounts(t *testing.T) {
	receipt := receiptRouteFixture(t)
	valid := blockReceiptsRouteArray(t, receipt)
	req := blockReceiptsRouteRequest(t, receipt)
	members, nodes := blockReceiptsRouteNodes(t, []json.RawMessage{valid, valid, valid})
	for index, processed := range []bool{false, true} {
		counts := &workCounters{}
		plan := instrumentedPlan(t, blockReceiptsRouteMethod, counts)
		if !processed {
			plan.ProcessCanonical = nil
		}
		run := blockReceiptsRouteAggregate(t, plan, members, req)
		assertBlockReceiptsRouteRequests(t, nodes, req, index+1)
		if run.err != nil || run.decision.Status != rpc.Corroborated {
			t.Fatalf("aggregate = %+v, %v", run.decision, run.err)
		}
		if plan.ProcessCanonical != nil {
			if counts.process.Load() != 3 || counts.admit.Load() != 0 || counts.encode.Load() != 0 || counts.normalize.Load() != 0 {
				t.Fatalf("canonical hooks: process=%d admit=%d encode=%d normalize=%d", counts.process.Load(), counts.admit.Load(), counts.encode.Load(), counts.normalize.Load())
			}
		} else if counts.process.Load() != 0 || counts.admit.Load() != 3 || counts.encode.Load() != 3 || counts.normalize.Load() != 0 {
			t.Fatalf("raw hooks: process=%d admit=%d encode=%d normalize=%d", counts.process.Load(), counts.admit.Load(), counts.encode.Load(), counts.normalize.Load())
		}
	}
}

// This independent adapter hands a complete result to the real pool and
// coordinator. HTTP correctly rejects oversized envelopes earlier; the adapter
// lets this contract identify RejectUnsafeJSON's own gate without that earlier
// transport limit hiding it.
type blockReceiptsSafetyTransport struct {
	raw   json.RawMessage
	calls atomic.Int64
}

func (tr *blockReceiptsSafetyTransport) Call(_ context.Context, _ string, _ ...any) rpc.Response {
	tr.calls.Add(1)
	return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: tr.raw}}
}

func (*blockReceiptsSafetyTransport) ChainID(context.Context) (*big.Int, error) {
	return big.NewInt(1), nil
}

func (*blockReceiptsSafetyTransport) Close() {}

type blockReceiptsSafetyDialer struct{ transport *blockReceiptsSafetyTransport }

func (d blockReceiptsSafetyDialer) DialContext(context.Context, string) (transport.Transport, error) {
	return d.transport, nil
}

func TestBlockReceiptsCanonicalRouteSafetyBeforeProcessor(t *testing.T) {
	receipt := receiptRouteFixture(t)
	// The receipt object plus 31 nested vendor arrays fits exactly 32 levels.
	// Wrapping that otherwise valid receipt in its response list adds level 33.
	nested := strings.Repeat("[", jsonsafe.MaxNestingDepth-1) + "0" + strings.Repeat("]", jsonsafe.MaxNestingDepth-1)
	row := receiptRouteReplace(t, receipt, "vendor", nested)
	if err := jsonsafe.Validate(row); err != nil {
		t.Fatalf("standalone row must fit the depth budget: %v", err)
	}
	if _, err := objectreceipt.Decode(row); err != nil {
		t.Fatalf("standalone row must pass receipt admission: %v", err)
	}
	tooDeep := blockReceiptsRouteArray(t, row)
	tooLarge := make(json.RawMessage, int(jsonsafe.MaxBodyBytes)+1)
	for i := range tooLarge {
		tooLarge[i] = ' '
	}
	copy(tooLarge, blockReceiptsRouteArray(t, receipt))
	for _, tc := range []struct {
		name  string
		raw   json.RawMessage
		cause error
	}{
		{"outer-list-depth", tooDeep, jsonsafe.ErrTooDeep},
		{"whole-list-size", tooLarge, jsonsafe.ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := jsonsafe.Validate(tc.raw); !errors.Is(err, tc.cause) {
				t.Fatalf("unsafe fixture: %v, want %v", err, tc.cause)
			}
			tr := &blockReceiptsSafetyTransport{raw: tc.raw}
			p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(blockReceiptsSafetyDialer{tr})}, pool.Member{Name: "unsafe", URL: "http://block-receipts-safety.invalid"})
			counts := &workCounters{}
			plan := instrumentedPlan(t, blockReceiptsRouteMethod, counts)
			if plan.ProcessCanonical == nil {
				t.Fatal("processor is not registered")
			}
			shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 1)
			if err != nil {
				t.Fatal(err)
			}
			recorder := &captureRecorder{}
			agg := New(p, WithRecorder(recorder), WithPlans(map[string]rpc.ReadPlan{blockReceiptsRouteMethod: plan}), WithCorroboration(shared))
			d, err := agg.Aggregate(context.Background(), blockReceiptsRouteRequest(t, receipt))
			if !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), tc.cause.Error()) || d.Status != rpc.Unknown || len(d.SourceFaults) != 1 {
				t.Fatalf("unsafe response outcome = %+v, %v", d, err)
			}
			if counts.process.Load() != 0 || counts.admit.Load() != 0 || counts.encode.Load() != 0 || counts.normalize.Load() != 0 || tr.calls.Load() != 1 {
				t.Fatalf("unsafe response reached method hooks: process=%d admit=%d encode=%d normalize=%d requests=%d", counts.process.Load(), counts.admit.Load(), counts.encode.Load(), counts.normalize.Load(), tr.calls.Load())
			}
			if event := oneEvent(t, recorder); event.Faults != len(d.Faults) {
				t.Fatalf("event fault count = %d, want %d", event.Faults, len(d.Faults))
			}
		})
	}
}

func BenchmarkBlockReceiptsCanonicalMicro(b *testing.B) {
	for _, rows := range []int{1, 16} {
		b.Run(fmt.Sprintf("Receipts%d", rows), func(b *testing.B) {
			receipt := receiptRouteFixture(b)
			items := make([]json.RawMessage, rows)
			for i := range items {
				items[i] = receipt
			}
			raw := blockReceiptsRouteArray(b, items...)
			req := blockReceiptsRouteRequest(b, receipt)
			for _, processed := range []bool{false, true} {
				name := "baseline-admit-plus-encode"
				if processed {
					name = "candidate-process-canonical"
				}
				b.Run(name, func(b *testing.B) {
					plan := blockReceiptsRoutePlan(b, processed)
					if processed && plan.ProcessCanonical == nil {
						b.Skip("canonical entry is not registered in this baseline")
					}
					b.ReportAllocs()
					b.SetBytes(int64(len(raw)))
					b.ResetTimer()
					for range b.N {
						if processed {
							if _, err := plan.ProcessCanonical(context.Background(), req, raw, nil); err != nil {
								b.Fatal(err)
							}
						} else {
							if _, err := plan.Admit(context.Background(), req, raw, nil); err != nil {
								b.Fatal(err)
							}
							if _, err := rpc.CanonicalOf(plan.Resolver, raw); err != nil {
								b.Fatal(err)
							}
						}
					}
				})
			}
		})
	}
}

// Synthetic repeated receipt lists exercise list-size costs; they do not claim
// block completeness or a new provider capture. Fixture construction, schema
// validation, servers, connections and warm-up are outside the measured loop.
func BenchmarkBlockReceiptsCanonicalAggregate(b *testing.B) {
	for _, rows := range []int{1, 16} {
		b.Run(fmt.Sprintf("Receipts%d", rows), func(b *testing.B) {
			receipt := receiptRouteFixture(b)
			items := make([]json.RawMessage, rows)
			for i := range items {
				items[i] = receipt
			}
			raw := blockReceiptsRouteArray(b, items...)
			if _, err := objectreceipt.EncodeBlock(raw); err != nil {
				b.Fatal(err)
			}
			req := blockReceiptsRouteRequest(b, receipt)
			members, _ := blockReceiptsRouteNodes(b, []json.RawMessage{raw, raw, raw})
			for _, processed := range []bool{false, true} {
				name := "baseline-raw-route"
				if processed {
					name = "candidate-canonical-route"
				}
				b.Run(name, func(b *testing.B) {
					plan := blockReceiptsRoutePlan(b, processed)
					if processed && plan.ProcessCanonical == nil {
						b.Skip("canonical entry is not registered in this baseline")
					}
					p := receiptRouteBenchPool(b, members)
					shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 2)
					if err != nil {
						b.Fatal(err)
					}
					agg := New(p, WithPlans(map[string]rpc.ReadPlan{req.Method: plan}), WithCorroboration(shared), WithProjectors(map[string]rpc.ObservationProjector{methods.ProjectorBlockReceipts: blockReceiptsRouteProjector(b)}))
					if d, err := agg.Aggregate(context.Background(), req); err != nil || d.Status != rpc.Corroborated || len(d.Support) != 3 {
						b.Fatalf("warm-up: %+v, %v", d, err)
					}
					b.ReportAllocs()
					b.SetBytes(int64(len(raw)))
					b.ReportMetric(float64(rows), "receipts/op")
					b.ResetTimer()
					for range b.N {
						if d, err := agg.Aggregate(context.Background(), req); err != nil || d.Status != rpc.Corroborated || len(d.Support) != 3 {
							b.Fatalf("aggregate: %+v, %v", d, err)
						}
					}
				})
			}
		})
	}
}
