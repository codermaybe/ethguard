package log

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

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
// protected domain because it is in the comparison key. log.Validate still
// rejects a missing required field. blockTimestamp is omitted when absent and
// is not filled with null.
func Encode(value json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("logs: expected an array")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(trimmed, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("logs: expected an array")
	}
	out := make([]canonicalLog, len(rows))
	for i, row := range rows {
		encoded, err := encodeLog(row)
		if err != nil {
			return nil, fmt.Errorf("logs[%d]: %w", i, err)
		}
		out[i] = encoded
	}
	return wire.MarshalNoEscape(out)
}

func encodeLog(value json.RawMessage) (canonicalLog, error) {
	if err := Validate(value); err != nil {
		return canonicalLog{}, err
	}
	var raw struct {
		Address          json.RawMessage   `json:"address"`
		Topics           []json.RawMessage `json:"topics"`
		Data             json.RawMessage   `json:"data"`
		BlockNumber      json.RawMessage   `json:"blockNumber"`
		TransactionHash  json.RawMessage   `json:"transactionHash"`
		TransactionIndex json.RawMessage   `json:"transactionIndex"`
		BlockHash        json.RawMessage   `json:"blockHash"`
		LogIndex         json.RawMessage   `json:"logIndex"`
		Removed          json.RawMessage   `json:"removed"`
		BlockTimestamp   json.RawMessage   `json:"blockTimestamp"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return canonicalLog{}, fmt.Errorf("malformed response")
	}
	address, err := wire.ValidateWireFixedHex(raw.Address, 40, "address")
	if err != nil {
		return canonicalLog{}, fmt.Errorf("address: %w", err)
	}
	topics := make([]string, len(raw.Topics))
	for i, topic := range raw.Topics {
		topics[i], err = wire.ValidateWireFixedHex(topic, 64, "hash")
		if err != nil {
			return canonicalLog{}, fmt.Errorf("topics[%d]: %w", i, err)
		}
	}
	data, err := wire.NormalizeWireData(raw.Data)
	if err != nil {
		return canonicalLog{}, fmt.Errorf("data: %w", err)
	}
	blockNumber, err := wire.CanonicalQuantity(raw.BlockNumber)
	if err != nil {
		return canonicalLog{}, fmt.Errorf("blockNumber: %w", err)
	}
	transactionHash, err := wire.ValidateWireFixedHex(raw.TransactionHash, 64, "hash")
	if err != nil {
		return canonicalLog{}, fmt.Errorf("transactionHash: %w", err)
	}
	transactionIndex, err := wire.CanonicalQuantity(raw.TransactionIndex)
	if err != nil {
		return canonicalLog{}, fmt.Errorf("transactionIndex: %w", err)
	}
	blockHash, err := wire.ValidateWireFixedHex(raw.BlockHash, 64, "hash")
	if err != nil {
		return canonicalLog{}, fmt.Errorf("blockHash: %w", err)
	}
	logIndex, err := wire.CanonicalQuantity(raw.LogIndex)
	if err != nil {
		return canonicalLog{}, fmt.Errorf("logIndex: %w", err)
	}
	var removed bool
	if err := json.Unmarshal(raw.Removed, &removed); err != nil {
		return canonicalLog{}, fmt.Errorf("removed must be boolean")
	}
	var timestamp *string
	if len(bytes.TrimSpace(raw.BlockTimestamp)) != 0 && !bytes.Equal(bytes.TrimSpace(raw.BlockTimestamp), []byte("null")) {
		text, err := wire.CanonicalQuantity(raw.BlockTimestamp)
		if err != nil {
			return canonicalLog{}, fmt.Errorf("blockTimestamp: %w", err)
		}
		timestamp = &text
	}
	return canonicalLog{
		Address:          address,
		Topics:           topics,
		Data:             data,
		BlockNumber:      blockNumber,
		TransactionHash:  transactionHash,
		TransactionIndex: transactionIndex,
		BlockHash:        blockHash,
		LogIndex:         logIndex,
		Removed:          removed,
		BlockTimestamp:   timestamp,
	}, nil
}

// canonicalLog is the protected eth_getLogs object. Field order is fixed.
// blockTimestamp is omitted when the source did not provide it.
type canonicalLog struct {
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
