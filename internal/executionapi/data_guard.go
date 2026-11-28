package executionapi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// ValidateSelectedData scans precisely the effective compiler inputs. It never
// fixes undisclosed paths, and a stale or unselected exception is an error.
func ValidateSelectedData(bundle Bundle, selections []SchemaSelection) error {
	exceptions := map[string]DataException{}
	used := map[string]bool{}
	for _, e := range bundle.DataExceptions {
		if _, exists := exceptions[e.Source.Path()]; exists {
			return fmt.Errorf("duplicate DATA exception %s", e.Source.Path())
		}
		exceptions[e.Source.Path()] = e
	}
	seen := map[string]bool{}
	for _, selection := range selections {
		raw, err := SelectSchema(bundle, selection)
		if err != nil {
			return err
		}
		if err := walkSchema(raw, selection.Source.Pointer, func(pointer string, node json.RawMessage) error {
			source := selection.Source
			source.Pointer = pointer
			path := source.Path()
			if seen[path] {
				return nil
			}
			seen[path] = true
			var schema map[string]json.RawMessage
			if err := json.Unmarshal(node, &schema); err != nil {
				return err
			}
			var pattern string
			if encoded, ok := schema["pattern"]; ok {
				if err := json.Unmarshal(encoded, &pattern); err != nil {
					return fmt.Errorf("invalid pattern at %s: %w", path, err)
				}
			}
			if pattern != DataOriginalPattern {
				return nil
			}
			exception, ok := exceptions[path]
			if !ok {
				return fmt.Errorf("unreviewed DATA pattern at %s; add an explicitly reviewed target or exception", path)
			}
			if !sameJSON(node, exception.Identity) {
				return fmt.Errorf("DATA exception identity changed at %s", path)
			}
			used[path] = true
			return nil
		}); err != nil {
			return err
		}
	}
	for path := range exceptions {
		if !used[path] {
			return fmt.Errorf("stale or unselected DATA exception %s", path)
		}
	}
	return nil
}

// Traverses JSON Schema vocabulary, never arbitrary example/default values.
func walkSchema(raw json.RawMessage, pointer string, visit func(string, json.RawMessage) error) error {
	var node map[string]json.RawMessage
	if err := json.Unmarshal(raw, &node); err != nil {
		return err
	}
	if err := visit(pointer, raw); err != nil {
		return err
	}
	for _, key := range []string{"properties", "patternProperties", "definitions", "$defs", "dependentSchemas"} {
		var children map[string]json.RawMessage
		if len(node[key]) == 0 {
			continue
		}
		if err := json.Unmarshal(node[key], &children); err != nil {
			return err
		}
		names := make([]string, 0, len(children))
		for name := range children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := walkSchemaValue(children[name], pointer+"/"+key+"/"+EscapePointerToken(name), visit); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"items", "additionalItems", "additionalProperties", "unevaluatedItems", "unevaluatedProperties", "contains", "not", "if", "then", "else", "propertyNames"} {
		if child, ok := node[key]; ok {
			if err := walkSchemaValue(child, pointer+"/"+key, visit); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf", "prefixItems"} {
		if child, ok := node[key]; ok {
			if err := walkSchemaValue(child, pointer+"/"+key, visit); err != nil {
				return err
			}
		}
	}
	return nil
}
func walkSchemaValue(raw json.RawMessage, pointer string, visit func(string, json.RawMessage) error) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	switch value.(type) {
	case bool:
		return nil
	case map[string]any:
		return walkSchema(raw, pointer, visit)
	case []any:
		var children []json.RawMessage
		if err := json.Unmarshal(raw, &children); err != nil {
			return err
		}
		for i, child := range children {
			if err := walkSchemaValue(child, pointer+"/"+strconv.Itoa(i), visit); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("invalid schema value at %s", pointer)
	}
}
