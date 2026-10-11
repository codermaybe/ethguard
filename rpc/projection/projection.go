package projection

import (
	"encoding/json"
	"sort"
	"strings"

	"ethguard/rpc"
)

// StandardField declares one method-level field in the semantic projection
// room. Paths use the same JSON Pointer notation produced by
// rpc.AggregateFields. Requiredness is a method contract, not a property of
// the generic observation room.
type StandardField struct {
	Path     string
	Required bool
}

// Projection is the result of projecting an evaluation into one method's
// compile-time standard field set. Extensions contain only accepted
// source-backed values; they are never synthesized from diagnostics.
type Projection struct {
	Core               []rpc.FieldObservation
	AcceptedExtensions []rpc.FieldObservation
	Missing            []string
}

type standardProjector struct{ fields []StandardField }

// NewStandardProjector builds the reusable method projector used by the
// aggregator assembly map. It owns a copy of the field declaration.
func NewStandardProjector(fields []StandardField) rpc.ObservationProjector {
	return standardProjector{fields: append([]StandardField(nil), fields...)}
}

func (p standardProjector) ProjectObservation(value json.RawMessage, evaluation rpc.Evaluation) (rpc.ExtensionSet, error) {
	return ProjectStandard(evaluation, p.fields).Carrier(value)
}

// ProjectStandard classifies already-accepted observations into a method's
// declared fields. It does not admit responses and it does not choose the
// comparison key. An accepted field is core only when its path is declared;
// accepted fields outside the declaration become extensions. Required fields
// absent from Accepted are reported in Missing. Diagnostics stay on the
// evaluation and do not become extensions.
func ProjectStandard(evaluation rpc.Evaluation, fields []StandardField) Projection {
	declared := make(map[string]StandardField, len(fields))
	for _, field := range fields {
		declared[field.Path] = field
	}

	result := Projection{}
	seen := make(map[string]struct{}, len(evaluation.Accepted))
	for _, field := range evaluation.Accepted {
		seen[field.Path] = struct{}{}
		if _, ok := standardFieldForPath(field.Path, declared); ok {
			result.Core = append(result.Core, field)
		} else {
			result.AcceptedExtensions = append(result.AcceptedExtensions, field)
		}
	}
	for _, field := range fields {
		if field.Required {
			if strings.Contains(field.Path, "/*") {
				continue
			}
			if _, ok := seen[field.Path]; !ok {
				result.Missing = append(result.Missing, field.Path)
			}
		}
	}
	sort.Slice(result.Core, func(i, j int) bool { return result.Core[i].Path < result.Core[j].Path })
	sort.Slice(result.AcceptedExtensions, func(i, j int) bool { return result.AcceptedExtensions[i].Path < result.AcceptedExtensions[j].Path })
	sort.Strings(result.Missing)
	return result
}

func standardFieldForPath(path string, fields map[string]StandardField) (StandardField, bool) {
	if field, ok := fields[path]; ok {
		return field, true
	}
	for pattern, field := range fields {
		if matchJSONPointerPattern(pattern, path) {
			return field, true
		}
	}
	return StandardField{}, false
}

func matchJSONPointerPattern(pattern, path string) bool {
	patternParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	pathParts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i := range patternParts {
		if patternParts[i] != "*" && patternParts[i] != pathParts[i] {
			return false
		}
	}
	return true
}

// Carrier binds accepted non-core fields to the selected core response. No
// extension is interpreted as a standard Ethereum field.
func (p Projection) Carrier(coreValue []byte) (rpc.ExtensionSet, error) {
	return rpc.NewExtensionSet(coreValue, p.AcceptedExtensions)
}

// NewSchemaProjector builds a projector directly from the pinned official
// result schema. The schema is the sole field declaration; method packages do
// not maintain a second StandardFields table.
func NewSchemaProjector(method string) (rpc.ObservationProjector, error) {
	fields, err := ExecutionAPIStandardFields(method)
	if err != nil {
		return nil, err
	}
	return NewStandardProjector(fields), nil
}

// ScalarStandardProjector projects scalar result methods such as balance and
// call. The root JSON path is the scalar value itself.
func ScalarStandardProjector() rpc.ObservationProjector {
	return NewStandardProjector([]StandardField{{Path: "", Required: true}})
}
