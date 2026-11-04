package wss

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestGorillaInteroperability(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		var request struct {
			JSONRPC string            `json:"jsonrpc"`
			ID      json.RawMessage   `json:"id"`
			Method  string            `json:"method"`
			Params  []json.RawMessage `json:"params"`
		}
		if err := conn.ReadJSON(&request); err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if request.JSONRPC != "2.0" {
			t.Errorf("jsonrpc = %q, want 2.0", request.JSONRPC)
		}
		if request.Method != "eth_getBalance" {
			t.Errorf("method = %q, want eth_getBalance", request.Method)
		}
		if len(request.ID) == 0 || string(request.ID) == "null" {
			t.Errorf("request id missing: %+v", request)
		}
		if len(request.Params) != 2 {
			t.Fatalf("params = %d, want 2", len(request.Params))
		}
		if got, want := string(request.Params[0]), `"0x0000000000000000000000000000000000000001"`; got != want {
			t.Errorf("address param = %s, want %s", got, want)
		}
		if got, want := string(request.Params[1]), `"latest"`; got != want {
			t.Errorf("block param = %s, want %s", got, want)
		}
		if err := conn.WriteJSON(struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Result  string          `json:"result"`
		}{
			JSONRPC: "2.0",
			ID:      request.ID,
			Result:  "0x64",
		}); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	transportValue, err := NewDialer().DialContext(
		context.Background(),
		"ws"+strings.TrimPrefix(server.URL, "http"),
	)
	if err != nil {
		t.Fatalf("dial gorilla peer: %v", err)
	}
	t.Cleanup(transportValue.Close)

	var result json.RawMessage
	if err := transportValue.CallContext(
		context.Background(),
		&result,
		"eth_getBalance",
		"0x0000000000000000000000000000000000000001",
		"latest",
	); err != nil {
		t.Fatalf("CallContext: %v", err)
	}
	if string(result) != `"0x64"` {
		t.Fatalf("result = %s, want %q", result, "0x64")
	}
}
