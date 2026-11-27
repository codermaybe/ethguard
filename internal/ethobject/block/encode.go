package block

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	objectheader "github.com/codermaybe/ethguard/internal/ethobject/header"
	"github.com/codermaybe/ethguard/internal/ethobject/hexcompat"
	"github.com/codermaybe/ethguard/rpc"
)

// Encode returns a source-local canonical official Block, preserving size
// and each embedded transaction's declared sender and inclusion context.
// Those declarations participate in the complete comparison value. They are
// not claims of sender recovery or independently proven inclusion. AdmitBlock
// checks payload commitments, body relations and method request binding.
// Internal number/hash anchor reads retain their separate minimal shape.
func Encode(raw json.RawMessage) (json.RawMessage, error) {
	if ethcodec.IsJSONNull(raw) {
		return json.RawMessage("null"), nil
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil || source == nil {
		return nil, fmt.Errorf("block encode: expected object")
	}
	if !hasBlockHead(source) {
		return nil, fmt.Errorf("block head: missing number or hash")
	}
	projected, err := objectheader.Projection(raw)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(projected, &fields); err != nil {
		return nil, err
	}
	// Reference reads may carry provider extensions, but any declared official
	// header/body member requires the complete corresponding object checks.
	reference := objectheader.ReferenceOnly(source)
	for _, name := range []string{"size", "transactions", "uncles", "withdrawals"} {
		if _, present := source[name]; present {
			reference = false
		}
	}
	if reference {
		return encodeHeaderOnly(source)
	}
	if _, err := objectheader.Decode(raw); err != nil {
		return nil, fmt.Errorf("block encode: %w", err)
	}
	if _, txs := source["transactions"]; !txs {
		if _, uncles := source["uncles"]; !uncles {
			return objectheader.Normalize(raw)
		}
	}
	body, err := hexcompat.BlockBody(raw)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	candidate, err := ethcodec.MarshalNoEscape(fields)
	if err != nil {
		return nil, err
	}
	out, err := ethapi.NewBlock(candidate)
	if err != nil {
		return nil, fmt.Errorf("block encode: %w", err)
	}
	return ethcodec.MarshalNoEscape(out)
}

func hasBlockHead(source map[string]json.RawMessage) bool {
	_, number := source["number"]
	_, hash := source["hash"]
	return number && hash
}

func encodeHeaderOnly(source map[string]json.RawMessage) (json.RawMessage, error) {
	var number, hash string
	if err := json.Unmarshal(source["number"], &number); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(source["hash"], &hash); err != nil {
		return nil, err
	}
	if number == "" || len(hash) < 3 {
		return nil, fmt.Errorf("block head: missing number or hash")
	}
	height, err := rpc.ParseHexQuantity(number)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"hash": strings.ToLower(hash), "number": fmt.Sprintf("0x%x", height)})
}
