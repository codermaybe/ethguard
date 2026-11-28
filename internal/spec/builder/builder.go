// Package builder turns an official, dereferenced execution-apis openrpc.json
// into the small methods.json bundle embedded by ethguard.
package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
		if _, duplicate := available[method.Name]; duplicate {
			return nil, fmt.Errorf("duplicate method %q", method.Name)
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

// BuildRequests preserves the parameter descriptors of official eth_ and net_
// RPC methods. This is reference inventory, not an executable-method list:
// catalog/ReadPlan continues to decide which methods callers can execute.
func BuildRequests(openRPC []byte, version string, prefixes ...string) ([]byte, error) {
	if version == "" {
		return nil, errors.New("version is required")
	}
	var document struct {
		Methods []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(openRPC, &document); err != nil {
		return nil, fmt.Errorf("openrpc: %w", err)
	}
	if len(prefixes) == 0 {
		return nil, errors.New("request inventory prefixes are required")
	}
	requests := map[string]json.RawMessage{}
	for _, method := range document.Methods {
		selected := false
		for _, prefix := range prefixes {
			selected = selected || strings.HasPrefix(method.Name, prefix)
		}
		if !selected {
			continue
		}
		if _, duplicate := requests[method.Name]; duplicate {
			return nil, fmt.Errorf("duplicate method %q", method.Name)
		}
		if len(method.Params) == 0 || bytes.Equal(bytes.TrimSpace(method.Params), []byte("null")) {
			return nil, fmt.Errorf("method %q has no params descriptor", method.Name)
		}
		var params []json.RawMessage
		if err := json.Unmarshal(method.Params, &params); err != nil {
			return nil, fmt.Errorf("method %q params must be an array: %w", method.Name, err)
		}
		requests[method.Name] = method.Params
	}
	if len(requests) == 0 {
		return nil, errors.New("no selected request schemas")
	}
	output := struct {
		Version  string                     `json:"version"`
		Requests map[string]json.RawMessage `json:"requests"`
	}{Version: version, Requests: requests}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", " ")
	if err := encoder.Encode(output); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
