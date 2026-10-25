package pool

import (
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

	t.Run("default-dialer-accepts-baseline-quirk", func(t *testing.T) {
		p := NewPoolWithOptions(0)
		t.Cleanup(p.Close)
		src, err := p.Join("geth", srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		emissions, err := p.Collect(context.Background(), "eth_chainId", nil, CollectOptions{Sources: []*Source{src}})
		if err != nil {
			t.Fatalf("Collect err = %v, want the baseline usable answer", err)
		}
		if len(emissions) != 1 || string(emissions[0].Val) != `"0x2a"` {
			t.Fatalf("emissions = %+v, want the value despite the mismatched id", emissions)
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
