package receipt

import (
	"encoding/json"
	"strings"
	"testing"
)

// The receipt encoder embeds each log through the shared log kernel's encode
// stage, whose value extraction resolves members with the item struct's own
// case-insensitive matching: the last occurrence in the document wins for the
// canonical bytes, and a malformed alias rejects the receipt. The fixtures
// splice each alias after the canonical member it shadows — the position the
// matching rule resolves from — and state the bloom of the projected values.

// aliasReceipt builds one receipt whose single log carries aliasMember
// appended after the log's canonical members. projected states what the
// kernel resolves the log to; the receipt's bloom is that projection's.
func aliasReceipt(t *testing.T, aliasMember string, projected func(row map[string]any)) json.RawMessage {
	t.Helper()
	raw := receiptFixture(t, logFixture())
	projection := logFixture()
	if projected != nil {
		projected(projection)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	var logs []json.RawMessage
	if err := json.Unmarshal(obj["logs"], &logs); err != nil || len(logs) != 1 {
		t.Fatal(err)
	}
	logJSON := string(logs[0])
	obj["logs"] = json.RawMessage(`[` + logJSON[:len(logJSON)-1] + aliasMember + `}]`)
	obj["logsBloom"] = json.RawMessage(`"` + strings.Trim(bloomOf(projection), `"`) + `"`)
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReceiptEncodeResolvesTheLogAliasOccurrence(t *testing.T) {
	alias := `,"Address":"0x` + strings.Repeat("77", 20) + `"`
	out, err := Encode(aliasReceipt(t, alias, func(row map[string]any) {
		row["address"] = "0x" + strings.Repeat("77", 20)
	}))
	if err != nil {
		t.Fatalf("receipt encode must resolve the alias-carried address, got %v", err)
	}
	if !strings.Contains(string(out), "0x"+strings.Repeat("77", 20)) {
		t.Fatalf("the canonical receipt must carry the last occurrence's address: %s", out)
	}
}

func TestReceiptEncodeKeepsAliasCarriedTimestamp(t *testing.T) {
	out, err := Encode(aliasReceipt(t, `,"BlockTimestamp":"0x2"`, nil))
	if err != nil {
		t.Fatalf("receipt encode must keep the alias-carried timestamp, got %v", err)
	}
	if !strings.Contains(string(out), `"blockTimestamp":"0x2"`) {
		t.Fatalf("the canonical receipt must carry the timestamp: %s", out)
	}
}

func TestReceiptEncodeKeepsAliasCarriedData(t *testing.T) {
	out, err := Encode(aliasReceipt(t, `,"Data":"0xbeef"`, func(row map[string]any) {
		row["data"] = "0xbeef"
	}))
	if err != nil {
		t.Fatalf("receipt encode must keep the alias-carried data, got %v", err)
	}
	if !strings.Contains(string(out), `"data":"0xbeef"`) {
		t.Fatalf("the canonical receipt must carry the alias data: %s", out)
	}
}

func TestReceiptEncodeRejectsAliasCarriedMalformedMembers(t *testing.T) {
	for _, tc := range []struct{ name, extra string }{
		{"data", `,"Data":"oops"`},
		{"blockTimestamp", `,"BlockTimestamp":"oops"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := aliasReceipt(t, tc.extra, nil)
			if _, err := Encode(raw); err == nil {
				t.Fatalf("an alias-carried malformed %s must reject the receipt", tc.name)
			}
		})
	}
}
