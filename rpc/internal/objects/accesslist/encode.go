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
	resolved, err := resolveProtectedMembers(trimmed)
	if err != nil {
		return nil, fmt.Errorf("accessList: malformed response")
	}
	if resolved.accessList == nil {
		return nil, fmt.Errorf("accessList: missing accessList")
	}
	items, err := checkItems(resolved.accessList)
	if err != nil {
		return nil, err
	}
	// The result assembles from the already-validated members: the items are
	// the generated model's own, and the scalars go through their generated
	// constructors. A second whole-object constructor pass here would re-run
	// the per-item model on bytes the loop just validated.
	var out ethapi.AccessListResult
	out.AccessList = &items
	if resolved.errorText != nil {
		text, err := ethapi.NewText(resolved.errorText)
		if err != nil {
			return nil, fmt.Errorf("accessList: error must be a string")
		}
		out.Error = &text
	}
	if resolved.gasUsed != nil {
		gas, err := ethapi.NewQuantity(resolved.gasUsed)
		if err != nil {
			return nil, fmt.Errorf("accessList: gasUsed: %w", err)
		}
		out.GasUsed = &gas
	}
	return ethcodec.MarshalNoEscape(out)
}

// checkItems validates the list one item at a time, in document order, with
// the item struct's own decoding semantics — the order and classifications
// the encode stage has always reported:
//
//  1. the item decodes into a struct whose storageKeys member is an array
//     type, so a storageKeys value of any other JSON type — under either
//     spelling, in either alias order — is a decode error reported as a
//     malformed item shape, before any presence or value check;
//  2. address presence, then storageKeys presence (null counts as missing);
//  3. the generated model validates the item's values: address first, then
//     storage keys.
//
// Each item finishes before the next one starts, so an earlier item's value
// error surfaces before a later item's missing member.
func checkItems(accessList json.RawMessage) ([]ethapi.AccessListItem, error) {
	var rows []json.RawMessage
	if err := json.Unmarshal(accessList, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("accessList: expected an array")
	}
	items := make([]ethapi.AccessListItem, len(rows))
	for i, row := range rows {
		var shape struct {
			Address     json.RawMessage   `json:"address"`
			StorageKeys []json.RawMessage `json:"storageKeys"`
		}
		if err := json.Unmarshal(row, &shape); err != nil {
			return nil, fmt.Errorf("accessList[%d]: expected an object", i)
		}
		if !ethcodec.PresentNonNull(shape.Address) {
			return nil, fmt.Errorf("accessList[%d]: missing address", i)
		}
		if shape.StorageKeys == nil {
			return nil, fmt.Errorf("accessList[%d]: missing storageKeys", i)
		}
		item, err := ethapi.NewAccessListItem(row)
		if err != nil {
			return nil, fmt.Errorf("accessList[%d]: %w", i, err)
		}
		items[i] = item
	}
	return items, nil
}

// protectedMembers is the resolved view of the members the comparison mask
// retains. Absent and null members are nil.
type protectedMembers struct {
	accessList json.RawMessage
	errorText  json.RawMessage
	gasUsed    json.RawMessage
}

// resolveProtectedMembers uses encoding/json's struct matching semantics for
// the three protected members. Absent and null members are nil.
func resolveProtectedMembers(raw json.RawMessage) (protectedMembers, error) {
	var fields struct {
		AccessList json.RawMessage `json:"accessList"`
		Error      json.RawMessage `json:"error"`
		GasUsed    json.RawMessage `json:"gasUsed"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return protectedMembers{}, err
	}
	resolved := protectedMembers{
		accessList: fields.AccessList,
		errorText:  fields.Error,
		gasUsed:    fields.GasUsed,
	}
	if ethcodec.IsJSONNull(resolved.accessList) {
		resolved.accessList = nil
	}
	if ethcodec.IsJSONNull(resolved.errorText) {
		resolved.errorText = nil
	}
	if ethcodec.IsJSONNull(resolved.gasUsed) {
		resolved.gasUsed = nil
	}
	return resolved, nil
}
