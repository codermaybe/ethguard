package methods

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// testAnchors are the anchor doubles both shapes bind to: the compiled deps
// and the hand-written constructors receive the same readers, so prepared
// requests are directly comparable.
func testAnchors() (rpc.AnchorFunc, rpc.AnchorHeightFunc) {
	return func(context.Context) (string, error) { return "0xabc", nil },
		func(context.Context) (uint64, error) { return 16, nil }
}

// compiledFor assembles one spec with the test anchors.
func compiledFor(t *testing.T, spec MethodSpec) rpc.Policy {
	t.Helper()
	hash, height := testAnchors()
	return Compile(spec, CompileDeps{AnchorHash: hash, AnchorHeight: height})
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

// capabilities lists the optional capabilities a policy exposes, by name.
// The compiled chain must expose exactly the set the constructor exposes:
// capability promotion follows the resolver kind, never the decorator count.
func capabilities(p rpc.Policy) []string {
	var names []string
	add := func(ok bool, name string) {
		if ok {
			names = append(names, name)
		}
	}
	_, corroborating := p.(rpc.CorroboratingPolicy)
	add(corroborating, "corroborating")
	_, outcome := p.(rpc.OutcomeResolver)
	add(outcome, "outcome")
	_, corroboratedOutcome := p.(rpc.CorroboratingOutcomeResolver)
	add(corroboratedOutcome, "corroborated-outcome")
	_, agreement := p.(rpc.AgreementPolicy)
	add(agreement && p.(rpc.AgreementPolicy).RequiresAgreement(), "agreement")
	_, admission := p.(rpc.AdmissionPolicy)
	add(admission, "admission")
	_, rpcError := p.(rpc.RPCErrorPolicy)
	add(rpcError && p.(rpc.RPCErrorPolicy).IncludeRPCError(), "rpc-error-bodies")
	_, local := p.(rpc.LocalObservationPolicy)
	add(local, "local-observation")
	_, single := p.(rpc.SingleSourcePolicy)
	add(single && p.(rpc.SingleSourcePolicy).SingleSource(), "single-source")
	_, blockRef := p.(rpc.BlockRefProvider)
	add(blockRef, "block-ref")
	_, requirement := p.(rpc.ReferenceRequirement)
	add(requirement, "reference-requirement")
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

// TestCompiledPoliciesMatchConstructors holds the spec inventory and the
// hand-written constructors together: for every registered method, the
// compiled policy and the constructor policy expose the same method-rule
// projection, the same capability set, the same declared reference need, and
// the same prepared requests and decisions over the same fixtures. This is
// the gate that lets the composition root install compiled policies.
func TestCompiledPoliciesMatchConstructors(t *testing.T) {
	hash, height := testAnchors()
	ctx := context.Background()
	for _, spec := range Specs() {
		spec := spec
		t.Run(spec.Method, func(t *testing.T) {
			compiled := compiledFor(t, spec)
			constructed := constructorFor(t, spec.Method, hash, height)

			compiledRule, _ := compiled.(rpc.MethodRuleSource).MethodRule()
			constructedRule, _ := constructed.(rpc.MethodRuleSource).MethodRule()
			if compiledRule != constructedRule {
				t.Fatalf("method rule mismatch:\ncompiled    %+v\nconstructor %+v", compiledRule, constructedRule)
			}

			compiledCaps, constructedCaps := capabilities(compiled), capabilities(constructed)
			if !reflect.DeepEqual(compiledCaps, constructedCaps) {
				t.Fatalf("capability mismatch:\ncompiled    %v\nconstructor %v", compiledCaps, constructedCaps)
			}

			compiledReq, compiledOK := compiled.(rpc.ReferenceRequirement)
			constructedReq, constructedOK := constructed.(rpc.ReferenceRequirement)
			if compiledOK != constructedOK {
				t.Fatalf("reference-requirement presence mismatch: compiled %v constructor %v", compiledOK, constructedOK)
			}
			if compiledOK {
				for _, params := range prepareFixtures(spec.Method) {
					declaredCompiled, needCompiled := compiledReq.ReferenceFor(rpc.Request{Method: spec.Method, Params: params})
					declaredConstructed, needConstructed := constructedReq.ReferenceFor(rpc.Request{Method: spec.Method, Params: params})
					if needCompiled != needConstructed || declaredCompiled != declaredConstructed {
						t.Fatalf("reference declaration mismatch for %v: compiled (%v,%v) constructor (%v,%v)",
							params, declaredCompiled, needCompiled, declaredConstructed, needConstructed)
					}
				}
			}

			for _, params := range prepareFixtures(spec.Method) {
				compiledPrepared, compiledErr := compiled.Prepare(ctx, rpc.Request{Method: spec.Method, Params: params})
				constructedPrepared, constructedErr := constructed.Prepare(ctx, rpc.Request{Method: spec.Method, Params: params})
				if (compiledErr == nil) != (constructedErr == nil) {
					t.Fatalf("prepare error mismatch for %v: compiled %v constructor %v", params, compiledErr, constructedErr)
				}
				if compiledErr != nil {
					continue
				}
				if !reflect.DeepEqual(preparedJSON(t, compiledPrepared), preparedJSON(t, constructedPrepared)) {
					t.Fatalf("prepared request mismatch for %v:\ncompiled    %s\nconstructor %s",
						params, preparedJSON(t, compiledPrepared), preparedJSON(t, constructedPrepared))
				}
			}

			// The same observations must resolve to the same decision under
			// both assemblies, whatever the resolver class does with them.
			obs := []rpc.Observation{
				{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: json.RawMessage(`"0x64"`)},
				{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"0x64"`)},
				{Source: rpc.SourceRef{Name: "c", ID: "c"}, Value: json.RawMessage(`"0x65"`)},
			}
			compiledDecision, compiledResolveErr := compiled.Resolve(obs)
			constructedDecision, constructedResolveErr := constructed.Resolve(obs)
			if (compiledResolveErr == nil) != (constructedResolveErr == nil) {
				t.Fatalf("resolve error mismatch: compiled %v constructor %v", compiledResolveErr, constructedResolveErr)
			}
			if compiledResolveErr != nil && compiledResolveErr.Error() != constructedResolveErr.Error() {
				t.Fatalf("resolve error text mismatch: compiled %v constructor %v", compiledResolveErr, constructedResolveErr)
			}
			compiledDecision.Faults = nil
			constructedDecision.Faults = nil
			if !reflect.DeepEqual(decisionJSON(t, compiledDecision), decisionJSON(t, constructedDecision)) {
				t.Fatalf("decision mismatch:\ncompiled    %s\nconstructor %s",
					decisionJSON(t, compiledDecision), decisionJSON(t, constructedDecision))
			}
		})
	}
}

// The reference declaration must be reachable through the decorator base on
// every compiled chain, so the aggregator's establishment phase sees what
// the inner anchoring layer needs. The probe uses each method's first
// moving-tag fixture so the anchored position actually carries a tag.
func TestCompiledReferenceDeclarationsAreVisible(t *testing.T) {
	for _, spec := range Specs() {
		compiled := compiledFor(t, spec)
		requirement, ok := compiled.(rpc.ReferenceRequirement)
		if !ok {
			if spec.Reference.Kind != ReferenceShapeNone {
				t.Fatalf("%s declares a reference but the compiled chain lost the declaration", spec.Method)
			}
			continue
		}
		needed := spec.Reference.Kind != ReferenceShapeNone
		params := prepareFixtures(spec.Method)[0]
		_, declared := requirement.ReferenceFor(rpc.Request{Method: spec.Method, Params: params})
		if declared != needed {
			t.Fatalf("%s: declared=%v, want %v for %v", spec.Method, declared, needed, params)
		}
	}
}

func referenceProbeParams(method string) []any {
	switch method {
	case MethodGetStorageAt:
		return []any{"0x1", "0x0", "latest"}
	case MethodGetLogs:
		return []any{map[string]any{"fromBlock": "latest", "toBlock": "latest"}}
	default:
		return []any{"0x1", "latest"}
	}
}

// The resolver kinds pin the capability promotion: only strict-shaped
// resolvers corroborate, and the corroboration composition invariant must
// hold through the compiler exactly as it does through the constructors.
func TestCompiledCapabilityPromotionFollowsResolverKind(t *testing.T) {
	for _, spec := range Specs() {
		compiled := compiledFor(t, spec)
		_, corroborating := compiled.(rpc.CorroboratingPolicy)
		switch spec.Resolver {
		case ResolverStrict, ResolverBlock, ResolverCallRevert:
			if !corroborating {
				t.Fatalf("%s: %s must corroborate", spec.Method, spec.Resolver)
			}
		default:
			if corroborating {
				t.Fatalf("%s: %s must not corroborate", spec.Method, spec.Resolver)
			}
		}
	}
}

// A spec whose declaration and rewrite diverge fails loudly: the reference
// declaration is derived from the same positions the anchor layer rewrites,
// so Compile panics on an inconsistent EIP-1898 declaration instead of
// assembling a silent mismatch.
func TestCompileRejectsInconsistentEIP1898Declaration(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("an EIP-1898 declaration without positions must panic")
		}
	}()
	hash, height := testAnchors()
	Compile(MethodSpec{
		Method:    "eth_broken",
		Resolver:  ResolverStrict,
		Normalize: wire.NormalizeWireQuantity,
		Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, EIP1898: true},
	}, CompileDeps{AnchorHash: hash, AnchorHeight: height})
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
