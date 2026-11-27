package methods

import (
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// assembleResolver is the value-domain assembly shared by CompilePlan and
// reference establishment. Preparation and admission remain plan hooks.
func assembleResolver(spec MethodSpec, opts []rpc.StrictOption) (rpc.Policy, error) {
	switch spec.Resolver {
	case ResolverStrict, ResolverCallRevert:
		opts = append([]rpc.StrictOption{rpc.WithRuleCompareKind(spec.CompareKind)}, opts...)
		switch spec.KeySource {
		case rpc.RuleKeyCanonicalBytes:
			opts = append([]rpc.StrictOption{rpc.WithCanonicalValue(spec.Encode)}, opts...)
		case rpc.RuleKeyNormalizer:
			if spec.Encode != nil {
				opts = append([]rpc.StrictOption{rpc.WithCanonicalObject(spec.Encode)}, opts...)
			}
		}
	}

	switch spec.Resolver {
	case ResolverStrict:
		return rpc.Strict(spec.Normalize, opts...), nil
	case ResolverMedian:
		// The median resolver pins its comparison-key class internally; the
		// spec's declaration is informational and must agree.
		if spec.CompareKind != "" && spec.CompareKind != rpc.RuleCompareQuantity {
			return nil, fmt.Errorf("methods: median resolver %s declares compare kind %q, want %q",
				spec.Method, spec.CompareKind, rpc.RuleCompareQuantity)
		}
		medianOpts := []rpc.MedianOption{}
		if spec.Encode != nil {
			medianOpts = append(medianOpts, rpc.WithMedianCanonical(spec.Encode))
		}
		return rpc.Median(spec.Normalize, medianOpts...), nil
	case ResolverLocal:
		localOpts := []rpc.LocalOption{rpc.WithLocalRuleCompareKind(spec.CompareKind)}
		if spec.Encode != nil {
			localOpts = append(localOpts, rpc.WithLocalCanonical(spec.Encode))
		}
		return rpc.Local(spec.Normalize, localOpts...), nil
	case ResolverCallRevert:
		// The controlled escape hatch: the call-revert resolver's value
		// domain (error bodies in, canonical success encoding) is the
		// escape's own identity, fixed here and pinned by the call-revert
		// tests — not spec fields.
		inner := append([]rpc.StrictOption{rpc.WithRPCErrorBodies(), rpc.WithCanonicalValue(encodeCallSuccess)}, opts...)
		return rpc.NewCallRevertPolicy(spec.Normalize, inner...), nil
	default:
		return nil, fmt.Errorf("methods: unknown resolver kind %q for %s", spec.Resolver, spec.Method)
	}
}

// CompilePlan assembles one MethodSpec into the explicit execution plan a
// registry drives through the fixed phase pipeline.
//
// The plan carries the method's declared facts — resolver class and value,
// reference shape, pending handling, hooks, projector binding — and no
// network callback at all: reference data arrives as established evidence
// at execution time, and the admission caller is a runtime dependency. The
// resolver owns only normalization and adjudication; the plan owns the
// preparation, admission, reference, and pending declarations.
//
// Compilation rejects an impossible declaration before it can register:
// a missing comparison key, an unknown resolver class, an illegal
// reference or pending shape, and a declared key source that contradicts
// the resolver's own are all composition errors, not runtime surprises.
func CompilePlan(spec MethodSpec) (rpc.ReadPlan, error) {
	if err := validatePlanComposition(spec); err != nil {
		return rpc.ReadPlan{}, err
	}
	resolver, err := assembleResolver(spec, nil)
	if err != nil {
		return rpc.ReadPlan{}, err
	}
	plan := rpc.ReadPlan{
		Method:           spec.Method,
		Resolver:         resolver,
		ResolverKind:     spec.Resolver,
		Reference:        spec.Reference,
		Pending:          spec.Pending,
		Prepare:          spec.Prepare,
		Admit:            spec.Admit,
		ProcessCanonical: spec.ProcessCanonical,
		ProjectorID:      spec.ObservationProjectorID,
	}
	// The declared pending rejection executes locally only when the method
	// does not own its rejection in the validate hook; otherwise the
	// declaration contributes the rule column alone.
	if spec.Pending.Mode == PendingRefuse && spec.Prepare == nil {
		plan.PendingReject = rpc.PendingRejectAt(spec.Pending.Pos, spec.Method)
	}
	if err := validatePlanBindings(plan, spec); err != nil {
		return rpc.ReadPlan{}, err
	}
	return plan, nil
}
