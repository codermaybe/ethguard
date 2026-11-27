package rpctest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInvalidFixtureReturnsHTTPFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler Handler
	}{
		{name: "unsupported result", handler: Value(make(chan int))},
		{name: "invalid result JSON", handler: Value(json.RawMessage(`{`))},
		{name: "invalid error data", handler: func(Call) (any, error) {
			return nil, RPCErrorWithData(3, "reverted", json.RawMessage(`{`), true)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := NewNode(0)
			t.Cleanup(node.Close)
			node.Handle("broken", tc.handler)
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, node.URL(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"broken","params":[]}`))
			if err != nil {
				t.Fatal(err)
			}
			response, err := node.srv.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close() //nolint:errcheck // Read-only fixture response; ReadAll owns data errors.
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "broken: encode fixture response:") {
				t.Fatalf("invalid fixture = HTTP %d, %q", response.StatusCode, body)
			}
			if strings.Contains(string(body), `"jsonrpc"`) {
				t.Fatalf("fixture failure became a protocol answer: %s", body)
			}
		})
	}
}

func TestFixtureWriteFailureRespectsCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			var diagnostics bytes.Buffer
			node := &Node{handlers: map[string]Handler{"m": Value("ok")}, srv: &httptest.Server{Config: &http.Server{ErrorLog: log.New(&diagnostics, "", 0)}}}
			ctx, cancel := context.WithCancel(t.Context())
			if canceled {
				cancel()
			}
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"m"}`))
			writer := &fixtureFailureWriter{ResponseRecorder: httptest.NewRecorder(), partial: partial}
			node.serve(writer, request)
			cancel()
			if canceled && diagnostics.Len() != 0 {
				t.Fatalf("canceled response logged a fixture failure: %s", diagnostics.String())
			}
			if !canceled && !strings.Contains(diagnostics.String(), "m: write fixture response: fixture connection failed") {
				t.Fatalf("write failure lost its method/error diagnostic: %s", diagnostics.String())
			}
			if partial && writer.Body.Len() == 0 {
				t.Fatal("partial writer did not accept any response bytes")
			}
		}
	}
}

type fixtureFailureWriter struct {
	*httptest.ResponseRecorder
	partial bool
}

func (w *fixtureFailureWriter) Write(p []byte) (int, error) {
	var n int
	if w.partial {
		n, _ = w.Body.Write(p[:len(p)/2])
	}
	return n, errors.New("fixture connection failed")
}
