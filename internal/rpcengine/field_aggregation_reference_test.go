package rpcengine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/rpc"
)

// This test oracle preserves the aggregation implementation from c8c7f82.
// Keep its traversal, decoding, and sorting independent of the production
// helpers so changes to those helpers cannot change both sides of a comparison.
func aggregateFieldsReference(observations []rpc.Observation, quorum int) (Evaluation, error) {
	if quorum <= 0 {
		return Evaluation{}, fmt.Errorf("rpc: observation quorum must be positive")
	}

	type candidate struct {
		value   json.RawMessage
		sources []rpc.SourceRef
	}
	groups := make(map[string]map[string]*candidate)
	seen := make(map[string]int, len(observations))
	var result Evaluation
	for i, observation := range observations {
		id := observation.Source.ID
		if id == "" {
			return Evaluation{}, fmt.Errorf("rpc: observation %d has no source identity", i)
		}
		if first, ok := seen[id]; ok {
			return Evaluation{}, fmt.Errorf("rpc: duplicate observation source %q at %d and %d", id, first, i)
		}
		seen[id] = i
		if observation.Response.Err() != nil || observation.Response.Answer.Result == nil {
			continue
		}

		value, err := decodeObservedJSONReference(observation.Response.Answer.Result)
		if err != nil {
			result.Malformed = append(result.Malformed, MalformedSource{Source: observation.Source, Err: err})
			continue
		}
		fields := make(map[string]json.RawMessage)
		flattenJSONReference("", value, fields)
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

	if len(groups) == 0 && len(result.Malformed) > 0 {
		first := result.Malformed[0]
		id := first.Source.ID
		if id == "" {
			id = first.Source.Name
		}
		return Evaluation{}, fmt.Errorf("rpc: source %q: %w: %v", id, rpc.ErrMalformedResponse, first.Err)
	}
	for path, byValue := range groups {
		var qualified []rpc.FieldObservation
		var residuals []rpc.FieldObservation
		for _, item := range byValue {
			sort.Slice(item.sources, func(i, j int) bool {
				if item.sources[i].ID != item.sources[j].ID {
					return item.sources[i].ID < item.sources[j].ID
				}
				return item.sources[i].Name < item.sources[j].Name
			})
			field := rpc.FieldObservation{
				Path:    path,
				Value:   append(json.RawMessage(nil), item.value...),
				Sources: append([]rpc.SourceRef(nil), item.sources...),
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
			result.Diagnostics.Residuals = append(result.Diagnostics.Residuals, residuals...)
		case 1:
			result.Accepted = append(result.Accepted, qualified[0])
			result.Diagnostics.Residuals = append(result.Diagnostics.Residuals, residuals...)
		default:
			result.Diagnostics.Conflicts = append(result.Diagnostics.Conflicts, rpc.ConflictField{Path: path, Values: qualified})
			result.Diagnostics.Residuals = append(result.Diagnostics.Residuals, residuals...)
		}
	}

	sortFieldObservationsReference(result.Accepted)
	sortFieldObservationsReference(result.Diagnostics.Residuals)
	sort.Slice(result.Diagnostics.Conflicts, func(i, j int) bool {
		return result.Diagnostics.Conflicts[i].Path < result.Diagnostics.Conflicts[j].Path
	})
	return result, nil
}

func decodeObservedJSONReference(raw []byte) (any, error) {
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

func flattenJSONReference(path string, value any, fields map[string]json.RawMessage) {
	switch current := value.(type) {
	case map[string]any:
		if len(current) == 0 {
			fields[path] = mustMarshalReference(current)
			return
		}
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			flattenJSONReference(joinJSONPointerReference(path, key), current[key], fields)
		}
	case []any:
		if len(current) == 0 {
			fields[path] = mustMarshalReference(current)
			return
		}
		for index, item := range current {
			flattenJSONReference(joinJSONPointerReference(path, fmt.Sprint(index)), item, fields)
		}
	default:
		fields[path] = mustMarshalReference(current)
	}
}

func joinJSONPointerReference(path, token string) string {
	token = strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
	return path + "/" + token
}

func mustMarshalReference(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("rpc: marshal validated JSON value: %v", err))
	}
	return raw
}

func sortFieldObservationsReference(fields []rpc.FieldObservation) {
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].Path != fields[j].Path {
			return fields[i].Path < fields[j].Path
		}
		return string(fields[i].Value) < string(fields[j].Value)
	})
}
