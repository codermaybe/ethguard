package rpc

import (
	"context"
	"encoding/json"
	"fmt"
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
func Local(normalize func(json.RawMessage) (string, error), opts ...LocalOption) Policy {
	f := localFunc{normalize: normalize}
	for _, opt := range opts {
		opt(&f)
	}
	return f
}

// LocalOption carries introspection or a scalar encoder for a local policy.
// WithLocalRuleCompareKind is not read by Prepare, Normalize, or Resolve.
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

func (f localFunc) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

func (f localFunc) Normalize(value json.RawMessage) (string, error) {
	return f.normalize(value)
}

// MethodRule reports local resolution and the coarse comparison-key class
// supplied at construction. It does not participate in request handling.
func (f localFunc) MethodRule() (MethodRule, Policy) {
	return MethodRule{Resolver: RuleResolverLocal, CompareKind: f.compareKind, Result: resultRule(f.encode)}, nil
}

func (f localFunc) CanonicalValue(raw json.RawMessage) (json.RawMessage, error) {
	if f.encode == nil {
		return nil, ErrNotCanonicalScalar
	}
	return f.encode(raw)
}

func (f localFunc) Resolve(obs []Observation) (Decision, error) {
	var faults []error
	for _, item := range obs {
		if item.Err != nil {
			continue
		}
		if _, err := f.normalize(item.Value); err != nil {
			faults = append(faults, sourceFault(item.Source, "normalize", err))
			continue
		}
		value := item.Value
		if f.encode != nil {
			canonical, err := f.encode(item.Value)
			if err != nil {
				faults = append(faults, sourceFault(item.Source, "encode", err))
				return Decision{Status: Unknown, Faults: faults}, fmt.Errorf("local: %w", err)
			}
			value = canonical
		}
		return Decision{
			Status:  Selected,
			Value:   value,
			Support: []SourceRef{item.Source},
			Faults:  faults,
		}, nil
	}
	return Decision{Status: Unknown, Faults: faults}, fmt.Errorf(
		"local: no source produced a valid response (%d attempted): %w", len(obs), ErrNoValidResponse)
}
