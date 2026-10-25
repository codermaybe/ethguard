package ethguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/pool"
)

// The selection tests use a response both adapters classify differently: a
// well-formed 2.0 response whose id does not match the request. The default
// Geth compatibility adapter accepts it (audited baseline quirk); the native
// HTTP adapter rejects it by contract. That makes adapter selection
// observable without protocol-specific types.

func wrongIDServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":999,"result":"0x2a"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collectOnce(t *testing.T, c *Client) ([]pool.Emission, error) {
	t.Helper()
	return c.Pool().Collect(context.Background(), "eth_chainId", nil, pool.CollectOptions{})
}

func TestDefaultClientKeepsGethAdapter(t *testing.T) {
	c, err := New(config.Config{Upstreams: []config.Upstream{{ID: "a", Endpoint: wrongIDServer(t).URL}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	emissions, err := collectOnce(t, c)
	if err != nil {
		t.Fatalf("Collect err = %v, want the default client to keep the Geth adapter", err)
	}
	if len(emissions) != 1 || string(emissions[0].Val) != `"0x2a"` {
		t.Fatalf("emissions = %+v, want the baseline usable answer", emissions)
	}
}

func TestWithHTTPAdapterNativeSelectsNativeAdapter(t *testing.T) {
	c, err := New(config.Config{Upstreams: []config.Upstream{{ID: "a", Endpoint: wrongIDServer(t).URL}}},
		WithHTTPAdapter(HTTPAdapterNative))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	emissions, err := collectOnce(t, c)
	if err == nil {
		t.Fatalf("Collect = %+v, want the native adapter to reject the mismatched response id", emissions)
	}
	var nativeErr pool.Emission
	found := false
	for _, e := range emissions {
		if e.Err != nil {
			nativeErr, found = e, true
		}
	}
	if !found {
		t.Fatalf("emissions = %+v, want a failed emission", emissions)
	}
	if !errors.Is(nativeErr.Err, rpcwire.ErrIDMismatch) {
		t.Fatalf("emission err = %v, want rpcwire.ErrIDMismatch", nativeErr.Err)
	}
}

func TestWithHTTPAdapterUnknownNameFailsConstruction(t *testing.T) {
	if _, err := New(config.Config{}, WithHTTPAdapter(HTTPAdapter("websocket"))); err == nil {
		t.Fatal("New accepted an unknown adapter name")
	}
}
