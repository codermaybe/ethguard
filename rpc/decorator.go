package rpc

import (
	"context"
	"encoding/json"
)

// Decorator is the shared base of Prepare decorators: it carries the wrapped
// policy and forwards every optional capability that must survive wrapping
// unchanged. Each forward is conditional — a capability the inner policy does
// not expose stays absent rather than acquiring a default answer — so the
// method set of a decorated policy is exactly the inner method set plus the
// decorator's own Prepare/BlockRef/MethodRule logic.
//
// Decorator deliberately has no ResolveCorroborated: that capability is added
// only by the corroborating wrapper types (corroboratingAnchoredPolicy and
// siblings), so wrapping a non-corroborating policy such as Median cannot
// look like a corroborated read (T01 review P3). A decorator that wants the
// capability exposes it directly, as admissionPolicy does.
//
// Decorators embed it by value and address the wrapped policy as .Policy:
//
//	type myPolicy struct {
//	    rpc.Decorator
//	    // decorator-specific state
//	}
//	func WithMyDecorator(...) func(Policy) Policy {
//	    return func(policy Policy) Policy {
//	        return myPolicy{Decorator: Decorator{Policy: policy}}
//	    }
//	}
type Decorator struct {
	Policy
}

// Admit forwards the inner AdmissionPolicy, or admits nothing when the inner
// policy has no admission rule.
func (d Decorator) Admit(ctx context.Context, req Request, value json.RawMessage, call AdmissionCaller) (json.RawMessage, error) {
	if inner, ok := d.Policy.(AdmissionPolicy); ok {
		return inner.Admit(ctx, req, value, call)
	}
	return nil, nil
}

// RequiresAgreement forwards the inner AgreementPolicy, or reports false when
// the inner policy declares no agreement requirement.
func (d Decorator) RequiresAgreement() bool {
	inner, ok := d.Policy.(AgreementPolicy)
	return ok && inner.RequiresAgreement()
}

// SingleSource forwards the inner SingleSourcePolicy, or reports false when
// the inner policy is not single-source.
func (d Decorator) SingleSource() bool {
	inner, ok := d.Policy.(SingleSourcePolicy)
	return ok && inner.SingleSource()
}

// IncludeRPCError forwards the inner RPCErrorPolicy, or reports false when
// the inner policy keeps RPC error bodies out of its value domain.
func (d Decorator) IncludeRPCError() bool {
	inner, ok := d.Policy.(RPCErrorPolicy)
	return ok && inner.IncludeRPCError()
}

// BlockRef forwards the inner BlockRefProvider, or reports no pin when the
// inner policy does not provide block references.
func (d Decorator) BlockRef(original, prepared Request) string {
	if provider, ok := d.Policy.(BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}

// MethodRule composes the inner policy's rule without adding a column of its
// own. Decorators that contribute a rule column use MethodRuleWith instead.
func (d Decorator) MethodRule() (MethodRule, Policy) {
	return d.MethodRuleWith(MethodRule{})
}

// MethodRuleWith composes the decorator's own rule column with the wrapped
// policy's rule and reports the wrapped policy as the rule's owner.
func (d Decorator) MethodRuleWith(rule MethodRule) (MethodRule, Policy) {
	if inner, ok := d.Policy.(MethodRuleSource); ok {
		innerRule, _ := inner.MethodRule()
		rule = rule.WithInner(innerRule)
	}
	return rule, d.Policy
}
