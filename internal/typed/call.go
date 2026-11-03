package typed

// The call result's typed projection: a corroborated eth_call decision
// separates return data from a corroborated revert envelope. The adapter
// maps the neutral Revert onto its own error type, keeping this package free
// of adapter error semantics.

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/rpc"
)

// Revert is one corroborated execution revert's typed carrier: the winning
// provider's message and the data field in both its raw JSON form and, when
// the provider spelled it as hex, its decoded bytes.
type Revert struct {
	Message string
	// Data is set only when the provider data is a hex string. Missing and
	// JSON null leave it nil; empty bytes ("0x") are a non-nil empty slice.
	Data []byte
	// DataRaw is the original JSON data value. Structured data stays here
	// and is not forced into hex. Missing data leaves DataRaw empty; JSON
	// null is retained as the bytes `null`.
	DataRaw json.RawMessage
}

// DecodeCallResult separates a corroborated revert envelope from return
// data. A revert envelope (a JSON object carrying code or message) returns
// a *Revert with nil bytes; a hex data payload returns the decoded bytes;
// anything else is a malformed-response failure.
func DecodeCallResult(raw json.RawMessage) ([]byte, *Revert, error) {
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && (envelope.Code != 0 || envelope.Message != "") {
		revert := &Revert{Message: envelope.Message, DataRaw: append(json.RawMessage(nil), envelope.Data...)}
		var hexData string
		if len(envelope.Data) > 0 && json.Unmarshal(envelope.Data, &hexData) == nil {
			if data, err := hexutil.Decode(hexData); err == nil {
				revert.Data = data
			}
		}
		return nil, revert, nil
	}
	if string(raw) == "null" {
		return nil, nil, fmt.Errorf("%w: null call result", rpc.ErrMalformedResponse)
	}
	var out hexutil.Bytes
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("%w: decode call result: %v", rpc.ErrMalformedResponse, err)
	}
	return out, nil, nil
}

// DecodeCallDecision projects one corroborated call decision: a decision
// carrying revert context projects the envelope from the revert's own value;
// a plain decision projects its value through DecodeCallResult.
func DecodeCallDecision(decision rpc.Decision) ([]byte, *Revert, error) {
	if decision.Revert == nil {
		return DecodeCallResult(decision.Value)
	}
	revert := &Revert{Message: decision.Revert.Message}
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(decision.Value, &payload); err == nil && len(payload.Data) > 0 {
		revert.DataRaw = append(json.RawMessage(nil), payload.Data...)
		var hexData string
		if json.Unmarshal(payload.Data, &hexData) == nil {
			if data, err := hexutil.Decode(hexData); err == nil {
				revert.Data = data
			}
		}
	}
	return nil, revert, nil
}
