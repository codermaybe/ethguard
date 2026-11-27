package rpcengine

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// CanonicalEncoder checks one admitted payload and returns its canonical JSON
// bytes. Quantity and successful DATA use this shape. A nil encoder means the
// method still returns the representative source's original payload. There is
// no encoder registry; the method file passes the function at assembly.
type CanonicalEncoder func(json.RawMessage) (json.RawMessage, error)

// CanonicalValue is the optional scalar encoder attached at method assembly.
// Policies that do not implement it, and policies whose encoder is nil,
// preserve the selected raw payload. Request handling does not consult the
// method-rule index to find an encoder.
type CanonicalValue interface {
	CanonicalValue(json.RawMessage) (json.RawMessage, error)
}

// CanonicalOf encodes one payload through the resolver's direct capability.
// A missing encoder returns ErrNotCanonicalScalar. An encode failure is
// returned as-is so the caller can record a Fault without shrinking the basis.
func CanonicalOf(policy Resolver, raw json.RawMessage) (json.RawMessage, error) {
	if canonical, ok := policy.(CanonicalValue); ok {
		return canonical.CanonicalValue(raw)
	}
	return nil, rpc.ErrNotCanonicalScalar
}

// CommitCanonical writes the canonical bytes when every supporter in the
// winning cluster encoded the same bytes. A disagreement is a code defect:
// Unknown, ErrInvalidCanonicalResult, and no second vote. The caller must not
// substitute another source's original payload.
func CommitCanonical(parts []json.RawMessage) (json.RawMessage, error) {
	if err := checkCanonical(parts); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), parts[0]...), nil
}

// checkCanonical also diagnoses rejected clusters without copying a value
// that will never be returned.
func checkCanonical(parts []json.RawMessage) error {
	if len(parts) == 0 {
		return fmt.Errorf("result encoding disagrees within cluster: %w", rpc.ErrInvalidCanonicalResult)
	}
	first := parts[0]
	for _, part := range parts[1:] {
		if !bytes.Equal(part, first) {
			return fmt.Errorf("result encoding disagrees within cluster: %w", rpc.ErrInvalidCanonicalResult)
		}
	}
	return nil
}

func resultRule(encode CanonicalEncoder) string {
	if encode != nil {
		return RuleResultCanonical
	}
	return RuleResultRaw
}
