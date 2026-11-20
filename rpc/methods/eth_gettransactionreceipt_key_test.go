package methods

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// PruneMethod owns the comparison key's hex folding and JSON encoding.
// Refolding hex or round-tripping the key through map[string]RawMessage must
// preserve its bytes, including numbers and escapes. The literals below also
// keep the comparison shape gate separate from full receipt admission.
func TestPrunedReceiptKeyIsAlreadyCanonical(t *testing.T) {
	// Uppercase hex at the top level, inside a nested log, and as a topic, so
	// the claim covers nested objects and arrays, not just the top object.
	raw := replaceReceiptField(t, sampleReceipt(t), "logs", `[{"address":"0xABCDEF0123456789ABCDEF0123456789ABCDEF01","topics":["0x00FF","0x00000000000000000000000000000000000000000000000000000000000000AB"],"data":"0xDEADBEEF","blockNumber":"0x00FF","transactionIndex":"0x0","transactionHash":"0xAA","blockHash":"0xBB","logIndex":"0x1","removed":false}]`)
	key, err := wire.PruneMethod(MethodGetTransactionReceipt, raw)
	if err != nil {
		t.Fatal(err)
	}
	if key == "null" {
		t.Fatal("pruned a real receipt to null")
	}
	assertNoUppercaseHex(t, key)

	var decoded any
	if err := json.Unmarshal([]byte(key), &decoded); err != nil {
		t.Fatal(err)
	}
	refolded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	ethcodec.CanonicalizeHex(&decoded)
	folded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(folded) != string(refolded) {
		t.Fatalf("CanonicalizeHex is not a fixed point on the key\nbefore %s\nafter  %s", refolded, folded)
	}

	// The key the method returns retains every official receipt field, including
	// from, while still using the same canonical spellings as the spec mask.
	stripped := foldReceiptHex(t, raw)
	pruned, err := wire.PruneMethod(MethodGetTransactionReceipt, stripped)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(pruned), &obj); err != nil {
		t.Fatal(err)
	}
	if _, present := obj["from"]; !present {
		t.Fatal("mask no longer retains from, so deleting it proves nothing")
	}
	want, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	got, err := normalizeReceiptObject(stripped)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != pruned {
		t.Fatalf("pruned key is not a JSON fixed point\nwant %s\ngot  %s", pruned, want)
	}
	if got != pruned {
		t.Fatalf("key is not the pruned key\nwant %s\ngot  %s", pruned, got)
	}

	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{name: "null", raw: "null", want: "null"},
		{name: "spaced null", raw: " \n null\t", want: "null"},
		{name: "empty input", wantErr: "receipt: incomplete object"},
		{name: "empty object", raw: `{}`, wantErr: "receipt: incomplete object"},
		{name: "array", raw: `[]`, wantErr: "receipt: incomplete object"},
		{name: "string", raw: `"receipt"`, wantErr: "receipt: incomplete object"},
		{name: "number", raw: `1`, wantErr: "receipt: incomplete object"},
		{name: "boolean", raw: `true`, wantErr: "receipt: incomplete object"},
		{name: "malformed JSON", raw: `{"status":`, wantErr: "receipt: incomplete object"},
		{name: "trailing value", raw: `{"status":"0x1","logs":[]} null`, wantErr: "receipt: incomplete object"},
		{name: "single official field", raw: `{"status":"0x1"}`, wantErr: "receipt: incomplete object"},
		{name: "single vendor field", raw: `{"vendor":true}`, wantErr: "receipt: incomplete object"},
		{name: "duplicate is still one field", raw: `{"status":"0x1","status":"0x2"}`, wantErr: "receipt: incomplete object"},
		{name: "all fields masked", raw: `{"vendorA":1,"vendorB":2}`, want: `{}`},
		{name: "one retained field", raw: `{"vendor":true,"status":"0X01"}`, want: `{"status":"0x01"}`},
		{name: "retained nulls", raw: `{"status":null,"logs":null}`, want: `{"logs":null,"status":null}`},
		{name: "duplicate retained field", raw: `{"status":"0x1","logs":[],"status":"0X02"}`, want: `{"logs":[],"status":"0x02"}`},
		{name: "large integer", raw: `{"gasUsed":900719925474099312345678901234567890,"logs":[]}`, want: `{"gasUsed":900719925474099312345678901234567890,"logs":[]}`},
		{name: "number spellings", raw: `{"gasUsed":1.2300E+09,"status":-0,"transactionIndex":1e+9999}`, want: `{"gasUsed":1.2300E+09,"status":-0,"transactionIndex":1e+9999}`},
		{name: "nested logs and escapes", raw: `{"\u0073tatus":"0XAB","logs":[{"topics":["0XCD","0X00"],"address":"0XAB","data":"<>&\u2028\u2029","vendor":true},{"data":"\"\\\n","topics":[]}],"vendor":true}`, want: `{"logs":[{"address":"0xab","data":"\u003c\u003e\u0026\u2028\u2029","topics":["0xcd","0x00"]},{"data":"\"\\\n","topics":[]}],"status":"0xab"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := json.RawMessage(tc.raw)
			got, err := normalizeReceiptObject(input)
			if string(input) != tc.raw {
				t.Fatal("normalization changed the input")
			}
			if tc.wantErr != "" {
				if got != "" || err == nil || err.Error() != tc.wantErr {
					t.Fatalf("key = %q, error = %v; want empty key and %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("key = %s, error = %v; want %s", got, err, tc.want)
			}
			if got == "null" {
				return
			}
			pruned, err := wire.PruneMethod(MethodGetTransactionReceipt, input)
			if err != nil || pruned != tc.want {
				t.Fatalf("pruned key = %s, error = %v; want %s", pruned, err, tc.want)
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal([]byte(pruned), &obj); err != nil {
				t.Fatal(err)
			}
			roundTrip, err := json.Marshal(obj)
			if err != nil || string(roundTrip) != pruned {
				t.Fatalf("pruned key changed on JSON round-trip: %s -> %s, %v", pruned, roundTrip, err)
			}
		})
	}
}

func assertNoUppercaseHex(t *testing.T, key string) {
	t.Helper()
	var walk func(any)
	walk = func(node any) {
		switch item := node.(type) {
		case map[string]any:
			for _, child := range item {
				walk(child)
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		case string:
			if len(item) < 2 || !strings.EqualFold(item[:2], "0x") {
				return
			}
			if item != strings.ToLower(item) {
				t.Fatalf("key holds non-canonical hex %q", item)
			}
		}
	}
	var decoded any
	if err := json.Unmarshal([]byte(key), &decoded); err != nil {
		t.Fatal(err)
	}
	walk(decoded)
}
