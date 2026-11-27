package ethguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcwire"
)

// A mismatched response ID makes native adapter selection observable.
func wrongIDServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":999,"result":"0x2a"}`)); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collectOnce(t *testing.T, c *Client) ([]pool.Emission, error) {
	t.Helper()
	return c.Pool().Collect(context.Background(), "eth_chainId", nil, pool.CollectOptions{})
}

func TestDefaultClientUsesNativeAdapter(t *testing.T) {
	c, err := New(config.Config{Upstreams: []config.Upstream{{ID: "a", Endpoint: wrongIDServer(t).URL}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	emissions, err := collectOnce(t, c)
	if err == nil {
		t.Fatalf("Collect = %+v, want the default client to run the native adapter (mismatched id rejected)", emissions)
	}
	if len(emissions) != 1 || emissions[0].Response.Answer.Result != nil {
		t.Fatalf("emissions = %+v, want the mismatched-id rejection", emissions)
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
		if e.Response.Err() != nil {
			nativeErr, found = e, true
		}
	}
	if !found {
		t.Fatalf("emissions = %+v, want a failed emission", emissions)
	}
	if !errors.Is(nativeErr.Response.Err(), rpcwire.ErrIDMismatch) {
		t.Fatalf("emission err = %v, want rpcwire.ErrIDMismatch", nativeErr.Response.Err())
	}
}

func TestWithHTTPAdapterUnknownNameFailsConstruction(t *testing.T) {
	cfg := config.Config{Upstreams: []config.Upstream{{ID: "a", Endpoint: "http://127.0.0.1:1"}}}
	for _, name := range []HTTPAdapter{"websocket", "geth"} {
		t.Run(string(name), func(t *testing.T) {
			for _, options := range [][]Option{{WithHTTPAdapter(name)}, {WithHTTPAdapter(name), DialerOption(stubDialer{})}} {
				if c, err := New(cfg, options...); err == nil {
					c.Close()
					t.Fatalf("New accepted unknown adapter %q", name)
				}
			}
		})
	}
}
