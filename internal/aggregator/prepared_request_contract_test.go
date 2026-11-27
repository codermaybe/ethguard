package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/pool"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// The prepared-request behavior contracts that survive preparation: the
// invocation's routing facts freeze at prepare time, the record carries the
// real prepared method, the wire keeps nil parameters and an empty parameter
// array distinct, and every consumer of the semantic request reads the one
// frozen JSON fact.

// TestPreparedPendingRouteStaysFrozenAfterPrepare pins the frozen pending
// route: the local-observation decision is made once from the prepared
// request, so mutating the caller's Go value after preparation — the shared
// map element the wire bytes were frozen from — cannot re-route the
// resolution. Without the frozen fact this mutation would re-derive the
// route during resolution and turn a local observation into a corroborated
// cluster read.
func TestPreparedPendingRouteStaysFrozenAfterPrepare(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 3)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated}}
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	pendingObject := map[string]any{"blockNumber": "pending"}
	var mutated sync.Once
	plan.Admit = func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error) {
		// Runs after preparation and collection, before resolution: the Go
		// map is still the caller's object, but the invocation's pending
		// fact and the wire bytes were frozen long before this write.
		mutated.Do(func() { pendingObject["blockNumber"] = "latest" })
		return nil, nil
	}
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
		WithPlans(map[string]rpcengine.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", pendingObject}})
	if aggErr != nil {
		t.Fatalf("pending observation failed: %v", aggErr)
	}
	if decision.Status != rpc.Selected || len(decision.Support) != 1 {
		t.Fatalf("mutated request re-routed the resolution: status %v, support %d — the pending fact was not frozen", decision.Status, len(decision.Support))
	}
	record := preparedParams(t, decision.Prepared)[1]
	object, ok := record.(map[string]any)
	if !ok || !reflect.DeepEqual(object, map[string]any{"blockNumber": "pending"}) {
		t.Fatalf("prepared record followed the post-prepare mutation: %#v", record)
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method != probeMethod {
				continue
			}
			if len(call.Params) != 2 || string(call.Params[1]) != `{"blockNumber":"pending"}` {
				t.Fatalf("wire carried mutated params: %v", call.Params)
			}
		}
	}
}

// TestPreparedRecordCarriesTheRealMethod pins the method fact: the wire, the
// recorded prepared request, and the block pin come from the prepared
// request's own method — the lookup key the invocation ran under — never
// from the plan value's Method field, which a directly installed plan may
// leave stale.
func TestPreparedRecordCarriesTheRealMethod(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated}}
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	plan.Method = "eth_planFieldStale"
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
		WithPlans(map[string]rpcengine.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if aggErr != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("planned read failed: %v, %v", decision.Status, aggErr)
	}
	if decision.Prepared == nil || decision.Prepared.Method != probeMethod {
		t.Fatalf("prepared record method = %+v, want %q from the prepared request", decision.Prepared, probeMethod)
	}
	if decision.BlockRef != "0xabc" {
		t.Fatalf("rewrite pin = %q, want the established hash", decision.BlockRef)
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == plan.Method {
				t.Fatalf("a node was asked %q: the plan field leaked into the wire", call.Method)
			}
		}
	}
}

// TestWireParamsKeepNilAndEmptyDistinct pins the wire-level shape contract
// through the forward path: nil parameters omit the params member entirely,
// an empty parameter array sends "params":[] — and the prepared record keeps
// the same distinction.
func TestWireParamsKeepNilAndEmptyDistinct(t *testing.T) {
	for _, tc := range []struct {
		name         string
		params       []any
		wantRecorded json.RawMessage
		wantWireNil  bool
	}{
		{"nil-params", nil, nil, true},
		{"empty-array", []any{}, json.RawMessage("[]"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			node.Return("eth_unregistered", "0x64")
			p := fixedPoolWith(t, 1, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())},
				pool.Member{Name: "solo", URL: node.URL()})
			agg := New(p, WithCorroboration(mustShared(t)))
			decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: "eth_unregistered", Params: tc.params})
			if aggErr != nil {
				t.Fatalf("forward read failed: %v", aggErr)
			}
			if string(decision.Prepared.Params) != string(tc.wantRecorded) {
				t.Fatalf("prepared record = %s, want %s", decision.Prepared.Params, tc.wantRecorded)
			}
			var wire []rpctest.Call
			for _, call := range node.Calls() {
				if call.Method == "eth_unregistered" {
					wire = append(wire, call)
				}
			}
			if len(wire) != 1 {
				t.Fatalf("wire calls = %d, want one", len(wire))
			}
			if tc.wantWireNil && wire[0].Params != nil {
				t.Fatalf("nil params produced a params member on the wire: %v", wire[0].Params)
			}
			if !tc.wantWireNil && (wire[0].Params == nil || len(wire[0].Params) != 0) {
				t.Fatalf("empty array did not reach the wire as an empty params member: %v", wire[0].Params)
			}
		})
	}
}

// TestAdmissionAndProcessorShareTheFrozenSemantics pins the one-JSON-fact
// contract on the transaction route: whichever per-source stage a route
// freezes — the canonical processor or the raw admission hook — it receives
// the same semantic request, decoded once from the same frozen array snapshot
// the wire carried and the record owns.
func TestAdmissionAndProcessorShareTheFrozenSemantics(t *testing.T) {
	raw, hash, _, _ := minedTransactionVector(t)
	request := rpc.Request{Method: methods.MethodGetTransactionByHash, Params: []any{hash}}
	stageView := func(t *testing.T, canonical bool) string {
		t.Helper()
		plan := planFor(t, methods.MethodGetTransactionByHash)
		if !canonical {
			plan.ProcessCanonical = nil
		}
		var (
			mu    sync.Mutex
			seen  rpc.Request
			calls int
		)
		if canonical {
			origProcess := plan.ProcessCanonical
			plan.ProcessCanonical = func(ctx context.Context, req rpc.Request, value json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
				mu.Lock()
				seen, calls = req, calls+1
				mu.Unlock()
				return origProcess(ctx, req, value, caller)
			}
		} else {
			origAdmit := plan.Admit
			plan.Admit = func(ctx context.Context, req rpc.Request, value json.RawMessage, caller rpc.AdmissionCaller) (json.RawMessage, error) {
				mu.Lock()
				seen, calls = req, calls+1
				mu.Unlock()
				return origAdmit(ctx, req, value, caller)
			}
		}
		agg := transactionWorkFleet(t, methods.MethodGetTransactionByHash, plan, raw)
		decision, aggErr := agg.Aggregate(context.Background(), request)
		if aggErr != nil || decision.Status != rpc.Corroborated {
			t.Fatalf("route (canonical=%v) failed: %v, %v", canonical, decision.Status, aggErr)
		}
		if calls != 3 || reflect.DeepEqual(seen, rpc.Request{}) {
			t.Fatalf("route (canonical=%v) ran its per-source stage %d times, want once per source", canonical, calls)
		}
		if seen.Method != decision.Prepared.Method {
			t.Fatalf("route (canonical=%v): semantic method %q diverged from the record %q", canonical, seen.Method, decision.Prepared.Method)
		}
		// The stage's semantic view decodes equal to a UseNumber pass over
		// the record's frozen bytes: one JSON fact, three consumers.
		view, err := json.Marshal(seen.Params)
		if err != nil {
			t.Fatal(err)
		}
		var recordParams []any
		decoder := json.NewDecoder(bytes.NewReader(decision.Prepared.Params))
		decoder.UseNumber()
		if err := decoder.Decode(&recordParams); err != nil {
			t.Fatal(err)
		}
		recordJSON, err := json.Marshal(recordParams)
		if err != nil {
			t.Fatal(err)
		}
		if string(view) != string(recordJSON) {
			t.Fatalf("route (canonical=%v): the semantic view diverged from the record: %s / %s", canonical, view, recordJSON)
		}
		return string(view)
	}
	processor := stageView(t, true)
	admission := stageView(t, false)
	if processor != admission {
		t.Fatalf("the two routes saw different JSON semantics: %s / %s", processor, admission)
	}
}

// TestFreezeFailureKeepsEvidenceAndZeroFanOut pins the freeze-failure
// contract: a parameter whose marshaler fails ends the read after the
// reference establishment but before any business fan-out, the established
// evidence stays on the decision, and the failure is an argument error.
func TestFreezeFailureKeepsEvidenceAndZeroFanOut(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated}}
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
		WithPlans(map[string]rpcengine.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{failingMarshaler{}, "latest"}})
	if !errors.Is(aggErr, rpc.ErrInvalidRequest) || decision.Status != rpc.Unknown {
		t.Fatalf("freeze failure did not fail the read: %+v %v", decision, aggErr)
	}
	if decision.Reference == nil {
		t.Fatal("the established reference evidence did not survive the freeze failure")
	}
	if len(stage.rounds) != 1 {
		t.Fatalf("establishment rounds = %d, want the one round that completed", len(stage.rounds))
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("failed freeze still fanned out: %+v", node.Calls())
		}
	}
}

// TestUnencodableElementKeepsZeroFanOut pins the semantic-decode contract:
// an element whose frozen bytes are not valid JSON (a raw-message passthrough)
// fails the read when the per-source stage needs the semantic view, before any
// fan-out — the decode failure is a request error, not a provider fault.
func TestUnencodableElementKeepsZeroFanOut(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	plan.Admit = func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error) {
		return nil, nil
	}
	agg := New(p, WithCorroboration(mustShared(t)), WithPlans(map[string]rpcengine.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", json.RawMessage(`{"blockNumber":`)}})
	if !errors.Is(aggErr, rpc.ErrInvalidRequest) || decision.Status != rpc.Unknown {
		t.Fatalf("unencodable element did not fail the read: %+v %v", decision, aggErr)
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("undecodable request still fanned out: %+v", node.Calls())
		}
	}
}

// TestMaterialFollowsTheInstalledCollector pins the material-collection
// contract: records land in the collector the caller installed, and an
// evidence configuration alone — without an installed collector — changes no
// business fact and leaves nothing to consume.
func TestMaterialFollowsTheInstalledCollector(t *testing.T) {
	request := rpc.Request{Method: "eth_unregistered", Params: []any{"0x1"}}
	run := func(ctx context.Context) (*uncertain.Snapshot, rpc.Decision, error) {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		node.Return("eth_unregistered", "0x64")
		p := fixedPoolWith(t, 1, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())},
			pool.Member{Name: "solo", URL: node.URL()})
		agg := New(p, WithCorroboration(mustShared(t)))
		decision, err := agg.Aggregate(ctx, request)
		return nil, decision, err
	}
	installed := uncertain.NewCollector(uncertain.Config{BudgetBytes: 1 << 20})
	_, decision, err := run(uncertain.WithCollector(context.Background(), installed))
	if err != nil || decision.Status == rpc.Unknown || len(decision.Value) == 0 {
		t.Fatalf("collected read failed: %v, %v", decision.Status, err)
	}
	snapshot := installed.Snapshot()
	if len(snapshot.Raw) == 0 {
		t.Fatal("no response was recorded into the installed collector")
	}
	for _, response := range snapshot.Raw {
		if response.Method != "eth_unregistered" {
			t.Fatalf("recorded method = %q, want the prepared method", response.Method)
		}
	}
	// A configuration without a collector must not fabricate one: the call
	// stays on its business contract with nothing recorded anywhere.
	_, plainDecision, plainErr := run(uncertain.WithConfig(context.Background(), uncertain.Config{BudgetBytes: 1 << 20}))
	if plainErr != nil || plainDecision.Status == rpc.Unknown || len(plainDecision.Value) == 0 {
		t.Fatalf("config-only read changed the business contract: %v, %v", plainDecision.Status, plainErr)
	}
}

// failingMarshaler is a parameter whose serialization always fails.
type failingMarshaler struct{}

func (failingMarshaler) MarshalJSON() ([]byte, error) {
	return nil, errors.New("unserializable parameter")
}
