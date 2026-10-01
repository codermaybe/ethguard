package receipt

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	receiptTxHash  = "0xaa1c6c878d686b6c14b77c6b78ff23ae4b069cf941736678e9c1f8102b219b15"
	receiptBlkHash = "0x9bc1666a9b54a70b8fc7f4caf052c25d2bef94897c2815dc168163d6bc188b1e"
	logAddress     = "0x0000000aa232009084bd71a5797d089aa4edfad4"
	logTopic       = "0x00000000000000000000000000000000000000000000000000000000000000ff"
)

// logFixture is one log carrying the hashes of the receipt that owns it. The
// bloom a receipt must ship is built from these, so the fixture states what a
// source is allowed to claim instead of copying an encoded value.
func logFixture() map[string]any {
	return map[string]any{
		"address":          logAddress,
		"topics":           []string{logTopic},
		"data":             "0xdeadbeef",
		"blockNumber":      "0x18d592f",
		"transactionHash":  receiptTxHash,
		"transactionIndex": "0x0",
		"blockHash":        receiptBlkHash,
		"logIndex":         "0x1",
		"removed":          false,
	}
}

func bloomOf(logs ...map[string]any) string {
	decoded := make([]*types.Log, len(logs))
	for i, item := range logs {
		entry := &types.Log{
			Address: common.HexToAddress(item["address"].(string)),
			Data:    common.FromHex(item["data"].(string)),
		}
		for _, topic := range item["topics"].([]string) {
			entry.Topics = append(entry.Topics, common.HexToHash(topic))
		}
		decoded[i] = entry
	}
	return hexutil.Encode(types.CreateBloom(&types.Receipt{Logs: decoded}).Bytes())
}

func receiptFixture(t *testing.T, logs ...map[string]any) json.RawMessage {
	t.Helper()
	rows := make([]any, len(logs))
	for i, item := range logs {
		rows[i] = item
	}
	receipt := map[string]any{
		"type":              "0x2",
		"status":            "0x1",
		"cumulativeGasUsed": "0x8779",
		"gasUsed":           "0x8779",
		"logs":              rows,
		"logsBloom":         bloomOf(logs...),
		"transactionHash":   receiptTxHash,
		"transactionIndex":  "0x0",
		"blockHash":         receiptBlkHash,
		"blockNumber":       "0x18d592f",
		"effectiveGasPrice": "0x135e45e7",
		"to":                logAddress,
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func replaceField(t *testing.T, raw json.RawMessage, name string, value any) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	patched, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	obj[name] = patched
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A receipt with logs must survive Encode, and the bloom check has to be a
// check: every receipt in the sample corpus carries an empty logs array, so
// without this the CreateBloom path would only ever see zero.
func TestEncodeAcceptsReceiptWithLogs(t *testing.T) {
	encoded, err := Encode(receiptFixture(t, logFixture()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"logs":[{"address":"`+logAddress+`"`)) || !bytes.Contains(encoded, []byte(`"topics":["`+logTopic+`"]`)) {
		t.Fatalf("log entered Value = %s", encoded)
	}
}

// The bloom must be the bloom of the logs EthGuard encodes. Case and escape
// spelling of the same source receipt is one value, so neither may move the
// expected bloom.
func TestEncodeBloomSpellingAgreesWithCanonicalLogs(t *testing.T) {
	raw := receiptFixture(t, logFixture())
	canonical, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"transactionHash", "blockHash", "logsBloom"} {
		var text string
		if err := json.Unmarshal(obj[name], &text); err != nil {
			t.Fatal(err)
		}
		obj[name] = json.RawMessage(`"` + strings.ToUpper(text) + `"`)
	}
	shouted, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(shouted)
	if err != nil {
		t.Fatalf("uppercase spelling = %v", err)
	}
	if !bytes.Equal(encoded, canonical) {
		t.Fatalf("spelling changed Value:\n%s\n%s", encoded, canonical)
	}
	escaped := json.RawMessage(strings.ReplaceAll(string(raw), `"0x`, `"`+`\u0030x`))
	encoded, err = Encode(escaped)
	if err != nil {
		t.Fatalf("escaped spelling = %v", err)
	}
	if !bytes.Equal(encoded, canonical) {
		t.Fatalf("spelling changed Value:\n%s\n%s", encoded, canonical)
	}
}

func TestEncodeRejectsBloomMismatch(t *testing.T) {
	raw := receiptFixture(t, logFixture())
	wrongBloom := replaceField(t, raw, "logsBloom", "0x"+strings.Repeat("00", 255)+"01")
	if _, err := Encode(wrongBloom); err == nil || !strings.Contains(err.Error(), "logsBloom disagrees with logs") {
		t.Fatalf("bloom = %v", err)
	}
}

// A log that omits its hashes states nothing about another transaction, and
// admission has always accepted that. Encode has to accept it too and carry
// the omission into Value verbatim: rejecting it would turn a Corroborated
// answer into a conflict over a payload no source produces by accident. A
// non-zero foreign hash is a claim, and stays a fault.
func TestEncodeTreatsZeroLogHashAsOmission(t *testing.T) {
	omitted := logFixture()
	omitted["transactionHash"] = zeroHash
	omitted["blockHash"] = zeroHash
	encoded, err := Encode(receiptFixture(t, omitted))
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(encoded, []byte(`"`+zeroHash+`"`)); got != 2 {
		t.Fatalf("omitted hashes entered Value %d times: %s", got, encoded)
	}
	foreignTx := logFixture()
	foreignTx["transactionHash"] = "0x" + strings.Repeat("34", 32)
	if _, err := Encode(receiptFixture(t, foreignTx)); err == nil || !strings.Contains(err.Error(), "does not match receipt transactionHash") {
		t.Fatalf("transactionHash = %v", err)
	}
	foreignBlock := logFixture()
	foreignBlock["blockHash"] = "0x" + strings.Repeat("56", 32)
	if _, err := Encode(receiptFixture(t, foreignBlock)); err == nil || !strings.Contains(err.Error(), "does not match receipt blockHash") {
		t.Fatalf("blockHash = %v", err)
	}
}
