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
		d, err := cp.ResolveCorroborated(c, obs)
		return p.restoreHeaderOnlyValue(d, obs), err
	}
	d, err := p.Policy.Resolve(obs)
	return p.restoreHeaderOnlyValue(d, obs), err
}

func (p blockPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	d, err := p.Policy.Resolve(obs)
	return p.restoreHeaderOnlyValue(d, obs), err
}

// Header-only eth_getBlockByNumber keeps the source spelling in its public
// Value for compatibility, while strict resolution still clusters on the
// canonical head bytes. Full block responses remain canonical committed Value.
func (p blockPolicy) restoreHeaderOnlyValue(d rpc.Decision, obs []rpc.Observation) rpc.Decision {
	if p.byHash || d.Status != rpc.Corroborated || d.Representative.ID == "" {
		return d
	}
	for _, item := range obs {
		if item.Source.ID != d.Representative.ID || !headerOnlyBlock(item.Value) {
			continue
		}
		d.Value = append(json.RawMessage(nil), item.Value...)
		break
	}
	return d
}

func headerOnlyBlock(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return false
	}
	_, hasNumber := object["number"]
	_, hasHash := object["hash"]
	_, hasParentHash := object["parentHash"]
	return hasNumber && hasHash && !hasParentHash
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
