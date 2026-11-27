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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":999,"result":"0x2a"}`)); err != nil && r.Context().Err() == nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	manifest := Manifest{
		ChainID:     0,
		HTTPAdapter: HTTPAdapterNative,
		Selection: &Selection{
			Selected: []SelectedMember{{SourceID: "probe", URL: srv.URL}},
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
	if !errors.Is(err, ethguard.ErrAllSourcesDown) || len(emissions) != 1 || !errors.Is(emissions[0].Response.Err(), rpcwire.ErrIDMismatch) {
		t.Fatalf("native executor did not reject the mismatched id: %+v, %v", emissions, err)
	}
}
