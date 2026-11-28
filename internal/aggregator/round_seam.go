package aggregator

import (
	"fmt"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcengine"
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
// pipeline resolves against, retaining its response and method evidence.
func ObservationFromEmission(emission pool.Emission) rpc.Observation {
	return rpc.Observation{Attempted: emission.Attempted, Source: SourceRefOf(emission.Src), Response: emission.Response, Evidence: emission.Evidence}
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

// RoundOutcome preserves source facts on exits before adjudication. Available
// answers remain unassessed, and the conclusion remains ReasonUnassessed.
func RoundOutcome(emissions []pool.Emission) rpcengine.Outcome {
	obs := make([]rpc.Observation, len(emissions))
	for i, emission := range emissions {
		obs[i] = ObservationFromEmission(emission)
	}
	return rpcengine.UnassessedOutcome(obs)
}

// RoundAttempts consumes judgments already formed by the round.
func RoundAttempts(verdicts []rpcengine.SourceVerdict) func(pool.Emission) bool {
	return attemptClassifier(verdicts)
}

// CollectFault renders one fan-out-level failure the way decision faults
// spell it.
func CollectFault(err error) error {
	return fmt.Errorf("collect: %w", err)
}

// RejectUnsafeJSON refuses unsafe values without altering the protocol answer
// or its original bytes. Only delivered result answers undergo this check.
func RejectUnsafeJSON(emissions []pool.Emission) {
	for i := range emissions {
		response := &emissions[i].Response
		if response.Answer.Kind != rpc.AnswerResult || response.Err() != nil {
			continue
		}
		if err := jsonsafe.Validate(response.Answer.Result); err != nil {
			response.Answer.Interpretation = rpc.Interpretation{State: rpc.InterpretationRejected, Cause: fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)}
		}
	}
}
