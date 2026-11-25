package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrInvalidCanonicalResult is the internal contract sentinel of the
// canonical seam: a constructor handed illegal output, a processor returned a
// result beside an error, a success observation lacks its result, or a
// consumer capability/slot pairing is broken. It is a process defect, not a
// provider verdict: the coordinator must distinguish it from
// ErrMalformedResponse before attributing anything to a source.
//
// The canonical single-source processing seam uses one method-owned processor
// to turn one source's raw response into an immutable canonical result, and the
// strict resolver consumes those results directly: the canonical bytes are
// the comparison key, so the resolver never re-runs the method's raw
// normalizer or encoder. The seam is a method-processing entry point, not a
// registry: every processor is installed by its method's spec at assembly,
// and the coordinator selects the route once per invocation.
//
// The design contract this file implements lives in docs/design/codebase-topology.md
// and the return-value design: the result carries no source
// identity, request, or invocation token; the coordinator places one result
// back into the one source slot it came from; results are used only inside
// the current invocation, and nothing here interprets Ethereum fields.
var ErrInvalidCanonicalResult = errors.New("rpc: invalid canonical result")

// CanonicalProcessor is one method's controlled processing entry. The
// coordinator calls it at most once per available source per invocation with
// the frozen semantic request (numbers preserved as json.Number), that
// source's raw response, and the same-source caller the admission phase
// already binds. A processor failure is the method's own admission-class
// verdict for that source; a processor must return a zero result whenever it
// returns a non-nil error, and a non-zero result beside an error is the
// internal contract error, not a provider fault.
type CanonicalProcessor func(context.Context, Request, json.RawMessage, AdmissionCaller) (CanonicalResult, error)

// CanonicalResult is one source's immutable processed answer: the canonical
// JSON bytes the method would commit for that response, plus the admission
// evidence the same processing run produced. The private strings make the
// bytes immutable after construction; the getters return copies. The empty
// value is the zero result — no success; a legal root null is the non-empty
// bytes "null". The result carries no key of its own: consumers cluster on
// the canonical bytes directly.
type CanonicalResult struct {
	value    string
	evidence string
}

// NewCanonicalResult validates one processing outcome's handoff structure:
// non-empty, well-formed JSON value bytes; evidence optional but well-formed
// when present. It does not interpret fields, reorder members, or judge
// business validity — that is the method processor's job, already done by the
// time a result reaches this constructor.
func NewCanonicalResult(value, evidence json.RawMessage) (CanonicalResult, error) {
	if len(value) == 0 {
		return CanonicalResult{}, fmt.Errorf("%w: empty canonical value", ErrInvalidCanonicalResult)
	}
	if !json.Valid(value) {
		return CanonicalResult{}, fmt.Errorf("%w: canonical value is not valid JSON", ErrInvalidCanonicalResult)
	}
	result := CanonicalResult{value: string(value)}
	if len(evidence) > 0 {
		if !json.Valid(evidence) {
			return CanonicalResult{}, fmt.Errorf("%w: canonical evidence is not valid JSON", ErrInvalidCanonicalResult)
		}
		result.evidence = string(evidence)
	}
	return result, nil
}

// Value returns a copy of the canonical bytes. The zero result has no value.
func (r CanonicalResult) Value() json.RawMessage {
	if r.value == "" {
		return nil
	}
	return json.RawMessage(r.value)
}

// Evidence returns a copy of the admission evidence, or nil when the
// processing run produced none. An explicit JSON null evidence stays "null".
func (r CanonicalResult) Evidence() json.RawMessage {
	if r.evidence == "" {
		return nil
	}
	return json.RawMessage(r.evidence)
}

// isZero reports whether the result carries no success. The zero value and a
// deliberately empty construction are the same: no processed answer.
func (r CanonicalResult) isZero() bool {
	return r.value == ""
}

// CanonicalObservation binds one source slot to its processed result. The
// observation keeps its existing meaning — raw identity, the coordinator's
// own error record, the original evidence — and is never rewritten with the
// canonical bytes. A failed observation keeps its slot without a result; a
// successful observation must carry one.
type CanonicalObservation struct {
	observation Observation
	result      CanonicalResult
}

// NewCanonicalObservation validates the slot pairing: a failed observation
// carries no result, a successful one must. The source identity must be
// present — the resolver clusters and classifies by it — and the result's
// bytes are not written back into the observation.
func NewCanonicalObservation(observation Observation, result CanonicalResult) (CanonicalObservation, error) {
	if observation.Source.ID == "" {
		return CanonicalObservation{}, fmt.Errorf("%w: canonical observation has no source identity", ErrInvalidCanonicalResult)
	}
	if observation.Response.Err() != nil && !result.isZero() {
		return CanonicalObservation{}, fmt.Errorf("%w: failed observation %q carries a result", ErrInvalidCanonicalResult, observation.Source.ID)
	}
	if observation.Response.Err() == nil && result.isZero() {
		return CanonicalObservation{}, fmt.Errorf("%w: successful observation %q carries no result", ErrInvalidCanonicalResult, observation.Source.ID)
	}
	return CanonicalObservation{observation: observation, result: result}, nil
}

// Observation returns the bound observation unchanged.
func (c CanonicalObservation) Observation() Observation { return c.observation }

// Result returns the processed result.
func (c CanonicalObservation) Result() CanonicalResult { return c.result }

// CanonicalOutcomeResolver is the strict resolver's consumption capability
// for processed observations. Both entries resolve with the same clustering,
// adjudication, verdicts, and commit kernel as the raw entries; they differ
// only in the candidate material. ResolveCanonical honors the resolver's
// construction-time corroboration configuration exactly as Resolve
// does, and ResolveCanonicalCorroborated adjudicates under the invocation's
// frozen snapshot exactly as ResolveCorroborated does.
type CanonicalOutcomeResolver interface {
	ResolveCanonical([]CanonicalObservation) (Outcome, error)
	ResolveCanonicalCorroborated(Corroboration, []CanonicalObservation) (Outcome, error)
}
