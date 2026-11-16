// Package feehistory contains the shared response validation rules for
// eth_feeHistory. Request parameter validation remains owned by methods.
package feehistory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
)

// feeHistoryModel is one parsed fee-history response. Every quantity is held
// in canonical wire decimal form ("5", not "0x5") and every ratio has already
// passed the project's CanonicalRatio gate. The comparison key and the
// protected value render this one model with their own spellings, so the two
// outputs can never drift apart on which responses are admitted.
type feeHistoryModel struct {
	oldest       string
	base         []string
	gas          []string
	reward       [][]string // nil when the response omitted reward
	baseBlob     []string   // nil when the response omitted baseFeePerBlobGas
	blobGasRatio []string   // nil when the response omitted blobGasUsedRatio
}

// parseFeeHistory validates one response against the fee-history object
// rules shared by the comparison key and the protected value: oldestBlock is
// a wire quantity, baseFeePerGas tracks gasUsedRatio length plus one, the
// optional blob arrays keep their lengths, reward rows are uniform in width
// and track gasUsedRatio length, quantities are canonical wire quantities,
// and ratios pass the project ratio gate. Bare errors; callers add their own
// stage prefix.
func parseFeeHistory(value json.RawMessage) (*feeHistoryModel, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || trimmed[0] != '{' || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("expected an object")
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
		return nil, errors.New("malformed response")
	}
	oldest, err := ethcodec.NormalizeWireQuantity(raw.Oldest)
	if err != nil {
		return nil, fmt.Errorf("oldestBlock: %w", err)
	}
	if len(raw.Base) != len(raw.Gas)+1 {
		return nil, errors.New("baseFeePerGas must have gasUsedRatio length plus one")
	}
	if raw.BaseBlob != nil && len(raw.BaseBlob) != len(raw.Gas)+1 {
		return nil, errors.New("baseFeePerBlobGas must have gasUsedRatio length plus one")
	}
	if raw.BlobGasRatio != nil && len(raw.BlobGasRatio) != len(raw.Gas) {
		return nil, errors.New("blobGasUsedRatio must have gasUsedRatio length")
	}
	base := make([]string, len(raw.Base))
	for i, item := range raw.Base {
		if base[i], err = ethcodec.NormalizeWireQuantity(item); err != nil {
			return nil, fmt.Errorf("baseFeePerGas[%d]: %w", i, err)
		}
	}
	baseBlob := make([]string, len(raw.BaseBlob))
	for i, item := range raw.BaseBlob {
		if baseBlob[i], err = ethcodec.NormalizeWireQuantity(item); err != nil {
			return nil, fmt.Errorf("baseFeePerBlobGas[%d]: %w", i, err)
		}
	}
	gas := make([]string, len(raw.Gas))
	for i, item := range raw.Gas {
		if gas[i], err = canonicalRatio(item); err != nil {
			return nil, fmt.Errorf("gasUsedRatio[%d]: %w", i, err)
		}
	}
	blobGasRatio := make([]string, len(raw.BlobGasRatio))
	for i, item := range raw.BlobGasRatio {
		if blobGasRatio[i], err = canonicalRatio(item); err != nil {
			return nil, fmt.Errorf("blobGasUsedRatio[%d]: %w", i, err)
		}
	}
	var reward [][]string
	if raw.Reward != nil {
		if len(raw.Reward) != len(raw.Gas) {
			return nil, errors.New("reward must have gasUsedRatio length")
		}
		reward = make([][]string, len(raw.Reward))
		width := -1
		for i, row := range raw.Reward {
			if width < 0 {
				width = len(row)
			} else if len(row) != width {
				return nil, errors.New("reward rows must have equal length")
			}
			reward[i] = make([]string, len(row))
			for j, item := range row {
				if reward[i][j], err = ethcodec.NormalizeWireQuantity(item); err != nil {
					return nil, fmt.Errorf("reward[%d][%d]: %w", i, j, err)
				}
			}
		}
	}
	return &feeHistoryModel{
		oldest:       oldest,
		base:         base,
		gas:          gas,
		reward:       reward,
		baseBlob:     baseBlob,
		blobGasRatio: blobGasRatio,
	}, nil
}

// Normalize reduces one fee-history response to its comparison key. Quantities
// compare as decimals so any wire spelling the grammar accepts yields the same
// key; ratios stay the canonical decimal text admitted by the ratio gate.
func Normalize(value json.RawMessage) (string, error) {
	model, err := parseFeeHistory(value)
	if err != nil {
		return "", fmt.Errorf("feeHistory: %w", err)
	}
	key := struct {
		Oldest       string        `json:"oldestBlock"`
		Base         []string      `json:"baseFeePerGas"`
		Gas          []json.Number `json:"gasUsedRatio"`
		Reward       [][]string    `json:"reward,omitempty"`
		BaseBlob     []string      `json:"baseFeePerBlobGas,omitempty"`
		BlobGasRatio []json.Number `json:"blobGasUsedRatio,omitempty"`
	}{
		Oldest:       model.oldest,
		Base:         model.base,
		Gas:          decimalNumbers(model.gas),
		Reward:       model.reward,
		BaseBlob:     model.baseBlob,
		BlobGasRatio: decimalNumbers(model.blobGasRatio),
	}
	encoded, err := json.Marshal(key)
	return string(encoded), err
}

// decimalNumbers renders canonical decimals as JSON numbers for the key.
func decimalNumbers(values []string) []json.Number {
	out := make([]json.Number, len(values))
	for i, value := range values {
		out[i] = json.Number(value)
	}
	return out
}

// canonicalRatio 与生成层共用的规范实现：wire.CanonicalRatio。
// 0.50 与 0.5 同值；最短形式改变十进制原值时失败。超出 [0,1]、NaN、Inf
// 在这里就是无效响应，不是另一种编码。
func canonicalRatio(value json.RawMessage) (string, error) {
	number, err := ethcodec.CanonicalRatio(value)
	if err != nil {
		return "", err
	}
	return string(number), nil
}

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
	if _, err := ethcodec.NormalizeWireQuantity(response.Oldest); err != nil {
		return fmt.Errorf("oldestBlock: %w", err)
	}
	return nil
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
