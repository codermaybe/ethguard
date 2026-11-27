package ethapi_test

// This oracle runs only in tests. It reads the source schema independently of
// the generator IR, and never calls a generated constructor or codec normalizer
// to manufacture the expected content.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

func declaredResultProjection(schema any, raw json.RawMessage) (json.RawMessage, error) {
	if err := conformance.ValidateResult(schema, raw); err != nil {
		return nil, err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	projected, err := projectDeclared(schema, value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(projected)
}

func projectDeclared(schema, value any) (any, error) {
	node, ok := schema.(map[string]any)
	if !ok { // A true schema preserves arbitrary JSON; false was rejected.
		return value, nil
	}
	var projections []any
	for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
		branches, ok := node[keyword].([]any)
		if !ok {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		for _, branch := range branches {
			if conformance.ValidateResult(branch, raw) != nil {
				continue
			}
			part, err := projectDeclared(branch, value)
			if err != nil {
				return nil, err
			}
			projections = append(projections, part)
		}
	}
	properties, _ := node["properties"].(map[string]any)
	patterns, _ := node["patternProperties"].(map[string]any)
	items, hasItems := node["items"]
	// Composition wrappers do not themselves declare the whole JSON object.
	// A genuinely free schema (including object without properties, or array
	// without items) does preserve the complete value.
	if len(properties) != 0 || len(patterns) != 0 || hasItems || len(projections) == 0 {
		local := value
		switch current := value.(type) {
		case map[string]any:
			if len(properties) != 0 || len(patterns) != 0 {
				out := map[string]any{}
				for name, member := range current {
					var schemas []any
					if child, exists := properties[name]; exists {
						schemas = append(schemas, child)
					}
					for pattern, child := range patterns {
						matched, err := regexp.MatchString(pattern, name)
						if err != nil {
							return nil, err
						}
						if matched {
							schemas = append(schemas, child)
						}
					}
					for _, child := range schemas {
						part, err := projectDeclared(child, member)
						if err != nil {
							return nil, err
						}
						if previous, exists := out[name]; exists {
							part, err = mergeDeclared(previous, part)
							if err != nil {
								return nil, err
							}
						}
						out[name] = part
					}
				}
				local = out
			}
		case []any:
			if hasItems {
				out := make([]any, len(current))
				for i, member := range current {
					var err error
					out[i], err = projectDeclared(items, member)
					if err != nil {
						return nil, err
					}
				}
				local = out
			}
		}
		projections = append(projections, local)
	}
	result := projections[0]
	for _, part := range projections[1:] {
		var err error
		result, err = mergeDeclared(result, part)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Matching anyOf branches can declare different parts of the same object.
// Merge recursively instead of losing fields by selecting the first branch.
func mergeDeclared(left, right any) (any, error) {
	switch left := left.(type) {
	case map[string]any:
		right, ok := right.(map[string]any)
		if !ok {
			break
		}
		for key, value := range right {
			if previous, exists := left[key]; exists {
				var err error
				value, err = mergeDeclared(previous, value)
				if err != nil {
					return nil, err
				}
			}
			left[key] = value
		}
		return left, nil
	case []any:
		right, ok := right.([]any)
		if !ok || len(left) != len(right) {
			break
		}
		for i := range left {
			var err error
			left[i], err = mergeDeclared(left[i], right[i])
			if err != nil {
				return nil, err
			}
		}
		return left, nil
	default:
		if reflect.DeepEqual(left, right) {
			return left, nil
		}
	}
	return nil, fmt.Errorf("inconsistent declared projections: %v / %v", left, right)
}

func TestDeclaredResultProjectionUsesEveryMatchingBranchAndPreservesFreeJSON(t *testing.T) {
	for _, tc := range []struct{ name, schema, raw, want string }{
		{"anyOf union", `{"anyOf":[{"type":"object","properties":{"a":{"type":"object","properties":{"x":{"type":"number"}}}}},{"type":"object","properties":{"a":{"type":"object","properties":{"y":{"type":"number"}}},"b":{"type":"boolean"}}}]}`, `{"a":{"x":1,"y":2,"extra":3},"b":true,"extra":4}`, `{"a":{"x":1,"y":2},"b":true}`},
		{"nested array", `{"type":"array","items":{"type":"object","properties":{"free":{"type":"object"},"rows":{"type":"array","items":{}}}}}`, `[{"free":{"vendor":[null,9007199254740993]},"rows":[{"keep":1}],"extra":false}]`, `[{"free":{"vendor":[null,9007199254740993]},"rows":[{"keep":1}]}]`},
		{"free array", `{"type":"array"}`, `[{"keep":[1,null]}]`, `[{"keep":[1,null]}]`},
		{"oneOf selection", `{"oneOf":[{"type":"object","required":["ok"],"properties":{"ok":{"const":true}}},{"type":"null"}]}`, `{"ok":true,"extra":1}`, `{"ok":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var schema any
			if err := json.Unmarshal([]byte(tc.schema), &schema); err != nil {
				t.Fatal(err)
			}
			got, err := declaredResultProjection(schema, json.RawMessage(tc.raw))
			if err != nil || string(got) != tc.want {
				t.Fatalf("projection = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}
