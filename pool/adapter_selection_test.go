package pool

import (
	"strings"

	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
)

// TestPoolOptionWithDialerSelectsAdapter proves the WithDialer construction
// option actually swaps the connection adapter, using a behavior both
// adapters can distinguish: a response whose id does not match the request.
// The default Geth compatibility adapter accepts it (audited baseline quirk);
// the native HTTP adapter rejects it by contract.
func TestPoolOptionWithDialerSelectsAdapter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately mismatched id, correct version and shape otherwise.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":999,"result":"0x2a"}`))
	}))
	t.Cleanup(srv.Close)

	t.Run("no-dialer-fails-before-contact", func(t *testing.T) {
		// A pool without a configured adapter fails every call before any
		// dial bookkeeping; the root facade and gethcompat always install
		// one (native by default).
		p := NewPoolWithOptions(0)
		t.Cleanup(p.Close)
		src, err := p.Join("none", srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		emissions, err := p.Collect(context.Background(), "eth_chainId", nil, CollectOptions{Sources: []*Source{src}})
		if !errors.Is(err, ErrAllSourcesDown) {
			t.Fatalf("Collect err = %v, want ErrAllSourcesDown", err)
		}
		if len(emissions) != 1 {
			t.Fatalf("emissions = %+v, want one per member", emissions)
		}
		if emissions[0].Err == nil || !strings.Contains(emissions[0].Err.Error(), "no connection adapter configured") {
			t.Fatalf("emission err = %v, want the explicit no-adapter cause", emissions[0].Err)
		}
	})

	t.Run("native-dialer-rejects-by-contract", func(t *testing.T) {
		p := NewPoolWithOptions(0, WithDialer(nativehttp.NewDialer()))
		t.Cleanup(p.Close)
		src, err := p.Join("native", srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		emissions, err := p.Collect(context.Background(), "eth_chainId", nil, CollectOptions{Sources: []*Source{src}})
		if !errors.Is(err, ErrAllSourcesDown) {
			t.Fatalf("Collect err = %v, want ErrAllSourcesDown", err)
		}
		if len(emissions) != 1 || emissions[0].Err == nil {
			t.Fatalf("emissions = %+v, want one failed emission", emissions)
		}
		var protocol rpc.ProtocolError
		if errors.As(emissions[0].Err, &protocol) {
			t.Fatalf("native rejection surfaced as a protocol error: %v", emissions[0].Err)
		}
	})
}
