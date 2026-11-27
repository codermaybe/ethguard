// Package hexcompat owns the finite hexadecimal input compatibility used by
// block, transaction and simulation consumers before generated validation.
package hexcompat

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

// A field tree describes only the known representation paths of the named
// generated models. It performs no schema validation and never traverses
// maps, additional properties or free text. Wrong shapes remain unchanged
// for the generated constructor to reject.
type fieldTree struct {
	hex        bool
	hexByte    bool
	project    bool
	fields     map[string]*fieldTree
	items      *fieldTree
	txBranches map[string]*fieldTree
}

var hexTypes = map[reflect.Type]bool{
	reflect.TypeOf(ethapi.Address("")):     true,
	reflect.TypeOf(ethapi.Hash("")):        true,
	reflect.TypeOf(ethapi.Data("")):        true,
	reflect.TypeOf(ethapi.Quantity("")):    true,
	reflect.TypeOf(ethapi.Quantity64("")):  true,
	reflect.TypeOf(ethapi.Quantity256("")): true,
	reflect.TypeOf(ethapi.HexByte("")):     true,
	reflect.TypeOf(ethapi.Bloom("")):       true,
	reflect.TypeOf(ethapi.BlockNonce("")):  true,
	reflect.TypeOf(ethapi.StorageKey("")):  true,
}

// These closed result objects historically entered consumers through a
// declared-field projection. No missing fields or malformed values are
// supplied by the projection. Open objects retain extras until their usual
// generated result projection; the original source remains evidence.
var projectedObjects = map[reflect.Type]bool{
	reflect.TypeOf(ethapi.AccessListItem{}): true,
	reflect.TypeOf(ethapi.Withdrawal{}):     true,
	reflect.TypeOf(ethapi.Log{}):            true,
}

// Selecting the actual transaction variant matters: v is a QUANTITY in a
// legacy transaction and a HexByte in typed transactions. A merged field
// tree would incorrectly repair legacy v="0x01" into a valid QUANTITY.
var transactionTypes = map[string]string{
	"SignedLegacyTransaction": "0x0",
	"Signed2930Transaction":   "0x1",
	"Signed1559Transaction":   "0x2",
	"Signed4844Transaction":   "0x3",
	"Signed7702Transaction":   "0x4",
}

var (
	transactionFields = describe(reflect.TypeOf(ethapi.Transaction{}))
	simulateFields    = describe(reflect.TypeOf([]ethapi.SimulateBlock{}))
	bodyFields        = func() *fieldTree {
		block := describe(reflect.TypeOf(ethapi.Block{}))
		body := &fieldTree{project: true, fields: make(map[string]*fieldTree)}
		for _, name := range []string{"size", "transactions", "uncles", "withdrawals"} {
			field, exists := block.fields[name]
			if !exists {
				panic("hexcompat: missing block body field " + name)
			}
			body.fields[name] = field
		}
		return body
	}()
)

// BlockBody selects the four declared body members and normalizes only
// their known hex fields. Header projection and commitment checks stay with
// the caller. Both the RPC encoder and public raw decoder use this candidate.
func BlockBody(raw json.RawMessage) (json.RawMessage, error) {
	return prepare(raw, bodyFields)
}

// Transaction folds a query transaction's known hex fields. Its generated
// union still owns branch validation and nullable inclusion context; this
// function supplies no pending defaults and does not verify a signature.
func Transaction(raw json.RawMessage) (json.RawMessage, error) {
	return prepare(raw, transactionFields)
}

// SimulateResult folds only known result hex fields. In particular, error
// messages, numbers and unknown extensions keep their decoded meaning.
// Simulation request inputs retain their separate strict schema contract.
func SimulateResult(raw json.RawMessage) (json.RawMessage, error) {
	return prepare(raw, simulateFields)
}

func prepare(raw json.RawMessage, tree *fieldTree) (json.RawMessage, error) {
	if err := jsonsafe.Validate(raw); err != nil {
		return nil, err
	}
	return fold(raw, tree)
}

func describe(t reflect.Type) *fieldTree {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if hexTypes[t] {
		return &fieldTree{hex: true, hexByte: t == reflect.TypeOf(ethapi.HexByte(""))}
	}
	switch t.Kind() {
	case reflect.Slice:
		if t == reflect.TypeOf(json.RawMessage{}) {
			return nil
		}
		return &fieldTree{items: describe(t.Elem())}
	case reflect.Struct:
		if t == reflect.TypeOf(ethapi.Transaction{}) || t == reflect.TypeOf(ethapi.BlockTransaction{}) {
			if t.NumField() != len(transactionTypes) {
				panic("hexcompat: changed transaction branch inventory")
			}
			out := &fieldTree{txBranches: make(map[string]*fieldTree)}
			for i := 0; i < t.NumField(); i++ {
				field := t.Field(i)
				kind, exists := transactionTypes[field.Name]
				if !exists {
					panic("hexcompat: unreviewed transaction branch " + field.Name)
				}
				out.txBranches[kind] = describe(field.Type)
			}
			return out
		}
		out := &fieldTree{project: projectedObjects[t], fields: make(map[string]*fieldTree)}
		var union *fieldTree
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				out.fields[name] = describe(field.Type)
			} else if field.Type.Kind() == reflect.Pointer {
				union = merge(union, describe(field.Type))
			}
		}
		if len(out.fields) == 0 {
			return union
		}
		return out
	default:
		return nil
	}
}

func merge(a, b *fieldTree) *fieldTree {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	out := &fieldTree{hex: a.hex || b.hex, hexByte: a.hexByte || b.hexByte, project: a.project && b.project,
		fields: make(map[string]*fieldTree), items: merge(a.items, b.items), txBranches: a.txBranches}
	if out.txBranches == nil {
		out.txBranches = b.txBranches
	}
	for name, child := range a.fields {
		out.fields[name] = child
	}
	for name, child := range b.fields {
		out.fields[name] = merge(out.fields[name], child)
	}
	return out
}

func fold(raw json.RawMessage, tree *fieldTree) (json.RawMessage, error) {
	if tree == nil {
		return raw, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, nil
	}
	switch trimmed[0] {
	case '"':
		if !tree.hex {
			return raw, nil
		}
		value := ethcodec.FoldHexSpelling(raw)
		if tree.hexByte {
			if checked, err := ethapi.NewHexByte(value); err == nil {
				return checked.MarshalJSON()
			}
		}
		return value, nil
	case '[':
		if tree.items == nil {
			return raw, nil
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, err
		}
		changed := false
		for i := range rows {
			value, err := fold(rows[i], tree.items)
			if err != nil {
				return nil, err
			}
			changed = changed || !bytes.Equal(rows[i], value)
			rows[i] = value
		}
		if !changed {
			return raw, nil
		}
		return ethcodec.MarshalNoEscape(rows)
	case '{':
		var members map[string]json.RawMessage
		if err := json.Unmarshal(raw, &members); err != nil {
			return nil, err
		}
		if tree.txBranches != nil {
			var kind string
			if err := json.Unmarshal(ethcodec.FoldHexSpelling(members["type"]), &kind); err != nil {
				return raw, nil
			}
			branch, exists := tree.txBranches[kind]
			if !exists {
				return raw, nil
			}
			tree = branch
		}
		if tree.fields == nil {
			return raw, nil
		}
		out := members
		if tree.project {
			out = make(map[string]json.RawMessage, len(tree.fields))
		}
		changed := false
		for name, child := range tree.fields {
			if member, present := members[name]; present {
				value, err := fold(member, child)
				if err != nil {
					return nil, err
				}
				changed = changed || !bytes.Equal(member, value)
				out[name] = value
			}
		}
		if !changed && len(out) == len(members) {
			return raw, nil
		}
		return ethcodec.MarshalNoEscape(out)
	default:
		return raw, nil
	}
}
