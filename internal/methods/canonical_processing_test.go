package methods

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

// Canonical methods compile a processed-only execution contract and keep
// their standalone diagnostic normalizer. TestRegisteredSpecsDeclareOnlyReviewedProcessors below pins the
// reviewed transaction and receipt methods in both directions: every reviewed
// method declares a processor, and no other registered method does.
func processingSpec(method string, mutate func(*MethodSpec)) MethodSpec {
	spec := MethodSpec{
		Method:      method,
		Resolver:    ResolverStrict,
		Normalize:   func(raw json.RawMessage) (string, error) { return string(raw), nil },
		KeySource:   rpcengine.RuleKeyCanonicalBytes,
		CompareKind: rpcengine.RuleCompareMethodSpecific,
	}
	if mutate != nil {
		mutate(&spec)
	}
	return spec
}

func TestCompilePlanProcessorRequiresStrictCanonicalBytes(t *testing.T) {
	processor := func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
		return rpcengine.CanonicalResult{}, nil
	}
	cases := []struct {
		name   string
		mutate func(*MethodSpec)
	}{
		{"median-resolver", func(s *MethodSpec) {
			s.Resolver = ResolverMedian
			s.KeySource = ""
			s.CompareKind = rpcengine.RuleCompareQuantity
		}},
		{"local-resolver", func(s *MethodSpec) { s.Resolver = ResolverLocal; s.KeySource = "" }},
		{"call-revert-resolver", func(s *MethodSpec) { s.Resolver = ResolverCallRevert; s.KeySource = "" }},
		{"normalizer-key", func(s *MethodSpec) { s.KeySource = rpcengine.RuleKeyNormalizer }},
		{"no-normalizer-adapter", func(s *MethodSpec) { s.Normalize = nil }},
		{"raw-encoder", func(s *MethodSpec) { s.Encode = func(raw json.RawMessage) (json.RawMessage, error) { return raw, nil } }},
		{"raw-admission", func(s *MethodSpec) {
			s.Admit = func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error) {
				return nil, nil
			}
		}},
		{"pending-observation", func(s *MethodSpec) { s.Pending = rpcengine.PendingSpec{Mode: rpcengine.PendingObserve} }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			spec := processingSpec("test_processor_"+item.name, func(s *MethodSpec) {
				s.ProcessCanonical = processor
				item.mutate(s)
			})
			if _, err := CompilePlan(spec); err == nil {
				t.Fatalf("illegal processing assembly compiled: %+v", spec)
			}
		})
	}
}

func TestCompilePlanCanonicalContractHasIndependentNormalize(t *testing.T) {
	calls := 0
	spec := processingSpec("test_processor_valid", func(s *MethodSpec) {
		s.ProcessCanonical = func(_ context.Context, _ rpc.Request, raw json.RawMessage, _ rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
			calls++
			return rpcengine.NewCanonicalResult(raw, nil)
		}
	})
	plan, err := CompilePlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Canonical == nil || plan.Resolver != nil || plan.Admit != nil {
		t.Fatalf("mixed or absent execution: %+v", plan)
	}
	raw := json.RawMessage(`"probe"`)
	if key, err := plan.Normalize(raw); err != nil || key != string(raw) || calls != 0 {
		t.Fatalf("standalone normalization = %q, %v; processing calls %d", key, err, calls)
	}
	result, err := plan.Canonical.Process(context.Background(), rpc.Request{Method: plan.Method}, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := rpcengine.NewCanonicalObservation(rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}}, result)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := plan.Canonical.Resolver.ResolveCanonicalCorroborated(rpcengine.Corroboration{Floor: 1}, []rpcengine.CanonicalObservation{obs})
	if err != nil || string(outcome.Decision.Value) != string(raw) || calls != 1 {
		t.Fatalf("canonical resolution = %+v, %v; calls=%d", outcome, err, calls)
	}
	if _, err := assembleResolver(spec, nil); err == nil {
		t.Fatal("canonical spec assembled a raw resolver")
	}
}

func TestRegisteredSpecsDeclareOnlyReviewedProcessors(t *testing.T) {
	reviewed := map[string]bool{
		MethodGetTransactionByHash:              true,
		MethodGetTransactionByBlockHashAndIndex: true,
		MethodGetTransactionReceipt:             true,
		MethodGetBlockReceipts:                  true,
	}
	for _, spec := range Specs() {
		if got, want := spec.ProcessCanonical != nil, reviewed[spec.Method]; got != want {
			t.Errorf("registered method %s declares processor=%t, reviewed=%t", spec.Method, got, want)
		}
		delete(reviewed, spec.Method)
	}
	for method := range reviewed {
		t.Errorf("reviewed processor method %s is not registered", method)
	}
}
