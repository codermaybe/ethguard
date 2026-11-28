package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

// The plan execution fixtures. Every behavior test assembles its subject
// through CompilePlan or the resolver assembly CompilePlan uses, so the
// tests verify exactly the code the registry runs — no hand-written chain
// exists beside the plan to keep honest.

// compiledSpec returns the registered spec for one method.
func compiledSpec(t *testing.T, method string) MethodSpec {
	t.Helper()
	for _, spec := range Specs() {
		if spec.Method == method {
			return spec
		}
	}
	t.Fatalf("%s is not a registered read method", method)
	return MethodSpec{}
}

// compiledPlan compiles the registered spec for one method: the same
// assembly the runtime registry installs.
func compiledPlan(t *testing.T, method string) rpcengine.ReadPlan {
	t.Helper()
	plan, err := CompilePlan(compiledSpec(t, method))
	if err != nil {
		t.Fatalf("%s: compile plan: %v", method, err)
	}
	return plan
}

// planResolver compiles one method's resolver with explicit strict options —
// the same assembly CompilePlan runs, with the options a driver pins (a
// quorum floor, for instance) supplied explicitly instead of the defaults.
func planResolver(t *testing.T, method string, opts ...rpcengine.StrictOption) rpcengine.Resolver {
	t.Helper()
	resolver, err := assembleResolver(compiledSpec(t, method), opts)
	if err != nil {
		t.Fatalf("%s: assemble resolver: %v", method, err)
	}
	return resolver
}

// fixtureReference is the established reference the behavior fixtures serve:
// hash 0xabc at height 16, corroborated, tagged with the requested level.
const (
	fixtureHash   = "0xabc"
	fixtureHeight = uint64(16)
)

// establishedEvidence returns the evidence a planned read holds for req:
// the fixture reference tagged with the level the request's declared
// requirement names. ok is false when the request needs no reference.
func establishedEvidence(t *testing.T, plan rpcengine.ReadPlan, req rpc.Request) (rpc.ReferenceEvidence, bool) {
	t.Helper()
	request, needed := plan.ReferenceRequirement(req)
	if !needed {
		return rpc.ReferenceEvidence{}, false
	}
	tag := string(request.Kind)
	if tag == string(rpc.ReferenceLatest) {
		tag = ""
	}
	return rpc.ReferenceEvidence{Hash: fixtureHash, Height: fixtureHeight, Status: rpc.Corroborated, Tag: tag}, true
}

// preparePlanRequest drives the plan's local phases — the method's own
// validation, its executable pending rejection — and the reference rewrite
// from the evidence the request's requirement declares. It returns the
// prepared request and the recorded pin.
func preparePlanRequest(t *testing.T, plan rpcengine.ReadPlan, req rpc.Request) (rpc.Request, string, error) {
	t.Helper()
	req = plan.ApplyReferenceDefault(req)
	if plan.Prepare != nil {
		prepared, err := plan.Prepare(context.Background(), req)
		if err != nil {
			return rpc.Request{}, "", err
		}
		req = prepared
	}
	if plan.PendingReject != nil {
		if err := plan.PendingReject(req); err != nil {
			return rpc.Request{}, "", err
		}
	}
	evidence, ok := establishedEvidence(t, plan, req)
	return plan.PrepareWithReference(req, evidence, ok, planTestBasis())
}

// resolveCanonical drives the compiled method processor and its processed-only
// resolver with an explicit request. Failed processing retains the source slot.
func resolveCanonical(t *testing.T, req rpc.Request, c rpcengine.Corroboration, obs []rpc.Observation) (rpcengine.Outcome, error) {
	t.Helper()
	plan := compiledPlan(t, req.Method)
	processed := make([]rpcengine.CanonicalObservation, len(obs))
	for i, item := range obs {
		var result rpcengine.CanonicalResult
		if item.Response.Err() == nil {
			var err error
			result, err = plan.Canonical.Process(context.Background(), req, item.Response.Answer.Result, nil)
			if err != nil {
				item.Response.Answer.Interpretation = rpc.Interpretation{State: rpc.InterpretationRejected, Cause: fmt.Errorf("admission: %w", err)}
			}
		}
		var err error
		processed[i], err = rpcengine.NewCanonicalObservation(item, result)
		if err != nil {
			t.Fatal(err)
		}
	}
	return plan.Canonical.Resolver.ResolveCanonicalCorroborated(c, processed)
}

func receiptRequest(t *testing.T) rpc.Request {
	t.Helper()
	var fields struct {
		Hash string `json:"transactionHash"`
	}
	if err := json.Unmarshal(sampleReceipt(t), &fields); err != nil {
		t.Fatal(err)
	}
	return rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{fields.Hash}}
}

func planTestBasis() rpcengine.Basis {
	return rpcengine.FreezeBasis([]rpc.SourceRef{{ID: "a"}, {ID: "b"}}, rpcengine.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2})
}
