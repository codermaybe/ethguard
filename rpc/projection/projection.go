package projection

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
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

type standardProjector struct {
	exactPaths       map[string]struct{}
	wildcardPatterns [][]string
	requiredPaths    []string
}

// NewStandardProjector builds the reusable method projector used by the
// aggregator assembly map. It owns the compiled field declaration.
func NewStandardProjector(fields []StandardField) rpc.ObservationProjector {
	return compileStandardFields(fields)
}

func compileStandardFields(fields []StandardField) standardProjector {
	declared := make(map[string]StandardField, len(fields))
	p := standardProjector{exactPaths: make(map[string]struct{}, len(fields))}
	for _, field := range fields {
		declared[field.Path] = field
		// Requiredness belongs to each declaration, including duplicates, and
		// uses the original path rather than the matching normalization.
		if field.Required && !strings.Contains(field.Path, "/*") {
			p.requiredPaths = append(p.requiredPaths, field.Path)
		}
	}
	for path := range declared {
		path = strings.TrimPrefix(path, "/")
		parts := strings.Split(path, "/")
		wildcard := false
		for _, part := range parts {
			if part == "*" {
				wildcard = true
				break
			}
		}
		if wildcard {
			p.wildcardPatterns = append(p.wildcardPatterns, parts)
		} else {
			// The previous matcher removed one leading slash even for exact
			// declarations, so "" and "/" classify the same accepted paths.
			p.exactPaths[path] = struct{}{}
		}
	}
	return p
}

func (p standardProjector) ProjectObservation(value json.RawMessage, evaluation rpc.Evaluation) (rpc.ExtensionSet, error) {
	return p.project(evaluation).Carrier(value)
}

// ProjectStandard classifies already-accepted observations into a method's
// declared fields. It does not admit responses and it does not choose the
// comparison key. An accepted field is core only when its path is declared;
// accepted fields outside the declaration become extensions. Required fields
// absent from Accepted are reported in Missing. Diagnostics stay on the
// evaluation and do not become extensions.
func ProjectStandard(evaluation rpc.Evaluation, fields []StandardField) Projection {
	return compileStandardFields(fields).project(evaluation)
}

func (p standardProjector) project(evaluation rpc.Evaluation) Projection {
	result := Projection{}
	seen := make(map[string]struct{}, len(evaluation.Accepted))
	for _, field := range evaluation.Accepted {
		seen[field.Path] = struct{}{}
		if p.matchesPath(field.Path) {
			result.Core = append(result.Core, field)
		} else {
			result.AcceptedExtensions = append(result.AcceptedExtensions, field)
		}
	}
	for _, path := range p.requiredPaths {
		if _, ok := seen[path]; !ok {
			result.Missing = append(result.Missing, path)
		}
	}
	sort.Slice(result.Core, func(i, j int) bool { return result.Core[i].Path < result.Core[j].Path })
	sort.Slice(result.AcceptedExtensions, func(i, j int) bool { return result.AcceptedExtensions[i].Path < result.AcceptedExtensions[j].Path })
	sort.Strings(result.Missing)
	return result
}

func (p standardProjector) matchesPath(path string) bool {
	path = strings.TrimPrefix(path, "/")
	if _, ok := p.exactPaths[path]; ok {
		return true
	}
	if len(p.wildcardPatterns) == 0 {
		return false
	}
	parts := strings.Split(path, "/")
	for _, pattern := range p.wildcardPatterns {
		if matchJSONPointerParts(pattern, parts) {
			return true
		}
	}
	return false
}

func matchJSONPointerPattern(pattern, path string) bool {
	patternParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	pathParts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	return matchJSONPointerParts(patternParts, pathParts)
}

func matchJSONPointerParts(patternParts, pathParts []string) bool {
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
