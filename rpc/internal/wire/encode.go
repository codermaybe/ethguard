// 标量规范编码。已迁移的数量和成功 DATA，比较键就是这里返回的规范 JSON
// 字节。EncodeCheck、比对和 EncodeCommit 看见同一串字节。
//
// 规范 QUANTITY 是 JSON 字符串：0x 加小写十六进制，无前导零，零写作 0x0。
// 已经拒绝前导零的数量继续拒绝，不折成同一个键。NormalizeWireQuantity 仍返回
// 十进制。那不是规范字节，也不能拿来反推 Value。
//
// 规范 DATA 是 JSON 字符串：0x 加小写十六进制，长度与字节数一致。DATA 的
// 前导零是内容，保留。0x00 不是 0x，也不是 0x0。
//
// net_version 的规范形式是无前导零的十进制 JSON 字符串，不是 0x。前导零先按
// 数值折合，再写成这个字符串。net_peerCount 同样先折合，再写成规范 QUANTITY。
//
// 这些字符串用 encoding/json 的紧凑编码。本文件不编码对象、feeHistory、
// revert 信封、code、message 或区块。
package wire

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// EncodeQuantity checks a wire QUANTITY and returns its canonical JSON string.
// Leading zeros are rejected, matching NormalizeWireQuantity. For a migrated
// quantity those bytes are the comparison key.
func EncodeQuantity(value json.RawMessage) (json.RawMessage, error) {
	decimal, err := NormalizeWireQuantity(value)
	if err != nil {
		return nil, err
	}
	return quantityJSON(decimal)
}

// EncodeFoldedQuantity accepts the spellings NormalizeHexQuantity already
// folds, including leading zeros, and returns canonical QUANTITY JSON.
// net_peerCount uses it. Strict wire quantities do not.
func EncodeFoldedQuantity(value json.RawMessage) (json.RawMessage, error) {
	decimal, err := NormalizeHexQuantity(value)
	if err != nil {
		return nil, err
	}
	return quantityJSON(decimal)
}

// EncodeDecimal returns the canonical decimal JSON string for net_version.
// Leading zeros fold. The result is not a 0x quantity.
func EncodeDecimal(value json.RawMessage) (json.RawMessage, error) {
	text, err := NormalizeDecimalString(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(text)
}

// EncodeData checks DATA and returns its canonical JSON string. Leading zeros
// are kept. Odd lengths and non-hex text fail.
func EncodeData(value json.RawMessage) (json.RawMessage, error) {
	text, err := NormalizeHexData(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(text)
}

// EncodeStorageWord checks one 32-byte storage word and returns canonical DATA.
// A shorter or longer string fails. It is not rewritten into a quantity.
func EncodeStorageWord(value json.RawMessage) (json.RawMessage, error) {
	text, err := NormalizeStorageWord(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(text)
}

func quantityJSON(decimal string) (json.RawMessage, error) {
	number, ok := new(big.Int).SetString(decimal, 10)
	if !ok {
		return nil, fmt.Errorf("hex quantity: invalid quantity %q", decimal)
	}
	return json.Marshal("0x" + number.Text(16))
}
