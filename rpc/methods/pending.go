package methods

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ethguard/rpc"
)

// PendingLocal decorates a default-block policy with method-specific pending
// support (decision 0035): a pending request keeps its node-state meaning
// and resolves as a node-local observation — the first healthy source
// stands, ceiling Selected, never Corroborated — instead of entering
// cross-source corroboration where unanimous agreement would forge a
// Corroborated claim over unfinalized state.
//
// Contract at pos:
//
//   - "pending" (any case) and {blockNumber: "pending"} pass through
//     Prepare untouched: no anchor call, no rewrite to a mined reference,
//     requireCanonical preserved on the wire.
//   - Those shapes resolve via ResolveLocal: first source with a
//     normalizable answer wins with Selected status, its source as Support,
//     and its admission evidence attached. Methods whose value domain
//     includes RPC error bodies (eth_call) keep that treatment on the
//     pending path; anything else rides as faults.
//   - No usable source fails Unknown with ErrNoValidResponse.
//   - Every other shape delegates to the inner policy untouched, including
//     RequiresAgreement (the adapter's corroboration gate still applies)
//     and the CorroboratingPolicy capability.
//
// This is applied per method, only where pending is supported. Methods
// without stable pending semantics reject pending locally instead
// (see BlockReceiptsPolicy's RejectPending); there is no global pending
// switch.
func PendingLocal(pos int) func(rpc.Policy) rpc.Policy {
	return func(policy rpc.Policy) rpc.Policy {
		if policy == nil {
			policy = rpc.ForwardPolicy{}
		}
		base := pendingLocalPolicy{policy: policy, pos: pos}
		if _, ok := policy.(rpc.CorroboratingPolicy); ok {
			return corroboratingPendingLocalPolicy{base}
		}
		return base
	}
}

type pendingLocalPolicy struct {
	policy rpc.Policy
	pos    int
}

// IsLocalObservation reports whether the prepared request addresses pending
// state, in string or EIP-1898 object spelling.
func (p pendingLocalPolicy) IsLocalObservation(prepared rpc.Request) bool {
	if p.pos < 0 || p.pos >= len(prepared.Params) {
		return false
	}
	switch ref := prepared.Params[p.pos].(type) {
	case string:
		return strings.EqualFold(ref, "pending")
	case map[string]any:
		number, ok := ref["blockNumber"].(string)
		return ok && strings.EqualFold(number, "pending")
	}
	return false
}

// ResolveLocal resolves pending observations as node-local: the first
// source with a normalizable answer stands. Unanimous agreement stays
// Selected — agreement over pending state is coincidence of independent
// node views, not corroboration of a deterministic truth.
func (p pendingLocalPolicy) ResolveLocal(obs []rpc.Observation) (rpc.Decision, error) {
	includeRPCError := false
	if provider, ok := p.policy.(rpc.RPCErrorPolicy); ok {
		includeRPCError = provider.IncludeRPCError()
	}
	var faults []error
	for _, item := range obs {
		value := item.Value
		if item.Err != nil {
			if !includeRPCError || item.RPCError == nil {
				continue
			}
			value = item.RPCError.Raw()
		}
		if _, err := p.policy.Normalize(value); err != nil {
			faults = append(faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		if canonical, err := rpc.CanonicalOf(p.policy, value); err == nil {
			value = canonical
		} else if !errors.Is(err, rpc.ErrNotCanonicalScalar) {
			faults = append(faults, fmt.Errorf("encode: %w", err))
			return rpc.Decision{Status: rpc.Unknown, Faults: faults}, fmt.Errorf("pending: %w", err)
		}
		return rpc.Decision{
			Status:   rpc.Selected,
			Value:    value,
			Support:  []rpc.SourceRef{item.Source},
			Faults:   faults,
			Evidence: append(json.RawMessage(nil), item.Evidence...),
		}, nil
	}
	return rpc.Decision{Status: rpc.Unknown, Faults: faults}, fmt.Errorf(
		"pending: no source produced a valid response (%d attempted): %w", len(obs), rpc.ErrNoValidResponse)
}

func (p pendingLocalPolicy) Admit(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	if inner, ok := p.policy.(rpc.AdmissionPolicy); ok {
		return inner.Admit(ctx, req, value, call)
	}
	return nil, nil
}

func (p pendingLocalPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	return p.policy.Prepare(ctx, req)
}

// BlockRef reports no pin for pending: nothing was rewritten. All other
// shapes defer to the inner anchored policy.
func (p pendingLocalPolicy) BlockRef(original, prepared rpc.Request) string {
	if p.IsLocalObservation(prepared) {
		return ""
	}
	if provider, ok := p.policy.(rpc.BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}

func (p pendingLocalPolicy) RequiresAgreement() bool {
	inner, ok := p.policy.(rpc.AgreementPolicy)
	return ok && inner.RequiresAgreement()
}

func (p pendingLocalPolicy) SingleSource() bool {
	inner, ok := p.policy.(rpc.SingleSourcePolicy)
	return ok && inner.SingleSource()
}

func (p pendingLocalPolicy) IncludeRPCError() bool {
	provider, ok := p.policy.(rpc.RPCErrorPolicy)
	return ok && provider.IncludeRPCError()
}

func (p pendingLocalPolicy) Normalize(value json.RawMessage) (string, error) {
	return p.policy.Normalize(value)
}

// MethodRule reports this decorator's pending semantics: a node-local
// observation capped at Selected. PendingLocal is not used for methods that
// reject pending, so the value is this layer's behavior rather than a label
// shared with rejection. It does not decide a request. The corroborating
// wrapper promotes this method.
func (p pendingLocalPolicy) MethodRule() (rpc.MethodRule, rpc.Policy) {
	rule := rpc.MethodRule{Pending: rpc.RulePendingLocalObservation}
	if inner, ok := p.policy.(rpc.MethodRuleSource); ok {
		innerRule, _ := inner.MethodRule()
		rule = rule.WithInner(innerRule)
	}
	return rule, p.policy
}

func (p pendingLocalPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	return p.policy.Resolve(obs)
}

type corroboratingPendingLocalPolicy struct{ pendingLocalPolicy }

func (p corroboratingPendingLocalPolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if inner, ok := p.policy.(rpc.CorroboratingPolicy); ok {
		return inner.ResolveCorroborated(c, obs)
	}
	return p.policy.Resolve(obs)
}
