package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"

	"ethguard/rpc"
)

// This file is the Log object slice: Log validation (requiredness) and the
// logs projection.
//
// Ownership split per R2: the generated mask (shared with the receipt slice)
// owns the Log WHITELIST — which fields participate in comparison; this file
// owns Log REQUIREDNESS — which fields must be present for a log to vote.
// Requiredness stays hand-written because the spec union spans variants a
// single response never carries; the whitelist stays generated because it
// must track the standard field-for-field.

func validateLogRequest(req rpc.Request) error {
	if len(req.Params) != 1 {
		return fmt.Errorf("%w: eth_getLogs expects one filter", rpc.ErrInvalidRequest)
	}
	filter, ok := req.Params[0].(map[string]any)
	if !ok {
		// Keep the policy usable by the generic anchor tests and callers that
		// perform their own typed decoding. The adapter supplies an object and
		// receives the full filter validation below.
		return nil
	}
	if _, hasHash := filter["blockHash"]; hasHash {
		if _, hasFrom := filter["fromBlock"]; hasFrom {
			return fmt.Errorf("%w: eth_getLogs blockHash conflicts with fromBlock", rpc.ErrInvalidRequest)
		}
		if _, hasTo := filter["toBlock"]; hasTo {
			return fmt.Errorf("%w: eth_getLogs blockHash conflicts with toBlock", rpc.ErrInvalidRequest)
		}
	}
	return nil
}

// normalizeLogs validates every log row, then reduces the array to its pruned
// comparison key. Ordering and duplicates stay significant; hexadecimal casing
// and vendor extension fields do not create false disagreement.
func normalizeLogs(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	var rawLogs []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawLogs); err != nil || string(trimmed) == "null" {
		return "", fmt.Errorf("logs: expected an array")
	}
	for i, raw := range rawLogs {
		if err := validateLogObject(raw); err != nil {
			return "", fmt.Errorf("logs[%d]: %w", i, err)
		}
	}
	return PruneMethod(MethodGetLogs, trimmed)
}

// validateLogsResponse is intentionally stricter than types.Log's JSON
// decoder.  The decoder assigns zero values to omitted fields, which would
// allow an incomplete response to become a valid vote.
func validateLogsResponse(req rpc.Request, value json.RawMessage) error {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("logs: expected an array")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(trimmed, &rows); err != nil || rows == nil {
		return fmt.Errorf("logs: expected an array")
	}
	for i, row := range rows {
		if err := validateLogObject(row); err != nil {
			return fmt.Errorf("logs[%d]: %w", i, err)
		}
	}
	return validateLogRequestBinding(req, rows)
}

func validateLogObject(value json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(value, &obj); err != nil || obj == nil {
		return fmt.Errorf("expected an object")
	}
	for _, field := range []string{"address", "topics", "data", "blockNumber", "transactionHash", "transactionIndex", "blockHash", "logIndex", "removed"} {
		if raw, ok := obj[field]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("missing %s", field)
		}
	}
	if err := strictAddress(obj["address"]); err != nil {
		return fmt.Errorf("address: %w", err)
	}
	var topics []json.RawMessage
	if err := json.Unmarshal(obj["topics"], &topics); err != nil || topics == nil {
		return fmt.Errorf("topics must be an array")
	}
	for i, topic := range topics {
		if err := strictHash(topic); err != nil {
			return fmt.Errorf("topics[%d]: %w", i, err)
		}
	}
	if err := strictData(obj["data"]); err != nil {
		return fmt.Errorf("data: %w", err)
	}
	for _, field := range []string{"blockNumber", "transactionIndex", "logIndex"} {
		if err := strictQuantity(obj[field]); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	for _, field := range []string{"transactionHash", "blockHash"} {
		if err := strictHash(obj[field]); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	var removed bool
	if err := json.Unmarshal(obj["removed"], &removed); err != nil {
		return fmt.Errorf("removed must be boolean")
	}
	return nil
}

func validateLogRequestBinding(req rpc.Request, rows []json.RawMessage) error {
	if len(req.Params) != 1 {
		return nil
	}
	filter, ok := req.Params[0].(map[string]any)
	if !ok {
		return nil
	}
	if requested, ok := filter["blockHash"]; ok {
		want, ok := valueHash(requested)
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
		bound, err := requestQuantity(value, false)
		if err != nil {
			return fmt.Errorf("fromBlock is invalid: %w", err)
		}
		from = &bound
	}
	if value, ok := filter["toBlock"]; ok {
		bound, err := requestQuantity(value, false)
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
		block, err := normalizeWireQuantity(obj["blockNumber"])
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

func strictAddress(value json.RawMessage) error {
	_, err := validateWireFixedHex(value, 40, "address")
	return err
}

func strictHash(value json.RawMessage) error {
	_, err := validateWireFixedHex(value, 64, "hash")
	if err != nil {
		return err
	}
	return nil
}

func strictData(value json.RawMessage) error {
	_, err := normalizeWireData(value)
	return err
}

func strictQuantity(value json.RawMessage) error {
	_, err := normalizeWireQuantity(value)
	return err
}

func valueHash(value any) (common.Hash, bool) {
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
