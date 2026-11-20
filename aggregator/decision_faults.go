package aggregator

import (
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// The invocation fault contract. Every pipeline exit that returns a decision
// — the degraded Prepare and reference exits, the two unusable-fan-out exits,
// and the normal projection exit — completes its fault fields through this
// one assembly point, so the four fields cannot drift apart per path:
//
//   - CollectionFaults repeats the pool-level fan-out failure, once;
//   - SourceFaults is the structured per-source attribution of every source
//     the round can blame, including gated members whose request never
//     started;
//   - Faults stays the single human-readable list covering the field
//     aggregation outcome, the collection failure, and the same source faults
//     SourceFaults attributes;
//   - Diagnostics carries the field-aggregation observations whenever the
//     aggregation produced any.
//
// Request-side failures (Prepare, reference establishment) ride the returned
// error instead: no source was attempted, so the fault tables stay empty by
// contract, not by omission. Every wrapped cause keeps its errors.Is chain.
//
// verdicts is the resolution's per-source statement when the policy stated
// one; degraded exits pass nil and the validity contract classifies instead.
//
// The two per-source tables come from one classifier and one pass over the
// invocation's emissions: an attempted source whose answer failed its class
// carries the human-readable fault and the structured attribution together,
// an unattempted member that failed a scheduling gate carries the structured
// attribution alone (the human-readable list stays unchanged), and a stated
// verdict is authoritative for its source. Both tables keep the emissions'
// order.
func (state *invocationState) attachFaults(decision *rpc.Decision, verdicts []rpc.SourceVerdict) {
	if state.aggregateErr != nil {
		decision.Faults = append(decision.Faults, fmt.Errorf("aggregate fields: %w", state.aggregateErr))
	}
	decision.Faults = append(decision.Faults, state.aggregateFaults...)
	if state.collectErr != nil {
		decision.Faults = append(decision.Faults, CollectFault(state.collectErr))
		decision.CollectionFaults = collectionFaultList(state.collectErr)
	}
	attemptClass := attemptClassifier(state.resolverPolicy(), verdicts)
	for _, emission := range state.emissions {
		if emission.Err == nil {
			continue
		}
		if emission.Attempted && attemptClass(emission) {
			continue
		}
		decision.SourceFaults = append(decision.SourceFaults, rpc.SourceFault{
			Source: SourceRefOf(emission.Src),
			Err:    emission.Err,
		})
		if emission.Attempted {
			decision.Faults = append(decision.Faults, fmt.Errorf("source %q: %w", emission.Src.Name(), emission.Err))
		}
	}
	if state.evaluation != nil {
		decision.Diagnostics = &state.evaluation.Diagnostics
	}
}
