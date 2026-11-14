package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

type mutateInputStage struct {
	scriptedStage
	mutate func()
}

func (s *mutateInputStage) Establish(ctx context.Context, invocation rpc.Invocation, request rpc.ReferenceRequest) (rpc.Invocation, error) {
	s.mutate()
	return s.scriptedStage.Establish(ctx, invocation, request)
}

type changingSimulationInput struct{ calls int }

func (v *changingSimulationInput) MarshalJSON() ([]byte, error) {
	v.calls++
	if v.calls > 1 {
		return []byte(`{"blockStateCalls":null}`), nil
	}
	return []byte(`{"blockStateCalls":[],"vendor":{"n":9007199254740993}}`), nil
}

type changingSimulationReference struct{ calls int }

func (v *changingSimulationReference) MarshalJSON() ([]byte, error) {
	v.calls++
	if v.calls > 1 {
		return []byte(`"pending"`), nil
	}
	return []byte(`"latest"`), nil
}

func TestSimulatePreparationFreezesUserInputBeforeReference(t *testing.T) {
	for _, kind := range []string{"map", "raw", "custom-marshaler"} {
		t.Run(kind, func(t *testing.T) {
			nodes, p := probeNodes(t, "0x1", 2)
			for _, node := range nodes {
				node.ReturnRaw(methods.MethodSimulateV1, json.RawMessage(`[]`))
			}
			plan := planFor(t, methods.MethodSimulateV1)
			original := json.RawMessage(`{"blockStateCalls":[],"vendor":{"n":9007199254740993}}`)
			object := map[string]any{"blockStateCalls": []any{}, "vendor": map[string]any{"n": json.Number("9007199254740993")}}
			custom := &changingSimulationInput{}
			customBase := &changingSimulationReference{}
			var payload any
			var base any = "latest"
			var mutate func()
			switch kind {
			case "map":
				payload = object
				mutate = func() { object["blockStateCalls"] = nil; object["vendor"] = false }
			case "raw":
				payload = original
				mutate = func() {
					for i := range original {
						original[i] = ' '
					}
				}
			default:
				payload = custom
				base = customBase
				mutate = func() {}
			}
			pin := "0x" + strings.Repeat("ab", 32)
			stage := &mutateInputStage{scriptedStage: scriptedStage{ref: rpc.ReferenceEvidence{Hash: pin, Height: 16, Status: rpc.Corroborated}}, mutate: mutate}
			agg := New(p, WithCorroboration(mustShared(t)), WithPlans(map[string]rpc.ReadPlan{methods.MethodSimulateV1: plan}), WithReferenceStage(stage))
			decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: methods.MethodSimulateV1, Params: []any{payload, base}})
			if err != nil || decision.Status != rpc.Corroborated || decision.Prepared == nil {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
			if kind == "custom-marshaler" && custom.calls != 1 {
				t.Fatalf("user marshaler ran %d times", custom.calls)
			}
			if kind == "custom-marshaler" && customBase.calls != 1 {
				t.Fatalf("base marshaler ran %d times", customBase.calls)
			}
			if len(stage.rounds) != 1 {
				t.Fatalf("reference count=%d", len(stage.rounds))
			}
			var prepared []json.RawMessage
			if err := json.Unmarshal(decision.Prepared.Params, &prepared); err != nil {
				t.Fatal(err)
			}
			if len(prepared) != 2 || string(prepared[0]) != `{"blockStateCalls":[],"vendor":{"n":9007199254740993}}` || string(prepared[1]) != `"`+pin+`"` {
				t.Fatalf("input drift: %s", decision.Prepared.Params)
			}
			for _, node := range nodes {
				count := 0
				for _, call := range node.Calls() {
					if call.Method != methods.MethodSimulateV1 {
						continue
					}
					count++
					if len(call.Params) != 2 || string(call.Params[0]) != string(prepared[0]) || string(call.Params[1]) != string(prepared[1]) {
						t.Fatalf("wire/prepared drift: %s / %s", call.Params, decision.Prepared.Params)
					}
				}
				if count != 1 {
					t.Fatalf("business count=%d", count)
				}
			}
		})
	}
}

func TestPreparationCannotChangeTheAllowedMethod(t *testing.T) {
	nodes, p := probeNodes(t, "0x1", 2)
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	plan.Prepare = func(_ context.Context, req rpc.Request) (rpc.Request, error) {
		req.Method = "eth_sendRawTransaction"
		return req, nil
	}
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Status: rpc.Corroborated}}
	agg := New(p, WithCorroboration(mustShared(t)), WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}), WithReferenceStage(stage))
	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}})
	if !errors.Is(err, rpc.ErrInvalidRequest) || decision.Status != rpc.Unknown || decision.Prepared != nil || len(decision.Faults) != 0 || len(decision.SourceFaults) != 0 {
		t.Fatalf("method substitution: %+v %v", decision, err)
	}
	if len(stage.rounds) != 0 {
		t.Fatal("method substitution established a reference")
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("method substitution reached provider: %+v", node.Calls())
		}
	}
}
