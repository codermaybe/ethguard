package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"ethguard/pool"
)

// CanonicalJSON reduces any JSON value to a comparable canonical form by
// decoding and re-encoding it, erasing key order and formatting differences.
func CanonicalJSON(value json.RawMessage) (string, error) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(decoded)
	return string(canonical), err
}

// Strict binds a response normalizer to strict-consensus resolution. Method
// authors supply only the normalizer — the one part that varies per method —
// instead of re-implementing the Policy shell.
func Strict(normalize func(json.RawMessage) (string, error)) Policy {
	return strictFunc{normalize}
}

type strictFunc struct{ normalize func(json.RawMessage) (string, error) }

func (f strictFunc) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

func (f strictFunc) Normalize(value json.RawMessage) (string, error) {
	return f.normalize(value)
}

func (f strictFunc) Resolve(obs []Observation) (Decision, error) {
	return ResolveStrict(f, obs)
}

// ResolveStrict groups observations by their normalized value and verifies
// that exactly one group remains.
func ResolveStrict(policy Policy, obs []Observation) (Decision, error) {
	groups := make(map[string][]*pool.Source)
	values := make(map[string]json.RawMessage)
	for _, item := range obs {
		if item.Err != nil {
			continue
		}
		key, err := policy.Normalize(item.Value)
		if err != nil {
			continue
		}
		groups[key] = append(groups[key], item.Source)
		values[key] = item.Value
	}
	if len(groups) == 0 {
		return Decision{}, fmt.Errorf("strict: no valid response")
	}
	if len(groups) != 1 {
		// Conflict reporting must be reproducible: pick the winner by sorted
		// canonical key rather than map iteration order, so identical inputs
		// always yield identical Support, Reject, and Value evidence.
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		winner := keys[0]
		decision := Decision{Status: Verified, Value: values[winner], Support: groups[winner]}
		for _, key := range keys[1:] {
			decision.Reject = append(decision.Reject, groups[key]...)
		}
		return decision, ErrConflict
	}
	for key, support := range groups {
		return Decision{Status: Verified, Value: values[key], Support: support}, nil
	}
	panic("unreachable")
}
