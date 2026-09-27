package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// CanonicalEncoder checks one admitted payload and returns its canonical JSON
// bytes. Quantity and successful DATA use this shape. A nil encoder means the
// method still returns the representative source's original payload. There is
// no encoder registry; the method file passes the function at assembly.
type CanonicalEncoder func(json.RawMessage) (json.RawMessage, error)

// ErrNotCanonicalScalar means the payload is outside the scalar encoder.
// eth_call reverts use it. The caller keeps the representative source's
// original payload and does not record a Fault.
var ErrNotCanonicalScalar = errors.New("rpc: result is not a canonical scalar")

// CanonicalValue is the optional scalar encoder attached at method assembly.
// Policies that do not implement it, and policies whose encoder is nil, are
// not migrated. Request handling does not consult the method-rule index to
// find an encoder.
type CanonicalValue interface {
	CanonicalValue(json.RawMessage) (json.RawMessage, error)
}

// CanonicalOf walks the decorator chain and encodes one payload. A missing
// encoder returns ErrNotCanonicalScalar. An encode failure is returned as-is
// so the caller can record a Fault without shrinking the denominator.
func CanonicalOf(policy Policy, raw json.RawMessage) (json.RawMessage, error) {
	for policy != nil {
		if canonical, ok := policy.(CanonicalValue); ok {
			return canonical.CanonicalValue(raw)
		}
		source, ok := policy.(MethodRuleSource)
		if !ok {
			break
		}
		_, next := source.MethodRule()
		if next == policy {
			break
		}
		policy = next
	}
	return nil, ErrNotCanonicalScalar
}

// CommitCanonical writes the canonical bytes when every supporter in the
// winning cluster encoded the same bytes. A disagreement is a code defect:
// Unknown, ErrMalformedResponse, and no second vote. The caller must not
// substitute another source's original payload.
func CommitCanonical(parts []json.RawMessage) (json.RawMessage, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("result encoding disagrees within cluster: %w", ErrMalformedResponse)
	}
	first := parts[0]
	for _, part := range parts[1:] {
		if !bytes.Equal(part, first) {
			return nil, fmt.Errorf("result encoding disagrees within cluster: %w", ErrMalformedResponse)
		}
	}
	return append(json.RawMessage(nil), first...), nil
}

func resultRule(encode CanonicalEncoder) string {
	if encode != nil {
		return RuleResultCanonical
	}
	return RuleResultRaw
}
