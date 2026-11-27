package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// Compiler-side contracts of the canonical processing dimension: a declared
// processor is a strict-only, canonical-bytes-only assembly with its raw
// compatibility adapters intact, and the compiled plan carries the same
// processor. TestRegisteredSpecsDeclareOnlyReviewedProcessors below pins the
// reviewed transaction and receipt methods in both directions: every reviewed
// method declares a processor, and no other registered method does.
func processingSpec(method string, mutate func(*MethodSpec)) MethodSpec {
	spec := MethodSpec{
		Method:      method,
		Resolver:    ResolverStrict,
		Normalize:   func(raw json.RawMessage) (string, error) { return string(raw), nil },
		Encode:      func(raw json.RawMessage) (json.RawMessage, error) { return raw, nil },
		KeySource:   rpc.RuleKeyCanonicalBytes,
		CompareKind: rpc.RuleCompareMethodSpecific,
	}
	if mutate != nil {
		mutate(&spec)
	}
	return spec
}

func TestCompilePlanProcessorRequiresStrictCanonicalBytes(t *testing.T) {
	processor := func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (rpc.CanonicalResult, error) {
		return rpc.CanonicalResult{}, nil
	}
	cases := []struct {
		name   string
		mutate func(*MethodSpec)
	}{
		{"median-resolver", func(s *MethodSpec) {
			s.Resolver = ResolverMedian
			s.KeySource = ""
			s.CompareKind = rpc.RuleCompareQuantity
		}},
		{"block-resolver", func(s *MethodSpec) { s.Resolver = ResolverBlock; s.ByHash = true }},
		{"call-revert-resolver", func(s *MethodSpec) { s.Resolver = ResolverCallRevert; s.KeySource = "" }},
		{"normalizer-key", func(s *MethodSpec) { s.KeySource = rpc.RuleKeyNormalizer }},
		{"no-normalizer-adapter", func(s *MethodSpec) { s.Normalize = nil }},
		{"no-encoder-adapter", func(s *MethodSpec) { s.Encode = nil; s.KeySource = rpc.RuleKeyNormalizer }},
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

func TestCompilePlanCarriesProcessorWithIntactAdapters(t *testing.T) {
	calls := 0
	processor := func(ctx context.Context, req rpc.Request, raw json.RawMessage, caller rpc.AdmissionCaller) (rpc.CanonicalResult, error) {
		calls++
		return rpc.NewCanonicalResult(raw, nil)
	}
	spec := processingSpec("test_processor_valid", func(s *MethodSpec) {
		s.ProcessCanonical = processor
		s.Admit = func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error) {
			return nil, nil
		}
	})
	plan, err := CompilePlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProcessCanonical == nil {
		t.Fatal("compiled plan dropped the processor")
	}
	// The raw route stays fully assembled beside the processor: the plan's
	// admission hook is intact and the resolver still answers raw entries.
	if plan.Admit == nil {
		t.Fatal("processor declaration nilled the raw admission adapter")
	}
	if _, ok := plan.Resolver.(rpc.CanonicalOutcomeResolver); !ok {
		t.Fatal("compiled resolver cannot consume canonical observations")
	}
	raw := json.RawMessage(`"probe"`)
	observations := []rpc.Observation{{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}}}
	if _, err := plan.Resolver.Resolve(observations); err != nil {
		t.Fatalf("raw route broken after processor declaration: %v", err)
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

// TestTransactionKeySourceSwitchKeepsClusters is the equivalence evidence for
// the two pilot methods' key-source switch: the old declaration (normalizer
// key with a canonical object encoder) and the compiled plan (canonical-bytes
// key) cluster the same observations identically — same support, same
// representative, same committed bytes — because this method's normalizer
// returns exactly its encoder's output.
func TestTransactionKeySourceSwitchKeepsClusters(t *testing.T) {
	raw := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	variants := []json.RawMessage{
		raw,
		foldHex(t, injectField(t, raw, "vendorExtension", `"x"`)),
	}
	for _, method := range []string{MethodGetTransactionByHash, MethodGetTransactionByBlockHashAndIndex} {
		t.Run(method, func(t *testing.T) {
			spec, ok := SpecFor(method)
			if !ok || spec.Normalize == nil || spec.Encode == nil {
				t.Fatalf("%s: missing raw hooks", method)
			}
			legacy := rpc.Strict(spec.Normalize,
				rpc.WithRuleCompareKind(spec.CompareKind),
				rpc.WithCanonicalObject(spec.Encode),
				rpc.WithQuorum(2))
			plan, err := CompilePlan(spec)
			if err != nil {
				t.Fatal(err)
			}
			observations := make([]rpc.Observation, 0, len(variants))
			for i, value := range variants {
				id := string(rune('a' + i))
				observations = append(observations, rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: id}, Evidence: json.RawMessage(`"` + id + `"`), Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: value}}})
			}
			legacyDecision, legacyErr := legacy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, observations)
			plannedDecision, plannedErr := plan.Resolver.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, observations)
			if (legacyErr == nil) != (plannedErr == nil) {
				t.Fatalf("verdicts differ: legacy=%v planned=%v", legacyErr, plannedErr)
			}
			if legacyErr != nil {
				return
			}
			if legacyDecision.Decision.Status != plannedDecision.Decision.Status || !bytes.Equal(legacyDecision.Decision.Value, plannedDecision.Decision.Value) {
				t.Fatalf("decisions differ: %+v vs %+v", legacyDecision, plannedDecision)
			}
			if legacyDecision.Decision.Representative.ID != plannedDecision.Decision.Representative.ID || len(legacyDecision.Decision.Support) != len(plannedDecision.Decision.Support) {
				t.Fatalf("cluster shape differs: %+v vs %+v", legacyDecision, plannedDecision)
			}
			// Source order cannot change the clustered outcome under either
			// declaration.
			reversed := []rpc.Observation{observations[1], observations[0]}
			legacyReversed, legacyRevErr := legacy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, reversed)
			plannedReversed, plannedRevErr := plan.Resolver.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, reversed)
			if legacyRevErr != nil || plannedRevErr != nil || string(legacyReversed.Decision.Value) != string(plannedReversed.Decision.Value) {
				t.Fatalf("reversed order diverged: %v / %v", legacyRevErr, plannedRevErr)
			}
		})
	}
}
