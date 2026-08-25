package observe

import (
	"errors"
	"time"

	"ethguard/pool"
	"ethguard/rpc"
)

// BuildDecision converts one completed aggregation from the internal model
// into a neutral event. resolveErr is authoritative: a decision that carries
// evidence but was rejected (for example by conflict detection) reports
// status "failed" with Conflict set, never a success class.
//
// Emissions whose call never started (Attempted is false) are skipped:
// they were scheduled but expired waiting for a slot, so they carry no
// observation.
func BuildDecision(started time.Time, method string, decision rpc.Decision, resolveErr error, emissions []pool.Emission) DecisionEvent {
	event := DecisionEvent{
		Method:   method,
		Duration: time.Since(started),
		Faults:   len(decision.Faults),
	}
	if resolveErr != nil {
		event.Status = "failed"
		event.Conflict = errors.Is(resolveErr, rpc.ErrConflict)
	} else {
		event.Status = decision.Status.String()
	}
	for _, emission := range emissions {
		if !emission.Attempted {
			continue
		}
		event.Attempts = append(event.Attempts, SourceEvent{
			Source: emission.Src.Name(),
			RTT:    emission.RTT,
			Failed: emission.Err != nil,
		})
	}
	return event
}
