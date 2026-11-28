package methods

import (
	"fmt"

	"github.com/codermaybe/ethguard/internal/rpcengine"
)

// assembleResolver is the value-domain assembly shared by CompilePlan and
// reference establishment. Preparation and admission remain plan hooks.
func assembleResolver(spec MethodSpec, opts []rpcengine.StrictOption) (rpcengine.Resolver, error) {
	if spec.ProcessCanonical != nil {
		return nil, fmt.Errorf("methods: %s uses canonical execution", spec.Method)
	}
	switch spec.Resolver {
	case ResolverStrict, ResolverCallRevert:
		opts = append([]rpcengine.StrictOption{rpcengine.WithRuleCompareKind(spec.CompareKind)}, opts...)
		switch spec.KeySource {
		case rpcengine.RuleKeyCanonicalBytes:
			opts = append([]rpcengine.StrictOption{rpcengine.WithCanonicalValue(spec.Encode)}, opts...)
		case rpcengine.RuleKeyNormalizer:
			if spec.Encode != nil {
				opts = append([]rpcengine.StrictOption{rpcengine.WithCanonicalObject(spec.Encode)}, opts...)
			}
		}
	}

	switch spec.Resolver {
	case ResolverStrict:
		return rpcengine.Strict(spec.Normalize, opts...), nil
	case ResolverMedian:
		// The median resolver pins its comparison-key class internally; the
		// spec's declaration is informational and must agree.
		if spec.CompareKind != "" && spec.CompareKind != rpcengine.RuleCompareQuantity {
			return nil, fmt.Errorf("methods: median resolver %s declares compare kind %q, want %q",
				spec.Method, spec.CompareKind, rpcengine.RuleCompareQuantity)
		}
		medianOpts := []rpcengine.MedianOption{}
		if spec.Encode != nil {
			medianOpts = append(medianOpts, rpcengine.WithMedianCanonical(spec.Encode))
		}
		return rpcengine.Median(spec.Normalize, medianOpts...), nil
	case ResolverLocal:
		localOpts := []rpcengine.LocalOption{rpcengine.WithLocalRuleCompareKind(spec.CompareKind)}
		if spec.Encode != nil {
			localOpts = append(localOpts, rpcengine.WithLocalCanonical(spec.Encode))
		}
		return rpcengine.Local(spec.Normalize, localOpts...), nil
	case ResolverCallRevert:
		// The controlled escape hatch: the call-revert resolver's value
		// domain (error bodies in, canonical success encoding) is the
		// escape's own identity, fixed here and pinned by the call-revert
		// tests — not spec fields.
		inner := append([]rpcengine.StrictOption{rpcengine.WithRPCErrorBodies(), rpcengine.WithCanonicalValue(encodeCallSuccess)}, opts...)
		return rpcengine.NewCallRevertResolver(normalizeCallOutcome, inner...), nil
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
// resolver owns adjudication; Normalize remains a separate capability. The plan owns the
// preparation, admission, reference, and pending declarations.
//
// Compilation rejects an impossible declaration before it can register:
// a missing comparison key, an unknown resolver class, an illegal
// reference or pending shape, and a declared key source that contradicts
// the resolver's own are all composition errors, not runtime surprises.
func CompilePlan(spec MethodSpec) (rpcengine.ReadPlan, error) {
	if err := validatePlanComposition(spec); err != nil {
		return rpcengine.ReadPlan{}, err
	}
	plan := rpcengine.ReadPlan{
		Method:       spec.Method,
		Normalize:    spec.Normalize,
		ResolverKind: spec.Resolver,
		Reference:    spec.Reference,
		Pending:      spec.Pending,
		Prepare:      spec.Prepare,
		Admit:        spec.Admit,
		ProjectorID:  spec.ObservationProjectorID,
	}
	if spec.ProcessCanonical != nil {
		plan.Canonical = &rpcengine.CanonicalRead{Process: spec.ProcessCanonical, Resolver: rpcengine.CanonicalStrict(spec.CompareKind)}
	} else {
		resolver, err := assembleResolver(spec, nil)
		if err != nil {
			return rpcengine.ReadPlan{}, err
		}
		plan.Resolver = resolver
	}
	// The declared pending rejection executes locally only when the method
	// does not own its rejection in the validate hook; otherwise the
	// declaration contributes the rule column alone.
	if spec.Pending.Mode == PendingRefuse && spec.Prepare == nil {
		plan.PendingReject = rpcengine.PendingRejectAt(spec.Pending.Pos, spec.Method)
	}
	if err := validatePlanBindings(plan, spec); err != nil {
		return rpcengine.ReadPlan{}, err
	}
	return plan, nil
}
