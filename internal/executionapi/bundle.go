// Package executionapi loads the pinned official method schemas for generation,
// comparison masks, and field projection. It owns assembly of reference data;
// reviewed schema errata are applied before consumers see the effective view.
// It never corrects provider responses or performs business admission.
package executionapi

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/internal/spec"
)

type Bundle struct {
	Version  string
	Results  map[string]json.RawMessage
	Requests map[string][]Parameter
	Errata   []AppliedErratum
}

type Parameter struct {
	Name     string          `json:"name"`
	Required bool            `json:"required"`
	Schema   json.RawMessage `json:"schema"`
}

// Read returns the effective schema view shared by generation, masks, and
// projection. A stale or redundant erratum fails before any consumer runs.
func Read() (Bundle, error) {
	bundle, err := ReadOriginal()
	if err != nil {
		return Bundle{}, err
	}
	if err := applyErrata(&bundle, spec.SourceJSON(), spec.ErrataFS()); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

// ReadOriginal assembles only the unmodified upstream schemas. Independent
// source conformance and upgrade audits must keep this view as their oracle.
func ReadOriginal() (Bundle, error) {
	bundle, err := load(spec.MethodsJSON(), spec.Version())
	if err != nil {
		return Bundle{}, err
	}
	requests, err := loadRequests(spec.RequestsJSON(), bundle.Version)
	if err != nil {
		return Bundle{}, err
	}
	bundle.Requests = requests
	return bundle, nil
}

func loadRequests(raw []byte, pin string) (map[string][]Parameter, error) {
	var requests struct {
		Version  string                 `json:"version"`
		Requests map[string][]Parameter `json:"requests"`
	}
	if err := json.Unmarshal(raw, &requests); err != nil {
		return nil, fmt.Errorf("execution-apis requests: %w", err)
	}
	if requests.Version != pin {
		return nil, fmt.Errorf("execution-apis requests version %q != pin %q", requests.Version, pin)
	}
	if len(requests.Requests) == 0 {
		return nil, fmt.Errorf("execution-apis requests bundle is empty")
	}
	for method, params := range requests.Requests {
		if params == nil {
			return nil, fmt.Errorf("execution-apis %s: params must be an array", method)
		}
		seen := map[string]bool{}
		for i, param := range params {
			if param.Name == "" || seen[param.Name] {
				return nil, fmt.Errorf("execution-apis %s params[%d]: missing or duplicate name %q", method, i, param.Name)
			}
			seen[param.Name] = true
			if !bytes.HasPrefix(bytes.TrimSpace(param.Schema), []byte("{")) {
				return nil, fmt.Errorf("execution-apis %s params[%d]: schema must be an object", method, i)
			}
		}
	}
	return requests.Requests, nil
}

func load(methods []byte, pin string) (Bundle, error) {
	var base struct {
		Version string                     `json:"version"`
		Results map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(methods, &base); err != nil {
		return Bundle{}, fmt.Errorf("execution-apis methods bundle: %w", err)
	}
	if base.Version != pin {
		return Bundle{}, fmt.Errorf("execution-apis methods version %q != pin %q", base.Version, pin)
	}
	if len(base.Results) == 0 {
		return Bundle{}, fmt.Errorf("execution-apis methods bundle has no results")
	}
	return Bundle{Version: pin, Results: base.Results}, nil
}
