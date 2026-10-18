// Package policy contains reusable method-policy decorators. It depends only
// on the transport contract in rpc and is not aware of concrete methods.
package policy

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
)

// AdmissionFunc validates one source response and may return opaque
// same-source evidence for the selected decision.
type AdmissionFunc func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error)

// WithAdmission decorates a policy while preserving all optional capabilities
// already exposed by the wrapped policy.
func WithAdmission(base rpc.Policy, admit AdmissionFunc) rpc.Policy {
	return admissionPolicy{Policy: base, admit: admit}
}

type admissionPolicy struct {
	rpc.Policy
	admit AdmissionFunc
}

func (p admissionPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	return p.Policy.Prepare(ctx, req)
}

// MethodRule forwards the inner rule composition. Admission is not a column
// in the method-rule index and this method does not admit a response.
func (p admissionPolicy) MethodRule() (rpc.MethodRule, rpc.Policy) {
	rule := rpc.MethodRule{}
	if inner, ok := p.Policy.(rpc.MethodRuleSource); ok {
		innerRule, _ := inner.MethodRule()
		rule = rule.WithInner(innerRule)
	}
	return rule, p.Policy
}
func (p admissionPolicy) Normalize(value json.RawMessage) (string, error) {
	return p.Policy.Normalize(value)
}
func (p admissionPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	return p.Policy.Resolve(obs)
}
func (p admissionPolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if cp, ok := p.Policy.(rpc.CorroboratingPolicy); ok {
		return cp.ResolveCorroborated(c, obs)
	}
	return p.Policy.Resolve(obs)
}
func (p admissionPolicy) Admit(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	return p.admit(ctx, req, value, call)
}
func (p admissionPolicy) RequiresAgreement() bool {
	if value, ok := p.Policy.(rpc.AgreementPolicy); ok {
		return value.RequiresAgreement()
	}
	return false
}
func (p admissionPolicy) SingleSource() bool {
	if value, ok := p.Policy.(rpc.SingleSourcePolicy); ok {
		return value.SingleSource()
	}
	return false
}
func (p admissionPolicy) IncludeRPCError() bool {
	if value, ok := p.Policy.(rpc.RPCErrorPolicy); ok {
		return value.IncludeRPCError()
	}
	return false
}
func (p admissionPolicy) BlockRef(original, prepared rpc.Request) string {
	if value, ok := p.Policy.(rpc.BlockRefProvider); ok {
		return value.BlockRef(original, prepared)
	}
	return ""
}
