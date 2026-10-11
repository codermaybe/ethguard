package projection

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ethguard/internal/spec"
)

var officialFields = struct {
	sync.Once
	fields map[string][]StandardField
	err    error
}{}

// ExecutionAPIStandardFields derives field paths from the pinned official
// result schema. It is used by offline shape reports and can be adopted by a
// method projector only after that method's encoder and admission rules cover
// the new fields.
func ExecutionAPIStandardFields(method string) ([]StandardField, error) {
	officialFields.Do(func() {
		var bundle struct {
			Version string                     `json:"version"`
			Results map[string]json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal(spec.MethodsJSON(), &bundle); err != nil {
			officialFields.err = err
			return
		}
		if bundle.Version != spec.Version() {
			officialFields.err = fmt.Errorf("spec version %q does not match pin %q", bundle.Version, spec.Version())
			return
		}
		officialFields.fields = make(map[string][]StandardField, len(bundle.Results))
		for name, raw := range bundle.Results {
			var schema any
			if err := json.Unmarshal(raw, &schema); err != nil {
				officialFields.err = fmt.Errorf("%s: %w", name, err)
				return
			}
			paths := map[string]bool{}
			collectSchemaPaths(schema, "", false, paths)
			fields := make([]StandardField, 0, len(paths))
			for path, required := range paths {
				fields = append(fields, StandardField{Path: path, Required: required})
			}
			sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
			officialFields.fields[name] = fields
		}
	})
	if officialFields.err != nil {
		return nil, officialFields.err
	}
	fields, ok := officialFields.fields[method]
	if !ok {
		return nil, fmt.Errorf("no official result schema for %q", method)
	}
	return append([]StandardField(nil), fields...), nil
}

func collectSchemaPaths(schema any, path string, required bool, paths map[string]bool) {
	m, ok := schema.(map[string]any)
	if !ok {
		return
	}
	if typeName, _ := m["type"].(string); typeName == "null" {
		return
	}
	if properties, ok := m["properties"].(map[string]any); ok {
		requiredNames := map[string]bool{}
		if requiredList, ok := m["required"].([]any); ok {
			for _, item := range requiredList {
				if name, ok := item.(string); ok {
					requiredNames[name] = true
				}
			}
		}
		keys := make([]string, 0, len(properties))
		for name := range properties {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			collectSchemaPaths(properties[name], path+"/"+escapePointer(name), requiredNames[name], paths)
		}
	} else if items, ok := m["items"]; ok {
		collectSchemaPaths(items, path+"/*", required, paths)
	} else {
		paths[path] = paths[path] || required
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if branches, ok := m[key].([]any); ok {
			for _, branch := range branches {
				collectSchemaPaths(branch, path, required, paths)
			}
		}
	}
}

func escapePointer(token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	return strings.ReplaceAll(token, "/", "~1")
}
