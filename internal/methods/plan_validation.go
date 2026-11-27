package methods

import (
	"fmt"

	"github.com/codermaybe/ethguard/internal/rpcengine"
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
	case ResolverStrict, ResolverMedian, ResolverLocal, ResolverCallRevert:
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
	if spec.Reference.DefaultTag != "" {
		if (spec.Reference.Kind != ReferenceShapeHashAt && spec.Reference.Kind != ReferenceShapeHeightAt) || len(spec.Reference.Positions) != 1 || spec.Reference.Positions[0] < 0 {
			return fmt.Errorf("methods: %s declares a reference default without one positional reference", spec.Method)
		}
		switch spec.Reference.DefaultTag {
		case "latest", "safe", "finalized", "earliest", "pending":
		default:
			return fmt.Errorf("methods: %s declares invalid reference default %q", spec.Method, spec.Reference.DefaultTag)
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
	case ResolverStrict, ResolverCallRevert:
		switch spec.KeySource {
		case rpcengine.RuleKeyCanonicalBytes:
			if spec.Encode == nil && spec.ProcessCanonical == nil {
				return fmt.Errorf("methods: %s declares %s without a canonical encoder", spec.Method, spec.KeySource)
			}
		case rpcengine.RuleKeyNormalizer:
		case "":
			if spec.Encode != nil {
				return fmt.Errorf("methods: %s carries an encoder without a key-source declaration", spec.Method)
			}
		default:
			return fmt.Errorf("methods: %s declares unknown key source %q", spec.Method, spec.KeySource)
		}
	}
	// The canonical processing route is a strict-only assembly: clustering on
	// canonical bytes requires the strict resolver's cluster/commit kernel,
	// and a processor beside a different key source would make the declared
	// key and the processed bytes disagree. Normalize remains independent;
	// raw admission and encoding cannot coexist with canonical execution.
	if spec.ProcessCanonical != nil {
		if spec.Resolver != ResolverStrict {
			return fmt.Errorf("methods: %s declares a canonical processor with resolver %q; the processing route requires %q",
				spec.Method, spec.Resolver, ResolverStrict)
		}
		if spec.KeySource != rpcengine.RuleKeyCanonicalBytes {
			return fmt.Errorf("methods: %s declares a canonical processor with key source %q; the processing route clusters on %q",
				spec.Method, spec.KeySource, rpcengine.RuleKeyCanonicalBytes)
		}
		if spec.Encode != nil || spec.Admit != nil || spec.Pending.Mode == PendingObserve {
			return fmt.Errorf("methods: %s mixes canonical processing with raw execution", spec.Method)
		}
	}
	return nil
}

// validatePlanBindings rejects a compiled plan whose resolver value does
// not honor the declared class: a corroborating class must resolve under a
// frozen corroboration snapshot — and state its corroborated outcome through
// the same value, so per-source verdicts come from the resolver itself and
// not from a second Normalize guess — a sampling class must claim neither
// corroboration entry point, and the projected key source must be the
// declared one. These are the ties that keep the declared facts and the
// executed capabilities from diverging.
func validatePlanBindings(plan rpcengine.ReadPlan, spec MethodSpec) error {
	if err := plan.ValidateExecution(); err != nil {
		return err
	}
	if plan.Canonical != nil {
		return nil
	}
	_, corroborating := plan.Resolver.(rpcengine.CorroboratingResolver)
	if plan.Corroborating() != corroborating {
		return fmt.Errorf("methods: %s declares resolver %q, but its value corroborates=%v",
			spec.Method, spec.Resolver, corroborating)
	}
	agreement, hasAgreement := plan.Resolver.(rpcengine.AgreementResolver)
	agreementRequired := plan.Corroborating()
	if !hasAgreement {
		if agreementRequired {
			return fmt.Errorf("methods: %s declares resolver %q, but its value has no agreement capability",
				spec.Method, spec.Resolver)
		}
	} else if agreement.RequiresAgreement() != agreementRequired {
		return fmt.Errorf("methods: %s declares resolver %q, but its agreement=%v want %v",
			spec.Method, spec.Resolver, agreement.RequiresAgreement(), agreementRequired)
	}
	if rule, ok := plan.Resolver.(rpcengine.MethodRuleSource); ok {
		projected := rule.MethodRule()
		switch spec.Resolver {
		case ResolverStrict, ResolverCallRevert:
			if spec.KeySource != "" && projected.KeySource != spec.KeySource {
				return fmt.Errorf("methods: %s declares key source %q but its resolver projects %q",
					spec.Method, spec.KeySource, projected.KeySource)
			}
		}
	}
	return nil
}
