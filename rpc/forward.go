package rpc

import (
	"context"
	"encoding/json"
	"fmt"

	"ethguard/pool"
)

// ForwardPolicy returns the first successful observation without claiming any
// agreement between sources. It is the fallback for unknown methods.
type ForwardPolicy struct{}

func (ForwardPolicy) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}
func (ForwardPolicy) Normalize(value json.RawMessage) (string, error) { return string(value), nil }
func (ForwardPolicy) Resolve(obs []Observation) (Decision, error) {
	for _, item := range obs {
		if item.Err == nil {
			return Decision{Status: Forwarded, Value: item.Value, Support: []*pool.Source{item.Source}}, nil
		}
	}
	return Decision{}, fmt.Errorf("forward: no source responded")
}

// SelectedPolicy returns the first successful observation while marking the
// decision as selected rather than verified.
type SelectedPolicy struct{}

func (SelectedPolicy) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}
func (SelectedPolicy) Normalize(value json.RawMessage) (string, error) { return string(value), nil }
func (SelectedPolicy) Resolve(obs []Observation) (Decision, error) {
	for _, item := range obs {
		if item.Err == nil {
			return Decision{Status: Selected, Value: item.Value, Support: []*pool.Source{item.Source}}, nil
		}
	}
	return Decision{}, fmt.Errorf("selected: no source responded")
}
