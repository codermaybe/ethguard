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

// StrictOption tunes a strict policy at construction time.
type StrictOption func(*strictFunc)

// WithQuorum requires the winning cluster to hold at least n sources AND a
// majority of the whole fan-out before a decision is Verified.
//
// n is an integrity floor against collusion: size it above the number of
// sources that could be compromised together. The majority clause is measured
// against every attempted source, not only the survivors, so knocking honest
// sources out of a call shrinks the attacker's achievable ratio instead of
// growing it; forging agreement requires corrupting more than half of the
// fleet rather than more than half of whoever is left.
//
// A non-positive n is a configuration error.
func WithQuorum(n int) StrictOption {
	return func(s *strictFunc) {
		if n <= 0 {
			panic("rpc: WithQuorum: n must be positive")
		}
		s.minQuorum = n
	}
}

// Strict binds a response normalizer to strict-consensus resolution. Method
// authors supply only the normalizer — the one part that varies per method —
// instead of re-implementing the Policy shell.
//
// Without options, every valid response must agree on one canonical value,
// and at least two responses are required: a single uncorroborated response
// is downgraded to Selected instead of being claimed as Verified.
func Strict(normalize func(json.RawMessage) (string, error), opts ...StrictOption) Policy {
	p := strictFunc{normalize: normalize}
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

type strictFunc struct {
	normalize func(json.RawMessage) (string, error)
	minQuorum int
}

func (f strictFunc) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

func (f strictFunc) Normalize(value json.RawMessage) (string, error) {
	return f.normalize(value)
}

func (f strictFunc) Resolve(obs []Observation) (Decision, error) {
	var decision Decision
	groups := make(map[string][]*pool.Source)
	values := make(map[string]json.RawMessage)
	valid := 0
	for _, item := range obs {
		if item.Err != nil {
			continue
		}
		key, err := f.normalize(item.Value)
		if err != nil {
			decision.Faults = append(decision.Faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		groups[key] = append(groups[key], item.Source)
		values[key] = item.Value
		valid++
	}
	if len(groups) == 0 {
		return Decision{Faults: decision.Faults}, fmt.Errorf(
			"strict: no source produced a valid response (%d attempted): %w", len(obs), ErrNoValidResponse)
	}

	// Evidence must be reproducible: rank clusters by size and break ties by
	// canonical key, so identical inputs always yield identical Support,
	// Reject, and Value regardless of arrival order or map iteration.
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(groups[keys[i]]) != len(groups[keys[j]]) {
			return len(groups[keys[i]]) > len(groups[keys[j]])
		}
		return keys[i] < keys[j]
	})
	winner := keys[0]
	decision.Status = Verified
	decision.Value = values[winner]
	decision.Support = groups[winner]
	for _, key := range keys[1:] {
		decision.Reject = append(decision.Reject, groups[key]...)
	}

	if f.minQuorum == 0 {
		if len(groups) != 1 {
			return decision, fmt.Errorf("strict: %d disagreeing values: %w", len(groups), ErrConflict)
		}
		if valid == 1 {
			decision.Status = Selected // no corroboration, no Verified claim
		}
		return decision, nil
	}

	size := len(groups[winner])
	if size >= f.minQuorum && 2*size > len(obs) {
		return decision, nil
	}
	return decision, fmt.Errorf("strict: quorum %d unmet: top cluster %d of %d attempted sources: %w",
		f.minQuorum, size, len(obs), ErrConflict)
}
