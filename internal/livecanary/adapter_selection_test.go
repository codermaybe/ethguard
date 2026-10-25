package livecanary

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/pool"
)

// TestExecutorHTTPAdapterThreadsIntoClient locks that the executor's adapter
// selection reaches the built client: the same mismatched-id response is
// accepted by the default Geth adapter and rejected when the executor selects
// the native adapter.
func TestExecutorHTTPAdapterThreadsIntoClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":999,"result":"0x2a"}`))
	}))
	t.Cleanup(srv.Close)

	manifest := Manifest{
		ChainID: 0,
		Selection: &Selection{
			Selected: []SelectedMember{{SourceID: "probe", URL: srv.URL}},
		},
		Reference: Reference{MaxLagBlocks: 8},
		Timeouts:  Timeouts{StepMS: 2000},
	}

	t.Run("default-geth", func(t *testing.T) {
		e := Executor{}
		client, err := e.clientFor(manifest)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		if _, err := client.Pool().Collect(context.Background(), "eth_chainId", nil, pool.CollectOptions{}); err != nil {
			t.Fatalf("default executor Collect err = %v, want the baseline usable answer", err)
		}
	})

	t.Run("native-selected", func(t *testing.T) {
		e := Executor{HTTPAdapter: ethguard.HTTPAdapterNative}
		nativeManifest := manifest
		nativeManifest.HTTPAdapter = string(ethguard.HTTPAdapterNative)
		client, err := e.clientFor(nativeManifest)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		emissions, err := client.Pool().Collect(context.Background(), "eth_chainId", nil, pool.CollectOptions{})
		if !errors.Is(err, pool.ErrAllSourcesDown) {
			t.Fatalf("native executor Collect err = %v, want rejection", err)
		}
		if len(emissions) != 1 || emissions[0].Err == nil {
			t.Fatalf("emissions = %+v, want one failed emission", emissions)
		}
		if !errors.Is(emissions[0].Err, rpcwire.ErrIDMismatch) {
			t.Fatalf("emission err = %v, want rpcwire.ErrIDMismatch", emissions[0].Err)
		}
	})
}
