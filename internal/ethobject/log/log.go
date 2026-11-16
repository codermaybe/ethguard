package log

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

// requiredFields is the shared full-log contract: every member must be
// present and non-null, in this order, before any field shape is checked.
var requiredFields = [...]string{"address", "topics", "data", "blockNumber", "transactionHash", "transactionIndex", "blockHash", "logIndex", "removed"}

// Validate checks the required fields and wire shapes of one log object.
func Validate(value json.RawMessage) error {
	_, err := parse(value)
	return err
}

// parse validates one log object and returns its canonical projection in a
// single pass, so Validate and EncodeOne accept and reject identical payloads
// with identical messages and no caller re-walks the same bytes twice.
func parse(value json.RawMessage) (CanonicalLog, error) {
	if err := jsonsafe.Validate(value); err != nil {
		return CanonicalLog{}, fmt.Errorf("log JSON: %w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(value, &obj); err != nil || obj == nil {
		return CanonicalLog{}, fmt.Errorf("expected an object")
	}
	for _, field := range requiredFields {
		if raw, ok := obj[field]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return CanonicalLog{}, fmt.Errorf("missing %s", field)
		}
	}
	var out CanonicalLog
	var err error
	if out.Address, err = ethcodec.ValidateWireFixedHex(obj["address"], 40, "address"); err != nil {
		return CanonicalLog{}, fmt.Errorf("address: %w", err)
	}
	var topics []json.RawMessage
	if err := json.Unmarshal(obj["topics"], &topics); err != nil || topics == nil {
		return CanonicalLog{}, fmt.Errorf("topics must be an array")
	}
	out.Topics = make([]string, len(topics))
	for i, topic := range topics {
		if out.Topics[i], err = ethcodec.ValidateWireFixedHex(topic, 64, "hash"); err != nil {
			return CanonicalLog{}, fmt.Errorf("topics[%d]: %w", i, err)
		}
	}
	if out.Data, err = ethcodec.NormalizeWireData(obj["data"]); err != nil {
		return CanonicalLog{}, fmt.Errorf("data: %w", err)
	}
	for _, field := range [...]struct {
		name   string
		target *string
	}{
		{"blockNumber", &out.BlockNumber},
		{"transactionIndex", &out.TransactionIndex},
		{"logIndex", &out.LogIndex},
	} {
		if *field.target, err = ethcodec.CanonicalQuantity(obj[field.name]); err != nil {
			return CanonicalLog{}, fmt.Errorf("%s: %w", field.name, err)
		}
	}
	for _, field := range [...]struct {
		name   string
		target *string
	}{
		{"transactionHash", &out.TransactionHash},
		{"blockHash", &out.BlockHash},
	} {
		if *field.target, err = ethcodec.ValidateWireFixedHex(obj[field.name], 64, "hash"); err != nil {
			return CanonicalLog{}, fmt.Errorf("%s: %w", field.name, err)
		}
	}
	if err := json.Unmarshal(obj["removed"], &out.Removed); err != nil {
		return CanonicalLog{}, fmt.Errorf("removed must be boolean")
	}
	if ethcodec.PresentNonNull(obj["blockTimestamp"]) {
		timestamp, err := ethcodec.CanonicalQuantity(obj["blockTimestamp"])
		if err != nil {
			return CanonicalLog{}, fmt.Errorf("blockTimestamp: %w", err)
		}
		out.BlockTimestamp = &timestamp
	}
	return out, nil
}

// Encode checks one eth_getLogs result and returns the canonical JSON of the
// protected fields. The result is an array. Each log keeps only the fields the
// comparison mask retains: address, topics, data, blockNumber, transactionHash,
// transactionIndex, blockHash, logIndex, removed, and blockTimestamp when it
// is present. A field the mask drops does not enter the value. Address and
// hashes are lowercase. Quantities are canonical QUANTITY. data keeps leading
// zeros. removed is a boolean. An empty array is []. JSON is compact, HTML
// escaping is off, and field order is the encoder struct order. Log order is
// unchanged.
//
// Admission is section 4.2 step 1 of return-value-design.md: a field is in the
// protected domain because it is in the comparison key. parse still rejects a
// missing required field. blockTimestamp is omitted when absent and is not
// filled with null.
func Encode(value json.RawMessage) (json.RawMessage, error) {
	if err := jsonsafe.Validate(value); err != nil {
		return nil, fmt.Errorf("log JSON: %w", err)
	}
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("logs: expected an array")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(trimmed, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("logs: expected an array")
	}
	out := make([]CanonicalLog, len(rows))
	for i, row := range rows {
		encoded, err := EncodeOne(row)
		if err != nil {
			return nil, fmt.Errorf("logs[%d]: %w", i, err)
		}
		out[i] = encoded
	}
	return ethcodec.MarshalNoEscape(out)
}

// EncodeOne checks and canonicalizes one log object. It is the shared per-log
// encoder for eth_getLogs and for the log embedded in a transaction receipt:
// requiredness and wire shapes come from the same parse as Validate, so both
// callers accept and reject the same payloads with the same messages. The
// returned canonical field order is the CanonicalLog struct order.
func EncodeOne(value json.RawMessage) (CanonicalLog, error) {
	return parse(value)
}

// CanonicalLog is the protected log object shared by eth_getLogs and the log
// embedded in a transaction receipt. Field order is fixed; blockTimestamp is
// omitted when the source did not provide it.
type CanonicalLog struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	BlockNumber      string   `json:"blockNumber"`
	TransactionHash  string   `json:"transactionHash"`
	TransactionIndex string   `json:"transactionIndex"`
	BlockHash        string   `json:"blockHash"`
	LogIndex         string   `json:"logIndex"`
	Removed          bool     `json:"removed"`
	BlockTimestamp   *string  `json:"blockTimestamp,omitempty"`
}
