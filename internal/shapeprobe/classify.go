package shapeprobe

import (
	"encoding/json"
	"fmt"

	"ethguard/rpc"
	"ethguard/rpc/projection"
)

// FieldClassification is a report-only projection of one observation batch.
// Core fields are declared by the method's current standard projection;
// extensions are observed values outside that projection. Diagnostics retain
// values that did not meet the fixed-basis threshold or had multiple
// threshold-qualified values.
type FieldClassification struct {
	Core        []rpc.FieldObservation
	Extensions  []rpc.FieldObservation
	Diagnostics rpc.FieldDiagnostics
}

// ClassifyFields applies the same fixed-basis field evaluator used by the
// runtime observation seam, then projects accepted paths through the current
// method field declaration. This is deliberately offline/report-only.
func ClassifyFields(method string, samples []FieldSample) (FieldClassification, error) {
	observations := make([]rpc.Observation, len(samples))
	for i, sample := range samples {
		observations[i] = rpc.Observation{
			Source: rpc.SourceRef{ID: sample.SourceID},
			Value:  append(json.RawMessage(nil), sample.Result...),
		}
	}
	evaluation, err := rpc.AggregateFieldsWithCorroboration(observations, rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2})
	if err != nil {
		return FieldClassification{}, err
	}
	projected := projection.ProjectStandard(evaluation, standardFields(method))
	return FieldClassification{
		Core:        projected.Core,
		Extensions:  projected.AcceptedExtensions,
		Diagnostics: evaluation.Diagnostics,
	}, nil
}

func standardFields(method string) []projection.StandardField {
	if fields, err := projection.ExecutionAPIStandardFields(method); err == nil {
		return fields
	}
	// Scalar results use the empty JSON Pointer path. Local object methods
	// remain extension observations until their official result schema is
	// registered; this fallback never promotes nested fields to Core.
	return []projection.StandardField{{Path: "", Required: true}}
}

func formatClassification(method string, samples []FieldSample) (string, error) {
	classification, err := ClassifyFields(method, samples)
	if err != nil {
		return "", fmt.Errorf("%s: %w", method, err)
	}
	return fmt.Sprintf("core=%d extensions=%d residuals=%d conflicts=%d",
		len(classification.Core), len(classification.Extensions),
		len(classification.Diagnostics.Residuals), len(classification.Diagnostics.Conflicts)), nil
}
