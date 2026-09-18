package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ethguard/internal/jsonsafe"
)

// FieldObservation is one JSON path/value pair observed from one or more
// upstreams. Value is copied and remains valid after the input observations
// are released.
type FieldObservation struct {
	Path    string
	Value   json.RawMessage
	Sources []SourceRef
}

// ConflictField retains all values observed for a path when no single value
// can be selected as the common value. It is intentionally semantic-free: the
// rpc package does not decide whether a field is standard or Ethereum-specific.
type ConflictField struct {
	Path   string
	Values []FieldObservation
}

// CommonObservation is the output of the generic observation room. Common
// contains paths with exactly one value meeting quorum. Residuals contains
// values that did not meet quorum. Conflicts contains paths with multiple
// quorum-qualified values. Method packages may project Common into their
// standard fields and attach the remaining observations as extensions.
type CommonObservation struct {
	Common    []FieldObservation
	Residuals []FieldObservation
	Conflicts []ConflictField
}

// AggregateFields flattens object and array responses into JSON Pointer paths
// and groups equal values by source. A path enters Common only when exactly
// one value reaches quorum. Multiple values reaching quorum are reported as a
// conflict rather than choosing arbitrarily. Quorum must be positive.
//
// This function performs only generic JSON work. It does not know Ethereum
// field names, scalar kinds, method semantics, or whether a field is part of
// a standard response.
func AggregateFields(observations []Observation, quorum int) (CommonObservation, error) {
	if quorum <= 0 {
		return CommonObservation{}, fmt.Errorf("rpc: observation quorum must be positive")
	}

	type candidate struct {
		value   json.RawMessage
		sources []SourceRef
	}
	groups := make(map[string]map[string]*candidate)
	seen := make(map[string]int, len(observations))
	for i, observation := range observations {
		id := observation.Source.ID
		if id == "" {
			return CommonObservation{}, fmt.Errorf("rpc: observation %d has no source identity", i)
		}
		if first, ok := seen[id]; ok {
			return CommonObservation{}, fmt.Errorf("rpc: duplicate observation source %q at %d and %d", id, first, i)
		}
		seen[id] = i
		if observation.Err != nil || observation.Value == nil {
			continue
		}

		value, err := decodeObservedJSON(observation.Value)
		if err != nil {
			return CommonObservation{}, fmt.Errorf("rpc: source %q: %w: %v", id, ErrMalformedResponse, err)
		}
		fields := make(map[string]json.RawMessage)
		flattenJSON("", value, fields)
		for path, raw := range fields {
			key := string(raw)
			byValue := groups[path]
			if byValue == nil {
				byValue = make(map[string]*candidate)
				groups[path] = byValue
			}
			item := byValue[key]
			if item == nil {
				item = &candidate{value: append(json.RawMessage(nil), raw...)}
				byValue[key] = item
			}
			item.sources = append(item.sources, observation.Source)
		}
	}

	var result CommonObservation
	for path, byValue := range groups {
		var qualified []FieldObservation
		var residuals []FieldObservation
		for _, item := range byValue {
			sort.Slice(item.sources, func(i, j int) bool {
				if item.sources[i].ID != item.sources[j].ID {
					return item.sources[i].ID < item.sources[j].ID
				}
				return item.sources[i].Name < item.sources[j].Name
			})
			field := FieldObservation{
				Path:    path,
				Value:   append(json.RawMessage(nil), item.value...),
				Sources: append([]SourceRef(nil), item.sources...),
			}
			if len(item.sources) >= quorum {
				qualified = append(qualified, field)
			} else {
				residuals = append(residuals, field)
			}
		}
		sort.Slice(qualified, func(i, j int) bool { return string(qualified[i].Value) < string(qualified[j].Value) })
		sort.Slice(residuals, func(i, j int) bool { return string(residuals[i].Value) < string(residuals[j].Value) })
		switch len(qualified) {
		case 0:
			result.Residuals = append(result.Residuals, residuals...)
		case 1:
			result.Common = append(result.Common, qualified[0])
			result.Residuals = append(result.Residuals, residuals...)
		default:
			result.Conflicts = append(result.Conflicts, ConflictField{Path: path, Values: qualified})
			result.Residuals = append(result.Residuals, residuals...)
		}
	}

	sortFieldObservations(result.Common)
	sortFieldObservations(result.Residuals)
	sort.Slice(result.Conflicts, func(i, j int) bool { return result.Conflicts[i].Path < result.Conflicts[j].Path })
	return result, nil
}

func decodeObservedJSON(raw []byte) (any, error) {
	if err := jsonsafe.Validate(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func flattenJSON(path string, value any, fields map[string]json.RawMessage) {
	switch current := value.(type) {
	case map[string]any:
		if len(current) == 0 {
			fields[path] = mustMarshal(current)
			return
		}
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			flattenJSON(joinJSONPointer(path, key), current[key], fields)
		}
	case []any:
		if len(current) == 0 {
			fields[path] = mustMarshal(current)
			return
		}
		for index, item := range current {
			flattenJSON(joinJSONPointer(path, fmt.Sprint(index)), item, fields)
		}
	default:
		fields[path] = mustMarshal(current)
	}
}

func joinJSONPointer(path, token string) string {
	token = strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
	return path + "/" + token
}

func mustMarshal(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("rpc: marshal validated JSON value: %v", err))
	}
	return raw
}

func sortFieldObservations(fields []FieldObservation) {
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].Path != fields[j].Path {
			return fields[i].Path < fields[j].Path
		}
		return string(fields[i].Value) < string(fields[j].Value)
	})
}
