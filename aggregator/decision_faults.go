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
func (state *invocationState) attachFaults(decision *rpc.Decision, verdicts []rpc.SourceVerdict) {
	if state.aggregateErr != nil {
		decision.Faults = append(decision.Faults, fmt.Errorf("aggregate fields: %w", state.aggregateErr))
	}
	decision.Faults = append(decision.Faults, state.aggregateFaults...)
	if state.collectErr != nil {
		decision.Faults = append(decision.Faults, CollectFault(state.collectErr))
		decision.CollectionFaults = collectionFaultList(state.collectErr)
	}
	decision.SourceFaults = append(decision.SourceFaults, structuredSourceFaults(state.validityPolicy(), state.emissions, verdicts)...)
	decision.Faults = append(decision.Faults, sourceFaults(state.validityPolicy(), state.emissions, verdicts)...)
	if state.evaluation != nil {
		decision.Diagnostics = &state.evaluation.Diagnostics
	}
}
