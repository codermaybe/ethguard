package rpcengine

import (
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

func rankedAdjudication(basis, required int, ranked []cluster) rpc.Adjudication {
	a := rpc.Adjudication{Basis: basis, Required: required, Reason: rpc.ReasonNoCandidate}
	for _, group := range ranked {
		a.Candidates = append(a.Candidates, rpc.CandidateGroup{Sources: group.sources})
	}
	return a
}

// Corroboration is a construction-time mode and support floor. Each invocation
// copies this rule into its frozen membership Basis.
type Corroboration struct {
	// Mode is the adjudication mode; the zero value is majority.
	Mode rpc.CorroborationMode
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
	return newAcceptanceCalculator(c).required(n)
}

// winnerDecision assembles the shared winning-cluster decision, the one
// block every adjudication starts from: the winner's value, provenance, and
// representative, the losing clusters as rejects, and the given fault list.
// The status stays Unknown until the caller's rule decides it.
func winnerDecision(ranked []cluster, faults []error) rpc.Decision {
	var decision rpc.Decision
	decision.Status = rpc.Unknown
	decision.Faults = faults
	winner := ranked[0]
	decision.Value = winner.value
	decision.Provenance = append(json.RawMessage(nil), winner.evidence...)
	decision.Representative = winner.sources[0]
	decision.Support = winner.sources
	for _, cl := range ranked[1:] {
		decision.Reject = append(decision.Reject, cl.sources...)
	}
	return decision
}

// adjudicate resolves size-ranked clusters (ties broken by canonical key)
// against a voting basis of n members. The winner's evidence is reported
// whether the decision succeeds or fails, so identical inputs always yield
// identical Support and Reject. Value, Provenance, and Representative come from
// the first supporter of the winning cluster in the captured member order.
func (c Corroboration) adjudicate(n int, ranked []cluster, faults []error) (rpc.Decision, error) {
	decision := winnerDecision(ranked, faults)
	decision.Adjudication = rankedAdjudication(n, c.Required(n), ranked)
	size := len(ranked[0].sources)
	if c.Mode == rpc.CorroborationUnanimity && len(ranked) > 1 {
		decision.Adjudication.Reason = rpc.ReasonConflictingCandidates
		return decision, fmt.Errorf("strict: %d disagreeing values: %w", len(ranked), rpc.ErrConflict)
	}
	if c.Mode == rpc.CorroborationForwarded {
		decision.Adjudication.Required, decision.Adjudication.Reason = 1, rpc.ReasonAccepted
		decision.Status = rpc.Forwarded
		return decision, nil
	}
	calculator := newAcceptanceCalculator(c)
	if !calculator.accepts(n, size) {
		required := calculator.required(n)
		decision.Adjudication.Reason = rpc.ReasonInsufficientSupport
		cause := rpc.ErrInsufficientSupport
		if len(ranked) > 1 {
			decision.Adjudication.Reason, cause = rpc.ReasonConflictingCandidates, rpc.ErrConflict
		}
		return decision, fmt.Errorf("strict: required support %d, winning cluster %d of %d voting members: %w",
			required, size, n, cause)
	}
	decision.Adjudication.Reason = rpc.ReasonAccepted
	// Corroborated claims corroboration, so a lone admitted response stays
	// Selected even where the floor lets it through (decision 0028).
	if size == 1 {
		decision.Status = rpc.Selected
		return decision, nil
	}
	decision.Status = rpc.Corroborated
	return decision, nil
}

// NewCorroboration validates the client construction rule.
func NewCorroboration(mode rpc.CorroborationMode, floor int) (Corroboration, error) {
	if err := validateCorroboration(mode, floor); err != nil {
		return Corroboration{}, err
	}
	return Corroboration{Mode: mode, Floor: floor}, nil
}

func validateCorroboration(mode rpc.CorroborationMode, floor int) error {
	switch mode {
	case rpc.CorroborationMajority, rpc.CorroborationUnanimity, rpc.CorroborationForwarded:
	default:
		return fmt.Errorf("rpc: unknown corroboration mode %d", int(mode))
	}
	if mode != rpc.CorroborationForwarded && floor < 1 {
		return fmt.Errorf("rpc: corroboration support floor must be at least 1, got %d", floor)
	}
	if mode == rpc.CorroborationForwarded && floor != 0 {
		return fmt.Errorf("rpc: forwarded mode has no corroboration support floor, got %d", floor)
	}
	return nil
}
