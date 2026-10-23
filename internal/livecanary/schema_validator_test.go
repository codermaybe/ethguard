package livecanary

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// The repository does not take a JSON Schema dependency. This test-only
// validator covers the keywords the manifest schema uses and resolves local
// $ref pointers, so the published schema is executable in ordinary go test.
type jsonSchema struct {
	root map[string]any
}

func loadManifestSchema(t *testing.T) *jsonSchema {
	t.Helper()
	return loadLiveCanarySchema(t, "manifest.schema.json")
}

func loadReportSchema(t *testing.T) *jsonSchema {
	t.Helper()
	return loadLiveCanarySchema(t, "report.schema.json")
}

func loadLiveCanarySchema(t *testing.T, name string) *jsonSchema {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "design", "live-canary", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	decoded, err := decodeJSON(data)
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		t.Fatal("schema root is not an object")
	}
	return &jsonSchema{root: root}
}

func decodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func (s *jsonSchema) validate(instance any) error {
	return s.validateAt(s.root, instance, "$", 0)
}

func (s *jsonSchema) validateAt(schema map[string]any, instance any, path string, depth int) error {
	if depth > 100 {
		return fmt.Errorf("%s: schema recursion limit", path)
	}
	if ref, ok := schema["$ref"].(string); ok {
		target, err := s.resolve(ref)
		if err != nil {
			return err
		}
		if err := s.validateAt(target, instance, path, depth+1); err != nil {
			return err
		}
	}
	if rawType, ok := schema["type"]; ok && !matchesType(instance, rawType) {
		return fmt.Errorf("%s: does not match type %v", path, rawType)
	}
	if constValue, ok := schema["const"]; ok && !jsonValueEqual(constValue, instance) {
		return fmt.Errorf("%s: does not equal const %v", path, constValue)
	}
	if rawEnum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range rawEnum {
			if jsonValueEqual(candidate, instance) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: value %v is not in enum", path, instance)
		}
	}
	if raw, ok := schema["allOf"].([]any); ok {
		for _, item := range raw {
			subschema, err := asSchema(item, path)
			if err != nil {
				return err
			}
			if err := s.validateAt(subschema, instance, path, depth+1); err != nil {
				return err
			}
		}
	}
	if raw, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, item := range raw {
			subschema, err := asSchema(item, path)
			if err != nil {
				return err
			}
			if s.validateAt(subschema, instance, path, depth+1) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: matches no anyOf branch", path)
		}
	}
	if raw, ok := schema["oneOf"].([]any); ok {
		matched := 0
		for _, item := range raw {
			subschema, err := asSchema(item, path)
			if err != nil {
				return err
			}
			if s.validateAt(subschema, instance, path, depth+1) == nil {
				matched++
			}
		}
		if matched != 1 {
			return fmt.Errorf("%s: matches %d oneOf branches", path, matched)
		}
	}
	if raw, ok := schema["not"].(map[string]any); ok {
		if s.validateAt(raw, instance, path, depth+1) == nil {
			return fmt.Errorf("%s: matches forbidden schema", path)
		}
	}
	if rawIf, ok := schema["if"].(map[string]any); ok {
		if s.validateAt(rawIf, instance, path, depth+1) == nil {
			if rawThen, ok := schema["then"].(map[string]any); ok {
				if err := s.validateAt(rawThen, instance, path, depth+1); err != nil {
					return err
				}
			}
		} else if rawElse, ok := schema["else"].(map[string]any); ok {
			if err := s.validateAt(rawElse, instance, path, depth+1); err != nil {
				return err
			}
		}
	}

	switch typed := instance.(type) {
	case map[string]any:
		return s.validateObject(schema, typed, path, depth)
	case []any:
		return s.validateArray(schema, typed, path, depth)
	case string:
		return validateString(schema, typed, path)
	case json.Number:
		return validateNumber(schema, typed, path)
	default:
		return nil
	}
}

func (s *jsonSchema) validateObject(schema map[string]any, object map[string]any, path string, depth int) error {
	properties, _ := schema["properties"].(map[string]any)
	if required, ok := schema["required"].([]any); ok {
		for _, raw := range required {
			name, ok := raw.(string)
			if !ok {
				continue
			}
			if _, exists := object[name]; !exists {
				return fmt.Errorf("%s: missing required property %q", path, name)
			}
		}
	}
	for name, value := range object {
		if rawProperty, ok := properties[name]; ok {
			subschema, err := asSchema(rawProperty, path+"."+name)
			if err != nil {
				return err
			}
			if err := s.validateAt(subschema, value, path+"."+name, depth+1); err != nil {
				return err
			}
			continue
		}
		if rawAdditional, ok := schema["additionalProperties"]; ok {
			switch allowed := rawAdditional.(type) {
			case bool:
				if !allowed {
					return fmt.Errorf("%s: unexpected property %q", path, name)
				}
			case map[string]any:
				if err := s.validateAt(allowed, value, path+"."+name, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *jsonSchema) validateArray(schema map[string]any, array []any, path string, depth int) error {
	if raw, ok := schema["minItems"]; ok {
		if len(array) < numberToInt(raw) {
			return fmt.Errorf("%s: fewer than %d items", path, numberToInt(raw))
		}
	}
	if raw, ok := schema["maxItems"]; ok {
		if len(array) > numberToInt(raw) {
			return fmt.Errorf("%s: more than %d items", path, numberToInt(raw))
		}
	}
	if rawItems, ok := schema["items"].(map[string]any); ok {
		for i, item := range array {
			if err := s.validateAt(rawItems, item, fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateString(schema map[string]any, value, path string) error {
	if raw, ok := schema["minLength"]; ok {
		if utf8.RuneCountInString(value) < numberToInt(raw) {
			return fmt.Errorf("%s: shorter than %d characters", path, numberToInt(raw))
		}
	}
	if raw, ok := schema["maxLength"]; ok {
		if utf8.RuneCountInString(value) > numberToInt(raw) {
			return fmt.Errorf("%s: longer than %d characters", path, numberToInt(raw))
		}
	}
	if raw, ok := schema["pattern"].(string); ok {
		matched, err := regexp.MatchString(raw, value)
		if err != nil {
			return fmt.Errorf("%s: invalid schema pattern: %w", path, err)
		}
		if !matched {
			return fmt.Errorf("%s: does not match pattern %q", path, raw)
		}
	}
	return nil
}

func validateNumber(schema map[string]any, value json.Number, path string) error {
	number, ok := new(big.Rat).SetString(value.String())
	if !ok {
		return fmt.Errorf("%s: invalid number %q", path, value)
	}
	if raw, ok := schema["minimum"]; ok {
		minimum, ok := new(big.Rat).SetString(fmt.Sprint(raw))
		if !ok {
			return fmt.Errorf("%s: invalid minimum", path)
		}
		if number.Cmp(minimum) < 0 {
			return fmt.Errorf("%s: below minimum", path)
		}
	}
	if raw, ok := schema["maximum"]; ok {
		maximum, ok := new(big.Rat).SetString(fmt.Sprint(raw))
		if !ok {
			return fmt.Errorf("%s: invalid maximum", path)
		}
		if number.Cmp(maximum) > 0 {
			return fmt.Errorf("%s: above maximum", path)
		}
	}
	return nil
}

func (s *jsonSchema) resolve(ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("schema: unsupported $ref %q", ref)
	}
	var node any = s.root
	for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		object, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("schema: $ref %q: not an object at %q", ref, token)
		}
		node, ok = object[token]
		if !ok {
			return nil, fmt.Errorf("schema: $ref %q: missing %q", ref, token)
		}
	}
	schema, ok := node.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema: $ref %q does not point to an object", ref)
	}
	return schema, nil
}

func matchesType(instance any, raw any) bool {
	switch spec := raw.(type) {
	case string:
		return matchesSingleType(instance, spec)
	case []any:
		for _, item := range spec {
			name, ok := item.(string)
			if ok && matchesSingleType(instance, name) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func matchesSingleType(instance any, name string) bool {
	switch name {
	case "object":
		_, ok := instance.(map[string]any)
		return ok
	case "array":
		_, ok := instance.([]any)
		return ok
	case "string":
		_, ok := instance.(string)
		return ok
	case "boolean":
		_, ok := instance.(bool)
		return ok
	case "null":
		return instance == nil
	case "number":
		_, ok := instance.(json.Number)
		return ok
	case "integer":
		number, ok := instance.(json.Number)
		if !ok {
			return false
		}
		value, ok := new(big.Rat).SetString(number.String())
		return ok && value.IsInt()
	default:
		return true
	}
}

func jsonValueEqual(a, b any) bool {
	aNumber, aIsNumber := a.(json.Number)
	bNumber, bIsNumber := b.(json.Number)
	if aIsNumber || bIsNumber {
		if !aIsNumber || !bIsNumber {
			return false
		}
		aValue, okA := new(big.Rat).SetString(aNumber.String())
		bValue, okB := new(big.Rat).SetString(bNumber.String())
		return okA && okB && aValue.Cmp(bValue) == 0
	}
	return reflect.DeepEqual(a, b)
}

func numberToInt(raw any) int {
	number, ok := raw.(json.Number)
	if !ok {
		return 0
	}
	value, err := number.Int64()
	if err != nil {
		return 0
	}
	return int(value)
}

func asSchema(raw any, path string) (map[string]any, error) {
	schema, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: schema branch is not an object", path)
	}
	return schema, nil
}

func collectSchemaRefs(value any, refs *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		if ref, ok := typed["$ref"].(string); ok {
			*refs = append(*refs, ref)
		}
		for _, item := range typed {
			collectSchemaRefs(item, refs)
		}
	case []any:
		for _, item := range typed {
			collectSchemaRefs(item, refs)
		}
	}
}
