package methods

import (
	"context"
	"encoding/json"

	"ethguard/rpc"
	blockobject "ethguard/rpc/internal/objects/block"
)

type blockPolicy struct {
	rpc.Policy
	byHash bool
}

func (p blockPolicy) RequiresAgreement() bool {
	if a, ok := p.Policy.(rpc.AgreementPolicy); ok {
		return a.RequiresAgreement()
	}
	return true
}

func (p blockPolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if cp, ok := p.Policy.(rpc.CorroboratingPolicy); ok {
		return cp.ResolveCorroborated(c, obs)
	}
	return p.Policy.Resolve(obs)
}

// MethodRule forwards the inner rule composition. Block admission is not a
// column in the method-rule index.
func (p blockPolicy) MethodRule() (rpc.MethodRule, rpc.Policy) {
	rule := rpc.MethodRule{}
	if inner, ok := p.Policy.(rpc.MethodRuleSource); ok {
		innerRule, _ := inner.MethodRule()
		rule = rule.WithInner(innerRule)
	}
	return rule, p.Policy
}

// Admit validates full block responses through the shared block object
// admission implementation. Header-only calls remain anchor reads.
func (p blockPolicy) Admit(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	if string(value) == "null" {
		return nil, nil
	}
	fullTx := len(req.Params) > 1 && req.Params[1] == true
	if !fullTx {
		return nil, nil
	}
	return blockobject.AdmitBlock(ctx, req, value, p.byHash, call)
}
