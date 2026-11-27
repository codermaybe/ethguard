package feehistory

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/codermaybe/ethguard/internal/ethcodec"
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
// 只含 execution-apis result schema 中的字段，接受域与比较键完全一致
// （parseFeeHistory），只把键的十进制数量重新渲染成规范 QUANTITY 拼写。
// 键外字段不进结果。
func Encode(value json.RawMessage) (json.RawMessage, error) {
	model, err := parseFeeHistory(value)
	if err != nil {
		return nil, fmt.Errorf("feeHistory: %w", err)
	}
	out := canonical{
		Oldest:       hexQuantity(model.oldest),
		Base:         hexQuantities(model.base),
		Gas:          ratioLiterals(model.gas),
		Reward:       hexRows(model.reward),
		BaseBlob:     hexQuantities(model.baseBlob),
		BlobGasRatio: ratioLiterals(model.blobGasRatio),
	}
	return ethcodec.MarshalNoEscape(out)
}

// hexQuantity renders a canonical wire decimal as its canonical 0x spelling.
// The decimal already passed the quantity grammar in parseFeeHistory, so the
// conversion cannot fail.
func hexQuantity(decimal string) string {
	number, _ := new(big.Int).SetString(decimal, 10)
	return "0x" + number.Text(16)
}

func hexQuantities(decimals []string) []string {
	out := make([]string, len(decimals))
	for i, decimal := range decimals {
		out[i] = hexQuantity(decimal)
	}
	return out
}

func hexRows(rows [][]string) [][]string {
	if rows == nil {
		return nil
	}
	out := make([][]string, len(rows))
	for i, row := range rows {
		out[i] = hexQuantities(row)
	}
	return out
}

func ratioLiterals(decimals []string) []ratioLiteral {
	out := make([]ratioLiteral, len(decimals))
	for i, decimal := range decimals {
		out[i] = ratioLiteral(decimal)
	}
	return out
}
