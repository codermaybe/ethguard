// Package header owns the shared mined-header integrity checks. Method
// request binding and null/query policy remain with the caller.
package header

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

// The explicit Header projection follows the generated model's JSON tags.
// Its field inventory therefore changes only with the audited generation.
var fields = func() []string {
	t := reflect.TypeOf(ethapi.Header{})
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			names = append(names, name)
		}
	}
	return names
}()

// ReferenceOnly reports whether an already decoded source declares no
// official Header members besides number/hash. Even an explicit null optional
// member counts as a Header declaration before compatibility projection.
func ReferenceOnly(source map[string]json.RawMessage) bool {
	for _, name := range fields {
		if name == "number" || name == "hash" {
			continue
		}
		if _, present := source[name]; present {
			return false
		}
	}
	return true
}

// Projection selects the official Header members, folds their hex spelling,
// and folds geth's unset optional pointer nulls to omission. It does not validate a header or supply
// missing members. Block uses the same explicit projection before its own
// generated contract is checked.
func Projection(raw json.RawMessage) (json.RawMessage, error) {
	if err := jsonsafe.Validate(raw); err != nil {
		return nil, fmt.Errorf("header JSON: %w", err)
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil || source == nil {
		return nil, fmt.Errorf("header: expected object")
	}
	return ethcodec.MarshalNoEscape(projectFields(source))
}

func projectFields(source map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage)
	for _, name := range fields {
		value, present := source[name]
		if !present || (optional(name) && ethcodec.IsJSONNull(value)) {
			continue
		}
		out[name] = ethcodec.FoldHexSpelling(value)
	}
	return out
}

// optionalNullMembers is the sole compatibility policy for geth's unset
// optional pointer fields. These official nonnullable members fold null to
// omission; required fields and malformed nonnull values never do.
var optionalNullMembers = [...]string{
	"baseFeePerGas", "withdrawalsRoot", "blobGasUsed", "excessBlobGas",
	"parentBeaconBlockRoot", "requestsHash", "blockAccessListHash", "slotNumber",
}

func optional(name string) bool {
	for _, member := range optionalNullMembers {
		if name == member {
			return true
		}
	}
	return false
}

// Decode checks the original declared wire hash against the header's own
// hash. Explicit scalar spelling folding runs before the generated check and private geth bridge,
// so valid JSON escapes and equivalent scalar spellings remain transparent.
// A Header has no uncle payload; a historical nonempty uncle root is valid.
func Decode(raw json.RawMessage) (*types.Header, error) {
	_, head, err := prepare(raw)
	return head, err
}

// DecodeWithCanonical returns both products of the same original-header
// check. Block admission can reuse the canonical projection without checking
// the source hash and parsing the header again.
func DecodeWithCanonical(raw json.RawMessage) (json.RawMessage, *types.Header, error) {
	return prepare(raw)
}

// Normalize checks the original commitment and returns the independent
// canonical official Header. It never manufactures a header from a
// number/hash reference or re-encodes geth's self-computed hash as evidence.
func Normalize(raw json.RawMessage) (json.RawMessage, error) {
	canonical, _, err := prepare(raw)
	return canonical, err
}

func prepare(raw json.RawMessage) (json.RawMessage, *types.Header, error) {
	if err := jsonsafe.Validate(raw); err != nil {
		return nil, nil, fmt.Errorf("header JSON: %w", err)
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil || source == nil {
		return nil, nil, fmt.Errorf("header: expected object")
	}
	// Retain the source declaration before any algorithm representation is
	// constructed. The projection folds spelling before schema validation.
	declared, err := ethcodec.ValidateWireFixedHex(source["hash"], 64, "hash")
	if err != nil {
		return nil, nil, fmt.Errorf("header hash: %w", err)
	}
	projected, err := ethcodec.MarshalNoEscape(projectFields(source))
	if err != nil {
		return nil, nil, err
	}
	value, err := ethapi.NewHeader(projected)
	if err != nil {
		return nil, nil, fmt.Errorf("header: %w", err)
	}
	canonical, err := ethcodec.MarshalNoEscape(value)
	if err != nil {
		return nil, nil, err
	}
	var head types.Header
	if err := json.Unmarshal(canonical, &head); err != nil {
		return nil, nil, fmt.Errorf("header: invalid payload: %w", err)
	}
	if common.HexToHash(declared) != head.Hash() {
		return nil, nil, fmt.Errorf("header: wire hash disagrees with computed header hash")
	}
	return canonical, &head, nil
}
