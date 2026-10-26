package feehistory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"

	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// canonical 是 eth_feeHistory 的规范对象。字段顺序固定。可选字段缺失就省略，
// 不补成 null 或空数组。数量是规范 QUANTITY 的 JSON 字符串，比例是已经判定过
// 的规范十进制，按原文写入，不再经 float64 编码。
type canonical struct {
	Oldest       string         `json:"oldestBlock"`
	Base         []string       `json:"baseFeePerGas"`
	Gas          []ratioLiteral `json:"gasUsedRatio"`
	Reward       [][]string     `json:"reward,omitempty"`
	BaseBlob     []string       `json:"baseFeePerBlobGas,omitempty"`
	BlobGasRatio []ratioLiteral `json:"blobGasUsedRatio,omitempty"`
}

// ratioLiteral 是一段规范十进制。MarshalJSON 原样输出，不加引号。
type ratioLiteral string

func (r ratioLiteral) MarshalJSON() ([]byte, error) {
	return []byte(r), nil
}

// Encode 检查一份已准入的 feeHistory，返回受保护字段的规范 JSON。
// 只含 execution-apis result schema 中的字段。键外字段不进结果。
// 比例按第 5 节：json.Number 读十进制原文，输出 strconv.FormatFloat(f, 'f', -1, 64)。
// 最短形式的十进制值与原文不同时失败，不折成另一个数。数量写成规范 QUANTITY。
func Encode(value json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || trimmed[0] != '{' || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("feeHistory: expected an object")
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
		return nil, fmt.Errorf("feeHistory: malformed response")
	}
	oldest, err := wire.CanonicalQuantity(raw.Oldest)
	if err != nil {
		return nil, fmt.Errorf("feeHistory: oldestBlock: %w", err)
	}
	if len(raw.Base) != len(raw.Gas)+1 {
		return nil, fmt.Errorf("feeHistory: baseFeePerGas must have gasUsedRatio length plus one")
	}
	if raw.BaseBlob != nil && len(raw.BaseBlob) != len(raw.Gas)+1 {
		return nil, fmt.Errorf("feeHistory: baseFeePerBlobGas must have gasUsedRatio length plus one")
	}
	if raw.BlobGasRatio != nil && len(raw.BlobGasRatio) != len(raw.Gas) {
		return nil, fmt.Errorf("feeHistory: blobGasUsedRatio must have gasUsedRatio length")
	}
	base := make([]string, len(raw.Base))
	for i, item := range raw.Base {
		base[i], err = wire.CanonicalQuantity(item)
		if err != nil {
			return nil, fmt.Errorf("feeHistory: baseFeePerGas[%d]: %w", i, err)
		}
	}
	baseBlob := make([]string, len(raw.BaseBlob))
	for i, item := range raw.BaseBlob {
		baseBlob[i], err = wire.CanonicalQuantity(item)
		if err != nil {
			return nil, fmt.Errorf("feeHistory: baseFeePerBlobGas[%d]: %w", i, err)
		}
	}
	gas := make([]ratioLiteral, len(raw.Gas))
	for i, item := range raw.Gas {
		ratio, err := canonicalRatio(item)
		if err != nil {
			return nil, fmt.Errorf("feeHistory: gasUsedRatio[%d]: %w", i, err)
		}
		gas[i] = ratioLiteral(ratio)
	}
	blobGasRatio := make([]ratioLiteral, len(raw.BlobGasRatio))
	for i, item := range raw.BlobGasRatio {
		ratio, ratioErr := canonicalRatio(item)
		if ratioErr != nil {
			return nil, fmt.Errorf("feeHistory: blobGasUsedRatio[%d]: %w", i, ratioErr)
		}
		blobGasRatio[i] = ratioLiteral(ratio)
	}
	var reward [][]string
	if raw.Reward != nil {
		if len(raw.Reward) != len(raw.Gas) {
			return nil, fmt.Errorf("feeHistory: reward must have gasUsedRatio length")
		}
		reward = make([][]string, len(raw.Reward))
		width := -1
		for i, row := range raw.Reward {
			if width < 0 {
				width = len(row)
			} else if len(row) != width {
				return nil, fmt.Errorf("feeHistory: reward rows must have equal length")
			}
			reward[i] = make([]string, len(row))
			for j, item := range row {
				reward[i][j], err = wire.CanonicalQuantity(item)
				if err != nil {
					return nil, fmt.Errorf("feeHistory: reward[%d][%d]: %w", i, j, err)
				}
			}
		}
	}
	return wire.MarshalNoEscape(canonical{oldest, base, gas, reward, baseBlob, blobGasRatio})
}

// canonicalRatio 读十进制原文。0.50 与 0.5 同值。最短形式改变十进制原值时失败。
// 超出 [0,1]、NaN、Inf 在这里就是无效响应，不是另一种编码。
func canonicalRatio(value json.RawMessage) (json.Number, error) {
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return "", fmt.Errorf("expected a decimal")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return "", fmt.Errorf("expected a decimal")
	}
	f, err := number.Float64()
	if err != nil || f < 0 || f > 1 {
		return "", fmt.Errorf("out of range")
	}
	shortest := strconv.FormatFloat(f, 'f', -1, 64)
	original, ok := new(big.Rat).SetString(number.String())
	back, okBack := new(big.Rat).SetString(shortest)
	if !ok || !okBack || original.Cmp(back) != 0 {
		return "", fmt.Errorf("decimal %s is not the shortest form %s", number, shortest)
	}
	return json.Number(shortest), nil
}
