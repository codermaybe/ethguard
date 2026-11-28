package rpc

import (
	"encoding/json"
	"errors"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

// CanonicalJSON reduces any JSON value to a comparable canonical form by
// decoding and re-encoding it, erasing key order and formatting differences.
func CanonicalJSON(value json.RawMessage) (string, error) {
	var decoded any
	if err := jsonsafe.Decode(value, &decoded); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(decoded)
	return string(canonical), err
}

// EncodeCanonicalJSON adapts the generic canonical JSON normalizer to the
// encoder seam used by selected local results.
func EncodeCanonicalJSON(value json.RawMessage) (json.RawMessage, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(canonical), nil
}

// ErrNotCanonicalScalar means the payload is outside the scalar encoder.
// eth_call reverts use it. The caller keeps the representative source's
// original payload and does not record a Fault.
var ErrNotCanonicalScalar = errors.New("rpc: result is not a canonical scalar")
