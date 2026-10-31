package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestClassifyErrorDataStates(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		state   string
		present bool
		data    string
	}{
		{name: "missing", raw: `{"code":3,"message":"revert"}`, state: "missing"},
		{name: "null", raw: `{"code":3,"message":"revert","data":null}`, state: "null", present: true, data: "null"},
		{name: "empty", raw: `{"code":3,"message":"revert","data":"0x"}`, state: "empty-hex", present: true, data: `"0x"`},
		{name: "hex", raw: `{"code":3,"message":"revert","data":"0xdeadbeef"}`, state: "value", present: true, data: `"0xdeadbeef"`},
		{name: "object", raw: `{"code":-32601,"message":"x","data":{"code":"retry"}}`, state: "value", present: true, data: `{"code":"retry"}`},
		{name: "malformed", raw: `[]`, state: "malformed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state, present, data := classifyErrorData(json.RawMessage(tc.raw))
			if state != tc.state || present != tc.present || data != tc.data {
				t.Fatalf("got state=%s present=%v data=%q, want state=%s present=%v data=%q", state, present, data, tc.state, tc.present, tc.data)
			}
		})
	}
}

func TestRedactorRemovesEndpointSecrets(t *testing.T) {
	raw := "wss://user:secret@example.test/private-key-1234?token=abc12345"
	redact := newRedactor([]string{raw}, nil)
	text := redact.text("dial " + raw + " failed")
	for _, secret := range []string{"secret", "private-key-1234", "abc12345"} {
		if strings.Contains(text, secret) {
			t.Fatalf("redacted text still contains %q: %s", secret, text)
		}
	}
	if got := sanitizeEndpoint(raw); got != "wss://example.test" {
		t.Fatalf("sanitized endpoint = %s", got)
	}
	if len(redact.scan) == 0 {
		t.Fatal("secret endpoint was not recorded for output scanning")
	}
}

func TestReplayChecksPresenceAndCallPolicy(t *testing.T) {
	dir := writeSyntheticCapture(t, []captureRecord{
		chainRecord(),
		callErrorRecord("missing", `{"code":3,"message":"execution reverted"}`),
		callErrorRecord("null", `{"code":3,"message":"execution reverted","data":null}`),
		callErrorRecord("empty-hex", `{"code":3,"message":"execution reverted","data":"0x"}`),
		callErrorRecord("value", `{"code":3,"message":"execution reverted","data":"0x08c379a0"}`),
	})
	var out bytes.Buffer
	if err := replayDirectory(dir, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "states: missing=1 null=1 empty-hex=1 value=1") {
		t.Fatalf("state summary missing:\n%s", got)
	}
	if !strings.Contains(got, "all four states observed") {
		t.Fatalf("four-state summary missing:\n%s", got)
	}
}

func TestCaptureAndReplayLocalFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(frame, &request) != nil {
				return
			}
			var response string
			switch request.Method {
			case "eth_chainId":
				response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"0x1"}`, request.ID)
			case "eth_unknownMethodProbe":
				response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}`, request.ID)
			case "eth_call":
				response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":3,"message":"execution reverted","data":"0xdeadbeef"}}`, request.ID)
			default:
				response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32602,"message":"invalid params","data":"bad input"}}`, request.ID)
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	dir := t.TempDir()
	cfg := captureConfig{Timeout: 2 * time.Second, ChainID: 1, ProviderClass: "local-test"}
	if err := capture(context.Background(), []string{endpoint}, dir, cfg); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := replayDirectory(dir, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "missing=1") || !strings.Contains(got, "value=3") {
		t.Fatalf("unexpected local capture summary:\n%s", got)
	}
	if !strings.Contains(got, "W4 exit not met") {
		t.Fatalf("local tool test should not claim a provider four-state exit:\n%s", got)
	}
}

func writeSyntheticCapture(t *testing.T, records []captureRecord) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "records"), 0o700); err != nil {
		t.Fatal(err)
	}
	index := captureIndex{
		Schema:        captureSchema,
		ObservedAt:    time.Now().UTC(),
		ChainID:       1,
		ProviderClass: "tool-test",
		EvidenceLevel: "tool-test",
	}
	for i := range records {
		record := &records[i]
		record.Schema = captureSchema
		record.Provider = "p01"
		record.ProviderClass = "tool-test"
		record.Endpoint = "wss://example.test"
		record.RequestID = 1
		record.EvidenceLevel = "tool-test"
		encoded, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, '\n')
		rel := filepath.ToSlash(filepath.Join("records", record.Scenario+"--p01.json"))
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(encoded)
		index.Records = append(index.Records, indexEntry{
			Provider:  record.Provider,
			Scenario:  record.Scenario,
			Method:    record.Method,
			RequestID: record.RequestID,
			DataState: record.DataState,
			File:      rel,
			SHA256:    hex.EncodeToString(sum[:]),
		})
	}
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func chainRecord() captureRecord {
	return captureRecord{
		Scenario:      "chain-id",
		Method:        "eth_chainId",
		Request:       `{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`,
		ResponseFrame: `{"jsonrpc":"2.0","id":1,"result":"0x1"}`,
		Result:        `"0x1"`,
		DataState:     "no-error",
	}
}

func callErrorRecord(scenario, errorBody string) captureRecord {
	frame := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"error":%s}`, errorBody)
	state, present, data := classifyErrorData(json.RawMessage(errorBody))
	return captureRecord{
		Scenario:      scenario,
		Method:        "eth_call",
		Request:       `{"jsonrpc":"2.0","id":1,"method":"eth_call","params":[]}`,
		ResponseFrame: frame,
		DataState:     state,
		DataPresent:   present,
		DataRaw:       data,
	}
}
