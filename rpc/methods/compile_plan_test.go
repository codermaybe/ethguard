package methods

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// planEvidenceFor builds the established evidence a planned read would hold
// for one request: the same hash and height the chain's test anchors return,
// tagged with the level the request declared.
func planEvidenceFor(t *testing.T, plan rpc.ReadPlan, params []any) (rpc.ReferenceEvidence, bool) {
	t.Helper()
	request, needed := plan.ReferenceRequirement(rpc.Request{Method: plan.Method, Params: params})
	if !needed {
		return rpc.ReferenceEvidence{}, false
	}
	tag := string(request.Kind)
	if tag == string(rpc.ReferenceLatest) {
		tag = ""
	}
	return rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated, Tag: tag}, true
}

// TestCompiledPlansMatchChains holds the plan compiler and the chain
// compiler together over the whole frozen inventory: for every registered
// method, the plan projects the same method rule, declares the same
// reference need, and prepares the same requests as the compiled chain —
// with the plan's established evidence carrying exactly what the chain's
// test anchors return.
func TestCompiledPlansMatchChains(t *testing.T) {
	hash, height := testAnchors()
	for _, spec := range Specs() {
		spec := spec
		t.Run(spec.Method, func(t *testing.T) {
			plan, err := CompilePlan(spec)
			if err != nil {
				t.Fatalf("compile plan: %v", err)
			}
			chain := Compile(spec, CompileDeps{AnchorHash: hash, AnchorHeight: height})

			planRule := plan.MethodRule()
			chainRule, _ := chain.(rpc.MethodRuleSource).MethodRule()
			if planRule != chainRule {
				t.Fatalf("method rule mismatch:\nplan %+v\nchain %+v", planRule, chainRule)
			}

			chainReq, chainRequirement := chain.(rpc.ReferenceRequirement)
			for _, params := range prepareFixtures(spec.Method) {
				req := rpc.Request{Method: spec.Method, Params: params}
				// The plan's Validate / Prepare phase runs the method's own
				// validation and its executable pending rejection before any
				// establishment; a chain folds both into Prepare.
				if plan.Validate != nil {
					if err := plan.Validate(context.Background(), req); err != nil {
						planPreparedAgainstChain(t, req, err, chain)
						continue
					}
				}
				if plan.PendingReject != nil {
					if err := plan.PendingReject(req); err != nil {
						planPreparedAgainstChain(t, req, err, chain)
						continue
					}
				}
				declaredPlan, needPlan := plan.ReferenceRequirement(req)
				declaredChain, needChain := rpc.ReferenceRequest{}, false
				if chainRequirement {
					declaredChain, needChain = chainReq.ReferenceFor(req)
				}
				if needPlan != needChain || declaredPlan != declaredChain {
					t.Fatalf("reference declaration mismatch for %v: plan (%v,%v) chain (%v,%v)",
						params, declaredPlan, needPlan, declaredChain, needChain)
				}

				evidence, established := planEvidenceFor(t, plan, params)
				preparedPlan, pin, planErr := plan.PrepareWithReference(req, evidence, established)
				preparedChain, chainErr := chain.Prepare(context.Background(), req)
				if (planErr == nil) != (chainErr == nil) {
					t.Fatalf("prepare error mismatch for %v: plan %v chain %v", params, planErr, chainErr)
				}
				if planErr != nil {
					if planErr.Error() != chainErr.Error() {
						t.Fatalf("prepare error text mismatch for %v: plan %q chain %q", params, planErr, chainErr)
					}
					continue
				}
				if !equalRequests(t, preparedPlan, preparedChain) {
					t.Fatalf("prepared request mismatch for %v:\nplan      %s\nchain     %s",
						params, requestJSON(t, preparedPlan), requestJSON(t, preparedChain))
				}
				// The pin the plan records must be the same block reference
				// the chain's BlockRef providers report.
				if pin != "" {
					if provider, ok := chain.(rpc.BlockRefProvider); ok {
						if chainPin := provider.BlockRef(rpc.Request{Method: spec.Method, Params: params}, preparedChain); chainPin != pin {
							t.Fatalf("pin mismatch for %v: plan %q chain %q", params, pin, chainPin)
						}
					} else {
						t.Fatalf("plan recorded pin %q but the chain reports none", pin)
					}
				}
			}
		})
	}
}

// planPreparedAgainstChain pins that a plan's local validation failure is
// the same failure the chain's Prepare reports: same error class, same text.
func planPreparedAgainstChain(t *testing.T, req rpc.Request, planErr error, chain rpc.Policy) {
	t.Helper()
	_, chainErr := chain.Prepare(context.Background(), req)
	if chainErr == nil || chainErr.Error() != planErr.Error() {
		t.Fatalf("validation failure mismatch for %v: plan %q chain %v", req.Params, planErr, chainErr)
	}
}

// TestCompilePlanCoversTheInventory is the registration gate: every
// registered method compiles as a plan, so a registry can migrate a method
// without first fixing a compiler surprise.
func TestCompilePlanCoversTheInventory(t *testing.T) {
	for _, spec := range Specs() {
		plan, err := CompilePlan(spec)
		if err != nil {
			t.Fatalf("%s: compile plan: %v", spec.Method, err)
		}
		if plan.Method != spec.Method {
			t.Fatalf("plan method %q, want %q", plan.Method, spec.Method)
		}
		if plan.Resolver == nil {
			t.Fatalf("%s compiled no resolver", spec.Method)
		}
	}
}

// TestCompilePlanRejectsImpossibleDeclarations pins the composition
// rejections: a spec that cannot compose into one executable plan fails at
// compile time with a named reason.
func TestCompilePlanRejectsImpossibleDeclarations(t *testing.T) {
	cases := []struct {
		name string
		spec MethodSpec
	}{
		{
			name: "missing method name",
			spec: MethodSpec{Resolver: ResolverStrict, Normalize: wire.NormalizeWireQuantity},
		},
		{
			name: "missing normalizer",
			spec: MethodSpec{Method: "eth_broken", Resolver: ResolverStrict},
		},
		{
			name: "unknown resolver kind",
			spec: MethodSpec{Method: "eth_broken", Resolver: "exotic", Normalize: wire.NormalizeWireQuantity},
		},
		{
			name: "hash anchor without positions",
			spec: MethodSpec{Method: "eth_broken", Resolver: ResolverStrict, Normalize: wire.NormalizeWireQuantity,
				Reference: ReferenceSpec{Kind: ReferenceShapeHashAt}},
		},
		{
			name: "height fields without fields",
			spec: MethodSpec{Method: "eth_broken", Resolver: ResolverStrict, Normalize: wire.NormalizeWireQuantity,
				Reference: ReferenceSpec{Kind: ReferenceShapeHeightFieldsAt, ParamPos: 0}},
		},
		{
			name: "eip-1898 without exactly one position",
			spec: MethodSpec{Method: "eth_broken", Resolver: ResolverStrict, Normalize: wire.NormalizeWireQuantity,
				Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1, 2}, EIP1898: true}},
		},
		{
			name: "eip-1898 on a height shape",
			spec: MethodSpec{Method: "eth_broken", Resolver: ResolverStrict, Normalize: wire.NormalizeWireQuantity,
				Reference: ReferenceSpec{Kind: ReferenceShapeHeightAt, Positions: []int{1}, EIP1898: true}},
		},
		{
			name: "unknown pending mode",
			spec: MethodSpec{Method: "eth_broken", Resolver: ResolverStrict, Normalize: wire.NormalizeWireQuantity,
				Pending: PendingSpec{Mode: "elsewhere", Pos: 0}},
		},
		{
			name: "canonical bytes without an encoder",
			spec: MethodSpec{Method: "eth_broken", Resolver: ResolverStrict, Normalize: wire.NormalizeWireQuantity,
				KeySource: rpc.RuleKeyCanonicalBytes},
		},
		{
			name: "encoder without a key source",
			spec: MethodSpec{Method: "eth_broken", Resolver: ResolverStrict, Normalize: wire.NormalizeWireQuantity,
				Encode: wire.EncodeQuantity},
		},
		{
			name: "median with a non-quantity compare kind",
			spec: MethodSpec{Method: "eth_broken", Resolver: ResolverMedian, Normalize: wire.NormalizeWireQuantity,
				CompareKind: rpc.RuleCompareObject},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CompilePlan(tc.spec); err == nil {
				t.Fatal("an impossible declaration compiled")
			}
		})
	}
}

// The EIP-1898 inconsistency the chain compiler panics on is a named
// compile error for plans.
func TestCompilePlanReportsInconsistentEIP1898Declaration(t *testing.T) {
	_, err := CompilePlan(MethodSpec{
		Method:    "eth_broken",
		Resolver:  ResolverStrict,
		Normalize: wire.NormalizeWireQuantity,
		Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, EIP1898: true},
	})
	if err == nil {
		t.Fatal("an EIP-1898 declaration without positions compiled")
	}
}

// A declared corroborating class must resolve under a frozen snapshot and a
// sampling class must not: the collection mode follows the declaration.
func TestCompilePlanBindingsFollowResolverClass(t *testing.T) {
	for _, spec := range Specs() {
		plan, err := CompilePlan(spec)
		if err != nil {
			t.Fatalf("%s: compile plan: %v", spec.Method, err)
		}
		_, corroborating := plan.Resolver.(rpc.CorroboratingPolicy)
		if plan.Corroborating() != corroborating {
			t.Fatalf("%s: declared corroborating=%v, resolver capability=%v", spec.Method, plan.Corroborating(), corroborating)
		}
	}
}

func equalRequests(t *testing.T, a, b rpc.Request) bool {
	t.Helper()
	return string(requestJSON(t, a)) == string(requestJSON(t, b))
}

func requestJSON(t *testing.T, req rpc.Request) []byte {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
