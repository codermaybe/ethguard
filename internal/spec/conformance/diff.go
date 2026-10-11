package conformance

import (
	"encoding/json"
	"fmt"
	"sort"
)

// BundleDiff is a review-oriented comparison of two methods.json bundles.
// It records method additions/removals and field-level schema changes without
// deciding whether a production projection should adopt them.
type BundleDiff struct {
	BeforeVersion string
	AfterVersion  string
	Methods       []MethodDiff
}

// MethodDiff contains one method's schema changes. Added and Removed are
// mutually exclusive for method-level changes; Changed is populated when both
// bundles contain the method.
type MethodDiff struct {
	Method  string
	Added   bool
	Removed bool
	Changed []SchemaChange
}

// SchemaChange describes one JSON Pointer-like schema location.
type SchemaChange struct {
	Path           string
	Kind           string
	BeforeType     string
	AfterType      string
	BeforeRequired bool
	AfterRequired  bool
	Before         string
	After          string
}

// DiffBundles compares two methods.json-shaped documents. It is intended for
// an official release upgrade review; it does not write or mutate either
// bundle.
func DiffBundles(beforeJSON, afterJSON []byte) (BundleDiff, error) {
	before, err := decodeBundle(beforeJSON)
	if err != nil {
		return BundleDiff{}, fmt.Errorf("before bundle: %w", err)
	}
	after, err := decodeBundle(afterJSON)
	if err != nil {
		return BundleDiff{}, fmt.Errorf("after bundle: %w", err)
	}
	result := BundleDiff{BeforeVersion: before.version, AfterVersion: after.version}
	names := map[string]bool{}
	for name := range before.results {
		names[name] = true
	}
	for name := range after.results {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		left, leftOK := before.results[name]
		right, rightOK := after.results[name]
		switch {
		case !leftOK:
			result.Methods = append(result.Methods, MethodDiff{Method: name, Added: true})
		case !rightOK:
			result.Methods = append(result.Methods, MethodDiff{Method: name, Removed: true})
		default:
			changes := diffSchema(left, right)
			if len(changes) > 0 {
				result.Methods = append(result.Methods, MethodDiff{Method: name, Changed: changes})
			}
		}
	}
	return result, nil
}

type decodedBundle struct {
	version string
	results map[string]any
}

func decodeBundle(raw []byte) (decodedBundle, error) {
	var value struct {
		Version string         `json:"version"`
		Results map[string]any `json:"results"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return decodedBundle{}, err
	}
	if value.Version == "" {
		return decodedBundle{}, fmt.Errorf("missing version")
	}
	if value.Results == nil {
		return decodedBundle{}, fmt.Errorf("missing results")
	}
	return decodedBundle{version: value.Version, results: value.Results}, nil
}

type schemaEntry struct {
	Type     string
	Required bool
	Summary  string
}

func diffSchema(before, after any) []SchemaChange {
	left := map[string]schemaEntry{}
	right := map[string]schemaEntry{}
	indexSchema(before, "$", false, left)
	indexSchema(after, "$", false, right)
	paths := map[string]bool{}
	for path := range left {
		paths[path] = true
	}
	for path := range right {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	changes := make([]SchemaChange, 0)
	for _, path := range ordered {
		old, oldOK := left[path]
		current, currentOK := right[path]
		switch {
		case !oldOK:
			changes = append(changes, SchemaChange{Path: path, Kind: "added", AfterType: current.Type, AfterRequired: current.Required, After: current.Summary})
		case !currentOK:
			changes = append(changes, SchemaChange{Path: path, Kind: "removed", BeforeType: old.Type, BeforeRequired: old.Required, Before: old.Summary})
		case old.Summary != current.Summary || old.Required != current.Required:
			changes = append(changes, SchemaChange{Path: path, Kind: "changed", BeforeType: old.Type, AfterType: current.Type, BeforeRequired: old.Required, AfterRequired: current.Required, Before: old.Summary, After: current.Summary})
		}
	}
	return changes
}

func indexSchema(node any, path string, required bool, out map[string]schemaEntry) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	typeName, _ := m["type"].(string)
	selected := make(map[string]any)
	for _, key := range []string{"type", "pattern", "additionalProperties", "enum", "minimum", "maximum", "minItems", "maxItems"} {
		if value, exists := m[key]; exists {
			selected[key] = value
		}
	}
	encoded, _ := json.Marshal(selected)
	out[path] = schemaEntry{Type: typeName, Required: required, Summary: string(encoded)}

	properties, _ := m["properties"].(map[string]any)
	requiredNames := map[string]bool{}
	if items, ok := m["required"].([]any); ok {
		for _, item := range items {
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
		indexSchema(properties[name], path+"/"+escape(name), requiredNames[name], out)
	}
	if items, ok := m["items"]; ok {
		indexSchema(items, path+"/*", false, out)
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		branches, _ := m[key].([]any)
		for i, branch := range branches {
			indexSchema(branch, path+"#"+key+"/"+fmt.Sprint(i), false, out)
		}
	}
}
