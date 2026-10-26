package accesslist

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// Encode checks one eth_createAccessList result and returns the canonical JSON
// of the fields the comparison mask retains: accessList, error, and gasUsed.
// Each access-list item keeps only address and storageKeys. A field the mask
// drops does not enter the value.
//
// Admission is section 4.2 step 1 of return-value-design.md: a field is in the
// protected domain because it is in the comparison key. A missing address or
// storageKeys fails. error and gasUsed are omitted when absent and are not
// filled with null. An empty accessList or storageKeys stays [].
//
// Addresses and storage keys are lowercase. gasUsed is canonical QUANTITY.
// error stays the original JSON string. Array order is unchanged. JSON is
// compact, HTML escaping is off, and field order is the encoder struct order.
func Encode(value json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || trimmed[0] != '{' || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("accessList: expected an object")
	}
	var raw struct {
		AccessList json.RawMessage `json:"accessList"`
		Error      json.RawMessage `json:"error"`
		GasUsed    json.RawMessage `json:"gasUsed"`
	}
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, fmt.Errorf("accessList: malformed response")
	}
	if !wire.PresentNonNull(raw.AccessList) {
		return nil, fmt.Errorf("accessList: missing accessList")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw.AccessList, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("accessList: expected an array")
	}
	items := make([]canonicalItem, len(rows))
	for i, row := range rows {
		item, err := encodeItem(row)
		if err != nil {
			return nil, fmt.Errorf("accessList[%d]: %w", i, err)
		}
		items[i] = item
	}
	out := canonical{AccessList: items}
	if wire.PresentNonNull(raw.Error) {
		var text string
		if err := json.Unmarshal(raw.Error, &text); err != nil {
			return nil, fmt.Errorf("accessList: error must be a string")
		}
		out.Error = &text
	}
	if wire.PresentNonNull(raw.GasUsed) {
		gas, err := wire.CanonicalQuantity(raw.GasUsed)
		if err != nil {
			return nil, fmt.Errorf("accessList: gasUsed: %w", err)
		}
		out.GasUsed = &gas
	}
	return wire.MarshalNoEscape(out)
}

func encodeItem(value json.RawMessage) (canonicalItem, error) {
	var raw struct {
		Address     json.RawMessage   `json:"address"`
		StorageKeys []json.RawMessage `json:"storageKeys"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return canonicalItem{}, fmt.Errorf("expected an object")
	}
	if !wire.PresentNonNull(raw.Address) {
		return canonicalItem{}, fmt.Errorf("missing address")
	}
	if raw.StorageKeys == nil {
		return canonicalItem{}, fmt.Errorf("missing storageKeys")
	}
	address, err := wire.ValidateWireFixedHex(raw.Address, 40, "address")
	if err != nil {
		return canonicalItem{}, fmt.Errorf("address: %w", err)
	}
	keys := make([]string, len(raw.StorageKeys))
	for i, key := range raw.StorageKeys {
		keys[i], err = wire.ValidateWireFixedHex(key, 64, "hash")
		if err != nil {
			return canonicalItem{}, fmt.Errorf("storageKeys[%d]: %w", i, err)
		}
	}
	return canonicalItem{Address: address, StorageKeys: keys}, nil
}

// canonical is the protected eth_createAccessList object. Field order is
// fixed. error and gasUsed are omitted when the source did not provide them.
type canonical struct {
	AccessList []canonicalItem `json:"accessList"`
	Error      *string         `json:"error,omitempty"`
	GasUsed    *string         `json:"gasUsed,omitempty"`
}

type canonicalItem struct {
	Address     string   `json:"address"`
	StorageKeys []string `json:"storageKeys"`
}
