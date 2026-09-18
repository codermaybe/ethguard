// Package feehistory contains the shared response validation rules for
// eth_feeHistory. Request parameter validation remains owned by methods.
package feehistory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// ValidateRange binds a fee-history response to the requested block count.
func ValidateRange(req rpc.Request, value json.RawMessage) error {
	count, err := requestCount(req.Params)
	if err != nil || count == 0 {
		return fmt.Errorf("invalid block count")
	}
	var response struct {
		Oldest json.RawMessage   `json:"oldestBlock"`
		Gas    []json.RawMessage `json:"gasUsedRatio"`
	}
	if err := json.Unmarshal(value, &response); err != nil {
		return fmt.Errorf("malformed response")
	}
	if uint64(len(response.Gas)) != count {
		return fmt.Errorf("gasUsedRatio length %d does not match requested block count %d", len(response.Gas), count)
	}
	if _, err := wire.NormalizeWireQuantity(response.Oldest); err != nil {
		return fmt.Errorf("oldestBlock: %w", err)
	}
	return nil
}

// Normalize validates and canonicalizes a fee-history response for voting.
func Normalize(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || trimmed[0] != '{' || bytes.Equal(trimmed, []byte("null")) {
		return "", fmt.Errorf("feeHistory: expected an object")
	}
	var raw struct {
		Oldest json.RawMessage     `json:"oldestBlock"`
		Base   []json.RawMessage   `json:"baseFeePerGas"`
		Gas    []float64           `json:"gasUsedRatio"`
		Reward [][]json.RawMessage `json:"reward"`
	}
	if err := json.Unmarshal(value, &raw); err != nil || len(raw.Oldest) == 0 {
		return "", fmt.Errorf("feeHistory: malformed response")
	}
	oldest, err := wire.NormalizeWireQuantity(raw.Oldest)
	if err != nil || len(raw.Base) != len(raw.Gas)+1 {
		return "", fmt.Errorf("feeHistory: baseFeePerGas must have gasUsedRatio length plus one")
	}
	base := make([]string, len(raw.Base))
	for i, item := range raw.Base {
		base[i], err = wire.NormalizeWireQuantity(item)
		if err != nil {
			return "", fmt.Errorf("feeHistory: baseFeePerGas[%d]: %w", i, err)
		}
	}
	for i, ratio := range raw.Gas {
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
			return "", fmt.Errorf("feeHistory: gasUsedRatio[%d] out of range", i)
		}
	}
	if raw.Reward != nil && len(raw.Reward) != len(raw.Gas) {
		return "", fmt.Errorf("feeHistory: reward must have gasUsedRatio length")
	}
	reward := make([][]string, len(raw.Reward))
	rewardWidth := -1
	for i, row := range raw.Reward {
		if rewardWidth < 0 {
			rewardWidth = len(row)
		} else if len(row) != rewardWidth {
			return "", fmt.Errorf("feeHistory: reward rows must have equal length")
		}
		reward[i] = make([]string, len(row))
		for j, item := range row {
			reward[i][j], err = wire.NormalizeWireQuantity(item)
			if err != nil {
				return "", fmt.Errorf("feeHistory: reward[%d][%d]: %w", i, j, err)
			}
		}
	}
	projection := struct {
		Oldest string     `json:"oldestBlock"`
		Base   []string   `json:"baseFeePerGas"`
		Gas    []float64  `json:"gasUsedRatio"`
		Reward [][]string `json:"reward,omitempty"`
	}{oldest, base, raw.Gas, reward}
	encoded, err := json.Marshal(projection)
	return string(encoded), err
}

func requestCount(params []any) (uint64, error) {
	if len(params) == 0 {
		return 0, fmt.Errorf("missing block count")
	}
	return requestQuantity(params[0])
}

func requestQuantity(value any) (uint64, error) {
	// The methods package owns the richer typed request grammar. Range
	// validation only needs the already-normalized JSON quantity in practice.
	if s, ok := value.(string); ok && len(s) >= 3 && s[:2] == "0x" {
		return strconv.ParseUint(s[2:], 16, 64)
	}
	if n, ok := value.(uint64); ok {
		return n, nil
	}
	return 0, fmt.Errorf("invalid block count")
}
