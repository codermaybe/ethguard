package rpc

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// CorroborationMode selects how clustered admissible answers are adjudicated
// against the invocation's fixed voting basis (decision 0028).
type CorroborationMode int

const (
	// CorroborationMajority is the default mode: the winning cluster must
	// hold a strict majority of the voting basis and the support floor; a
	// minority of dissenting admissible answers does not fail the call.
	CorroborationMajority CorroborationMode = iota
	// CorroborationUnanimity additionally requires every admissible answer
	// to agree. It applies the same support floor and majority bound, so a
	// smaller surviving set cannot succeed solely because survivors agree.
	CorroborationUnanimity
	// CorroborationForwarded is intentional single-source mode. The selected
	// source is still subject to the method policy's local normalization and
	// binding checks, but no corroboration claim is made.
	CorroborationForwarded
)

// String names the mode for diagnostics.
func (m CorroborationMode) String() string {
	switch m {
	case CorroborationMajority:
		return "majority"
	case CorroborationUnanimity:
		return "unanimity"
	case CorroborationForwarded:
		return "forwarded"
	default:
		return fmt.Sprintf("corroboration(%d)", int(m))
	}
}

// Corroboration is the adjudication snapshot of one invocation: the mode and
// the support floor H captured before any request is issued (decision 0025).
// Later configuration changes never rewrite a captured snapshot.
type Corroboration struct {
	// Mode is the adjudication mode; the zero value is majority.
	Mode CorroborationMode
	// Floor is H, the minimum winning-cluster size the caller configures.
	// The zero value keeps the pure majority bound; the client-level default
	// (decision 0029, H=2) is supplied by the config package.
	Floor int
}

// Required returns the support a winning cluster must hold for a voting
// basis of n members: Q = max(H, floor(n/2)+1) (decisions 0028 and 0029).
// Every member of the basis counts, including those that answered invalidly
// or were not requested at all.
func (c Corroboration) Required(n int) int {
	q := n/2 + 1
	if c.Floor > q {
		q = c.Floor
	}
	return q
}

// adjudicate resolves size-ranked clusters (ties broken by canonical key)
// against a voting basis of n members. The winner's evidence is reported
// whether the decision succeeds or fails, so identical inputs always yield
// identical Support, Reject, and Value.
func (c Corroboration) adjudicate(n int, ranked []cluster, faults []error) (Decision, error) {
	var decision Decision
	decision.Faults = faults
	winner := ranked[0]
	decision.Value = winner.value
	decision.Evidence = append(json.RawMessage(nil), winner.evidence...)
	decision.Support = winner.sources
	for _, cl := range ranked[1:] {
		decision.Reject = append(decision.Reject, cl.sources...)
	}

	size := len(winner.sources)
	if c.Mode == CorroborationUnanimity && len(ranked) > 1 {
		return decision, fmt.Errorf("strict: %d disagreeing values: %w", len(ranked), ErrConflict)
	}
	if required := c.Required(n); size < required {
		return decision, fmt.Errorf("strict: required support %d, winning cluster %d of %d voting members: %w",
			required, size, n, ErrConflict)
	}
	// Verified claims corroboration, so a lone admitted response stays
	// Selected even where the floor lets it through (decision 0028).
	if size == 1 {
		decision.Status = Selected
		return decision, nil
	}
	decision.Status = Verified
	return decision, nil
}

// CorroborationShared is the runtime-mutable corroboration configuration
// shared by one client's aggregators. Set takes effect for invocations that
// start after the call; an invocation in flight keeps the snapshot it
// captured at its start (decisions 0025 and 0027). Mode and floor change
// together or not at all.
type CorroborationShared struct {
	v atomic.Pointer[Corroboration]
}

// NewCorroborationShared builds the shared configuration, rejecting unknown
// modes and floors below one.
func NewCorroborationShared(mode CorroborationMode, floor int) (*CorroborationShared, error) {
	if err := validateCorroboration(mode, floor); err != nil {
		return nil, err
	}
	s := &CorroborationShared{}
	s.v.Store(&Corroboration{Mode: mode, Floor: floor})
	return s, nil
}

// Snapshot returns the current mode and floor. The zero receiver behaves as
// an unset configuration: majority with no floor.
func (s *CorroborationShared) Snapshot() Corroboration {
	if s == nil {
		return Corroboration{}
	}
	if c := s.v.Load(); c != nil {
		return *c
	}
	return Corroboration{}
}

// Set atomically replaces the mode and floor for subsequent invocations.
func (s *CorroborationShared) Set(mode CorroborationMode, floor int) error {
	if err := validateCorroboration(mode, floor); err != nil {
		return err
	}
	s.v.Store(&Corroboration{Mode: mode, Floor: floor})
	return nil
}

func validateCorroboration(mode CorroborationMode, floor int) error {
	switch mode {
	case CorroborationMajority, CorroborationUnanimity, CorroborationForwarded:
	default:
		return fmt.Errorf("rpc: unknown corroboration mode %d", int(mode))
	}
	if mode != CorroborationForwarded && floor < 1 {
		return fmt.Errorf("rpc: corroboration support floor must be at least 1, got %d", floor)
	}
	if mode == CorroborationForwarded && floor != 0 {
		return fmt.Errorf("rpc: forwarded mode has no corroboration support floor, got %d", floor)
	}
	return nil
}
