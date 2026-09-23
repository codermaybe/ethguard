package rpcsample

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Revert-data samples are an offline replay of one capture from two mainnet
// HTTPS endpoints. They show that one message, "execution reverted", arrived
// with data missing, with "0x", and with non-empty hex. They are not a
// census and do not show code -32000 or structured data; those shapes are
// locked by method tests, not by this fixture.
const revertSampleDir = "testdata/revert-data"

func TestRevertDataSamplesReplayWithoutEndpoints(t *testing.T) {
	records := loadSampleRecords(t, revertSampleDir)
	byRequest := map[string][]Record{}
	var empty []Record
	for _, record := range records {
		byRequest[record.RequestID] = append(byRequest[record.RequestID], record)
		if len(record.RequestID) >= len("revert-empty") && record.RequestID[:len("revert-empty")] == "revert-empty" {
			empty = append(empty, record)
		}
	}
	if !hasRevertData(t, empty, "missing") || !hasRevertData(t, empty, "0x") {
		t.Fatal("empty-revert capture lost the missing-versus-empty split")
	}
	assertRevertData(t, byRequest["revert-hex"], "0xdeadbeef", 2)
	assertRevertPrefix(t, byRequest["revert-error-string"], "0x08c379a0", 2)
	for _, record := range byRequest["revert-hex"] {
		if revertMessage(t, record) != "execution reverted" {
			t.Fatalf("%s message = %q", record.Source.ID, revertMessage(t, record))
		}
	}
}

func hasRevertData(t *testing.T, records []Record, want string) bool {
	t.Helper()
	for _, record := range records {
		if revertData(t, record) == want && revertMessage(t, record) == "execution reverted" {
			return true
		}
	}
	return false
}

func assertRevertData(t *testing.T, records []Record, want string, min int) {
	t.Helper()
	n := 0
	for _, record := range records {
		if revertData(t, record) == want {
			n++
		}
	}
	if n < min {
		t.Fatalf("data %s count = %d, want at least %d", want, n, min)
	}
}

func assertRevertPrefix(t *testing.T, records []Record, prefix string, min int) {
	t.Helper()
	n := 0
	for _, record := range records {
		data := revertData(t, record)
		if len(data) >= len(prefix) && data[:len(prefix)] == prefix {
			n++
		}
	}
	if n < min {
		t.Fatalf("prefix %s count = %d, want at least %d", prefix, n, min)
	}
}

func revertMessage(t *testing.T, record Record) string {
	t.Helper()
	return revertError(t, record).Message
}

func revertData(t *testing.T, record Record) string {
	t.Helper()
	err := revertError(t, record)
	if len(err.Data) == 0 || string(err.Data) == "null" {
		return "missing"
	}
	var text string
	if json.Unmarshal(err.Data, &text) != nil {
		t.Fatalf("%s/%s data is not a string: %s", record.RequestID, record.Source.ID, err.Data)
	}
	return text
}

type sampleError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func revertError(t *testing.T, record Record) sampleError {
	t.Helper()
	var doc struct {
		Error *sampleError `json:"error"`
	}
	if err := json.Unmarshal([]byte(record.Response.Body), &doc); err != nil || doc.Error == nil {
		t.Fatalf("%s/%s: %v body %s", record.RequestID, record.Source.ID, err, record.Response.Body)
	}
	return *doc.Error
}

const objectSampleDir = "testdata/block-receipt-tx"

func TestBlockReceiptTxSamplesReplayWithoutEndpoints(t *testing.T) {
	records := loadSampleRecords(t, objectSampleDir)
	byRequest := map[string][]Record{}
	for _, record := range records {
		if record.Network.Fork != "unspecified" || record.Network.ChainID != 1 {
			t.Fatalf("%s network = %+v", record.RequestID, record.Network)
		}
		byRequest[record.RequestID] = append(byRequest[record.RequestID], record)
	}
	assertResult(t, byRequest["block-empty-number"], 2)
	assertResult(t, byRequest["block-filled-number"], 2)
	assertResult(t, byRequest["tx-one"], 2)
	assertResult(t, byRequest["receipt-one"], 2)
	assertJSONNull(t, byRequest["block-null"], 2)
	assertJSONNull(t, byRequest["tx-null"], 2)
	assertJSONNull(t, byRequest["receipt-null"], 2)
	for _, record := range byRequest["receipts-empty"] {
		if resultText(t, record) != "[]" {
			t.Fatalf("%s/%s = %s, want []", record.RequestID, record.Source.ID, resultText(t, record))
		}
	}
	for _, record := range byRequest["block-empty-number"] {
		var block struct {
			Transactions []json.RawMessage `json:"transactions"`
			Uncles       []json.RawMessage `json:"uncles"`
		}
		if err := json.Unmarshal(json.RawMessage(resultText(t, record)), &block); err != nil {
			t.Fatal(err)
		}
		if len(block.Transactions) != 0 || len(block.Uncles) != 0 {
			t.Fatalf("empty block was not empty: txs %d uncles %d", len(block.Transactions), len(block.Uncles))
		}
	}
}

func assertJSONNull(t *testing.T, records []Record, min int) {
	t.Helper()
	n := 0
	for _, record := range records {
		if resultText(t, record) == "null" {
			n++
		}
	}
	if n < min {
		t.Fatalf("null count = %d, want at least %d", n, min)
	}
}

func resultText(t *testing.T, record Record) string {
	t.Helper()
	var doc struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(record.Response.Body), &doc); err != nil {
		t.Fatal(err)
	}
	return string(doc.Result)
}

func loadSampleRecords(t *testing.T, dir string) []Record {
	t.Helper()
	indexRaw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	rejectURL(t, dir+"/index.json", indexRaw)
	var index Index
	if err := json.Unmarshal(indexRaw, &index); err != nil {
		t.Fatal(err)
	}
	if index.Schema != IndexSchema || len(index.Records) == 0 {
		t.Fatalf("%s index header = %+v", dir, index)
	}
	var records []Record
	for _, entry := range index.Records {
		path := filepath.Join(dir, filepath.FromSlash(entry.File))
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
			t.Fatal(err)
		}
		if record.Source.Endpoint != "[REDACTED]" || record.Schema != RecordSchema {
			t.Fatalf("%s identity = %+v", entry.File, record.Source)
		}
		records = append(records, record)
	}
	return records
}
