// Package feehistory contains the shared response validation rules for
// eth_feeHistory. Request parameter validation remains owned by methods.
package feehistory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// ValidateRange binds a fee-history response to the requested block count.
func ValidateRange(req rpc.Request, value json.RawMessage) error {
	count, err := requestCount(req.Params)
	if err != nil || count == 0 {
		return fmt.Errorf("invalid block count")
	}
	var response struct {
		Oldest       json.RawMessage   `json:"oldestBlock"`
		BaseBlob     []json.RawMessage `json:"baseFeePerBlobGas"`
		Gas          []json.RawMessage `json:"gasUsedRatio"`
		BlobGasRatio []json.RawMessage `json:"blobGasUsedRatio"`
	}
	if err := json.Unmarshal(value, &response); err != nil {
		return fmt.Errorf("malformed response")
	}
	if uint64(len(response.Gas)) != count {
		return fmt.Errorf("gasUsedRatio length %d does not match requested block count %d", len(response.Gas), count)
	}
	if response.BaseBlob != nil && len(response.BaseBlob) != len(response.Gas)+1 {
		return fmt.Errorf("baseFeePerBlobGas length %d does not match gasUsedRatio length %d", len(response.BaseBlob), len(response.Gas))
	}
	if response.BlobGasRatio != nil && len(response.BlobGasRatio) != len(response.Gas) {
		return fmt.Errorf("blobGasUsedRatio length %d does not match gasUsedRatio length %d", len(response.BlobGasRatio), len(response.Gas))
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
		Oldest       json.RawMessage     `json:"oldestBlock"`
		Base         []json.RawMessage   `json:"baseFeePerGas"`
		Gas          []json.RawMessage   `json:"gasUsedRatio"`
		Reward       [][]json.RawMessage `json:"reward"`
		BaseBlob     []json.RawMessage   `json:"baseFeePerBlobGas"`
		BlobGasRatio []json.RawMessage   `json:"blobGasUsedRatio"`
	}
	if err := json.Unmarshal(value, &raw); err != nil || len(raw.Oldest) == 0 {
		return "", fmt.Errorf("feeHistory: malformed response")
	}
	oldest, err := wire.NormalizeWireQuantity(raw.Oldest)
	if err != nil {
		return "", fmt.Errorf("feeHistory: oldestBlock: %w", err)
	}
	if len(raw.Base) != len(raw.Gas)+1 {
		return "", fmt.Errorf("feeHistory: baseFeePerGas must have gasUsedRatio length plus one")
	}
	if raw.BaseBlob != nil && len(raw.BaseBlob) != len(raw.Gas)+1 {
		return "", fmt.Errorf("feeHistory: baseFeePerBlobGas must have gasUsedRatio length plus one")
	}
	if raw.BlobGasRatio != nil && len(raw.BlobGasRatio) != len(raw.Gas) {
		return "", fmt.Errorf("feeHistory: blobGasUsedRatio must have gasUsedRatio length")
	}
	base := make([]string, len(raw.Base))
	for i, item := range raw.Base {
		base[i], err = wire.NormalizeWireQuantity(item)
		if err != nil {
			return "", fmt.Errorf("feeHistory: baseFeePerGas[%d]: %w", i, err)
		}
	}
	baseBlob := make([]string, len(raw.BaseBlob))
	for i, item := range raw.BaseBlob {
		baseBlob[i], err = wire.NormalizeWireQuantity(item)
		if err != nil {
			return "", fmt.Errorf("feeHistory: baseFeePerBlobGas[%d]: %w", i, err)
		}
	}
	gas := make([]json.Number, len(raw.Gas))
	for i, item := range raw.Gas {
		ratio, err := canonicalRatio(item)
		if err != nil {
			return "", fmt.Errorf("feeHistory: gasUsedRatio[%d]: %w", i, err)
		}
		gas[i] = ratio
	}
	blobGasRatio := make([]json.Number, len(raw.BlobGasRatio))
	for i, item := range raw.BlobGasRatio {
		ratio, ratioErr := canonicalRatio(item)
		if ratioErr != nil {
			return "", fmt.Errorf("feeHistory: blobGasUsedRatio[%d]: %w", i, ratioErr)
		}
		blobGasRatio[i] = ratio
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
		Oldest       string        `json:"oldestBlock"`
		Base         []string      `json:"baseFeePerGas"`
		Gas          []json.Number `json:"gasUsedRatio"`
		Reward       [][]string    `json:"reward,omitempty"`
		BaseBlob     []string      `json:"baseFeePerBlobGas,omitempty"`
		BlobGasRatio []json.Number `json:"blobGasUsedRatio,omitempty"`
	}{oldest, base, gas, reward, baseBlob, blobGasRatio}
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
	switch n := value.(type) {
	case uint64:
		return n, nil
	case uint:
		return uint64(n), nil
	case uint32:
		return uint64(n), nil
	case uint16:
		return uint64(n), nil
	case uint8:
		return uint64(n), nil
	case int:
		if n < 0 {
			return 0, fmt.Errorf("invalid block count")
		}
		return uint64(n), nil
	case int64:
		if n < 0 {
			return 0, fmt.Errorf("invalid block count")
		}
		return uint64(n), nil
	case *big.Int:
		if n == nil || n.Sign() < 0 || !n.IsUint64() {
			return 0, fmt.Errorf("invalid block count")
		}
		return n.Uint64(), nil
	case json.Number:
		parsed, err := strconv.ParseUint(string(n), 10, 64)
		if err == nil {
			return parsed, nil
		}
	}
	return 0, fmt.Errorf("invalid block count")
}
