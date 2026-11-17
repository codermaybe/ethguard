package accesslist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
	resolved, _, err := resolveProtectedMembers(trimmed)
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

// resolveProtectedMembers resolves the three protected members with
// encoding/json's own matching semantics: a key matches its member
// case-insensitively, the last occurrence wins, and unknown members are
// ignored. A resolved null folds to absence. faithful reports whether the
// document's exact-key members already match what the generated model's own
// decode would resolve — canonically spelled, non-null, and with no
// case-variant alias riding alongside — so the constructor can consume the
// raw bytes unchanged. Vendor members never break that contract: the model's
// decode ignores them exactly as it would the raw object.
func resolveProtectedMembers(raw json.RawMessage) (protectedMembers, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return protectedMembers{}, false, err
	}
	faithful := true
	for key, value := range fields {
		switch key {
		case "accessList", "error", "gasUsed":
			if ethcodec.IsJSONNull(value) {
				faithful = false // a null member must fold to absence
			}
		default:
			if strings.EqualFold(key, "accessList") || strings.EqualFold(key, "error") || strings.EqualFold(key, "gasUsed") {
				faithful = false // a case-variant alias rides alongside
			}
		}
	}
	if faithful {
		return protectedMembers{
			accessList: fields["accessList"],
			errorText:  fields["error"],
			gasUsed:    fields["gasUsed"],
		}, true, nil
	}
	return resolveByDocumentOrder(raw)
}

// resolveByDocumentOrder is the slow path for documents whose member spelling
// needs re-resolution: a decoder walks the object in document order so a
// case-variant alias resolves with the same last-occurrence-wins precedence a
// struct decode applies, and vendor members stay out of the resolution.
func resolveByDocumentOrder(raw json.RawMessage) (protectedMembers, bool, error) {
	var resolved protectedMembers
	seen := map[string]bool{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := decoder.Token(); err != nil {
		return resolved, false, err
	} else if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return resolved, false, fmt.Errorf("expected an object")
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return resolved, false, err
		}
		key, _ := keyToken.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return resolved, false, err
		}
		name := ""
		switch {
		case strings.EqualFold(key, "accessList"):
			name = "accessList"
		case strings.EqualFold(key, "error"):
			name = "errorText"
		case strings.EqualFold(key, "gasUsed"):
			name = "gasUsed"
		}
		if name == "" {
			continue // a vendor member the mask drops
		}
		seen[name] = true
		switch name {
		case "accessList":
			resolved.accessList = value
		case "errorText":
			resolved.errorText = value
		case "gasUsed":
			resolved.gasUsed = value
		}
	}
	if _, err := decoder.Token(); err != nil { // consume '}'
		return resolved, false, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return resolved, false, fmt.Errorf("multiple JSON values")
	}
	if resolved.accessList != nil && ethcodec.IsJSONNull(resolved.accessList) {
		resolved.accessList = nil
	}
	if resolved.errorText != nil && ethcodec.IsJSONNull(resolved.errorText) {
		resolved.errorText = nil
	}
	if resolved.gasUsed != nil && ethcodec.IsJSONNull(resolved.gasUsed) {
		resolved.gasUsed = nil
	}
	return resolved, false, nil
}

