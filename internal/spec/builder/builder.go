// Package builder turns an official, dereferenced execution-apis openrpc.json
// into the small methods.json bundle embedded by ethguard.
package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Build selects result schemas from a dereferenced OpenRPC document. Methods
// is an explicit policy boundary: provider observations never influence it.
func Build(openRPC []byte, version string, methods []string) ([]byte, error) {
	if version == "" {
		return nil, errors.New("version is required")
	}
	if len(methods) == 0 {
		return nil, errors.New("method set is empty")
	}
	var document struct {
		Methods []struct {
			Name   string `json:"name"`
			Result *struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"result"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(openRPC, &document); err != nil {
		return nil, fmt.Errorf("openrpc: %w", err)
	}
	available := make(map[string]json.RawMessage, len(document.Methods))
	for _, method := range document.Methods {
		if method.Name == "" || method.Result == nil || len(method.Result.Schema) == 0 {
			continue
		}
		available[method.Name] = append(json.RawMessage(nil), method.Result.Schema...)
	}
	ordered := append([]string(nil), methods...)
	sort.Strings(ordered)
	results := make(map[string]json.RawMessage, len(ordered))
	seen := make(map[string]bool, len(ordered))
	for _, name := range ordered {
		if name == "" || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate method %q", name)
		}
		seen[name] = true
		schema, ok := available[name]
		if !ok {
			return nil, fmt.Errorf("method %q has no result.schema in openrpc", name)
		}
		results[name] = schema
	}
	var output struct {
		Version string                     `json:"version"`
		Results map[string]json.RawMessage `json:"results"`
	}
	output.Version = version
	output.Results = results
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", " ")
	if err := encoder.Encode(output); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MethodsFromBundle returns the sorted method names from an existing
// methods.json bundle, preserving the current support boundary during an
// official release rebuild.
func MethodsFromBundle(bundleJSON []byte) ([]string, error) {
	var bundle struct {
		Results map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(bundleJSON, &bundle); err != nil {
		return nil, err
	}
	if len(bundle.Results) == 0 {
		return nil, errors.New("bundle results is empty")
	}
	methods := make([]string, 0, len(bundle.Results))
	for name := range bundle.Results {
		methods = append(methods, name)
	}
	sort.Strings(methods)
	return methods, nil
}
