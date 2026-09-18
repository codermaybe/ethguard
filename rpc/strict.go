package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"ethguard/internal/jsonsafe"
)

// CanonicalJSON reduces any JSON value to a comparable canonical form by
// decoding and re-encoding it, erasing key order and formatting differences.
func CanonicalJSON(value json.RawMessage) (string, error) {
	var decoded any
	if err := jsonsafe.Decode(value, &decoded); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(decoded)
	return string(canonical), err
}

// cluster is one equivalence class of admissible answers: every source whose
// response normalizes to the same key.
type cluster struct {
	key      string
	value    json.RawMessage
	evidence json.RawMessage
	sources  []SourceRef
}

// StrictOption tunes a strict policy at construction time.
type StrictOption func(*strictConfig)

type strictConfig struct {
	mode            CorroborationMode
	modeSet         bool
	floor           int
	floorSet        bool
	includeRPCError bool
}

// WithQuorum sets the support floor H: the winning cluster must hold at
// least H sources AND a strict majority of the invocation's fixed voting
// basis, Q = max(H, floor(N/2)+1) (decisions 0028 and 0029).
//
// H is an integrity floor against collusion: size it above the number of
// sources that could be compromised together. The majority clause is
// measured against the whole voting basis, not only the sources that
// answered, so failures inside an invocation do not shrink the required
// support. A non-positive H is a configuration error. Even where the floor
// admits a single response (H = 1, one-member basis), that response stays
// Selected: a corroboration claim needs two supporters.
func WithQuorum(n int) StrictOption {
	return func(s *strictConfig) {
		if n <= 0 {
			panic("rpc: WithQuorum: n must be positive")
		}
		s.floor, s.floorSet = n, true
	}
}

// WithUnanimity selects the explicit valid-answer unanimity mode (decision
// 0029): the same support floor and majority bound as the default mode, plus
// the requirement that every admissible answer agree. It never admits a
// smaller surviving set solely because the survivors agree.
func WithUnanimity() StrictOption {
	return func(s *strictConfig) { s.mode, s.modeSet = CorroborationUnanimity, true }
}

// WithRPCErrorBodies opts a strict policy into normalizing and clustering
// structured JSON-RPC error bodies. It is intentionally opt-in: most method
// errors are not interchangeable with successful result values.
func WithRPCErrorBodies() StrictOption {
	return func(s *strictConfig) { s.includeRPCError = true }
}

// Strict binds a response normalizer to strict-consensus resolution. Method
// authors supply only the normalizer — the one part that varies per method —
// instead of re-implementing the Policy shell.
//
// Corroboration options install the decision-0028 adjudication (majority or
// unanimity over the invocation's fixed voting basis). Without one, the
// legacy decision-0006 default applies: every valid response must agree, and
// a single uncorroborated response is Selected rather than Verified. The
// legacy default is retained for direct construction — the reference
// establishment paths depend on it until T03 aligns them — while the
// client's default path always resolves under a captured corroboration
// snapshot through ResolveCorroborated.
func Strict(normalize func(json.RawMessage) (string, error), opts ...StrictOption) Policy {
	cfg := strictConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	p := strictFunc{normalize: normalize, includeRPCError: cfg.includeRPCError}
	if cfg.modeSet || cfg.floorSet {
		p.corroboration = &Corroboration{Mode: cfg.mode, Floor: cfg.floor}
	}
	return p
}

type strictFunc struct {
	normalize       func(json.RawMessage) (string, error)
	corroboration   *Corroboration
	includeRPCError bool
}

func (f strictFunc) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

func (f strictFunc) Normalize(value json.RawMessage) (string, error) {
	return f.normalize(value)
}

func (f strictFunc) RequiresAgreement() bool { return true }

func (f strictFunc) IncludeRPCError() bool { return f.includeRPCError }

func (f strictFunc) Resolve(obs []Observation) (Decision, error) {
	if f.corroboration == nil {
		return f.resolveLegacy(obs)
	}
	return f.resolve(*f.corroboration, obs)
}

// ResolveCorroborated resolves under the invocation's frozen corroboration
// snapshot, ignoring construction-time corroboration options. The aggregator
// calls this with the mode and floor captured before any request was issued
// (decision 0025).
func (f strictFunc) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	return f.resolve(c, obs)
}

// resolve clusters admissible answers and adjudicates them against the
// invocation's voting basis.
func (f strictFunc) resolve(c Corroboration, obs []Observation) (Decision, error) {
	ranked, faults, err := f.cluster(obs)
	if err != nil {
		return Decision{Faults: faults}, err
	}
	return c.adjudicate(len(obs), ranked, faults)
}

// resolveLegacy is the decision-0006 default for policies constructed
// without corroboration options: agreement among every valid response, with
// one uncorroborated response downgraded to Selected.
func (f strictFunc) resolveLegacy(obs []Observation) (Decision, error) {
	ranked, faults, err := f.cluster(obs)
	if err != nil {
		return Decision{Faults: faults}, err
	}
	var decision Decision
	decision.Faults = faults
	winner := ranked[0]
	decision.Value = winner.value
	decision.Evidence = append(json.RawMessage(nil), winner.evidence...)
	decision.Support = winner.sources
	for _, cl := range ranked[1:] {
		decision.Reject = append(decision.Reject, cl.sources...)
	}
	if len(ranked) != 1 {
		return decision, fmt.Errorf("strict: %d disagreeing values: %w", len(ranked), ErrConflict)
	}
	if len(winner.sources) == 1 {
		decision.Status = Selected // no corroboration, no Verified claim
	}
	return decision, nil
}

// cluster groups admissible answers by normalized form and ranks the
// clusters by size, breaking ties by canonical key, so identical inputs
// always yield identical Support, Reject, and Value regardless of arrival
// order or map iteration.
func (f strictFunc) cluster(obs []Observation) ([]cluster, []error, error) {
	var faults []error
	groups := make(map[string][]SourceRef)
	values := make(map[string]json.RawMessage)
	evidence := make(map[string]json.RawMessage)
	seen := make(map[string]int, len(obs))
	for i, item := range obs {
		id := item.Source.ID
		if id == "" {
			return nil, faults, fmt.Errorf("strict: observation %d has no source identity: %w", i, ErrConflict)
		}
		if first, dup := seen[id]; dup {
			return nil, faults, fmt.Errorf(
				"strict: duplicate observation for source %q (first at %d, again at %d): %w",
				id, first, i, ErrConflict)
		}
		seen[id] = i
		value := item.Value
		if item.Err != nil {
			if !f.includeRPCError || item.RPCError == nil {
				continue
			}
			value = item.RPCError.Raw()
		}
		key, err := f.normalize(value)
		if err != nil {
			faults = append(faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		groups[key] = append(groups[key], item.Source)
		values[key] = value
		if evidence[key] == nil && item.Evidence != nil {
			evidence[key] = append(json.RawMessage(nil), item.Evidence...)
		}
	}
	if len(groups) == 0 {
		return nil, faults, fmt.Errorf(
			"strict: no source produced a valid response (%d attempted): %w", len(obs), ErrNoValidResponse)
	}
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
	ranked := make([]cluster, 0, len(keys))
	for _, key := range keys {
		ranked = append(ranked, cluster{key: key, value: values[key], evidence: evidence[key], sources: groups[key]})
	}
	return ranked, faults, nil
}
