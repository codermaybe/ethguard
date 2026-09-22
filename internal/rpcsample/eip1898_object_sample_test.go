package rpcsample

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EIP-1898 object samples live in testdata, not in rpctest and not inline in
// a method test. rpctest is the fake node. The records were captured from
// chainlist Ethereum mainnet HTTPS endpoints that advertise no tracking and
// carry no URL path or query. Endpoints are removed before the files are
// written. This test replays that fixture offline. It is not a live
// recollection and not a provider-compatibility verdict.
const eip1898SampleDir = "testdata/eip1898-object"

func TestEIP1898ObjectSamplesReplayWithoutEndpoints(t *testing.T) {
	indexRaw, err := os.ReadFile(filepath.Join(eip1898SampleDir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	rejectURL(t, "index.json", indexRaw)
	var index Index
	if err := json.Unmarshal(indexRaw, &index); err != nil {
		t.Fatal(err)
	}
	if index.Schema != IndexSchema || index.Network.ChainID != 1 || len(index.Records) == 0 {
		t.Fatalf("index header = %+v", index)
	}
	byRequest := map[string][]Record{}
	for _, entry := range index.Records {
		path := filepath.Join(eip1898SampleDir, filepath.FromSlash(entry.File))
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rejectURL(t, entry.File, raw)
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			t.Fatalf("%s sha256 changed", entry.File)
		}
		var record Record
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatalf("%s: %v", entry.File, err)
		}
		if record.Schema != RecordSchema || record.Source.Endpoint != "[REDACTED]" || record.Source.ID == "" {
			t.Fatalf("%s identity = %+v", entry.File, record.Source)
		}
		if record.Method != entry.Method || record.RequestID != entry.RequestID {
			t.Fatalf("%s index/record mismatch", entry.File)
		}
		var wire WireRequest
		if err := json.Unmarshal([]byte(record.Request), &wire); err != nil {
			t.Fatalf("%s request: %v", entry.File, err)
		}
		if wire.Method != record.Method || !paramsContainObject(wire.Params) {
			t.Fatalf("%s is not an EIP-1898 object request: %s", entry.File, record.Request)
		}
		byRequest[record.RequestID] = append(byRequest[record.RequestID], record)
	}

	// Pins are what this capture showed. A later capture may differ; do not
	// read a count as "all providers".
	assertResult(t, byRequest["balance-latest-object"], 3)
	assertResult(t, byRequest["balance-pending-object"], 3)
	assertResult(t, byRequest["estimate-latest-object"], 3)
	assertResult(t, byRequest["estimate-pending-object"], 3)
	assertAccessListResult(t, byRequest["access-latest-object"], 2)
	assertJSONRPCCode(t, byRequest["balance-mixed-object"], -32602, 3)
	if !hasClass(t, byRequest["balance-unknown-field"], "result", 0) || !hasClass(t, byRequest["balance-unknown-field"], "jsonrpc", -32602) {
		t.Fatal("unknown-field capture lost the accept/reject split")
	}
	if !hasClass(t, byRequest["balance-safe-canonical"], "result", 0) || !hasClass(t, byRequest["balance-safe-canonical"], "jsonrpc", -32602) {
		t.Fatal("requireCanonical capture lost the accept/reject split")
	}
	if !hasClass(t, byRequest["storage-number-object"], "result", 0) || !hasClass(t, byRequest["storage-number-object"], "jsonrpc", -32603) {
		t.Fatal("storage object capture should show both a stored word and pruned-state errors")
	}
	if !hasClass(t, byRequest["balance-hash-canonical"], "result", 0) || !hasClass(t, byRequest["balance-hash-canonical"], "jsonrpc", -32603) {
		t.Fatal("blockHash object capture should show both a result and a pruned-state error")
	}
}

func rejectURL(t *testing.T, name string, raw []byte) {
	t.Helper()
	text := string(raw)
	for _, needle := range []string{"http://", "https://", "ws://", "wss://"} {
		if strings.Contains(text, needle) {
			t.Fatalf("%s contains %s", name, needle)
		}
	}
}

func paramsContainObject(raw json.RawMessage) bool {
	var params []any
	if err := json.Unmarshal(raw, &params); err != nil {
		return false
	}
	for _, param := range params {
		if _, ok := param.(map[string]any); ok {
			return true
		}
	}
	return false
}

func outcome(t *testing.T, record Record) (string, int) {
	t.Helper()
	if record.Response.Body == "" {
		return "empty", 0
	}
	var doc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(record.Response.Body), &doc); err != nil {
		t.Fatalf("%s/%s body: %v", record.RequestID, record.Source.ID, err)
	}
	if doc.Error != nil {
		return "jsonrpc", doc.Error.Code
	}
	if len(doc.Result) == 0 || string(doc.Result) == "null" {
		return "empty", 0
	}
	return "result", 0
}

func assertResult(t *testing.T, records []Record, min int) {
	t.Helper()
	if len(records) == 0 {
		t.Fatal("missing records")
	}
	n := 0
	for _, record := range records {
		if kind, _ := outcome(t, record); kind == "result" {
			n++
		}
	}
	if n < min {
		t.Fatalf("%s results = %d, want at least %d", records[0].RequestID, n, min)
	}
}

func assertAccessListResult(t *testing.T, records []Record, min int) {
	t.Helper()
	n := 0
	for _, record := range records {
		kind, _ := outcome(t, record)
		if kind != "result" {
			continue
		}
		var doc struct {
			Result struct {
				AccessList json.RawMessage `json:"accessList"`
				GasUsed    string          `json:"gasUsed"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(record.Response.Body), &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Result.AccessList) == 0 || !strings.HasPrefix(doc.Result.GasUsed, "0x") {
			t.Fatalf("%s result is not an access-list payload", record.Source.ID)
		}
		n++
	}
	if n < min {
		t.Fatalf("access-list results = %d, want at least %d", n, min)
	}
}

func assertJSONRPCCode(t *testing.T, records []Record, code int, min int) {
	t.Helper()
	if len(records) == 0 {
		t.Fatal("missing records")
	}
	n := 0
	for _, record := range records {
		kind, got := outcome(t, record)
		if kind == "jsonrpc" && got == code {
			n++
		}
	}
	if n < min {
		t.Fatalf("%s code %d count = %d, want at least %d", records[0].RequestID, code, n, min)
	}
}

func hasClass(t *testing.T, records []Record, want string, code int) bool {
	t.Helper()
	for _, record := range records {
		kind, got := outcome(t, record)
		if kind == want && (want != "jsonrpc" || got == code) {
			return true
		}
	}
	return false
}
