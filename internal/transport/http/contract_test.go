package http

import (
	"context"
	"testing"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/transport/contract"
)

// The native adapter is the normative L1 implementation: the whole suite must
// pass against it. docs/design/transport-contract.md is the written form of
// these clauses.
func TestNativeTransportL1Contract(t *testing.T) {
	contract.RunL1(t, contract.Adapter{
		Name: "native-http",
		Dial: func(t *testing.T, rawURL string) transport.Transport {
			tr, err := NewDialer().DialContext(context.Background(), rawURL)
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
