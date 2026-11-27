package rpcengine

import (
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
)

// Local binds a normalizer to upstream-local resolution for results that
// describe one provider's own state (peer tables, sync progress): there is
// no fleet truth to agree on, so the first healthy source's answer stands —
// deterministically in pool order — labelled Selected at best, never
// Corroborated.
//
// Honesty notes: Support names only the consulted source; local answers are
// not comparable, so no cluster semantics apply. The caller inherits the
// trust of whichever source answered first — health gates keep failing
// sources out, but a live liar's local view cannot be cross-checked by
// definition. Consumers needing conservatism over local state should consult
// several sources themselves or wait for a derivation strategy.
func Local(normalize func(json.RawMessage) (string, error), opts ...LocalOption) Resolver {
	f := localFunc{normalize: normalize}
	for _, opt := range opts {
		opt(&f)
	}
	return f
}

// LocalOption carries introspection or a scalar encoder for a local policy.
// WithLocalRuleCompareKind is not read by Normalize or Resolve.
// WithLocalCanonical encodes only the source Resolve already selected.
type LocalOption func(*localFunc)

// WithLocalRuleCompareKind records the coarse comparison-key class for a
// local policy. It does not change which source is selected.
func WithLocalRuleCompareKind(kind string) LocalOption {
	return func(f *localFunc) { f.compareKind = kind }
}

// WithLocalCanonical encodes the one selected source. It does not change
// which source is selected and it does not raise Status to Corroborated.
func WithLocalCanonical(encode CanonicalEncoder) LocalOption {
	return func(f *localFunc) { f.encode = encode }
}

type localFunc struct {
	normalize   func(json.RawMessage) (string, error)
	compareKind string
	encode      CanonicalEncoder
}

func (f localFunc) Normalize(value json.RawMessage) (string, error) {
	return f.normalize(value)
}

// MethodRule reports local resolution and the coarse comparison-key class
// supplied at construction. It does not participate in request handling.
func (f localFunc) MethodRule() MethodRule {
	return MethodRule{Resolver: RuleResolverLocal, CompareKind: f.compareKind, Result: resultRule(f.encode)}
}

func (f localFunc) CanonicalValue(raw json.RawMessage) (json.RawMessage, error) {
	return encodeCanonicalValue(f.encode, raw)
}

func (f localFunc) Resolve(obs []rpc.Observation) (Outcome, error) {
	return resolveLocal(f, obs, false)
}
