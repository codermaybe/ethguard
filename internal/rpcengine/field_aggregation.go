package rpcengine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/rpc"
)

// Evaluation is the output of the generic observation evaluator. Accepted
// contains paths with exactly one value meeting the invocation threshold.
// Diagnostics retains values below that threshold and paths with multiple
// threshold-qualified values. Method packages project Accepted into Core and
// Accepted Extensions; diagnostics never changes the core result.
type Evaluation struct {
	Accepted    []rpc.FieldObservation
	Diagnostics rpc.FieldDiagnostics
	// Malformed names the sources whose response carried a value that failed
	// generic JSON validation. They abstain from every field vote while the
	// quorum denominator stays the fixed basis size, mirroring how strict and
	// median treat a single undecodable answer as a fault rather than a
	// verdict. When every value-bearing source is malformed there is no vote
	// to report and the function still fails with ErrMalformedResponse.
	Malformed []MalformedSource
}

// MalformedSource attributes one abstention to its source.
type MalformedSource struct {
	Source rpc.SourceRef
	Err    error
}

type fieldCandidate struct {
	value   json.RawMessage
	sources []rpc.SourceRef
}

// AggregateFields flattens object and array responses into JSON Pointer paths
// and groups equal values by source. A path enters Accepted only when exactly
// one value reaches quorum. Multiple values reaching quorum are reported as a
// conflict rather than choosing arbitrarily. Quorum must be positive.
//
// This function performs only generic JSON work. It does not know Ethereum
// field names, scalar kinds, method semantics, or whether a field is part of
// a standard response.
func AggregateFields(observations []rpc.Observation, quorum int) (Evaluation, error) {
	if quorum <= 0 {
		return Evaluation{}, fmt.Errorf("rpc: observation quorum must be positive")
	}

	groups := make(map[string]map[string]*fieldCandidate)
	seen := make(map[string]int, len(observations))
	var result Evaluation
	reuseFields := identicalObservedResults(observations)
	haveFirst := false
	var firstErr error
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

		if reuseFields && haveFirst {
			if firstErr != nil {
				result.Malformed = append(result.Malformed, MalformedSource{Source: observation.Source, Err: firstErr})
			} else {
				// Every candidate belongs to the same validated raw result. Each
				// still owns its source slice and receives this source's vote.
				for _, byValue := range groups {
					for _, item := range byValue {
						item.sources = append(item.sources, observation.Source)
					}
				}
			}
			continue
		}
		value, err := decodeObservedJSON(observation.Response.Answer.Result)
		if reuseFields {
			haveFirst = true
			firstErr = err
		}
		if err != nil {
			result.Malformed = append(result.Malformed, MalformedSource{Source: observation.Source, Err: err})
			continue
		}
		collectFieldCandidates("", value, observation.Source, groups)
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
				Value:   item.value,
				Sources: item.sources,
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

	sortFieldObservations(result.Accepted)
	sortFieldObservations(result.Diagnostics.Residuals)
	sort.Slice(result.Diagnostics.Conflicts, func(i, j int) bool {
		return result.Diagnostics.Conflicts[i].Path < result.Diagnostics.Conflicts[j].Path
	})
	return result, nil
}

// AggregateFieldsWithCorroboration applies the invocation's frozen voting
// basis to field observations. Every captured member occupies the denominator,
// including absent, gated, failed, and malformed members; observations only
// determine which members contribute field votes.
//
// Passing the Basis instead of deriving its size from observations prevents
// partial delivery from shrinking majority support.
func AggregateFieldsWithCorroboration(observations []rpc.Observation, basis Basis) (Evaluation, error) {
	return AggregateFields(observations, basis.Required())
}

// identicalObservedResults permits reuse only for an entirely identical set of
// value-bearing responses. Mixed results keep the ordinary per-source path.
// Source identities are checked by AggregateFields in their original order.
func identicalObservedResults(observations []rpc.Observation) bool {
	var first json.RawMessage
	count := 0
	for _, observation := range observations {
		raw := observation.Response.Answer.Result
		if observation.Response.Err() != nil || raw == nil {
			continue
		}
		// Oversized values must reach the usual constant-time validation rejection
		// without first comparing their entire contents. Do not trim JSON input.
		if int64(len(raw)) > jsonsafe.MaxBodyBytes {
			return false
		}
		if count == 0 {
			first = raw
		} else if !bytes.Equal(first, raw) {
			return false
		}
		count++
	}
	return count > 1
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

// collectFieldCandidates consumes a completely validated tree. Each candidate
// owns the bytes returned by json.Marshal and its source slice; both can be
// transferred to the delivered observation without another copy.
func collectFieldCandidates(path string, value any, source rpc.SourceRef, groups map[string]map[string]*fieldCandidate) {
	switch current := value.(type) {
	case map[string]any:
		if len(current) != 0 {
			for key, item := range current {
				collectFieldCandidates(joinJSONPointer(path, key), item, source, groups)
			}
			return
		}
	case []any:
		if len(current) != 0 {
			for index, item := range current {
				collectFieldCandidates(joinJSONPointer(path, strconv.Itoa(index)), item, source, groups)
			}
			return
		}
	}
	raw := mustMarshal(value)
	valueKey := string(raw)
	byValue := groups[path]
	if byValue == nil {
		byValue = make(map[string]*fieldCandidate)
		groups[path] = byValue
	}
	item := byValue[valueKey]
	if item == nil {
		item = &fieldCandidate{value: raw}
		byValue[valueKey] = item
	}
	item.sources = append(item.sources, source)
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

func sortFieldObservations(fields []rpc.FieldObservation) {
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].Path != fields[j].Path {
			return fields[i].Path < fields[j].Path
		}
		return string(fields[i].Value) < string(fields[j].Value)
	})
}
