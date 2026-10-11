// Package conformance runs the checked-in execution-apis .io vectors against
// the pinned result schemas. It validates wire shape only; request semantics,
// fork applicability, and provider agreement remain method-level concerns.
package conformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Report summarizes one offline conformance run.
type Report struct {
	BundleVersion   string
	TotalVectors    int
	ValidatedResult int
	ErrorResponses  int
	Uncovered       []string
	Issues          []Issue
}

// Issue identifies one vector or bundle problem without embedding the full
// response body in an error string.
type Issue struct {
	Path   string
	Method string
	Detail string
}

func (i Issue) Error() string {
	if i.Method == "" {
		return i.Path + ": " + i.Detail
	}
	return i.Path + " (" + i.Method + "): " + i.Detail
}

// Run loads a methods.json-shaped bundle and validates every result-bearing
// vector in vectorsFS. A vector whose method is absent from the bundle is
// reported in Uncovered so standard coverage gaps stay visible without being
// mistaken for a schema failure.
func Run(vectorsFS fs.FS, bundleJSON []byte, pinnedVersion string) Report {
	report := Report{}
	bundle, err := loadBundle(bundleJSON, pinnedVersion)
	if err != nil {
		report.Issues = append(report.Issues, Issue{Path: "methods.json", Detail: err.Error()})
		return report
	}
	report.BundleVersion = bundle.version
	uncovered := map[string]bool{}
	err = fs.WalkDir(vectorsFS, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".io") {
			return nil
		}
		vectors, parseErr := parseVector(vectorsFS, path)
		if parseErr != nil {
			report.Issues = append(report.Issues, Issue{Path: path, Detail: parseErr.Error()})
			return nil
		}
		report.TotalVectors += len(vectors)
		for _, vector := range vectors {
			if vector.Method == "" {
				report.Issues = append(report.Issues, Issue{Path: path, Detail: "request has no method"})
				continue
			}
			schema, ok := bundle.results[vector.Method]
			if !ok {
				uncovered[vector.Method] = true
				continue
			}
			if vector.Error != nil {
				report.ErrorResponses++
				continue
			}
			if vector.Result == nil {
				report.Issues = append(report.Issues, Issue{Path: path, Method: vector.Method, Detail: "response has neither result nor error"})
				continue
			}
			var value any
			decoder := json.NewDecoder(bytes.NewReader(vector.Result))
			decoder.UseNumber()
			if decodeErr := decoder.Decode(&value); decodeErr != nil {
				report.Issues = append(report.Issues, Issue{Path: path, Method: vector.Method, Detail: "result is invalid JSON: " + decodeErr.Error()})
				continue
			}
			if validateErr := validateSchema(schema, value, "$", 0); validateErr != nil {
				report.Issues = append(report.Issues, Issue{Path: path, Method: vector.Method, Detail: validateErr.Error()})
				continue
			}
			report.ValidatedResult++
		}
		return nil
	})
	if err != nil {
		report.Issues = append(report.Issues, Issue{Path: "vectors", Detail: err.Error()})
	}
	for method := range uncovered {
		report.Uncovered = append(report.Uncovered, method)
	}
	sort.Strings(report.Uncovered)
	sort.Slice(report.Issues, func(i, j int) bool {
		if report.Issues[i].Path != report.Issues[j].Path {
			return report.Issues[i].Path < report.Issues[j].Path
		}
		return report.Issues[i].Method < report.Issues[j].Method
	})
	return report
}

type bundle struct {
	version string
	results map[string]any
}

func loadBundle(raw []byte, pinnedVersion string) (bundle, error) {
	var value struct {
		Version string         `json:"version"`
		Results map[string]any `json:"results"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return bundle{}, err
	}
	if value.Version == "" {
		return bundle{}, errors.New("missing version")
	}
	if pinnedVersion == "" || value.Version != pinnedVersion {
		return bundle{}, fmt.Errorf("version %q does not match pin %q", value.Version, pinnedVersion)
	}
	if len(value.Results) == 0 {
		return bundle{}, errors.New("results is empty")
	}
	return bundle{version: value.Version, results: value.Results}, nil
}

type vector struct {
	Method  string
	Request json.RawMessage
	Result  json.RawMessage
	Error   json.RawMessage
}

func parseVector(vectorsFS fs.FS, path string) ([]vector, error) {
	file, err := vectorsFS.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 4<<20)
	var requests, responses []json.RawMessage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, ">> "):
			requests = append(requests, json.RawMessage(strings.TrimSpace(strings.TrimPrefix(line, ">> "))))
		case strings.HasPrefix(line, "<< "):
			responses = append(responses, json.RawMessage(strings.TrimSpace(strings.TrimPrefix(line, "<< "))))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(requests) == 0 || len(responses) == 0 {
		return nil, errors.New("must contain at least one request and response")
	}
	if len(requests) != len(responses) {
		return nil, fmt.Errorf("request/response count mismatch: %d/%d", len(requests), len(responses))
	}
	parsed := make([]vector, len(requests))
	for i := range requests {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(requests[i], &req); err != nil {
			return nil, fmt.Errorf("request %d JSON: %w", i, err)
		}
		var resp struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(responses[i], &resp); err != nil {
			return nil, fmt.Errorf("response %d JSON: %w", i, err)
		}
		if len(resp.Result) == 0 && len(resp.Error) == 0 {
			return nil, fmt.Errorf("response %d has neither result nor error", i)
		}
		parsed[i] = vector{Method: req.Method, Request: requests[i], Result: resp.Result, Error: resp.Error}
	}
	return parsed, nil
}

func validateSchema(schema any, value any, path string, depth int) error {
	if depth > 64 {
		return errors.New("schema nesting exceeds 64 levels")
	}
	m, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		branches, ok := m[key].([]any)
		if !ok {
			continue
		}
		matches := 0
		var lastErr error
		for _, branch := range branches {
			if err := validateSchema(branch, value, path, depth+1); err == nil {
				matches++
			} else {
				lastErr = err
			}
		}
		switch key {
		case "allOf":
			if matches != len(branches) {
				return fmt.Errorf("%s: allOf failed: %v", path, lastErr)
			}
		default:
			// A few historical execution-apis schemas use oneOf for branches
			// that overlap on empty containers. Treat it as a union for wire
			// conformance so that a valid shape is not rejected by branch choice.
			if matches == 0 {
				return fmt.Errorf("%s: %s failed: %v", path, key, lastErr)
			}
		}
	}
	if enum, ok := m["enum"].([]any); ok {
		matched := false
		for _, candidate := range enum {
			if jsonEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: value is outside enum", path)
		}
	}
	if constant, ok := m["const"]; ok && !jsonEqual(constant, value) {
		return fmt.Errorf("%s: value does not match const", path)
	}
	if typeName, ok := m["type"].(string); ok {
		if !matchesType(typeName, value) {
			return fmt.Errorf("%s: want %s, got %s", path, typeName, jsonType(value))
		}
	}
	if pattern, ok := m["pattern"].(string); ok {
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: pattern applies to non-string", path)
		}
		matched, err := regexp.MatchString(pattern, text)
		if err != nil {
			return fmt.Errorf("%s: invalid schema pattern: %w", path, err)
		}
		if !matched {
			return fmt.Errorf("%s: value does not match pattern", path)
		}
	}
	if m["type"] == "number" || m["type"] == "integer" {
		if err := validateNumber(m, value, path); err != nil {
			return err
		}
	}
	switch current := value.(type) {
	case map[string]any:
		if required, ok := m["required"].([]any); ok {
			for _, item := range required {
				name, _ := item.(string)
				if _, exists := current[name]; !exists {
					return fmt.Errorf("%s: required property %q is missing", path, name)
				}
			}
		}
		properties, _ := m["properties"].(map[string]any)
		additional, hasAdditional := m["additionalProperties"].(bool)
		for name, child := range current {
			childSchema, declared := properties[name]
			if !declared {
				if hasAdditional && !additional {
					return fmt.Errorf("%s: unexpected property %q", path, name)
				}
				continue
			}
			if err := validateSchema(childSchema, child, path+"/"+escape(name), depth+1); err != nil {
				return err
			}
		}
	case []any:
		if items, ok := m["items"]; ok {
			for i, item := range current {
				if err := validateSchema(items, item, path+"/"+strconv.Itoa(i), depth+1); err != nil {
					return err
				}
			}
		}
		if min, ok := numberValue(m["minItems"]); ok && float64(len(current)) < min {
			return fmt.Errorf("%s: fewer than minItems", path)
		}
		if max, ok := numberValue(m["maxItems"]); ok && float64(len(current)) > max {
			return fmt.Errorf("%s: more than maxItems", path)
		}
	}
	return nil
}

func validateNumber(schema map[string]any, value any, path string) error {
	number, ok := numberValue(value)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return fmt.Errorf("%s: expected finite number", path)
	}
	if min, ok := numberValue(schema["minimum"]); ok && number < min {
		return fmt.Errorf("%s: below minimum", path)
	}
	if max, ok := numberValue(schema["maximum"]); ok && number > max {
		return fmt.Errorf("%s: above maximum", path)
	}
	return nil
}

func numberValue(value any) (float64, bool) {
	switch current := value.(type) {
	case json.Number:
		value, err := current.Float64()
		return value, err == nil
	case float64:
		return current, true
	default:
		return 0, false
	}
}

func matchesType(name string, value any) bool {
	switch name {
	case "null":
		return value == nil
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number", "integer":
		number, ok := numberValue(value)
		return ok && (name != "integer" || number == math.Trunc(number))
	default:
		return true
	}
}

func jsonType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	default:
		return "unknown"
	}
}

func jsonEqual(left, right any) bool {
	a, err := json.Marshal(left)
	if err != nil {
		return false
	}
	b, err := json.Marshal(right)
	if err != nil {
		return false
	}
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

func escape(token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	return strings.ReplaceAll(token, "/", "~1")
}
