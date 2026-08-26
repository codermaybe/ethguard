package rpc

import (
	"context"
	"encoding/json"
	"fmt"

	"ethguard/pool"
)

// Local binds a normalizer to upstream-local resolution for results that
// describe one provider's own state (peer tables, sync progress): there is
// no fleet truth to agree on, so the first healthy source's answer stands —
// deterministically in pool order — labelled Selected at best, never
// Verified.
//
// Honesty notes: Support names only the consulted source; local answers are
// not comparable, so no cluster semantics apply. The caller inherits the
// trust of whichever source answered first — health gates keep failing
// sources out, but a live liar's local view cannot be cross-checked by
// definition. Consumers needing conservatism over local state should consult
// several sources themselves or wait for a derivation strategy.
func Local(normalize func(json.RawMessage) (string, error)) Policy {
	return localFunc{normalize: normalize}
}

type localFunc struct {
	normalize func(json.RawMessage) (string, error)
}

func (f localFunc) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

func (f localFunc) Normalize(value json.RawMessage) (string, error) {
	return f.normalize(value)
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
			Support: []*pool.Source{item.Source},
			Faults:  faults,
		}, nil
	}
	return Decision{Faults: faults}, fmt.Errorf(
		"local: no source produced a valid response (%d attempted): %w", len(obs), ErrNoValidResponse)
}
