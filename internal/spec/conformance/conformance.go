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
	"io"
	"io/fs"
	"math"
	"math/big"
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
	Methods         map[string]VectorCounts
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
	vectors, err := ReadVectors(vectorsFS)
	if err != nil {
		report.Issues = append(report.Issues, Issue{Path: "vectors", Detail: err.Error()})
		return report
	}
	report.Methods = CountVectors(vectors)
	for _, vector := range vectors {
		report.TotalVectors++
		if vector.Error != nil {
			report.ErrorResponses++
		}
		schema, ok := bundle.results[vector.Method]
		if !ok {
			uncovered[vector.Method] = true
			continue
		}
		if vector.Error != nil {
			continue
		}
		if validateErr := ValidateResult(schema, vector.Result); validateErr != nil {
			report.Issues = append(report.Issues, Issue{Path: vector.Identity(), Method: vector.Method, Detail: validateErr.Error()})
			continue
		}
		report.ValidatedResult++
		counts := report.Methods[vector.Method]
		counts.ValidatedResults++
		report.Methods[vector.Method] = counts
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
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return bundle{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return bundle{}, errors.New("bundle contains trailing JSON")
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

// Vector is one request/response pair in an official .io file. Index is the
// zero-based pair position; Result == nil means absent, while JSON null remains
// the literal bytes "null". Error responses have Error set and no Result.
type Vector struct {
	Path    string
	Index   int
	Method  string
	Request json.RawMessage
	Result  json.RawMessage
	Error   json.RawMessage
}

// Identity includes the complete filesystem path and pair index.
func (v Vector) Identity() string { return fmt.Sprintf("%s[%d]", v.Path, v.Index) }

// VectorCounts classifies interactions independently of schema availability.
// Results counts result-bearing responses, including explicit JSON null; it
// does not claim that these results passed a schema or generated constructor.
type VectorCounts struct {
	Total, Results, Errors int
	ValidatedResults       int // populated by Run, not by response classification
}

// CountVectors accounts for every parsed interaction by exact method name.
func CountVectors(vectors []Vector) map[string]VectorCounts {
	counts := map[string]VectorCounts{}
	for _, vector := range vectors {
		count := counts[vector.Method]
		count.Total++
		if vector.Error != nil {
			count.Errors++
		} else {
			count.Results++
		}
		counts[vector.Method] = count
	}
	return counts
}

// ReadVectors enumerates the shared corpus in path and pair order. The full
// path and pair index survive into each vector's identity.
func ReadVectors(vectorsFS fs.FS) ([]Vector, error) {
	var vectors []Vector
	err := fs.WalkDir(vectorsFS, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".io") {
			return nil
		}
		pairs, err := ParseVector(vectorsFS, path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		vectors = append(vectors, pairs...)
		return nil
	})
	return vectors, err
}

// ParseVector reads every request and response in path and pairs them in their
// respective file order, rather than choosing only the last response.
func ParseVector(vectorsFS fs.FS, path string) ([]Vector, error) {
	file, err := vectorsFS.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // Read-only vector input; Scanner owns data errors and Close commits no output.
	scanner := bufio.NewScanner(file)
	// The pinned no-topics corpus contains a 21 MB response line. This is
	// an offline evidence reader, independent of runtime response budgets.
	scanner.Buffer(make([]byte, 1024), 32<<20)
	var requests, responses []json.RawMessage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, ">>"):
			requests = append(requests, json.RawMessage(strings.TrimSpace(strings.TrimPrefix(line, ">>"))))
		case strings.HasPrefix(line, "<<"):
			responses = append(responses, json.RawMessage(strings.TrimSpace(strings.TrimPrefix(line, "<<"))))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(requests) == 0 || len(responses) == 0 {
		return nil, fmt.Errorf("%s: must contain at least one request and response", path)
	}
	if len(requests) != len(responses) {
		return nil, fmt.Errorf("%s: request/response count mismatch: %d/%d", path, len(requests), len(responses))
	}
	parsed := make([]Vector, len(requests))
	for i := range requests {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(requests[i], &req); err != nil {
			return nil, fmt.Errorf("%s[%d]: request JSON: %w", path, i, err)
		}
		if req.Method == "" {
			return nil, fmt.Errorf("%s[%d]: request has no method", path, i)
		}
		var resp struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(responses[i], &resp); err != nil {
			return nil, fmt.Errorf("%s[%d]: response JSON: %w", path, i, err)
		}
		if len(resp.Result) == 0 && len(resp.Error) == 0 {
			return nil, fmt.Errorf("%s[%d]: response has neither result nor error", path, i)
		}
		if len(resp.Result) != 0 && len(resp.Error) != 0 {
			return nil, fmt.Errorf("%s[%d]: response has both result and error", path, i)
		}
		parsed[i] = Vector{Path: path, Index: i, Method: req.Method, Request: requests[i], Result: resp.Result, Error: resp.Error}
	}
	return parsed, nil
}

// ValidateResult checks one JSON result against an already-resolved result
// schema from the pinned bundle. It checks official wire shape, not protected
// encoder commitments or project canonical byte choices.
func ValidateResult(schema any, raw json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("result is invalid JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("result contains trailing JSON")
	}
	return validateSchema(schema, value, "$", 0)
}

func validateSchema(schema any, value any, path string, depth int) error {
	if depth > 64 {
		return errors.New("schema nesting exceeds 64 levels")
	}
	if allowed, ok := schema.(bool); ok {
		if allowed {
			return nil
		}
		return fmt.Errorf("%s: false schema rejects every value", path)
	}
	m, ok := schema.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: schema must be an object or boolean", path)
	}
	if forbidden, exists := m["not"]; exists {
		if err := validateSchema(forbidden, value, path, depth+1); err == nil {
			return fmt.Errorf("%s: value matches forbidden schema", path)
		}
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
		case "oneOf":
			if matches != 1 {
				return fmt.Errorf("%s: oneOf matched %d branches, want exactly one: %v", path, matches, lastErr)
			}
		case "anyOf":
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
		patterns, _ := m["patternProperties"].(map[string]any)
		additional, hasAdditional := m["additionalProperties"]
		for name, child := range current {
			childSchema, declared := properties[name]
			matchedPattern := false
			for pattern, patternSchema := range patterns {
				matched, err := regexp.MatchString(pattern, name)
				if err != nil {
					return fmt.Errorf("%s: invalid property pattern: %w", path, err)
				}
				if matched {
					matchedPattern = true
					if err := validateSchema(patternSchema, child, path+"/"+escape(name), depth+1); err != nil {
						return err
					}
				}
			}
			if !declared {
				if !matchedPattern && hasAdditional {
					if err := validateSchema(additional, child, path+"/"+escape(name), depth+1); err != nil {
						return err
					}
				}
				continue
			}
			if err := validateSchema(childSchema, child, path+"/"+escape(name), depth+1); err != nil {
				return err
			}
		}
	case []any:
		if unique, _ := m["uniqueItems"].(bool); unique {
			for i, item := range current {
				for _, previous := range current[:i] {
					if jsonEqual(item, previous) {
						return fmt.Errorf("%s: duplicate array item", path)
					}
				}
			}
		}
		if items, ok := m["items"]; ok {
			for i, item := range current {
				if err := validateSchema(items, item, path+"/"+strconv.Itoa(i), depth+1); err != nil {
					return err
				}
			}
		}
		if m["minItems"] != nil || m["maxItems"] != nil {
			count, _ := exactNumber(json.Number(strconv.Itoa(len(current))))
			if min, ok := exactNumber(m["minItems"]); ok && compareNumbers(count, min) < 0 {
				return fmt.Errorf("%s: fewer than minItems", path)
			}
			if max, ok := exactNumber(m["maxItems"]); ok && compareNumbers(count, max) > 0 {
				return fmt.Errorf("%s: more than maxItems", path)
			}
		}
	}
	return nil
}

func validateNumber(schema map[string]any, value any, path string) error {
	number, ok := exactNumber(value)
	if !ok {
		return fmt.Errorf("%s: expected finite number", path)
	}
	if min, ok := exactNumber(schema["minimum"]); ok && compareNumbers(number, min) < 0 {
		return fmt.Errorf("%s: below minimum", path)
	}
	if max, ok := exactNumber(schema["maximum"]); ok && compareNumbers(number, max) > 0 {
		return fmt.Errorf("%s: above maximum", path)
	}
	return nil
}

// decimalNumber stores a significand and decimal order, not an expanded
// numerator/denominator. Even enormous JSON exponents need only their written
// digits; the offline oracle never rounds through binary floating point.
type decimalNumber struct {
	negative bool
	digits   string
	order    *big.Int
}

func exactNumber(value any) (decimalNumber, bool) {
	var text string
	switch value := value.(type) {
	case json.Number:
		text = value.String()
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return decimalNumber{}, false
		}
		text = strconv.FormatFloat(value, 'g', -1, 64)
	default:
		return decimalNumber{}, false
	}
	if len(text) == 0 || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) || !json.Valid([]byte(text)) {
		return decimalNumber{}, false
	}
	number := decimalNumber{negative: text[0] == '-', order: new(big.Int)}
	text = strings.TrimPrefix(text, "-")
	if at := strings.IndexAny(text, "eE"); at >= 0 {
		if _, ok := number.order.SetString(text[at+1:], 10); !ok {
			return decimalNumber{}, false
		}
		text = text[:at]
	}
	integerDigits := len(text)
	if at := strings.IndexByte(text, '.'); at >= 0 {
		integerDigits = at
		text = text[:at] + text[at+1:]
	}
	significant := strings.TrimLeft(text, "0")
	if significant == "" {
		number.negative = false
		number.order.SetInt64(0)
		return number, true
	}
	leadingZeros := len(text) - len(significant)
	number.digits = strings.TrimRight(significant, "0")
	number.order.Add(number.order, big.NewInt(int64(integerDigits-leadingZeros)))
	return number, true
}

func compareNumbers(left, right decimalNumber) int {
	if left.digits == "" && right.digits == "" {
		return 0
	}
	if left.negative != right.negative {
		if left.negative {
			return -1
		}
		return 1
	}
	var comparison int
	switch {
	case left.digits == "":
		comparison = -1
	case right.digits == "":
		comparison = 1
	default:
		comparison = left.order.Cmp(right.order)
		if comparison == 0 {
			for i := 0; i < len(left.digits) || i < len(right.digits); i++ {
				a, b := byte('0'), byte('0')
				if i < len(left.digits) {
					a = left.digits[i]
				}
				if i < len(right.digits) {
					b = right.digits[i]
				}
				if a < b {
					comparison = -1
					break
				}
				if a > b {
					comparison = 1
					break
				}
			}
		}
	}
	if left.negative {
		return -comparison
	}
	return comparison
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
		number, ok := exactNumber(value)
		return ok && (name != "integer" || number.digits == "" || number.order.Cmp(big.NewInt(int64(len(number.digits)))) >= 0)
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
	if a, ok := exactNumber(left); ok {
		b, ok := exactNumber(right)
		return ok && compareNumbers(a, b) == 0
	}
	switch left := left.(type) {
	case map[string]any:
		right, ok := right.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, exists := right[key]
			if !exists || !jsonEqual(value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := right.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i, value := range left {
			if !jsonEqual(value, right[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(left, right)
	}
}

func escape(token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	return strings.ReplaceAll(token, "/", "~1")
}
