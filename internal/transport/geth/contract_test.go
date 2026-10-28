package geth

import (
	"context"
	"testing"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/transport/contract"
)

// The compatibility adapter must satisfy the same L1 clauses as the native
// adapter it complements. The suite is the alignment gate: only documented
// L2/L3 deviations (diagnostics, body retention sizes) may differ.
func TestGethTransportL1Contract(t *testing.T) {
	contract.RunL1(t, contract.Adapter{
		Name: "geth-compat",
		Dial: func(t *testing.T, rawURL string) transport.Transport {
			tr, err := Dialer{}.DialContext(context.Background(), rawURL)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			return tr
		},
		DialWithBodyLimit: func(t *testing.T, rawURL string, limit int64) transport.Transport {
			tr, err := Dialer{BodyLimit: limit}.DialContext(context.Background(), rawURL)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			return tr
		},
	})
}
