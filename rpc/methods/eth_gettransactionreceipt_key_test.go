package methods

import (
	"encoding/json"
	"strings"
	"testing"

	"ethguard/rpc/internal/wire"
)

// The second pass over the comparison key used to fold hex a second time.
// PruneMethod has already done it, and CanonicalizeHex only lowercases, so
// folding an already-folded key is a fixed point. This pins both halves of
// that argument: the key PruneMethod returns is already canonical hex, and
// folding it again reproduces it byte for byte. If the mask or the folder ever
// stops canonicalizing, this test is what notices.
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
	wire.CanonicalizeHex(&decoded)
	folded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(folded) != string(refolded) {
		t.Fatalf("CanonicalizeHex is not a fixed point on the key\nbefore %s\nafter  %s", refolded, folded)
	}

	// The key the method returns is that same key with from dropped, so
	// dropping the second pass removed no bytes.
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
	delete(obj, "from")
	want, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	got, err := normalizeReceiptObject(stripped)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("key is not the pruned key minus from\nwant %s\ngot  %s", want, got)
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
