package methods

import (
	"context"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
	rpcpolicy "github.com/codermaybe/ethguard/rpc/internal/policy"
)

// CompileDeps carries the static runtime bindings Compile needs: the
// consumption-only anchor readers bound to the client's reference stage.
// They never establish anything themselves, so the compiled policy holds no
// callback that fans out its own network round.
type CompileDeps struct {
	AnchorHash   rpc.AnchorFunc
	AnchorHeight rpc.AnchorHeightFunc
}

// Compile assembles one MethodSpec into the policy the registry installs.
//
// The composition order is fixed and mirrors every hand-written constructor
// chain (outermost first): validation, pending, admission, EIP-1898 object
// form, anchor rewrite, resolver. Capability promotion follows the resolver
// kind through the existing decorator constructors, so wrapping a
// non-corroborating resolver never acquires a corroboration claim and the
// MethodRule source chain survives composition. Compile is the only place a
// new policy chain is born; the hand-written constructors stay as the public
// migration-compatible surface and the equivalence tests hold the two
// shapes together.
func Compile(spec MethodSpec, deps CompileDeps, opts ...rpc.StrictOption) rpc.Policy {
	resolver := compileResolver(spec, deps, opts)
	policy := applyReference(spec, deps, resolver)
	if spec.Admit != nil {
		policy = rpcpolicy.WithAdmission(policy, rpcpolicy.AdmissionFunc(spec.Admit))
	}
	if spec.Pending.Mode != PendingNone {
		switch spec.Pending.Mode {
		case PendingObserve:
			policy = PendingLocal(spec.Pending.Pos)(policy)
		case PendingRefuse:
			if spec.Validate != nil {
				// The method owns its pre-anchor rejection in the validate
				// hook; the declaration contributes the rule column without
				// a second rejection layer.
				policy = declaredPendingRule{Decorator: rpc.Decorator{Policy: policy}, pending: rpc.RulePendingLocalReject}
			} else {
				policy = rpc.RejectPending(spec.Pending.Pos)(policy)
			}
		default:
			panic(fmt.Sprintf("methods: unknown pending mode %q for %s", spec.Pending.Mode, spec.Method))
		}
	}
	if spec.Validate != nil {
		if spec.Pending.Mode == PendingObserve {
			policy = validatingLocalPolicy{validatedPolicy{Decorator: rpc.Decorator{Policy: policy}, validate: spec.Validate}}
		} else {
			policy = validatedPolicy{Decorator: rpc.Decorator{Policy: policy}, validate: spec.Validate}
		}
	}
	return policy
}

// declaredPendingRule projects a pending-reject declaration for a method
// whose rejection lives in its own validate hook; it decides nothing.
type declaredPendingRule struct {
	rpc.Decorator
	pending string
}

func (p declaredPendingRule) MethodRule() (rpc.MethodRule, rpc.Policy) {
	return p.MethodRuleWith(rpc.MethodRule{Pending: p.pending})
}

func (p declaredPendingRule) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	return compiledResolveCorroborated(p.Policy, c, obs)
}

func (p declaredPendingRule) ResolveOutcome(obs []rpc.Observation) (rpc.Outcome, error) {
	return resolveOutcomeOf(p.Policy, obs)
}

func (p declaredPendingRule) ResolveOutcomeCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Outcome, error) {
	return compiledResolveOutcomeCorroborated(p.Policy, c, obs)
}

// compiledResolveCorroborated forwards an invocation snapshot to the inner
// CorroboratingPolicy with the plain-resolve fallback, mirroring the
// corroborating twin wrappers rpc's own decorators use.
func compiledResolveCorroborated(policy rpc.Policy, c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if cp, ok := policy.(rpc.CorroboratingPolicy); ok {
		return cp.ResolveCorroborated(c, obs)
	}
	return policy.Resolve(obs)
}

// compiledResolveOutcomeCorroborated is the per-source statement variant of
// the same fallback.
func compiledResolveOutcomeCorroborated(policy rpc.Policy, c rpc.Corroboration, obs []rpc.Observation) (rpc.Outcome, error) {
	if co, ok := policy.(rpc.CorroboratingOutcomeResolver); ok {
		return co.ResolveOutcomeCorroborated(c, obs)
	}
	d, err := compiledResolveCorroborated(policy, c, obs)
	return rpc.Outcome{Decision: d}, err
}

// resolveOutcomeOf is the outcome forwarding with the plain-resolve fallback,
// shared by the compiler wrappers that mirror the method-owned shapes.
func resolveOutcomeOf(policy rpc.Policy, obs []rpc.Observation) (rpc.Outcome, error) {
	if inner, ok := policy.(rpc.OutcomeResolver); ok {
		return inner.ResolveOutcome(obs)
	}
	d, err := policy.Resolve(obs)
	return rpc.Outcome{Decision: d}, err
}

// applyReference wraps the resolver with the declared anchor layers: the
// string-form rewrite innermost, the EIP-1898 object form outside it,
// matching every hand-written chain.
func applyReference(spec MethodSpec, deps CompileDeps, policy rpc.Policy) rpc.Policy {
	ref := spec.Reference
	if ref.Kind == ReferenceShapeNone {
		return policy
	}
	switch ref.Kind {
	case ReferenceShapeHashAt:
		policy = rpc.WithAnchor(deps.AnchorHash, ref.Positions...)(policy)
	case ReferenceShapeHeightAt:
		policy = rpc.WithAnchorHeight(deps.AnchorHeight, ref.Positions...)(policy)
	case ReferenceShapeHeightFieldsAt:
		policy = rpc.WithAnchorHeightFields(deps.AnchorHeight, ref.ParamPos, ref.Fields...)(policy)
	default:
		panic(fmt.Sprintf("methods: unknown reference shape %q for %s", ref.Kind, spec.Method))
	}
	if ref.EIP1898 {
		if len(ref.Positions) != 1 {
			panic(fmt.Sprintf("methods: %s declares EIP-1898 without exactly one position", spec.Method))
		}
		policy = EIP1898BlockRef(deps.AnchorHash, ref.Positions[0])(policy)
	}
	return policy
}

// compileResolver builds the resolver the spec names. The strict option
// plumbing mirrors the constructors: canonical-value methods prepend their
// encoder, compare-kind is pinned beside the normalizer, and the block and
// call-revert shapes carry their wrapper-specific identity.
func compileResolver(spec MethodSpec, deps CompileDeps, opts []rpc.StrictOption) rpc.Policy {
	switch spec.Resolver {
	case ResolverStrict, ResolverBlock, ResolverCallRevert:
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
		return rpc.Strict(spec.Normalize, opts...)
	case ResolverBlock:
		return blockPolicy{Decorator: rpc.Decorator{Policy: rpc.Strict(spec.Normalize, opts...)}, byHash: spec.ByHash}
	case ResolverMedian:
		// The median resolver pins its comparison-key class internally; the
		// spec's declaration is informational and must agree.
		if spec.CompareKind != "" && spec.CompareKind != rpc.RuleCompareQuantity {
			panic(fmt.Sprintf("methods: median resolver %s declares compare kind %q, want %q",
				spec.Method, spec.CompareKind, rpc.RuleCompareQuantity))
		}
		medianOpts := []rpc.MedianOption{}
		if spec.Encode != nil {
			medianOpts = append(medianOpts, rpc.WithMedianCanonical(spec.Encode))
		}
		return rpc.Median(spec.Normalize, medianOpts...)
	case ResolverLocal:
		localOpts := []rpc.LocalOption{rpc.WithLocalRuleCompareKind(spec.CompareKind)}
		if spec.Encode != nil {
			localOpts = append(localOpts, rpc.WithLocalCanonical(spec.Encode))
		}
		return rpc.Local(spec.Normalize, localOpts...)
	case ResolverCallRevert:
		// The controlled escape hatch: the call-revert resolver's value
		// domain (error bodies in, canonical success encoding) is the
		// escape's own identity, fixed here and pinned by the call-revert
		// tests — not spec fields.
		inner := append([]rpc.StrictOption{rpc.WithRPCErrorBodies(), rpc.WithCanonicalValue(encodeCallSuccess)}, opts...)
		return rpc.NewCallRevertPolicy(spec.Normalize, inner...)
	default:
		panic(fmt.Sprintf("methods: unknown resolver kind %q for %s", spec.Resolver, spec.Method))
	}
}

// validatedPolicy is the compiled form of a method's Validate hook. It
// mirrors the method-owned wrappers' capability semantics: the corroborated
// resolution is exposed with the same inner-first fallback the constructors
// use, because the aggregator's collection mode follows the capability.
type validatedPolicy struct {
	rpc.Decorator
	validate func(context.Context, rpc.Request) error
}

func (p validatedPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	if err := p.validate(ctx, req); err != nil {
		return rpc.Request{}, err
	}
	return p.Policy.Prepare(ctx, req)
}

func (p validatedPolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	return compiledResolveCorroborated(p.Policy, c, obs)
}

func (p validatedPolicy) ResolveOutcome(obs []rpc.Observation) (rpc.Outcome, error) {
	return resolveOutcomeOf(p.Policy, obs)
}

func (p validatedPolicy) ResolveOutcomeCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Outcome, error) {
	return compiledResolveOutcomeCorroborated(p.Policy, c, obs)
}

// validatingLocalPolicy adds the local-observation forwarding for chains
// whose pending-local layer sits beneath the validate hook.
type validatingLocalPolicy struct {
	validatedPolicy
}

func (p validatingLocalPolicy) IsLocalObservation(prepared rpc.Request) bool {
	if inner, ok := p.Policy.(rpc.LocalObservationPolicy); ok {
		return inner.IsLocalObservation(prepared)
	}
	return false
}

func (p validatingLocalPolicy) ResolveLocal(obs []rpc.Observation) (rpc.Decision, error) {
	if inner, ok := p.Policy.(rpc.LocalObservationPolicy); ok {
		return inner.ResolveLocal(obs)
	}
	return p.Policy.Resolve(obs)
}
