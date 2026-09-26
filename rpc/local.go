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

// LocalOption carries introspection for a local policy. Options are not
// read by Prepare, Normalize, or Resolve.
type LocalOption func(*localFunc)

// WithLocalRuleCompareKind records the coarse comparison-key class for a
// local policy. It does not change which source is selected.
func WithLocalRuleCompareKind(kind string) LocalOption {
	return func(f *localFunc) { f.compareKind = kind }
}

type localFunc struct {
	normalize   func(json.RawMessage) (string, error)
	compareKind string
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
	return MethodRule{Resolver: RuleResolverLocal, CompareKind: f.compareKind}, nil
}

func (f localFunc) Resolve(obs []Observation) (Decision, error) {
	var faults []error
	for _, item := range obs {
		if item.Err != nil {
			continue
		}
		if _, err := f.normalize(item.Value); err != nil {
			faults = append(faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		return Decision{
			Status:  Selected,
			Value:   item.Value,
			Support: []SourceRef{item.Source},
			Faults:  faults,
		}, nil
	}
	return Decision{Status: Unknown, Faults: faults}, fmt.Errorf(
		"local: no source produced a valid response (%d attempted): %w", len(obs), ErrNoValidResponse)
}
