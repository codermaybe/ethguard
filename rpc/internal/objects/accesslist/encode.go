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
	resolved, faithful, err := resolveProtectedMembers(trimmed)
	if err != nil {
		return nil, fmt.Errorf("accessList: malformed response")
	}
	if resolved.accessList == nil {
		return nil, fmt.Errorf("accessList: missing accessList")
	}
	if err := checkItemPresence(resolved.accessList); err != nil {
		return nil, err
	}
	input := trimmed
	if !faithful {
		// Re-render only the resolved members so vendor members and resolved
		// nulls cannot leak into the generated model's view of the object.
		var out bytes.Buffer
		out.WriteByte('{')
		out.WriteString(`"accessList":`)
		out.Write(resolved.accessList)
		if resolved.errorText != nil {
			out.WriteString(`,"error":`)
			out.Write(resolved.errorText)
		}
		if resolved.gasUsed != nil {
			out.WriteString(`,"gasUsed":`)
			out.Write(resolved.gasUsed)
		}
		out.WriteByte('}')
		input = out.Bytes()
	}
	result, err := ethapi.NewAccessListResult(input)
	if err != nil {
		return nil, fmt.Errorf("accessList: %w", err)
	}
	return ethcodec.MarshalNoEscape(result)
}

// protectedMembers is the resolved view of the members the comparison mask
// retains. Absent and null members are nil.
type protectedMembers struct {
	accessList json.RawMessage
	errorText  json.RawMessage
	gasUsed    json.RawMessage
}

// resolveProtectedMembers walks the raw object in document order and resolves
// the three protected members with encoding/json's own matching semantics: a
// key matches its member case-insensitively, the last occurrence wins, and
// unknown members are ignored. A resolved null folds to absence. faithful
// reports whether the document already spells every resolved member exactly
// once, canonically, with no vendor members or nulls — so the constructor can
// consume the raw bytes unchanged.
func resolveProtectedMembers(raw json.RawMessage) (protectedMembers, bool, error) {
	var resolved protectedMembers
	seen := map[string]bool{}
	faithful := true
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
			faithful = false // a vendor member the mask drops
			continue
		}
		if seen[name] || ethcodec.IsJSONNull(value) {
			faithful = false
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
	return resolved, faithful, nil
}

// checkItemPresence runs the item-level presence rules the encode stage has
// always reported, in their original order: the item must be an object, both
// members must be present (null counts as missing), and only then do value
// errors surface. The generated constructor keeps every value check; this
// pre-check only pins which member a partially broken item names first.
func checkItemPresence(accessList json.RawMessage) error {
	var rows []json.RawMessage
	if err := json.Unmarshal(accessList, &rows); err != nil || rows == nil {
		return fmt.Errorf("accessList: expected an array")
	}
	for i, row := range rows {
		if err := itemPresence(row); err != nil {
			return fmt.Errorf("accessList[%d]: %w", i, err)
		}
	}
	return nil
}

func itemPresence(row json.RawMessage) error {
	var item struct {
		Address     json.RawMessage `json:"address"`
		StorageKeys json.RawMessage `json:"storageKeys"`
	}
	if err := json.Unmarshal(row, &item); err != nil {
		return fmt.Errorf("expected an object")
	}
	if !ethcodec.PresentNonNull(item.Address) {
		return fmt.Errorf("missing address")
	}
	if !ethcodec.PresentNonNull(item.StorageKeys) {
		return fmt.Errorf("missing storageKeys")
	}
	return nil
}
