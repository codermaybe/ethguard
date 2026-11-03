package methods

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// The plan-level equivalence gate: for every registered method, the explicit
// execution plan and the hand-written constructor chain agree on the
// method-rule projection, the capability projection, the declared reference
// need, the prepared requests and pins, and the resolved decisions. This is
// the gate that lets the default registry install plans only.

// testAnchors are the anchor doubles the hand-written constructors bind to,
// so the constructor chains' prepared requests are directly comparable with
// a plan rewrite from the same established evidence.
func testAnchors() (rpc.AnchorFunc, rpc.AnchorHeightFunc) {
	return func(context.Context) (string, error) { return "0xabc", nil },
		func(context.Context) (uint64, error) { return 16, nil }
}

// constructorFor assembles the hand-written policy for one method.
func constructorFor(t *testing.T, method string, hash rpc.AnchorFunc, height rpc.AnchorHeightFunc, opts ...rpc.StrictOption) rpc.Policy {
	t.Helper()
	switch method {
	case MethodChainID:
		return ChainIDPolicy(opts...)
	case MethodGetBlockByHash:
		return BlockByHashPolicy(opts...)
	case MethodGetBlockByNumber:
		return BlockByNumberPolicy(opts...)
	case MethodGetBalance:
		return GetBalancePolicy(hash, opts...)
	case MethodGetTransactionReceipt:
		return TransactionReceiptPolicy(opts...)
	case MethodGetTransactionByHash:
		return TransactionByHashPolicy(opts...)
	case MethodGetBlockReceipts:
		return BlockReceiptsPolicy(hash, opts...)
	case MethodGetTransactionCount:
		return GetTransactionCountPolicy(hash, opts...)
	case MethodGetStorageAt:
		return GetStorageAtPolicy(hash, opts...)
	case MethodGetCode:
		return GetCodePolicy(hash, opts...)
	case MethodCall:
		return CallPolicy(hash, opts...)
	case MethodEstimateGas:
		return EstimateGasPolicy(hash)
	case MethodGetLogs:
		return LogsPolicy(height, opts...)
	case MethodFeeHistory:
		return FeeHistoryPolicy(height, opts...)
	case MethodGasPrice:
		return GasPricePolicy()
	case MethodMaxPriorityFeePerGas:
		return MaxPriorityFeePerGasPolicy()
	case MethodBlobBaseFee:
		return BlobBaseFeePolicy()
	case MethodBlockNumber:
		return BlockNumberPolicy()
	case MethodCreateAccessList:
		return CreateAccessListPolicy(hash, opts...)
	case MethodPeerCount:
		return PeerCountPolicy()
	case MethodSyncing:
		return SyncingPolicy()
	case MethodNetVersion:
		return NetVersionPolicy(opts...)
	case MethodListening:
		return ListeningPolicy()
	}
	t.Fatalf("no constructor fixture for %s", method)
	return nil
}

// capabilities lists the capabilities a policy's behavior actually exposes,
// by name. The plan's projection must name exactly the set the constructor
// exposes: the resolver class, not a decorator count, decides what a method
// does. The decorator base's forwarding methods (outcome statements, block
// references, reference declarations) answer their assertions even when the
// chain carries no such behavior, so they stay out of this list — the pin
// comparison and the resolution comparison below hold those behaviors
// directly.
func capabilities(p rpc.Policy) []string {
	var names []string
	add := func(ok bool, name string) {
		if ok {
			names = append(names, name)
		}
	}
	_, corroborating := p.(rpc.CorroboratingPolicy)
	add(corroborating, "corroborating")
	_, agreement := p.(rpc.AgreementPolicy)
	add(agreement && p.(rpc.AgreementPolicy).RequiresAgreement(), "agreement")
	add(hasRealAdmission(p), "admission")
	_, rpcError := p.(rpc.RPCErrorPolicy)
	add(rpcError && p.(rpc.RPCErrorPolicy).IncludeRPCError(), "rpc-error-bodies")
	_, local := p.(rpc.LocalObservationPolicy)
	add(local, "local-observation")
	return names
}

// hasRealAdmission distinguishes a method's own admission hook from the
// decorator base's vacuous forwarding: every Decorator-embedding chain
// answers the AdmissionPolicy assertion, but a chain without a real hook
// admits nothing. A real hook rejects a malformed body, so the probe asks
// exactly that; a full-block-shaped request keeps the block wrappers past
// their null and header-only shortcuts.
func hasRealAdmission(p rpc.Policy) bool {
	admission, ok := p.(rpc.AdmissionPolicy)
	if !ok {
		return false
	}
	_, err := admission.Admit(context.Background(),
		rpc.Request{Method: "probe", Params: []any{"0x64", true}},
		json.RawMessage("{"), nil)
	return err != nil
}

// planCapabilities projects the plan's declared facts into the same
// vocabulary: the resolver class, the admission hook, the error-body
// opt-in, and the pending-observation route.
func planCapabilities(plan rpc.ReadPlan) []string {
	var names []string
	add := func(ok bool, name string) {
		if ok {
			names = append(names, name)
		}
	}
	add(plan.Corroborating(), "corroborating")
	agreement, hasAgreement := plan.Resolver.(rpc.AgreementPolicy)
	add(hasAgreement && agreement.RequiresAgreement(), "agreement")
	add(plan.Admit != nil, "admission")
	provider, hasProvider := plan.Resolver.(rpc.RPCErrorPolicy)
	add(hasProvider && provider.IncludeRPCError(), "rpc-error-bodies")
	add(plan.Pending.Mode == PendingObserve, "local-observation")
	return names
}

// prepareFixtures gives every method a request whose prepared shape the two
// assemblies must agree on: a moving tag where the method anchors, an
// EIP-1898 object where the object form is declared, and pending where the
// method observes it.
func prepareFixtures(method string) [][]any {
	switch method {
	case MethodGetBalance, MethodGetTransactionCount, MethodGetCode, MethodCall, MethodCreateAccessList, MethodEstimateGas:
		return [][]any{
			{"0x1", "latest"},
			{"0x1", map[string]any{"blockNumber": "safe"}},
			{"0x1", "pending"},
		}
	case MethodGetStorageAt:
		return [][]any{
			{"0x1", "0x0", "latest"},
			{"0x1", "0x0", "pending"},
		}
	case MethodGetBlockReceipts:
		return [][]any{{"latest"}, {"pending"}}
	case MethodGetLogs:
		return [][]any{
			{map[string]any{"fromBlock": "latest", "toBlock": "latest"}},
			{map[string]any{"fromBlock": "pending"}},
		}
	case MethodFeeHistory:
		return [][]any{{"0x5", "latest", nil}, {"0x5", "pending", nil}}
	case MethodGetBlockByNumber:
		return [][]any{{"latest"}, {"pending"}}
	default:
		return [][]any{{"0x1"}}
	}
}

// planEvidenceFor builds the established evidence a planned read would hold
// for one request: the same hash and height the constructor's test anchors
// return, tagged with the level the request declared.
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

// TestPlansMatchConstructors holds the plan inventory and the hand-written
// constructors together: for every registered method, the plan projects the
// same method rule, the same capability set, the same declared reference
// need, the same prepared requests and pins, and the same decisions over the
// same fixtures as the constructor chain.
func TestPlansMatchConstructors(t *testing.T) {
	hash, height := testAnchors()
	ctx := context.Background()
	for _, spec := range Specs() {
		t.Run(spec.Method, func(t *testing.T) {
			plan, err := CompilePlan(spec)
			if err != nil {
				t.Fatalf("compile plan: %v", err)
			}
			constructed := constructorFor(t, spec.Method, hash, height)

			if planRule, constructedRule := plan.MethodRule(), methodRuleOf(t, constructed); planRule != constructedRule {
				t.Fatalf("method rule mismatch:\nplan        %+v\nconstructor %+v", planRule, constructedRule)
			}

			planCaps, constructedCaps := planCapabilities(plan), capabilities(constructed)
			if !reflect.DeepEqual(planCaps, constructedCaps) {
				t.Fatalf("capability mismatch:\nplan        %v\nconstructor %v", planCaps, constructedCaps)
			}

			for _, params := range prepareFixtures(spec.Method) {
				req := rpc.Request{Method: spec.Method, Params: params}
				// The plan's Validate / Prepare phase: the method's own
				// validation and its executable pending rejection.
				if plan.Validate != nil {
					if err := plan.Validate(ctx, req); err != nil {
						constructorFailedTheSame(t, req, err, constructed)
						continue
					}
				}
				if plan.PendingReject != nil {
					if err := plan.PendingReject(req); err != nil {
						constructorFailedTheSame(t, req, err, constructed)
						continue
					}
				}
				evidence, established := planEvidenceFor(t, plan, params)
				preparedPlan, pin, planErr := plan.PrepareWithReference(req, evidence, established)
				preparedConstructor, constructorErr := constructed.Prepare(ctx, req)
				if (planErr == nil) != (constructorErr == nil) {
					t.Fatalf("prepare error mismatch for %v: plan %v constructor %v", params, planErr, constructorErr)
				}
				if planErr != nil {
					if planErr.Error() != constructorErr.Error() {
						t.Fatalf("prepare error text mismatch for %v: plan %q constructor %q", params, planErr, constructorErr)
					}
					continue
				}
				if !reflect.DeepEqual(preparedJSON(t, preparedPlan), preparedJSON(t, preparedConstructor)) {
					t.Fatalf("prepared request mismatch for %v:\nplan        %s\nconstructor %s",
						params, preparedJSON(t, preparedPlan), preparedJSON(t, preparedConstructor))
				}
				// The pin the plan records must be the same block reference
				// the constructor's BlockRef providers report.
				if pin != "" {
					provider, ok := constructed.(rpc.BlockRefProvider)
					if !ok {
						t.Fatalf("plan recorded pin %q but the constructor reports none", pin)
					}
					if constructorPin := provider.BlockRef(req, preparedConstructor); constructorPin != pin {
						t.Fatalf("pin mismatch for %v: plan %q constructor %q", params, pin, constructorPin)
					}
				}
			}

			// The same observations must resolve to the same decision,
			// whatever the resolver class does with them.
			obs := []rpc.Observation{
				{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: json.RawMessage(`"0x64"`)},
				{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"0x64"`)},
				{Source: rpc.SourceRef{Name: "c", ID: "c"}, Value: json.RawMessage(`"0x65"`)},
			}
			planDecision, planResolveErr := plan.Resolver.Resolve(obs)
			constructedDecision, constructedResolveErr := constructed.Resolve(obs)
			if (planResolveErr == nil) != (constructedResolveErr == nil) {
				t.Fatalf("resolve error mismatch: plan %v constructor %v", planResolveErr, constructedResolveErr)
			}
			if planResolveErr != nil && planResolveErr.Error() != constructedResolveErr.Error() {
				t.Fatalf("resolve error text mismatch: plan %q constructor %q", planResolveErr, constructedResolveErr)
			}
			planDecision.Faults = nil
			constructedDecision.Faults = nil
			if !reflect.DeepEqual(decisionJSON(t, planDecision), decisionJSON(t, constructedDecision)) {
				t.Fatalf("decision mismatch:\nplan        %s\nconstructor %s",
					decisionJSON(t, planDecision), decisionJSON(t, constructedDecision))
			}
		})
	}
}

func methodRuleOf(t *testing.T, policy rpc.Policy) rpc.MethodRule {
	t.Helper()
	source, ok := policy.(rpc.MethodRuleSource)
	if !ok {
		t.Fatalf("%T exposes no method rule", policy)
	}
	rule, _ := source.MethodRule()
	return rule
}

// constructorFailedTheSame pins that a plan's local validation failure is
// the same failure the constructor's Prepare reports: same class, same text.
func constructorFailedTheSame(t *testing.T, req rpc.Request, planErr error, constructed rpc.Policy) {
	t.Helper()
	_, constructorErr := constructed.Prepare(context.Background(), req)
	if constructorErr == nil || constructorErr.Error() != planErr.Error() {
		t.Fatalf("validation failure mismatch for %v: plan %q constructor %v", req.Params, planErr, constructorErr)
	}
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

func preparedJSON(t *testing.T, req rpc.Request) []byte {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func decisionJSON(t *testing.T, d rpc.Decision) []byte {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
