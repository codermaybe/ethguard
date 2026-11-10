package wire

// The numeric rules the generated native types rely on. CanonicalRatio is
// moved here from rpc/internal/objects/feehistory (the objects packages
// already depend on wire, so the rule lives beside the other canonicalizers
// and both callers share one implementation). CanonicalHexByte is the
// return-value-design.md section 5 rule: byte-like fields (type, v, yParity)
// are admitted as 1-2 hex digits and shipped as canonical QUANTITY.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// CanonicalRatio reads one JSON number literal via json.Number, requires the
// value to stay inside [0,1], and returns the shortest round-trip decimal.
// 0.50 compares equal to 0.5 and will be emitted as 0.5; a decimal whose
// shortest form would change its value is rejected.
func CanonicalRatio(value json.RawMessage) (json.Number, error) {
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

// CanonicalHexByte admits the official byte-shaped family (type, v, yParity):
// 0x with 1-2 hex digits. It returns the canonical QUANTITY spelling, so
// 0x4, 0x04, and the lowercase form of 0x4A all ship as the same canonical
// bytes.
func CanonicalHexByte(value json.RawMessage) (string, error) {
	var text string
	decoder := json.NewDecoder(bytes.NewReader(value))
	if err := decoder.Decode(&text); err != nil {
		return "", fmt.Errorf("hex byte: expected a string")
	}
	if !strings.HasPrefix(text, "0x") {
		return "", fmt.Errorf("hex byte: expected 0x plus 1-2 hex digits")
	}
	digits := text[2:]
	if len(digits) == 0 || len(digits) > 2 {
		return "", fmt.Errorf("hex byte: expected 0x plus 1-2 hex digits")
	}
	parsed, ok := new(big.Int).SetString(digits, 16)
	if !ok {
		return "", fmt.Errorf("hex byte: malformed value")
	}
	if parsed.Cmp(big.NewInt(0xff)) > 0 {
		return "", fmt.Errorf("hex byte: value exceeds one byte")
	}
	return "0x" + parsed.Text(16), nil
}
