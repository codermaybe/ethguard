package log

import (
	"encoding/json"
	"strings"
	"testing"
)

// The shared log kernel has three consumer surfaces — the getLogs admission
// and comparison key, the log embedded in a transaction receipt, and the
// typed subscription projection — and they must all resolve one log the same
// way: field matching, optional members, and which error wins.

func logFixture(extra string) string {
	return `{"address":"0x` + strings.Repeat("11", 20) + `","topics":["0x` + strings.Repeat("22", 32) + `"],"data":"0xabcd","blockNumber":"0x1",` +
		`"transactionHash":"0x` + strings.Repeat("33", 32) + `","transactionIndex":"0x0",` +
		`"blockHash":"0x` + strings.Repeat("44", 32) + `","logIndex":"0x0","removed":false` + extra + `}`
}

func TestKernelRejectsCaseAliasesLikeTheBaselineMapContract(t *testing.T) {
	// The kernel's map-based member lookup matches keys exactly: a
	// case-variant spelling is not the member, and the required-field pass
	// names the first missing one.
	_, err := EncodeOne(json.RawMessage(strings.Replace(logFixture(""), `"address":"0x`, `"ADDRESS":"0x`, 1)))
	if err == nil || !strings.Contains(err.Error(), "missing address") {
		t.Fatalf("case-variant member must not resolve, got %v", err)
	}
	_, err = EncodeOne(json.RawMessage(strings.Replace(logFixture(""), `"removed":false`, `"REMOVED":false`, 1)))
	if err == nil || !strings.Contains(err.Error(), "missing removed") {
		t.Fatalf("case-variant member must not resolve, got %v", err)
	}
}

func TestKernelReportsTheFirstRequiredFieldItFindsMissing(t *testing.T) {
	// Two required members missing together: the documented order wins.
	broken := strings.Replace(logFixture(""), `"topics":`, `"x":`, 1)
	_, err := EncodeOne(json.RawMessage(broken))
	if err == nil || !strings.Contains(err.Error(), "missing topics") {
		t.Fatalf("first missing required field must be topics, got %v", err)
	}
}

func TestReceiptEmbeddedLogKeepsTheTimestampAtTheEncodeStage(t *testing.T) {
	// Receipts encode each embedded log through EncodeOne: a malformed
	// optional timestamp rejects the receipt's log at the encode stage, while
	// the admission kernel the getLogs route consults stays silent.
	_, err := EncodeOne(json.RawMessage(logFixture(`,"blockTimestamp":"oops"`)))
	if err == nil || !strings.Contains(err.Error(), "blockTimestamp") {
		t.Fatalf("receipt log encode must reject the malformed timestamp, got %v", err)
	}
	if err := Validate(json.RawMessage(logFixture(`,"blockTimestamp":"oops"`))); err != nil {
		t.Fatalf("the same log must pass the admission kernel, got %v", err)
	}
}

func TestReceiptEmbeddedLogRoundTripsCanonicalBytes(t *testing.T) {
	one, err := EncodeOne(json.RawMessage(logFixture(`,"blockTimestamp":"0x652f0c1e"`)))
	if err != nil {
		t.Fatal(err)
	}
	list, err := Encode(json.RawMessage(`[` + logFixture(`,"blockTimestamp":"0x652f0c1e"`) + `]`))
	if err != nil {
		t.Fatal(err)
	}
	// The single-log encoder and the getLogs array encoder render the same
	// member through the same projection.
	embedded, err := json.Marshal(one)
	if err != nil {
		t.Fatal(err)
	}
	var single, row map[string]json.RawMessage
	if err := json.Unmarshal(embedded, &single); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(list, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("list shape: %v", err)
	}
	row = rows[0]
	if len(single) != len(row) {
		t.Fatalf("member sets diverge: %d vs %d", len(single), len(row))
	}
	for name, value := range single {
		if string(row[name]) != string(value) {
			t.Fatalf("member %s diverged: %s vs %s", name, value, row[name])
		}
	}
}

func TestKernelDataAndTopicsKeepTheirShapes(t *testing.T) {
	// data keeps its leading zeros; topics stay an array of 32-byte hashes;
	// an empty topics array is legal.
	_, err := EncodeOne(json.RawMessage(logFixture("")))
	if err != nil {
		t.Fatalf("empty topics array must be legal, got %v", err)
	}
	_, err = EncodeOne(json.RawMessage(strings.Replace(logFixture(""), `"data":"0xabcd"`, `"data":"0x00abcd"`, 1)))
	if err != nil {
		t.Fatalf("leading-zero data must survive, got %v", err)
	}
	_, err = EncodeOne(json.RawMessage(strings.Replace(logFixture(""), `"data":"0xabcd"`, `"data":"0xabc"`, 1)))
	if err == nil || !strings.Contains(err.Error(), "data") {
		t.Fatalf("odd-length data must fail, got %v", err)
	}
}
