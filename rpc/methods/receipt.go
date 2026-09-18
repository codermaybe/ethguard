package methods

import (
	"context"
	"encoding/json"

	"ethguard/rpc"
)

// This file is the Receipt object slice: the Receipt shape table, its
// semantic gates, and its projections, shared by eth_getTransactionReceipt
// and eth_getBlockReceipts (R2: this slice solely owns the Receipt table).
//
// It also hosts the shared admission infrastructure used by the receipt and
// transaction slices: the admissionPolicy decorator, request-reference
// parsing (requestedHash/requestedBlockRef/parseBlockHeight), and the JSON
// null/presence helpers. These are cross-object mechanics, not Receipt
// semantics; they move to prune.go when the pruning engine lands.

// admissionPolicy keeps method-specific admission visible through request
// decorators.  The aggregator checks AdmissionPolicy on the final policy
// value, so wrapping an anchored policy after its decorators is necessary to
// ensure validation runs before normalization and clustering.
type admissionPolicy struct {
	rpc.Policy
	admit func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error)
}

func (p admissionPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	return p.Policy.Prepare(ctx, req)
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
	if agreement, ok := p.Policy.(rpc.AgreementPolicy); ok {
		return agreement.RequiresAgreement()
	}
	return false
}

func (p admissionPolicy) SingleSource() bool {
	if single, ok := p.Policy.(rpc.SingleSourcePolicy); ok {
		return single.SingleSource()
	}
	return false
}

func (p admissionPolicy) IncludeRPCError() bool {
	if errors, ok := p.Policy.(rpc.RPCErrorPolicy); ok {
		return errors.IncludeRPCError()
	}
	return false
}

func (p admissionPolicy) BlockRef(original, prepared rpc.Request) string {
	if provider, ok := p.Policy.(rpc.BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}
