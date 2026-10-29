package aggregator

import (
	"fmt"

	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// The round classification seam. The read pipeline derives three statements
// from every completed fan-out: the observation each emission projects to, the
// fault tables the source answered (or was skipped) with, and the per-attempt
// classification for observability. Reference establishment needs the same
// statements without a second aggregation coordinator, so the statements live
// here once and both the pipeline and the establishment engine project rounds
// through this seam. It is evidence-only by contract: nothing here resolves,
// prepares, or touches a registry.

// ObservationFromEmission projects one emission into the observation the
// pipeline resolves against, attributing structured protocol errors through
// the project-owned capabilities.
func ObservationFromEmission(emission pool.Emission) rpc.Observation {
	return rpc.Observation{
		Source:   SourceRefOf(emission.Src),
		Value:    emission.Val,
		Err:      emission.Err,
		RPCError: ObservationRPCError(emission.Err),
		Evidence: emission.Evidence,
	}
}

// SourceRefOf copies a pool source's identity for evidence records; a nil
// source (an unattempted emission) yields the zero reference.
func SourceRefOf(src *pool.Source) rpc.SourceRef {
	if src == nil {
		return rpc.SourceRef{}
	}
	return rpc.SourceRef{Name: src.Name(), URL: src.URL(), ID: src.ID()}
}

// RoundHasUsable reports whether a fan-out produced anything a resolution
// could even start from: a delivered value or a structured protocol error the
// policy domain may yet read.
func RoundHasUsable(emissions []pool.Emission) bool {
	return hasUsableEmission(emissions)
}

// RoundMalformedFault reports whether every delivered response failed the
// structural admission stage, and the classified error the pipeline surfaces
// for that state.
func RoundMalformedFault(emissions []pool.Emission) (bool, error) {
	return hasMalformedAdmission(emissions), firstMalformedAdmission(emissions)
}

// RoundFaults folds one completed round's fault tables against the policy's
// validity contract. The first table is the human-readable fault list used on
// decisions (attempted calls that failed their class), the second the
// structured per-source attribution reports use. When the policy stated
// per-source verdicts they remain authoritative for their sources.
func RoundFaults(policy rpc.Policy, emissions []pool.Emission, verdicts []rpc.SourceVerdict) (faultList []error, sourceFaultList []rpc.SourceFault) {
	return sourceFaults(policy, emissions, verdicts), structuredSourceFaults(policy, emissions, verdicts)
}

// RoundAttempts classifies the attempted emissions of one round for an
// observability event, reusing the resolution's per-source verdicts when the
// policy stated them and the policy's validity contract otherwise.
func RoundAttempts(policy rpc.Policy, verdicts []rpc.SourceVerdict) func(pool.Emission) bool {
	return attemptClassifier(policy, verdicts)
}

// CollectFault renders one fan-out-level failure the way decision faults
// spell it.
func CollectFault(err error) error {
	return fmt.Errorf("collect: %w", err)
}
