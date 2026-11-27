package jsonsafe

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type validationBenchmarkCase struct {
	name  string
	raw   []byte
	valid bool
}

var decodedBenchmarkValue any

func BenchmarkValidate(b *testing.B) {
	for _, fixture := range validationBenchmarkCases() {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.raw)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				err := Validate(fixture.raw)
				if (err == nil) != fixture.valid {
					b.Fatalf("Validate() error = %v, want valid=%v", err, fixture.valid)
				}
			}
		})
	}
}

func BenchmarkDecode(b *testing.B) {
	for _, fixture := range validationBenchmarkCases() {
		if !fixture.valid || fixture.name == "HugeNumber" {
			continue
		}
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.raw)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var value any
				if err := Decode(fixture.raw, &value); err != nil {
					b.Fatal(err)
				}
				decodedBenchmarkValue = value
			}
		})
	}
}

// Fixtures represent raw JSON values at the jsonsafe boundary, not provider
// samples or proof of Ethereum object validity. Construction is outside timers.
func validationBenchmarkCases() []validationBenchmarkCase {
	hash := "0x" + strings.Repeat("a", 64)
	address := "0x" + strings.Repeat("1", 40)
	logs := make([]map[string]any, 8)
	for i := range logs {
		logs[i] = map[string]any{
			"address": address, "topics": []string{hash, hash}, "data": "0x" + strings.Repeat("ab", 64),
			"blockNumber": "0x10", "blockHash": hash, "transactionHash": hash,
			"transactionIndex": "0x1", "logIndex": fmt.Sprintf("0x%x", i), "removed": false,
		}
	}
	receipt := validationBenchmarkJSON(map[string]any{
		"transactionHash": hash, "transactionIndex": "0x1", "blockHash": hash, "blockNumber": "0x10",
		"from": address, "to": address, "contractAddress": nil, "cumulativeGasUsed": "0x5208",
		"gasUsed": "0x5208", "effectiveGasPrice": "0x1", "status": "0x1", "type": "0x0",
		"logsBloom": "0x" + strings.Repeat("0", 512), "logs": logs,
	})
	transactions := make([]map[string]any, 100)
	for i := range transactions {
		transactions[i] = map[string]any{
			"hash": hash, "from": address, "to": address, "blockHash": hash, "blockNumber": "0x10",
			"transactionIndex": fmt.Sprintf("0x%x", i), "nonce": fmt.Sprintf("0x%x", i),
			"gas": "0x5208", "gasPrice": "0x1", "value": "0x1", "input": "0x" + strings.Repeat("ab", 32),
			"v": "0x25", "r": "0x1", "s": "0x2", "chainId": "0x1", "type": "0x0",
		}
	}
	block := validationBenchmarkJSON(map[string]any{
		"number": "0x10", "hash": hash, "parentHash": hash, "nonce": "0x0000000000000000",
		"sha3Uncles": hash, "logsBloom": "0x" + strings.Repeat("0", 512), "transactionsRoot": hash,
		"stateRoot": hash, "receiptsRoot": hash, "miner": address, "difficulty": "0x0", "extraData": "0x",
		"size": "0x10000", "gasLimit": "0x1c9c380", "gasUsed": "0x200b20", "timestamp": "0x64",
		"mixHash": hash, "baseFeePerGas": "0x1", "transactions": transactions, "uncles": []string{},
	})
	largeTail := strings.Repeat("x", 1<<20)
	wideObject := validationObjectKeys(4096)
	return []validationBenchmarkCase{
		{name: "Scalar", raw: []byte(`"0x1234567890abcdef"`), valid: true},
		{name: "HugeNumber", raw: []byte(`1e400`), valid: true},
		{name: "RootNull", raw: []byte(`null`), valid: true},
		{name: "RootBoolean", raw: []byte(`true`), valid: true},
		{name: "RootLeadingWhitespace", raw: []byte(" \t\r\n\"0x1234567890abcdef\""), valid: true},
		{name: "RootLargeString", raw: []byte(`"` + largeTail + `"`), valid: true},
		{name: "Receipt8Logs", raw: receipt, valid: true},
		{name: "Block100Tx", raw: block, valid: true},
		{name: "WideArray4096", raw: []byte("[" + strings.Repeat("0,", 4095) + "0]"), valid: true},
		{name: "DeepArray32", raw: []byte(strings.Repeat("[", 32) + "0" + strings.Repeat("]", 32)), valid: true},
		{name: "WideObject4096", raw: wideObject, valid: true},
		{name: "EscapedKeys", raw: []byte(`{"\u0061":1,"a/b":2,"\u007e":3,"\ud83d\ude00":4}`), valid: true},
		{name: "InvalidSyntax", raw: []byte(`{"value":?}`)},
		{name: "InvalidLateSyntax", raw: append(append([]byte(nil), block...), '?')},
		// These errors precede a large legal suffix. The frozen decoder rejects
		// them before reading the suffix, so they expose extra screening work.
		{name: "EarlyInvalidLeadingZeroLargeTail", raw: []byte(`{"value":01,"tail":"` + largeTail + `"}`)},
		{name: "EarlyInvalidLiteralLargeTail", raw: []byte(`{"value":tru?,"tail":"` + largeTail + `"}`)},
		{name: "EarlyInvalidTokenLargeTail", raw: []byte(`{"value":?,"tail":"` + largeTail + `"}`)},
		{name: "EarlyInvalidEscapeLargeTail", raw: []byte(`{"value":"\q","tail":"` + largeTail + `"}`)},
		// Root scalar candidates must keep early syntax failures bounded and
		// preserve the decoder's treatment of a second, potentially large value.
		{name: "RootInvalidLeadingZeroLargeTail", raw: []byte(`01 {"tail":"` + largeTail + `"}`)},
		{name: "RootInvalidLiteralLargeTail", raw: []byte(`tru? {"tail":"` + largeTail + `"}`)},
		{name: "RootInvalidTokenLargeTail", raw: []byte(`? {"tail":"` + largeTail + `"}`)},
		{name: "RootInvalidEscapeLargeTail", raw: []byte(`"\q" {"tail":"` + largeTail + `"}`)},
		{name: "RootTrailingLargeContainer", raw: []byte(`null {"tail":"` + largeTail + `"}`)},
		{name: "RootLateInvalidEscape", raw: []byte(`"` + largeTail + `\q"`)},
		{name: "DuplicateKey", raw: []byte(`{"value":1,"\u0076alue":2}`)},
		{name: "EarlyDuplicateLargeTail", raw: []byte(`{"x":0,"x":"` + largeTail + `"}`)},
		{name: "TrailingData", raw: []byte(`{"value":1} []`)},
		{name: "Depth33", raw: []byte(strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33))},
		{name: "EarlyDepthLargeTail", raw: []byte(strings.Repeat("[", 33) + `"` + largeTail + `"` + strings.Repeat("]", 33))},
		{name: "Keys4097", raw: validationObjectKeys(4097)},
		{name: "EarlyKeysLargeTail", raw: []byte(string(wideObject[:len(wideObject)-1]) + `,"overflow":"` + largeTail + `"}`)},
	}
}

func validationBenchmarkJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func validationObjectKeys(count int) []byte {
	var raw strings.Builder
	raw.WriteByte('{')
	for i := 0; i < count; i++ {
		if i != 0 {
			raw.WriteByte(',')
		}
		fmt.Fprintf(&raw, `"key%d":0`, i)
	}
	raw.WriteByte('}')
	return []byte(raw.String())
}
