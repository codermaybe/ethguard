package methods

import (
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// Plan composition validation. A spec that cannot compose into one
// executable plan is a compile-time rejection, not a runtime surprise:
// the registry installs only plans whose declared facts and resolver
// bindings agree. The checks read the frozen field set — a new dimension
// goes through the call-revert escape hatch first, never through a new
// field for one method.

// validatePlanComposition rejects the declarations that cannot compose:
// a missing comparison key, an unknown resolver class, a reference shape
// that does not match its positions or fields, an EIP-1898 declaration
// detached from exactly one anchored position, an unknown pending mode,
// and a key-source declaration the resolver cannot honor.
func validatePlanComposition(spec MethodSpec) error {
	if spec.Method == "" {
		return fmt.Errorf("methods: a read plan needs a method name")
	}
	if spec.Normalize == nil {
		return fmt.Errorf("methods: %s declares no comparison-key normalizer", spec.Method)
	}
	switch spec.Resolver {
	case ResolverStrict, ResolverMedian, ResolverLocal, ResolverCallRevert, ResolverBlock:
	default:
		return fmt.Errorf("methods: %s declares unknown resolver kind %q", spec.Method, spec.Resolver)
	}
	switch spec.Reference.Kind {
	case ReferenceShapeNone:
	case ReferenceShapeHashAt, ReferenceShapeHeightAt:
		if len(spec.Reference.Positions) == 0 {
			return fmt.Errorf("methods: %s declares %s without positions", spec.Method, spec.Reference.Kind)
		}
	case ReferenceShapeHeightFieldsAt:
		if spec.Reference.ParamPos < 0 {
			return fmt.Errorf("methods: %s declares %s with a negative parameter position", spec.Method, spec.Reference.Kind)
		}
		if len(spec.Reference.Fields) == 0 {
			return fmt.Errorf("methods: %s declares %s without fields", spec.Method, spec.Reference.Kind)
		}
	default:
		return fmt.Errorf("methods: %s declares unknown reference shape %q", spec.Method, spec.Reference.Kind)
	}
	if spec.Reference.EIP1898 {
		if spec.Reference.Kind != ReferenceShapeHashAt || len(spec.Reference.Positions) != 1 {
			return fmt.Errorf("methods: %s declares EIP-1898 without exactly one hash-anchored position", spec.Method)
		}
	}
	switch spec.Pending.Mode {
	case PendingNone:
	case PendingObserve, PendingRefuse:
		if spec.Pending.Pos < 0 {
			return fmt.Errorf("methods: %s declares pending at a negative position", spec.Method)
		}
	default:
		return fmt.Errorf("methods: %s declares unknown pending mode %q", spec.Method, spec.Pending.Mode)
	}
	// The key-source declaration is the plan-level contract between the
	// spec and the resolver's own rule: canonical bytes need an encoder, an
	// empty declaration on a strict resolver needs none. median and local
	// sample by number or select without clustering and carry no key-source
	// column, so their encoders ride the sampling options instead.
	switch spec.Resolver {
	case ResolverStrict, ResolverBlock, ResolverCallRevert:
		switch spec.KeySource {
		case rpc.RuleKeyCanonicalBytes:
			if spec.Encode == nil {
				return fmt.Errorf("methods: %s declares %s without a canonical encoder", spec.Method, spec.KeySource)
			}
		case rpc.RuleKeyNormalizer:
		case "":
			if spec.Encode != nil {
				return fmt.Errorf("methods: %s carries an encoder without a key-source declaration", spec.Method)
			}
		default:
			return fmt.Errorf("methods: %s declares unknown key source %q", spec.Method, spec.KeySource)
		}
	}
	return nil
}

// validatePlanBindings rejects a compiled plan whose resolver value does
// not honor the declared class: a corroborating class must resolve under a
// frozen corroboration snapshot, a sampling class must not claim one, and
// the projected key source must be the declared one. These are the ties
// that keep the declared facts and the executed capabilities from
// diverging.
func validatePlanBindings(plan rpc.ReadPlan, spec MethodSpec) error {
	if plan.Resolver == nil {
		return fmt.Errorf("methods: %s compiled no resolver", spec.Method)
	}
	_, corroborating := plan.Resolver.(rpc.CorroboratingPolicy)
	if plan.Corroborating() != corroborating {
		return fmt.Errorf("methods: %s declares resolver %q but its value corroborates=%v", spec.Method, spec.Resolver, corroborating)
	}
	if rule, ok := plan.Resolver.(rpc.MethodRuleSource); ok {
		projected, _ := rule.MethodRule()
		switch spec.Resolver {
		case ResolverStrict, ResolverBlock, ResolverCallRevert:
			if spec.KeySource != "" && projected.KeySource != spec.KeySource {
				return fmt.Errorf("methods: %s declares key source %q but its resolver projects %q",
					spec.Method, spec.KeySource, projected.KeySource)
			}
		}
	}
	return nil
}
