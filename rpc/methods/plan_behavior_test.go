package methods

import (
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// The plan behavior matrix: for every registered method, the compiled plan
// is verified directly against the business expectations — the method-rule
// projection names the declared facts, the capability projection follows the
// resolver class, and the prepare phase rewrites exactly the declared
// moving-tag shapes with the established reference and records the pin.
// There is no second assembly to compare against: the plan is what runs.

// planPrepareCase is one prepare-phase expectation: the request parameters,
// the expected prepared parameters after the declared rewrite, the expected
// pin, whether the request declares a reference need, and the expected local
// failure class.
type planPrepareCase struct {
	name      string
	params    []any
	want      []any
	pin       string
	establish bool
	wantErr   error
}

// fixtureObjectRewrite is the EIP-1898 object rewrite the fixture reference
// produces: the moving blockNumber becomes the established blockHash, and a
// requireCanonical flag rides along.
func fixtureObjectRewrite(canon bool) map[string]any {
	obj := map[string]any{"blockHash": fixtureHash}
	if canon {
		obj["requireCanonical"] = true
	}
	return obj
}

// planPrepareCases is the prepare-phase matrix: one entry per registered
// method, covering the moving-tag shapes its spec declares plus the shapes
// that must pass through untouched.
func planPrepareCases(method string) []planPrepareCase {
	// hashAnchor is the default-block state read with the EIP-1898 object
	// form at position 1: a string tag rewrites to the established hash, an
	// object tag rewrites to a hash object, explicit references and pending
	// pass through, and pending needs no establishment.
	hashAnchor := func(prefix []any, pos int) []planPrepareCase {
		prefixAt := func(ref any) []any {
			params := append([]any(nil), prefix...)
			for len(params) <= pos {
				params = append(params, nil)
			}
			params[pos] = ref
			return params
		}
		return []planPrepareCase{
			{name: "latest string rewrites to the established hash", params: prefixAt("latest"),
				want: prefixAt(fixtureHash), pin: fixtureHash, establish: true},
			{name: "safe object rewrites to a hash object", params: prefixAt(map[string]any{"blockNumber": "safe", "requireCanonical": true}),
				want: prefixAt(fixtureObjectRewrite(true)), pin: fixtureHash, establish: true},
			{name: "explicit number passes through", params: prefixAt("0x10"),
				want: prefixAt("0x10")},
			{name: "explicit hash object passes through", params: prefixAt(map[string]any{"blockHash": "0x" + repeatAB(32), "requireCanonical": false}),
				want: prefixAt(map[string]any{"blockHash": "0x" + repeatAB(32), "requireCanonical": false})},
			{name: "pending passes through without establishment", params: prefixAt("pending"),
				want: prefixAt("pending")},
		}
	}
	switch method {
	case MethodGetBalance:
		return hashAnchor([]any{"0x1"}, 1)
	case MethodGetTransactionCount:
		return hashAnchor([]any{"0x1"}, 1)
	case MethodGetCode:
		return hashAnchor([]any{"0x1"}, 1)
	case MethodGetStorageAt:
		return hashAnchor([]any{"0x1", "0x0"}, 2)
	case MethodCall:
		return hashAnchor([]any{map[string]any{"to": "0x1"}}, 1)
	case MethodCreateAccessList:
		return hashAnchor([]any{map[string]any{"to": "0x1"}}, 1)
	case MethodEstimateGas:
		return hashAnchor([]any{map[string]any{"to": "0x1"}}, 1)
	case MethodGetBlockReceipts:
		return []planPrepareCase{
			{name: "latest string rewrites to the established hash", params: []any{"latest"},
				want: []any{fixtureHash}, pin: fixtureHash, establish: true},
			{name: "explicit hash passes through", params: []any{"0x" + repeatAB(32)},
				want: []any{"0x" + repeatAB(32)}},
			{name: "pending is refused locally", params: []any{"pending"},
				wantErr: rpc.ErrInvalidRequest},
		}
	case MethodGetLogs:
		return []planPrepareCase{
			{name: "moving range fields rewrite to the established height", params: []any{map[string]any{"fromBlock": "latest", "toBlock": "latest"}},
				want: []any{map[string]any{"fromBlock": "0x10", "toBlock": "0x10"}}, pin: "0x10", establish: true},
			{name: "absent range fields default to the established height", params: []any{map[string]any{"address": "0x1"}},
				want: []any{map[string]any{"address": "0x1", "fromBlock": "0x10", "toBlock": "0x10"}}, pin: "0x10", establish: true},
			{name: "a blockHash pin passes through", params: []any{map[string]any{"blockHash": "0x" + repeatAB(32)}},
				want: []any{map[string]any{"blockHash": "0x" + repeatAB(32)}}},
			{name: "blockHash and range fields are contradictory", params: []any{map[string]any{"blockHash": "0x" + repeatAB(32), "fromBlock": "latest"}},
				wantErr: rpc.ErrInvalidRequest},
			{name: "pending range is refused locally", params: []any{map[string]any{"fromBlock": "pending", "toBlock": "pending"}},
				wantErr: rpc.ErrInvalidRequest},
		}
	case MethodFeeHistory:
		return []planPrepareCase{
			{name: "latest rewrites to the established height", params: []any{uint64(4), "latest", []any{}},
				want: []any{uint64(4), "0x10", []any{}}, pin: "0x10", establish: true},
			{name: "explicit height passes through", params: []any{uint64(4), "0x10", []any{}},
				want: []any{uint64(4), "0x10", []any{}}},
			{name: "pending passes through without establishment", params: []any{uint64(4), "pending", []any{}},
				want: []any{uint64(4), "pending", []any{}}},
		}
	case MethodGetBlockTransactionCountByNumber:
		return []planPrepareCase{
			{name: "latest rewrites to the established height", params: []any{"latest"},
				want: []any{"0x10"}, pin: "0x10", establish: true},
			{name: "explicit height passes through", params: []any{"0x10"},
				want: []any{"0x10"}},
			{name: "pending passes through without establishment", params: []any{"pending"},
				want: []any{"pending"}},
		}
	default:
		// Methods without a declared reference: every request rides the
		// fan-out untouched and records no pin.
		return []planPrepareCase{
			{name: "no declared reference keeps the request untouched", params: []any{"0x1"},
				want: []any{"0x1"}},
		}
	}
}

func repeatAB(n int) string {
	out := ""
	for range n {
		out += "ab"
	}
	return out
}

// TestPlansDriveTheBehaviorMatrix verifies the compiled plans directly: the
// rule projection, the capability projection, and the prepare phase over the
// per-method fixtures above.
func TestPlansDriveTheBehaviorMatrix(t *testing.T) {
	for _, spec := range Specs() {
		t.Run(spec.Method, func(t *testing.T) {
			plan, err := CompilePlan(spec)
			if err != nil {
				t.Fatalf("compile plan: %v", err)
			}

			// The rule projection names exactly the declared facts.
			rule := plan.MethodRule()
			switch spec.Reference.Kind {
			case ReferenceShapeHashAt:
				if rule.Anchor != "hash@"+joinPositions(spec.Reference.Positions) {
					t.Fatalf("rule anchor = %q, want the declared hash positions", rule.Anchor)
				}
			case ReferenceShapeHeightAt:
				if rule.Anchor != "height@"+joinPositions(spec.Reference.Positions) {
					t.Fatalf("rule anchor = %q, want the declared height positions", rule.Anchor)
				}
			case ReferenceShapeHeightFieldsAt:
				if rule.Anchor == "" {
					t.Fatal("rule anchor empty for a field-anchored method")
				}
			default:
				if rule.Anchor != "" {
					t.Fatalf("rule anchor = %q, want none for an unanchored method", rule.Anchor)
				}
			}
			if spec.Reference.EIP1898 != (rule.BlockRef == rpc.RuleBlockRefEIP1898) {
				t.Fatalf("rule blockRef = %q, want the EIP-1898 declaration %v", rule.BlockRef, spec.Reference.EIP1898)
			}
			wantPending := ""
			switch spec.Pending.Mode {
			case PendingObserve:
				wantPending = rpc.RulePendingLocalObservation
			case PendingRefuse:
				wantPending = rpc.RulePendingLocalReject
			}
			if rule.Pending != wantPending {
				t.Fatalf("rule pending = %q, want %q", rule.Pending, wantPending)
			}

			// The capability projection follows the resolver class and the
			// declared hooks.
			if plan.Corroborating() != (spec.Resolver == ResolverStrict || spec.Resolver == ResolverBlock || spec.Resolver == ResolverCallRevert) {
				t.Fatalf("corroborating = %v for resolver class %q", plan.Corroborating(), spec.Resolver)
			}
			if (plan.Admit != nil) != (spec.Admit != nil) {
				t.Fatalf("admission hook presence = %v, want the spec's %v", plan.Admit != nil, spec.Admit != nil)
			}
			if (plan.Pending.Mode == PendingObserve) != (spec.Pending.Mode == PendingObserve) {
				t.Fatalf("pending observation declaration drifted")
			}

			// The prepare phase executes the declared shapes.
			for _, tc := range planPrepareCases(spec.Method) {
				t.Run(tc.name, func(t *testing.T) {
					req := rpc.Request{Method: spec.Method, Params: tc.params}
					prepared, pin, err := preparePlanRequest(t, plan, req)
					if tc.wantErr != nil {
						if !errors.Is(err, tc.wantErr) {
							t.Fatalf("err = %v, want %v", err, tc.wantErr)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if len(prepared.Params) != len(tc.want) {
						t.Fatalf("prepared = %#v, want %#v", prepared.Params, tc.want)
					}
					for i := range tc.want {
						if !prepareParamsEqual(prepared.Params[i], tc.want[i]) {
							t.Fatalf("prepared[%d] = %#v, want %#v", i, prepared.Params[i], tc.want[i])
						}
					}
					if pin != tc.pin {
						t.Fatalf("pin = %q, want %q", pin, tc.pin)
					}
					// A request that declares no reference need must never
					// demand establishment.
					if _, needed := plan.ReferenceRequirement(req); needed != tc.establish {
						t.Fatalf("reference requirement = %v, want %v", needed, tc.establish)
					}
				})
			}
		})
	}
}

// prepareParamsEqual compares one prepared parameter position: maps compare
// by their full contents, slices element-wise, everything else by identity
// of value.
func prepareParamsEqual(got, want any) bool {
	if gotMap, ok := got.(map[string]any); ok {
		wantMap, ok := want.(map[string]any)
		if !ok || len(gotMap) != len(wantMap) {
			return false
		}
		for key, value := range wantMap {
			other, present := gotMap[key]
			if !present || !prepareParamsEqual(value, other) {
				return false
			}
		}
		return true
	}
	if gotSlice, ok := got.([]any); ok {
		wantSlice, ok := want.([]any)
		if !ok || len(gotSlice) != len(wantSlice) {
			return false
		}
		for i := range wantSlice {
			if !prepareParamsEqual(gotSlice[i], wantSlice[i]) {
				return false
			}
		}
		return true
	}
	return got == want
}

func joinPositions(positions []int) string {
	out := ""
	for i, pos := range positions {
		if i > 0 {
			out += ","
		}
		out += string(rune('0' + pos))
	}
	return out
}

// TestCompilePlanCoversTheInventory is the registration gate: every
// registered method compiles as a plan, so the registry can install the
// whole catalog without a compiler surprise.
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

// The EIP-1898 inconsistency is a named compile error: a plan that declares
// the object form without exactly one anchored position cannot register.
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

// TestPlansServeTheDeclaredTag pins the establishment contract at the plan
// boundary: a rewrite consumes only corroborated evidence that serves the
// request's tag — each level keeps its meaning, and uncorroborated evidence
// never rewrites a request.
func TestPlansServeTheDeclaredTag(t *testing.T) {
	for _, spec := range Specs() {
		if spec.Reference.Kind == ReferenceShapeNone && !spec.Reference.EIP1898 {
			continue
		}
		t.Run(spec.Method, func(t *testing.T) {
			plan, err := CompilePlan(spec)
			if err != nil {
				t.Fatalf("compile plan: %v", err)
			}
			params := planPrepareCases(spec.Method)[0].params
			req := rpc.Request{Method: spec.Method, Params: params}
			request, needed := plan.ReferenceRequirement(req)
			if !needed {
				t.Fatal("the moving-tag fixture declared no reference need")
			}
			tag := string(request.Kind)

			// Evidence for a different level does not serve the request.
			wrong := fixtureReference(tag)
			if tag == string(rpc.ReferenceLatest) {
				wrong.Tag = string(rpc.ReferenceSafe)
			} else {
				wrong.Tag = ""
			}
			if _, _, err := plan.PrepareWithReference(req, wrong, true); !errors.Is(err, rpc.ErrReferenceRequired) {
				t.Fatalf("mismatched tag: err = %v, want ErrReferenceRequired", err)
			}
			// Uncorroborated evidence never rewrites a request.
			unconfirmed := fixtureReference(tag)
			unconfirmed.Status = rpc.Selected
			if _, _, err := plan.PrepareWithReference(req, unconfirmed, true); !errors.Is(err, rpc.ErrConflict) {
				t.Fatalf("uncorroborated: err = %v, want ErrConflict", err)
			}
			// Serving evidence rewrites and records the pin.
			_, pin, err := plan.PrepareWithReference(req, fixtureReference(tag), true)
			if err != nil {
				t.Fatalf("serving evidence failed: %v", err)
			}
			if pin == "" {
				t.Fatal("serving evidence recorded no pin")
			}
		})
	}
}

// fixtureReference is the fixture reference tagged for one request kind.
func fixtureReference(tag string) rpc.ReferenceEvidence {
	if tag == string(rpc.ReferenceLatest) {
		tag = ""
	}
	return rpc.ReferenceEvidence{Hash: fixtureHash, Height: fixtureHeight, Status: rpc.Corroborated, Tag: tag}
}
