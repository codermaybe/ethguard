package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ForwardPolicy returns the first successful observation without claiming any
// agreement between sources. It is the fallback for unknown methods.
type ForwardPolicy struct{}

// SingleSource marks the policy as requiring ordered one-source forwarding.
func (ForwardPolicy) SingleSource() bool { return true }

// IncludeRPCError preserves authoritative protocol answers in observations.
func (ForwardPolicy) IncludeRPCError() bool { return true }

func (ForwardPolicy) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}
func (ForwardPolicy) Normalize(value json.RawMessage) (string, error) { return string(value), nil }
func (ForwardPolicy) Resolve(obs []Observation) (Decision, error) {
	for _, item := range obs {
		if item.Err == nil {
			return Decision{Status: Forwarded, Value: item.Value, Support: []SourceRef{item.Source}}, nil
		}
		var protocolErr ProtocolError
		if errors.As(item.Err, &protocolErr) {
			// The selected provider's protocol response is authoritative for a
			// forwarded method; preserve the original RPC error for the caller.
			// No confidence claim is made on an error return: Unknown, not Forwarded.
			return Decision{Status: Unknown, Support: []SourceRef{item.Source}}, item.Err
		}
	}
	return Decision{Status: Unknown}, fmt.Errorf("forward: no source responded: %w", ErrNoValidResponse)
}

// SelectedPolicy returns the first successful observation while marking the
// decision as selected rather than corroborated.
type SelectedPolicy struct{}

func (SelectedPolicy) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}
func (SelectedPolicy) Normalize(value json.RawMessage) (string, error) { return string(value), nil }
func (SelectedPolicy) Resolve(obs []Observation) (Decision, error) {
	for _, item := range obs {
		if item.Err == nil {
			return Decision{Status: Selected, Value: item.Value, Support: []SourceRef{item.Source}}, nil
		}
	}
	return Decision{Status: Unknown}, fmt.Errorf("selected: no source responded: %w", ErrNoValidResponse)
}
