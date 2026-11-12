package methods

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// Compiler-side contracts of the canonical processing dimension: a declared
// processor is a strict-only, canonical-bytes-only assembly with its raw
// compatibility adapters intact, and the compiled plan carries the same
// processor. No registered method declares one yet — the dimension exists so
// the transaction pilot can adopt it without another seam revision.

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
	observations := []rpc.Observation{{Source: rpc.SourceRef{ID: "a"}, Value: raw}}
	if _, err := plan.Resolver.Resolve(observations); err != nil {
		t.Fatalf("raw route broken after processor declaration: %v", err)
	}
}

func TestRegisteredSpecsDeclareNoProcessor(t *testing.T) {
	for _, spec := range Specs() {
		if spec.ProcessCanonical != nil {
			t.Fatalf("registered method %s already declares a processor: production wiring is a separate reviewed step", spec.Method)
		}
	}
}
