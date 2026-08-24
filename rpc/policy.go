package rpc

import (
	"context"
	"encoding/json"
)

// Policy prepares requests and resolves raw observations. The contract lives
// beside the transport model; concrete per-method implementations live in
// rpc/methods, one file per RPC method.
type Policy interface {
	Prepare(context.Context, Request) (Request, error)
	Normalize(json.RawMessage) (string, error)
	Resolve([]Observation) (Decision, error)
}

// Registry maps JSON-RPC method names to policies. An empty or nil registry
// forwards every method transparently.
type Registry map[string]Policy

// PolicyFor returns a registered policy or transparent forwarding.
func (r Registry) PolicyFor(method string) Policy {
	if p := r[method]; p != nil {
		return p
	}
	return ForwardPolicy{}
}
