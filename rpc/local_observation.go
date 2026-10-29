package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ResolveLocalObservation resolves one observation set as a node-local
// pending observation (decision 0035): the first source with a normalizable
// answer stands with Selected status and its source as Support — unanimous
// agreement stays Selected, because agreement over pending state is
// coincidence of independent node views, not corroboration of a
// deterministic truth. No usable source fails Unknown with
// ErrNoValidResponse.
//
// The policy argument is the method's resolver value: normalization and the
// canonical encoder are the resolver's own, and the value domain includes
// structured RPC error bodies only when the resolver opted in. This helper is
// the one implementation behind the PendingLocal compatibility decorator and
// the pending branch of a planned read.
func ResolveLocalObservation(policy Policy, obs []Observation) (Decision, error) {
	includeRPCError := false
	if provider, ok := policy.(RPCErrorPolicy); ok {
		includeRPCError = provider.IncludeRPCError()
	}
	var faults []error
	// faultSource mirrors the resolver fault attribution so pending's
	// node-local path reads identically in Decision.Faults.
	faultSource := func(source SourceRef, cause string, err error) error {
		id := source.ID
		if id == "" {
			id = source.Name
		}
		return fmt.Errorf("source %q: %s: %w", id, cause, err)
	}
	for _, item := range obs {
		value := item.Value
		if item.Err != nil {
			if !includeRPCError || item.RPCError == nil {
				continue
			}
			value = item.RPCError.Raw()
		}
		if _, err := policy.Normalize(value); err != nil {
			faults = append(faults, faultSource(item.Source, "normalize", err))
			continue
		}
		if canonical, err := CanonicalOf(policy, value); err == nil {
			value = canonical
		} else if !errors.Is(err, ErrNotCanonicalScalar) {
			faults = append(faults, faultSource(item.Source, "encode", err))
			return Decision{Status: Unknown, Faults: faults}, fmt.Errorf("pending: %w", err)
		}
		return Decision{
			Status:     Selected,
			Value:      value,
			Support:    []SourceRef{item.Source},
			Faults:     faults,
			Provenance: append(json.RawMessage(nil), item.Evidence...),
		}, nil
	}
	return Decision{Status: Unknown, Faults: faults}, fmt.Errorf(
		"pending: no source produced a valid response (%d attempted): %w", len(obs), ErrNoValidResponse)
}
