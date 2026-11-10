package wire_test

// Parity between the generated execution-apis layer and the hand-written
// protected encoders. The .io conformance vectors are the shared truth:
// every result-bearing vector is parsed by the generated types and re-encoded;
// the bytes must equal what the matching protected encoder produces. The
// encoded byte stream is the project's comparison key (return-value-design.md
// section 2), so byte equality here is the interlock that lets the generated
// layer stand in for the hand-written one.
//
// Error-message text is intentionally not compared: the layers share the
// accept/reject semantics but not the wording.

import (
	"encoding/json"
	"io/fs"
	"path"
	"sort"
	"testing"

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/rpc/internal/objects/accesslist"
	"github.com/codermaybe/ethguard/rpc/internal/objects/feehistory"
	"github.com/codermaybe/ethguard/rpc/internal/objects/log"
	receiptobjects "github.com/codermaybe/ethguard/rpc/internal/objects/receipt"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// resultVectors collects every non-error, non-null result body for one
// method, keyed by vector file base name.
func resultVectors(t *testing.T, method string) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	err := fs.WalkDir(spec.VectorsFS(), ".", func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path.Ext(file) != ".io" {
			return nil
		}
		data, err := fs.ReadFile(spec.VectorsFS(), file)
		if err != nil {
			t.Fatal(err)
		}
		if !walkHasMethod(data, method) {
			return nil
		}
		result, valid, err := walkResult(data)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if !valid {
			return nil // error or null response: not a parity input
		}
		out[path.Base(file)] = result
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("no parity vectors found for %s", method)
	}
	return out
}

// walkHasMethod reports whether the .io file carries the method.
func walkHasMethod(data []byte, method string) bool {
	var found bool
	for _, line := range splitLines(data) {
		if len(line) >= 2 && line[0] == '>' && line[1] == '>' {
			for _, probe := range []string{`"method":"`} {
				at := indexOf(line, probe+"")
				if at < 0 {
					continue
				}
				rest := line[at+len(probe):]
				if len(rest) >= len(method) && string(rest[:len(method)]) == method {
					found = true
				}
			}
		}
	}
	return found
}

// walkResult extracts the JSON result from the .io response line. The second
// return is false for error responses and formal null results.
func walkResult(data []byte) (json.RawMessage, bool, error) {
	var response json.RawMessage
	for _, line := range splitLines(data) {
		if len(line) >= 2 && line[0] == '<' && line[1] == '<' {
			response = json.RawMessage(line[2:])
		}
	}
	if len(response) == 0 {
		return nil, false, nil
	}
	var body struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response, &body); err != nil {
		return nil, false, err
	}
	if body.Error != nil {
		return nil, false, nil
	}
	if wire.IsJSONNull(body.Result) {
		return nil, false, nil
	}
	return body.Result, true, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for at, b := range data {
		if b == '\n' {
			if at > start {
				lines = append(lines, data[start:at])
			}
			start = at + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

func indexOf(haystack []byte, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return i
		}
	}
	return -1
}

// compareBytes asserts byte equality between the generated re-encode and the
// protected encoder, with both accept/reject and content covered.
func compareBytes(t *testing.T, file string, got json.RawMessage, gotErr error, want json.RawMessage, wantErr error) {
	t.Helper()
	if wantErr != nil && gotErr != nil {
		return // both reject: parity for admission is already demonstrated
	}
	if wantErr != nil {
		t.Fatalf("%s: protected encoder rejects a payload the generated layer admits", file)
	}
	if gotErr != nil {
		t.Fatalf("%s: generated layer rejects a payload the protected encoder admits: %v", file, gotErr)
	}
	if string(got) != string(want) {
		t.Fatalf("%s: canonical bytes diverge:\n generated: %s\n protected: %s", file, got, want)
	}
}

func TestGeneratedLogsMatchProtectedLogEncoder(t *testing.T) {
	for file, raw := range resultVectors(t, "eth_getLogs") {
		wireLogs, wireErr := wire.NewGetLogsResult(raw)
		var wireOut json.RawMessage
		if wireErr == nil {
			wireOut, wireErr = wire.MarshalNoEscape(wireLogs)
		}
		protected, protectedErr := log.Encode(raw)
		compareBytes(t, file, wireOut, wireErr, protected, protectedErr)
	}
}

func TestGeneratedReceiptMatchesProtectedReceiptEncoder(t *testing.T) {
	receipts := map[string]json.RawMessage{}
	walkFS(t, "eth_getTransactionReceipt", func(file string, result json.RawMessage, isNull bool) {
		if isNull {
			receipt, err := wire.NewGetTransactionReceiptResult(json.RawMessage("null"))
			if err != nil {
				t.Fatalf("%s: null receipt: %v", file, err)
			}
			if receipt != nil {
				t.Fatalf("%s: null receipt must stay nil", file)
			}
			return
		}
		receipts[file] = result
	})
	if len(receipts) == 0 {
		t.Fatal("no receipt parity vectors")
	}
	for file, raw := range receipts {
		wireReceipt, wireErr := wire.NewGetTransactionReceiptResult(raw)
		var wireOut json.RawMessage
		if wireErr == nil {
			if wireReceipt == nil {
				t.Fatalf("%s: non-null receipt must parse", file)
			}
			wireOut, wireErr = wire.MarshalNoEscape(wireReceipt)
		}
		protected, protectedErr := receiptobjects.Encode(raw)
		compareBytes(t, file, wireOut, wireErr, protected, protectedErr)
	}
}

func TestGeneratedAccessListMatchesProtectedAccessListEncoder(t *testing.T) {
	for file, raw := range resultVectors(t, "eth_createAccessList") {
		wireList, wireErr := wire.NewAccessListResult(raw)
		var wireOut json.RawMessage
		if wireErr == nil {
			wireOut, wireErr = wire.MarshalNoEscape(wireList)
		}
		protected, protectedErr := accesslist.Encode(raw)
		compareBytes(t, file, wireOut, wireErr, protected, protectedErr)
	}
}

func TestGeneratedFeeHistoryMatchesProtectedFeeHistoryEncoder(t *testing.T) {
	for file, raw := range resultVectors(t, "eth_feeHistory") {
		wireResult, wireErr := wire.NewFeeHistoryResult(raw)
		var wireOut json.RawMessage
		if wireErr == nil {
			wireOut, wireErr = wire.MarshalNoEscape(wireResult)
		}
		protected, protectedErr := feehistory.Encode(raw)
		compareBytes(t, file, wireOut, wireErr, protected, protectedErr)
	}
}

func walkFS(t *testing.T, method string, visit func(file string, result json.RawMessage, isNull bool)) {
	t.Helper()
	names := make([]string, 0)
	files := map[string][]byte{}
	err := fs.WalkDir(spec.VectorsFS(), ".", func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path.Ext(file) != ".io" {
			return nil
		}
		data, err := spec.VectorsFS().(fs.ReadFileFS).ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !walkHasMethod(data, method) {
			return nil
		}
		names = append(names, file)
		files[file] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, file := range names {
		data := files[file]
		response := lastResponse(data)
		if response == nil {
			t.Fatalf("%s: no response line", file)
			continue
		}
		var body struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(response, &body); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		visit(file, body.Result, wire.IsJSONNull(body.Result))
	}
}

func lastResponse(data []byte) json.RawMessage {
	var found json.RawMessage
	for _, line := range splitLines(data) {
		if len(line) >= 2 && line[0] == '<' && line[1] == '<' {
			found = json.RawMessage(line[2:])
		}
	}
	return found
}
