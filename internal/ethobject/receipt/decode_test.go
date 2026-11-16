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
	decodeTxHash  = "0xaa1c6c878d686b6c14b77c6b78ff23ae4b069cf941736678e9c1f8102b219b15"
	decodeBlkHash = "0x9bc1666a9b54a70b8fc7f4caf052c25d2bef94897c2815dc168163d6bc188b1e"
	decodeAddress = "0x0000000aa232009084bd71a5797d089aa4edfad4"
)

// decodeLogFixture is one log carrying the hashes of the receipt that owns
// it; the fixture's bloom is derived from it, not copied.
func decodeLogFixture() map[string]any {
	return map[string]any{
		"address":          decodeAddress,
		"topics":           []string{"0x00000000000000000000000000000000000000000000000000000000000000ff"},
		"data":             "0xdeadbeef",
		"blockNumber":      "0x18d592f",
		"transactionHash":  decodeTxHash,
		"transactionIndex": "0x0",
		"blockHash":        decodeBlkHash,
		"logIndex":         "0x1",
		"removed":          false,
	}
}

func decodeReceiptFixture(t *testing.T) json.RawMessage {
	t.Helper()
	entry := decodeLogFixture()
	logs := []any{entry}
	bloomBytes := types.CreateBloom(&types.Receipt{Logs: []*types.Log{{
		Address: common.HexToAddress(entry["address"].(string)),
		Topics:  []common.Hash{common.HexToHash(entry["topics"].([]string)[0])},
		Data:    common.FromHex(entry["data"].(string)),
	}}}).Bytes()
	bloom := hexutil.Encode(bloomBytes)
	receipt := map[string]any{
		"type":              "0x2",
		"status":            "0x1",
		"cumulativeGasUsed": "0x8779",
		"gasUsed":           "0x8779",
		"logs":              logs,
		"logsBloom":         bloom,
		"transactionHash":   decodeTxHash,
		"transactionIndex":  "0x0",
		"blockHash":         decodeBlkHash,
		"blockNumber":       "0x18d592f",
		"effectiveGasPrice": "0x135e45e7",
		"from":              decodeAddress,
		"to":                decodeAddress,
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func decodeShoutedSpelling(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"transactionHash", "blockHash", "from", "to"} {
		obj[key] = strings.ToUpper(obj[key].(string))
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestDecodeWithCanonicalReturnsValidatedBytes pins the reuse contract: the
// canonical bytes come from the same checked Encode run that fed the private
// decode — byte-identical to the encoder's own output for the same input —
// and the decoded receipt carries the mined identity the private checks
// guarantee. The expectations are built independently of the helper: from
// the encoder's own output and from literal hash values.
func TestDecodeWithCanonicalReturnsValidatedBytes(t *testing.T) {
	raw := decodeReceiptFixture(t)
	canonical, r, err := DecodeWithCanonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, want) {
		t.Fatalf("canonical bytes diverge from the encoder's output:\n%s\n%s", canonical, want)
	}
	if r == nil {
		t.Fatal("decoded receipt is nil")
	}
	if r.TxHash != common.HexToHash(decodeTxHash) {
		t.Fatalf("TxHash = %s", r.TxHash)
	}
	if r.BlockHash != common.HexToHash(decodeBlkHash) {
		t.Fatalf("BlockHash = %s", r.BlockHash)
	}
	if r.Status != 1 || r.Logs == nil || len(r.Logs) != 1 {
		t.Fatalf("decoded receipt shape: status %v logs %d", r.Status, len(r.Logs))
	}
	// A different spelling of the same source folds to the same bytes.
	shouted, _, err := DecodeWithCanonical(decodeShoutedSpelling(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shouted, canonical) {
		t.Fatalf("spelling changed canonical bytes:\n%s\n%s", shouted, canonical)
	}
	// The checked value is a fixed point of the encoder.
	again, err := Encode(canonical)
	if err != nil || !bytes.Equal(again, canonical) {
		t.Fatalf("canonical value is not a fixed point: %s, %v", again, err)
	}
}

// TestDecodeWithCanonicalPreservesErrors pins the failure contract: both
// returns are nil and the error text is the one Decode has always produced,
// covering the encoder's own rejections and the private geth checks.
func TestDecodeWithCanonicalPreservesErrors(t *testing.T) {
	raw := decodeReceiptFixture(t)
	cases := []struct {
		name string
		raw  func() json.RawMessage
		want string
	}{
		{
			name: "missing-status-and-root",
			raw: func() json.RawMessage {
				var obj map[string]json.RawMessage
				if err := json.Unmarshal(raw, &obj); err != nil {
					t.Fatal(err)
				}
				delete(obj, "status")
				out, err := json.Marshal(obj)
				if err != nil {
					t.Fatal(err)
				}
				return out
			},
			want: "receipt: missing status or root",
		},
		{
			name: "logs-not-array",
			raw: func() json.RawMessage {
				return []byte(`{"transactionHash":"` + decodeTxHash + `"}`)
			},
			want: "receipt: logs must be an array",
		},
		{
			name: "non-object",
			raw:  func() json.RawMessage { return json.RawMessage(`"0x1"`) },
			want: "receipt: expected an object",
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			canonical, r, err := DecodeWithCanonical(item.raw())
			if canonical != nil || r != nil {
				t.Fatalf("failure returned non-nil values: %s %+v", canonical, r)
			}
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("err = %v, want it to carry %q", err, item.want)
			}
			// The thin adapter reports the identical error.
			_, decodeErr := Decode(item.raw())
			if decodeErr == nil || decodeErr.Error() != err.Error() {
				t.Fatalf("adapter error = %v, processing error = %v", decodeErr, err)
			}
		})
	}
	t.Run("private-zero-hash", func(t *testing.T) {
		// A zero transactionHash passes the encoder (a zero log hash is an
		// omission, and the encoder does not judge the receipt's own hash);
		// the private check is the layer that rejects it.
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		obj["transactionHash"] = json.RawMessage(`"` + common.Hash{}.Hex() + `"`)
		entry := decodeLogFixture()
		entry["transactionHash"] = common.Hash{}.Hex()
		logs, err := json.Marshal([]any{entry})
		if err != nil {
			t.Fatal(err)
		}
		obj["logs"] = logs
		out, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		if _, encodeErr := Encode(out); encodeErr != nil {
			t.Fatalf("fixture does not reach the private check: %v", encodeErr)
		}
		canonical, r, err := DecodeWithCanonical(out)
		if canonical != nil || r != nil || err == nil ||
			err.Error() != "receipt: missing transaction or block hash" {
			t.Fatalf("zero-hash case = %s, %+v, %v", canonical, r, err)
		}
	})
}
