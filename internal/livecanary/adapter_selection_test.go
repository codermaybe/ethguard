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
// rejected by the native default adapter and would be accepted by the
// compatibility adapter (the audited baseline quirk), which the executor
// reaches through the gethcompat seam.
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

	t.Run("default-native", func(t *testing.T) {
		e := Executor{}
		client, err := e.clientFor(manifest)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		emissions, err := client.Pool().Collect(context.Background(), "eth_chainId", nil, pool.CollectOptions{})
		if !errors.Is(err, pool.ErrAllSourcesDown) {
			t.Fatalf("default executor Collect err = %v, want the native adapter's mismatched-id rejection", err)
		}
		if len(emissions) != 1 || !errors.Is(emissions[0].Err, rpcwire.ErrIDMismatch) {
			t.Fatalf("emissions = %+v, want the native rejection", emissions)
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

// The geth selection builds through the compatibility seam: the root
// constructor refuses a bare geth adapter, so the executor's geth choice
// must compose the seam's dialer with the adapter option and stay usable.
func TestExecutorGethSelectionBuildsThroughCompatibilitySeam(t *testing.T) {
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
		Reference:   Reference{MaxLagBlocks: 8},
		Timeouts:    Timeouts{StepMS: 2000},
		HTTPAdapter: HTTPAdapterGeth,
	}
	e := Executor{}
	client, err := e.clientFor(manifest)
	if err != nil {
		t.Fatalf("geth selection via seam: %v", err)
	}
	t.Cleanup(client.Close)
	emissions, err := client.Pool().Collect(context.Background(), "eth_chainId", nil, pool.CollectOptions{})
	if err != nil {
		t.Fatalf("Collect err = %v, want the compatibility adapter's usable answer", err)
	}
	if len(emissions) != 1 || string(emissions[0].Val) != `"0x2a"` {
		t.Fatalf("emissions = %+v, want the baseline usable answer", emissions)
	}
}
