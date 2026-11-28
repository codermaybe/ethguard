package livecanary

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/internal/rpcwire"
)

// Clients admitted for execution use native HTTP.
func TestExecutorUsesNativeHTTP(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":999,"result":"0x2a"}`)); err != nil && r.Context().Err() == nil {
			t.Errorf("write fixture response: %v", err)
		}
	})
	first := httptest.NewServer(handler)
	second := httptest.NewServer(handler)
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)

	manifest := Manifest{
		ChainID:     0,
		HTTPAdapter: HTTPAdapterNative,
		Selection: &Selection{
			Selected: []SelectedMember{{SourceID: "probe-1", URL: first.URL}, {SourceID: "probe-2", URL: second.URL}},
		},
		Reference: Reference{MaxLagBlocks: 8},
		Timeouts:  Timeouts{StepMS: 2000},
	}

	e := Executor{}
	client, err := e.clientFor(manifest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	emissions, err := client.Sample(context.Background(), "eth_chainId")
	if !errors.Is(err, ethguard.ErrAllSourcesDown) || len(emissions) != 2 {
		t.Fatalf("native executor did not reject the mismatched id: %+v, %v", emissions, err)
	}
	for _, emission := range emissions {
		if !errors.Is(emission.Response.Err(), rpcwire.ErrIDMismatch) {
			t.Fatalf("native emission accepted mismatched ID: %+v", emission)
		}
	}
}
