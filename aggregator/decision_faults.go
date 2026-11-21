package aggregator

import (
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// attachFaults adds collection and internal diagnostics to the outcome.
// Source attribution is already projected by Outcome.ProjectSources.
func (state *invocationState) attachFaults(decision *rpc.Decision) {
	if state.aggregateErr != nil {
		decision.Faults = append(decision.Faults, fmt.Errorf("aggregate fields: %w", state.aggregateErr))
	}
	decision.Faults = append(decision.Faults, state.aggregateFaults...)
	if state.collectErr != nil {
		decision.Faults = append(decision.Faults, CollectFault(state.collectErr))
		decision.CollectionFaults = collectionFaultList(state.collectErr)
	}
	if state.evaluation != nil {
		decision.Diagnostics = &state.evaluation.Diagnostics
	}
}
