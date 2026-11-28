package ethguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/rpc"
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

func collectOnce(t *testing.T, c *Client) ([]rpc.Observation, error) {
	t.Helper()
	return c.Sample(context.Background(), "eth_chainId")
}

func TestClientUsesNativeHTTP(t *testing.T) {
	for _, transport := range []config.Transport{"", config.TransportNativeHTTP} {
		t.Run(string(transport), func(t *testing.T) {
			c, err := New(config.Config{Upstreams: []config.Upstream{{ID: "a", Endpoint: wrongIDServer(t).URL, Transport: transport}}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(c.Close)
			emissions, err := collectOnce(t, c)
			if !errors.Is(err, ErrAllSourcesDown) || len(emissions) != 1 || !errors.Is(emissions[0].Response.Err(), rpcwire.ErrIDMismatch) || emissions[0].Response.Answer.Result != nil {
				t.Fatalf("native HTTP did not reject the mismatched id: %+v, %v", emissions, err)
			}
		})
	}
}
