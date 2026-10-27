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
// already exposed by the wrapped policy (rpc.Decorator carries the forwards).
func WithAdmission(base rpc.Policy, admit AdmissionFunc) rpc.Policy {
	return admissionPolicy{Decorator: rpc.Decorator{Policy: base}, admit: admit}
}

type admissionPolicy struct {
	rpc.Decorator
	admit AdmissionFunc
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
