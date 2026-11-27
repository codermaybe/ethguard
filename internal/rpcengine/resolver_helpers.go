package rpcengine

import (
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// encodeCanonicalValue is the shared canonical-value shell of the resolvers
// that carry one optional encoder: without one the value is not a canonical
// scalar; with one, the encoder speaks.
func encodeCanonicalValue(encode CanonicalEncoder, raw json.RawMessage) (json.RawMessage, error) {
	if encode == nil {
		return nil, rpc.ErrNotCanonicalScalar
	}
	return encode(raw)
}

// noValidResponse is the shared no-usable-answer sentinel, prefixed by the
// resolver class, with the observation count kept visible.
func noValidResponse(class string, observations int) error {
	return fmt.Errorf("%s: no source produced a valid response (%d observations): %w", class, observations, rpc.ErrNoValidResponse)
}

// missingIdentity is the shared empty-identity sentinel: an observation
// without a source identity is the process-conflict, never a skipped answer.
func missingIdentity(prefix, kind string, i int) error {
	return fmt.Errorf("%s: %s %d has no source identity: %w", prefix, kind, i, rpc.ErrInvalidOutcome)
}

// duplicateIdentity is the shared duplicate-identity sentinel with the slot
// positions kept visible.
func duplicateIdentity(prefix, kind, id string, first, i int) error {
	return fmt.Errorf("%s: duplicate %s for source %q (first at %d, again at %d): %w", prefix, kind, id, first, i, rpc.ErrInvalidOutcome)
}
