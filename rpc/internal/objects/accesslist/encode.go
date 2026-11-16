package accesslist

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
)

// Encode checks one eth_createAccessList result and returns the canonical JSON
// of the fields the comparison mask retains: accessList, error, and gasUsed.
// Field shapes and scalar constraints come from the generated official model
// (ethapi.AccessListResult): addresses and storage keys are lowercase fixed
// hex, gasUsed is a canonical QUANTITY, and error keeps its decoded text.
//
// Two project rules sit on top of the generated model and must not be dropped:
// accessList is required here (the official schema marks it optional), and a
// present-but-null error or gasUsed means the member is omitted, not rejected.
// A field the comparison mask drops does not enter the value. An empty
// accessList or storageKeys stays []. JSON is compact, HTML escaping is off,
// and field order is the generated struct order.
func Encode(value json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || trimmed[0] != '{' || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("accessList: expected an object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, fmt.Errorf("accessList: malformed response")
	}
	if !ethcodec.PresentNonNull(fields["accessList"]) {
		return nil, fmt.Errorf("accessList: missing accessList")
	}
	result, err := ethapi.NewAccessListResult(nullsOmitted(trimmed, fields))
	if err != nil {
		return nil, fmt.Errorf("accessList: %w", err)
	}
	return ethcodec.MarshalNoEscape(result)
}

// nullsOmitted re-renders the object with top-level null members dropped, so
// the generated model's non-nullable fields match the project rule that a
// null error or gasUsed is folded to absence. The common no-null object is
// passed through untouched; unknown members and nested values are always
// untouched, and the constructor still validates everything itself.
func nullsOmitted(raw json.RawMessage, fields map[string]json.RawMessage) json.RawMessage {
	hasNull := false
	for _, value := range fields {
		if ethcodec.IsJSONNull(value) {
			hasNull = true
			break
		}
	}
	if !hasNull {
		return raw
	}
	cleaned := make(map[string]json.RawMessage, len(fields))
	for name, value := range fields {
		if ethcodec.IsJSONNull(value) {
			continue
		}
		cleaned[name] = value
	}
	encoded, err := json.Marshal(cleaned)
	if err != nil {
		return raw
	}
	return encoded
}
