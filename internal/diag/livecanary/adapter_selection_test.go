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
)

// Newly built clients use native HTTP for both current and omitted provenance.
func TestExecutorUsesNativeHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":999,"result":"0x2a"}`)); err != nil && r.Context().Err() == nil {
			t.Errorf("write fixture response: %v", err)
		}
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

	for _, provenance := range []string{"", HTTPAdapterNative} {
		t.Run(provenance, func(t *testing.T) {
			e := Executor{}
			current := manifest
			current.HTTPAdapter = provenance
			client, err := e.clientFor(current)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			emissions, err := client.Sample(context.Background(), "eth_chainId")
			if !errors.Is(err, ethguard.ErrAllSourcesDown) || len(emissions) != 1 || !errors.Is(emissions[0].Response.Err(), rpcwire.ErrIDMismatch) {
				t.Fatalf("native executor did not reject the mismatched id: %+v, %v", emissions, err)
			}
		})
	}
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
