package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

// Median binds a response normalizer to robust-selection resolution for
// variable results: values with no single true answer (gas prices, fee
// estimates) are resolved to the fleet's lower median instead of being voted
// into agreement.
//
// The normalized form must be a base-10 integer string, which the shared hex
// quantity normalizers produce. Resolution never claims Corroborated — agreeing
// on a moving value at one instant is coincidence, not corroboration of a
// deterministic truth — so every median decision is Selected at best. The
// choice resists manipulation the same way the tip gate does: dragging the
// median requires buying more than half of the fleet, and outliers stay on
// Reject as spread evidence.
func Median(normalize func(json.RawMessage) (string, error)) Policy {
	return medianFunc{normalize: normalize}
}

type medianFunc struct {
	normalize func(json.RawMessage) (string, error)
}

func (f medianFunc) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

func (f medianFunc) Normalize(value json.RawMessage) (string, error) {
	return f.normalize(value)
}

func (f medianFunc) Resolve(obs []Observation) (Decision, error) {
	var decision Decision
	type sample struct {
		source SourceRef
		value  *big.Int
		raw    json.RawMessage
	}
	samples := make([]sample, 0, len(obs))
	for _, item := range obs {
		if item.Err != nil {
			continue
		}
		canonical, err := f.normalize(item.Value)
		if err != nil {
			decision.Faults = append(decision.Faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		value, ok := new(big.Int).SetString(canonical, 10)
		if !ok {
			decision.Faults = append(decision.Faults, fmt.Errorf(
				"normalize: median selection needs an integer, got %q", canonical))
			continue
		}
		samples = append(samples, sample{source: item.Source, value: value, raw: item.Value})
	}
	if len(samples) == 0 {
		return Decision{Faults: decision.Faults}, fmt.Errorf(
			"median: no source produced a valid response (%d attempted): %w", len(obs), ErrNoValidResponse)
	}

	// Keep equal-valued samples in pool order. When the median value has
	// multiple raw spellings, the first source's spelling is the deterministic
	// winner rather than an artifact of an unstable sort.
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].value.Cmp(samples[j].value) < 0 })
	medianValue := samples[(len(samples)-1)/2].value // lower median: never above the middle
	winner := samples[0]
	for _, candidate := range samples {
		if candidate.value.Cmp(medianValue) == 0 {
			winner = candidate // first equal sample remains first in pool order
			break
		}
	}

	decision.Status = Selected // variable result: corroboration proves nothing
	decision.Value = winner.raw
	for _, s := range samples {
		if s.value.Cmp(winner.value) == 0 {
			decision.Support = append(decision.Support, s.source)
		} else {
			decision.Reject = append(decision.Reject, s.source)
		}
	}
	return decision, nil
}
