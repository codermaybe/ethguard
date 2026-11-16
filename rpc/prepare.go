package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

// The resolver shells share three pieces of plumbing that carry no method
// semantics: a prepare that takes the caller's request unchanged, the
// canonical-value shell over an optional encoder, and the no-usable-answer
// sentinel. Method-specific preparation lives in the plan's Prepare hook, not
// in the resolver.

// requestPassthrough is the shared prepare of the resolver shells: these
// policies take the caller's request unchanged and never rewrite it.
type requestPassthrough struct{}

func (requestPassthrough) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

// encodeCanonicalValue is the shared canonical-value shell of the resolvers
// that carry one optional encoder: without one the value is not a canonical
// scalar; with one, the encoder speaks.
func encodeCanonicalValue(encode CanonicalEncoder, raw json.RawMessage) (json.RawMessage, error) {
	if encode == nil {
		return nil, ErrNotCanonicalScalar
	}
	return encode(raw)
}

// noValidResponse is the shared no-usable-answer sentinel, prefixed by the
// resolver class, with the attempted count kept visible.
func noValidResponse(class string, attempted int) error {
	return fmt.Errorf("%s: no source produced a valid response (%d attempted): %w", class, attempted, ErrNoValidResponse)
}

// missingIdentity is the shared empty-identity sentinel: an observation
// without a source identity is the process-conflict, never a skipped answer.
func missingIdentity(prefix, kind string, i int) error {
	return fmt.Errorf("%s: %s %d has no source identity: %w", prefix, kind, i, ErrConflict)
}

// duplicateIdentity is the shared duplicate-identity sentinel with the slot
// positions kept visible.
func duplicateIdentity(prefix, kind, id string, first, i int) error {
	return fmt.Errorf("%s: duplicate %s for source %q (first at %d, again at %d): %w", prefix, kind, id, first, i, ErrConflict)
}
