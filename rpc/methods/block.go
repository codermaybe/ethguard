package methods

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
	blockobject "github.com/codermaybe/ethguard/rpc/internal/objects/block"
)

type blockPolicy struct {
	// Policy is the inner strict resolver the block wrapper restores
	// header-only shapes over; the plan path reads its resolution and
	// normalization capabilities directly.
	Policy rpc.Policy
	byHash bool
}

// Prepare forwards the inner resolver's prepare: the plan pipeline never
// calls it (the plan owns preparation), but the value satisfies rpc.Policy.
func (p blockPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	return p.Policy.Prepare(ctx, req)
}

// Normalize forwards the inner resolver's canonical form: the block wrapper
// changes identity handling, not the value encoding.
func (p blockPolicy) Normalize(value json.RawMessage) (string, error) {
	return p.Policy.Normalize(value)
}

// MethodRule composes the inner resolver's rule column and reports the
// block wrapper as its owner, so capability walks (CanonicalOf and the
// method-rule index) keep reaching the wrapped resolver.
func (p blockPolicy) MethodRule() (rpc.MethodRule, rpc.Policy) {
	var rule rpc.MethodRule
	if inner, ok := p.Policy.(rpc.MethodRuleSource); ok {
		rule, _ = inner.MethodRule()
	}
	return rule, p.Policy
}

// admitBlockResponse is the single full-block admission hook shared by the
// explicit plan and the compatibility policy. The block identity is supplied
// by the method assembly, so admission never infers hash-versus-number
// semantics from a request method string.
func admitBlockResponse(ctx context.Context, req rpc.Request, value json.RawMessage, byHash bool, call rpc.AdmissionCaller) (json.RawMessage, error) {
	if string(value) == "null" {
		return nil, nil
	}
	fullTx := len(req.Params) > 1 && isTrueParam(req.Params[1])
	if !fullTx {
		return nil, nil
	}
	return blockobject.AdmitBlock(ctx, req, value, byHash, call)
}

// isTrueParam recognizes the fullTx flag in the spellings a generic Call
// can carry: the bool literal and its JSON encoding. A wrapper type carrying
// JSON true must enable the full-block admission exactly like the literal,
// or a whole-block response would skip its cryptographic checks by spelling.
func isTrueParam(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case json.RawMessage:
		return string(bytes.TrimSpace(typed)) == "true"
	default:
		return false
	}
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

func (p blockPolicy) ResolveOutcome(obs []rpc.Observation) (rpc.Outcome, error) {
	if inner, ok := p.Policy.(rpc.OutcomeResolver); ok {
		return inner.ResolveOutcome(obs)
	}
	d, err := p.Resolve(obs)
	return rpc.Outcome{Decision: d}, err
}

func (p blockPolicy) ResolveOutcomeCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Outcome, error) {
	if inner, ok := p.Policy.(rpc.CorroboratingOutcomeResolver); ok {
		return inner.ResolveOutcomeCorroborated(c, obs)
	}
	d, err := p.ResolveCorroborated(c, obs)
	return rpc.Outcome{Decision: d}, err
}

func (p blockPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	return p.Policy.Resolve(obs)
}

// Admit validates full block responses through the shared block object
// admission implementation. Header-only calls remain anchor reads.
func (p blockPolicy) Admit(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	return admitBlockResponse(ctx, req, value, p.byHash, call)
}
