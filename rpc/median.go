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
func Median(normalize func(json.RawMessage) (string, error), opts ...MedianOption) Policy {
	f := medianFunc{normalize: normalize}
	for _, opt := range opts {
		opt(&f)
	}
	return f
}

// MedianOption attaches a scalar encoder. It does not change the median rule.
type MedianOption func(*medianFunc)

// WithMedianCanonical encodes samples before they enter the median. A sample
// that cannot be encoded is a Fault and does not cause a second vote. Status
// stays Selected.
func WithMedianCanonical(encode CanonicalEncoder) MedianOption {
	return func(f *medianFunc) { f.encode = encode }
}

type medianFunc struct {
	normalize func(json.RawMessage) (string, error)
	encode    CanonicalEncoder
}

func (f medianFunc) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

func (f medianFunc) Normalize(value json.RawMessage) (string, error) {
	return f.normalize(value)
}

// MethodRule reports median resolution. Median's contract is a base-10
// integer key, so the coarse class is quantity. This method does not
// participate in request handling.
func (f medianFunc) MethodRule() (MethodRule, Policy) {
	return MethodRule{Resolver: RuleResolverMedian, CompareKind: RuleCompareQuantity, Result: resultRule(f.encode)}, nil
}

func (f medianFunc) CanonicalValue(raw json.RawMessage) (json.RawMessage, error) {
	if f.encode == nil {
		return nil, ErrNotCanonicalScalar
	}
	return f.encode(raw)
}

func (f medianFunc) Resolve(obs []Observation) (Decision, error) {
	var decision Decision
	type sample struct {
		source    SourceRef
		value     *big.Int
		raw       json.RawMessage
		evidence  json.RawMessage
		canonical json.RawMessage
	}
	samples := make([]sample, 0, len(obs))
	for _, item := range obs {
		if item.Err != nil {
			continue
		}
		var canonical json.RawMessage
		if f.encode != nil {
			encoded, err := f.encode(item.Value)
			if err != nil {
				decision.Faults = append(decision.Faults, fmt.Errorf("encode: %w", err))
				continue
			}
			canonical = encoded
		}
		key, err := f.normalize(item.Value)
		if err != nil {
			decision.Faults = append(decision.Faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		value, ok := new(big.Int).SetString(key, 10)
		if !ok {
			decision.Faults = append(decision.Faults, fmt.Errorf(
				"normalize: median selection needs an integer, got %q", key))
			continue
		}
		samples = append(samples, sample{
			source: item.Source, value: value, raw: item.Value, evidence: item.Evidence, canonical: canonical,
		})
	}
	if len(samples) == 0 {
		return Decision{Status: Unknown, Faults: decision.Faults}, fmt.Errorf(
			"median: no source produced a valid response (%d attempted): %w", len(obs), ErrNoValidResponse)
	}

	// Keep equal-valued samples in pool order. When the median value has
	// multiple raw spellings, the first source's spelling is the deterministic
	// winner rather than an artifact of an unstable sort. A canonical encoder
	// replaces that spelling in Value and still records this person.
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].value.Cmp(samples[j].value) < 0 })
	medianValue := samples[(len(samples)-1)/2].value // lower median: never above the middle
	equal := make([]representativeCandidate, 0, len(samples))
	for _, candidate := range samples {
		if candidate.value.Cmp(medianValue) == 0 {
			equal = append(equal, representativeCandidate{
				source:    candidate.source,
				value:     candidate.raw,
				evidence:  candidate.evidence,
				canonical: candidate.canonical,
			})
		}
	}
	rep := firstRepresentative(equal)

	decision.Status = Selected // variable result: corroboration proves nothing
	decision.Value = rep.value
	decision.Provenance = rep.evidence
	decision.Representative = rep.source
	for _, s := range samples {
		if s.value.Cmp(medianValue) == 0 {
			decision.Support = append(decision.Support, s.source)
		} else {
			decision.Reject = append(decision.Reject, s.source)
		}
	}
	if f.encode == nil {
		return decision, nil
	}
	// This check holds because equal is selected by number, above, and the
	// encoder is a function of that number alone, so every member of equal
	// encodes to the same bytes. The five median methods satisfy it: all of
	// them attach WithMedianCanonical(wire.EncodeQuantity), which renders a
	// quantity and depends on nothing else in the payload. If a median method
	// ever attached an encoder that is not a function of the number alone, this
	// check would start rejecting a result it used to commit, and the guard
	// would carry the weight again.
	canonical := make([]json.RawMessage, len(equal))
	for i, candidate := range equal {
		canonical[i] = append(json.RawMessage(nil), candidate.canonical...)
	}
	committed, err := CommitCanonical(canonical)
	if err != nil {
		decision.Status = Unknown
		decision.Value = nil
		decision.Faults = append(decision.Faults, err)
		return decision, err
	}
	decision.Value = committed
	decision.Status = Selected
	return decision, nil
}
