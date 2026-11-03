package methods

import (
	"context"
	"testing"

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
func compiledPlan(t *testing.T, method string) rpc.ReadPlan {
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
func planResolver(t *testing.T, method string, opts ...rpc.StrictOption) rpc.Policy {
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
func establishedEvidence(t *testing.T, plan rpc.ReadPlan, req rpc.Request) (rpc.ReferenceEvidence, bool) {
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
func preparePlanRequest(t *testing.T, plan rpc.ReadPlan, req rpc.Request) (rpc.Request, string, error) {
	t.Helper()
	if plan.Validate != nil {
		if err := plan.Validate(context.Background(), req); err != nil {
			return rpc.Request{}, "", err
		}
	}
	if plan.PendingReject != nil {
		if err := plan.PendingReject(req); err != nil {
			return rpc.Request{}, "", err
		}
	}
	evidence, ok := establishedEvidence(t, plan, req)
	return plan.PrepareWithReference(req, evidence, ok)
}
