package rpc

import (
	"fmt"
	"strings"
)

// EIP-1898 BlockNumberOrHash object semantics. These are request-shape
// facts shared by the execution plan and the EIP1898BlockRef compatibility
// decorator: one implementation, so the object form's validation and its
// rewrite condition cannot drift between the two surfaces.

// ValidateEIP1898Object checks one EIP-1898 BlockNumberOrHash object at an
// anchored position. It returns whether requireCanonical was set, whether
// the object carries a moving tag that still needs its reference, and the
// tag itself. Unknown fields, contradictory objects, malformed hashes,
// non-string blockNumber/blockHash, non-boolean requireCanonical, and
// missing both fields are rejected locally with ErrInvalidRequest.
func ValidateEIP1898Object(method string, obj map[string]any) (pinned, needAnchor bool, tag string, err error) {
	invalid := func(format string, args ...any) (bool, bool, string, error) {
		return false, false, "", fmt.Errorf("rpc: %s: %s: %w", method, fmt.Sprintf(format, args...), ErrInvalidRequest)
	}
	var (
		hash, hasHash     = obj["blockHash"]
		number, hasNumber = obj["blockNumber"]
		canon, hasCanon   = obj["requireCanonical"]
	)
	for key := range obj {
		switch key {
		case "blockHash", "blockNumber", "requireCanonical":
		default:
			return invalid("unknown field %q", key)
		}
	}
	if hasHash && hasNumber {
		return invalid("mixes blockHash with blockNumber")
	}
	if !hasHash && !hasNumber {
		return invalid("requires blockHash or blockNumber")
	}
	if hasCanon {
		canonBool, ok := canon.(bool)
		if !ok {
			return invalid("requireCanonical must be a boolean")
		}
		pinned = canonBool
	}
	if hasHash {
		hashStr, ok := hash.(string)
		if !ok || !isBlockHash(hashStr) {
			return invalid("blockHash must be a 32-byte hash")
		}
		return pinned, false, "", nil
	}
	numberStr, ok := number.(string)
	if !ok {
		return invalid("blockNumber must be a tag or hex quantity string")
	}
	lowered := strings.ToLower(numberStr)
	switch lowered {
	case "latest", "safe", "finalized":
		return pinned, true, lowered, nil
	case "pending", "earliest":
		return pinned, false, "", nil
	}
	if _, err := ParseHexQuantity(numberStr); err != nil {
		return invalid("blockNumber %q is not a tag or hex quantity", numberStr)
	}
	return pinned, false, "", nil
}

func isBlockHash(s string) bool {
	if len(s) != 2+64 || !strings.HasPrefix(s, "0x") {
		return false
	}
	for _, c := range s[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// EIP1898Rewrite returns the rewritten object for a moving blockNumber at an
// anchored position: {blockHash: hash[, requireCanonical]}. The tag is never
// downgraded and requireCanonical is never dropped. It is the shared rewrite
// behind the plan and the EIP1898BlockRef decorator.
func EIP1898Rewrite(hash string, requireCanonical bool) map[string]any {
	out := map[string]any{"blockHash": hash}
	if requireCanonical {
		out["requireCanonical"] = true
	}
	return out
}
