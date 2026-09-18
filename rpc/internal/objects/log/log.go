// Package log contains the shared Ethereum log object validation rules.
package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"

	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// Validate checks the required fields and wire shapes of one log object.
func Validate(value json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(value, &obj); err != nil || obj == nil {
		return fmt.Errorf("expected an object")
	}
	for _, field := range []string{"address", "topics", "data", "blockNumber", "transactionHash", "transactionIndex", "blockHash", "logIndex", "removed"} {
		if raw, ok := obj[field]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("missing %s", field)
		}
	}
	if _, err := wire.ValidateWireFixedHex(obj["address"], 40, "address"); err != nil {
		return fmt.Errorf("address: %w", err)
	}
	var topics []json.RawMessage
	if err := json.Unmarshal(obj["topics"], &topics); err != nil || topics == nil {
		return fmt.Errorf("topics must be an array")
	}
	for i, topic := range topics {
		if _, err := wire.ValidateWireFixedHex(topic, 64, "hash"); err != nil {
			return fmt.Errorf("topics[%d]: %w", i, err)
		}
	}
	if _, err := wire.NormalizeWireData(obj["data"]); err != nil {
		return fmt.Errorf("data: %w", err)
	}
	for _, field := range []string{"blockNumber", "transactionIndex", "logIndex"} {
		if _, err := wire.NormalizeWireQuantity(obj[field]); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	for _, field := range []string{"transactionHash", "blockHash"} {
		if _, err := wire.ValidateWireFixedHex(obj[field], 64, "hash"); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	var removed bool
	if err := json.Unmarshal(obj["removed"], &removed); err != nil {
		return fmt.Errorf("removed must be boolean")
	}
	return nil
}

// ValidateResponse validates a getLogs response and binds it to its request.
func ValidateResponse(req rpc.Request, value json.RawMessage) error {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("logs: expected an array")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(trimmed, &rows); err != nil || rows == nil {
		return fmt.Errorf("logs: expected an array")
	}
	for i, row := range rows {
		if err := Validate(row); err != nil {
			return fmt.Errorf("logs[%d]: %w", i, err)
		}
	}
	return ValidateRequestBinding(req, rows)
}

// ValidateRequestBinding ensures logs match the requested hash or block range.
func ValidateRequestBinding(req rpc.Request, rows []json.RawMessage) error {
	if len(req.Params) != 1 {
		return nil
	}
	filter, ok := req.Params[0].(map[string]any)
	if !ok {
		return nil
	}
	if requested, ok := filter["blockHash"]; ok {
		want, ok := ValueHash(requested)
		if !ok {
			return fmt.Errorf("blockHash filter must be a 32-byte hash")
		}
		for i, row := range rows {
			var obj map[string]json.RawMessage
			_ = json.Unmarshal(row, &obj)
			var got common.Hash
			if err := json.Unmarshal(obj["blockHash"], &got); err != nil || got != want {
				return fmt.Errorf("log %d blockHash does not match requested blockHash", i)
			}
		}
		return nil
	}
	var from, to *uint64
	if value, ok := filter["fromBlock"]; ok {
		bound, err := requestQuantity(value)
		if err != nil {
			return fmt.Errorf("fromBlock is invalid: %w", err)
		}
		from = &bound
	}
	if value, ok := filter["toBlock"]; ok {
		bound, err := requestQuantity(value)
		if err != nil {
			return fmt.Errorf("toBlock is invalid: %w", err)
		}
		to = &bound
	}
	if from != nil && to != nil && *from > *to {
		return fmt.Errorf("fromBlock is greater than toBlock")
	}
	for i, row := range rows {
		if from == nil && to == nil {
			break
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(row, &obj); err != nil {
			return fmt.Errorf("log %d is malformed: %w", i, err)
		}
		block, err := wire.NormalizeWireQuantity(obj["blockNumber"])
		if err != nil {
			return fmt.Errorf("log %d blockNumber: %w", i, err)
		}
		value, ok := new(big.Int).SetString(block, 10)
		if !ok || !value.IsUint64() {
			return fmt.Errorf("log %d blockNumber is out of range", i)
		}
		height := value.Uint64()
		if from != nil && height < *from || to != nil && height > *to {
			return fmt.Errorf("log %d blockNumber is outside requested range", i)
		}
	}
	return nil
}

func requestQuantity(value any) (uint64, error) {
	s, ok := value.(string)
	if !ok || len(s) < 3 || s[:2] != "0x" {
		return 0, fmt.Errorf("invalid quantity")
	}
	n, ok := new(big.Int).SetString(s[2:], 16)
	if !ok || n.Sign() < 0 || !n.IsUint64() {
		return 0, fmt.Errorf("invalid quantity")
	}
	return n.Uint64(), nil
}

// ValueHash parses a typed or string hash filter.
func ValueHash(value any) (common.Hash, bool) {
	switch v := value.(type) {
	case common.Hash:
		return v, true
	case string:
		if len(v) != 66 || !common.IsHexHash(v) {
			return common.Hash{}, false
		}
		return common.HexToHash(v), true
	default:
		return common.Hash{}, false
	}
}
