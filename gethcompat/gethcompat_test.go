package gethcompat_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/gethcompat"
	"github.com/codermaybe/ethguard/pool"
)

// The compatibility seam builds a client whose pool dials through the
// go-ethereum adapter. The mismatched-id response distinguishes the channels:
// the compatibility adapter accepts it (audited baseline quirk), the native
// default rejects it by contract.
func TestNewBuildsGethChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":999,"result":"0x2a"}`))
	}))
	t.Cleanup(srv.Close)

	client, err := gethcompat.New(config.Config{
		Upstreams: []config.Upstream{{ID: "a", Endpoint: srv.URL}},
	})
	if err != nil {
		t.Fatalf("gethcompat.New: %v", err)
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

// The root default stays native: selecting the geth adapter through the root
// option fails construction with a pointer to this seam.
func TestRootDefaultRejectsGethSelection(t *testing.T) {
	_, err := ethguard.New(config.Config{
		Upstreams: []config.Upstream{{ID: "a", Endpoint: "http://127.0.0.1:1"}},
	}, ethguard.WithHTTPAdapter(ethguard.HTTPAdapterGeth))
	if err == nil || !strings.Contains(err.Error(), "gethcompat") {
		t.Fatalf("New err = %v, want the gethcompat pointer", err)
	}
}
