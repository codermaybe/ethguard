package livecanary

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/pool"
)

// TestExecutorHTTPAdapterThreadsIntoClient locks the native adapter selection
// used for newly built clients.
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
		if len(emissions) != 1 || !errors.Is(emissions[0].Response.Err(), rpcwire.ErrIDMismatch) {
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
		if len(emissions) != 1 || emissions[0].Response.Err() == nil {
			t.Fatalf("emissions = %+v, want one failed emission", emissions)
		}
		if !errors.Is(emissions[0].Response.Err(), rpcwire.ErrIDMismatch) {
			t.Fatalf("emission err = %v, want rpcwire.ErrIDMismatch", emissions[0].Response.Err())
		}
	})
}

// Historical manifests preserve their geth provenance and cannot be rerun
// with the native adapter under a relabeled identity.
func TestExecutorRejectsHistoricalGethAdapter(t *testing.T) {
	manifest := testManifest(t)
	manifest.HTTPAdapter = "geth"
	e := Executor{}
	_, err := e.clientFor(manifest)
	if err == nil || !strings.Contains(err.Error(), "geth-adapter manifests is unsupported") {
		t.Fatalf("clientFor error = %v, want explicit unsupported error", err)
	}

	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Rerun(context.Background(), manifestBytes, e); err == nil || !strings.Contains(err.Error(), "geth-adapter manifests are unsupported") {
		t.Fatalf("Rerun error = %v, want explicit unsupported error", err)
	}
}
