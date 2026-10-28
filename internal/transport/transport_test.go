package transport

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
)

// The Port is the seam every adapter implements and core packages consume.
// Its shape and its neutral sentinels are contract surface: the interlock
// keeps adapters out of this package, and these assertions keep the
// sentinel identity stable for the classification seams that wrap it.
func TestPortSurface(t *testing.T) {
	var (
		_ Transport = portStub{}
		_ Dialer    = dialerStub{}
	)
	if ErrBodyTooLarge == nil || ErrConnectionClosed == nil {
		t.Fatal("neutral sentinels must be defined")
	}
	if errors.Is(ErrBodyTooLarge, ErrConnectionClosed) {
		t.Fatal("the body-limit and lifecycle sentinels must stay distinct")
	}
	// Wrapping preserves the neutral identity, which is how adapters attach
	// their own diagnostics without leaking their types to core packages.
	wrapped := fmt.Errorf("adapter detail: %w", ErrConnectionClosed)
	if !errors.Is(wrapped, ErrConnectionClosed) {
		t.Fatal("wrapped close errors must keep the neutral identity")
	}
}

type portStub struct{}

func (portStub) CallContext(context.Context, any, string, ...any) error { return nil }
func (portStub) ChainID(context.Context) (*big.Int, error)              { return nil, nil }
func (portStub) Close()                                                 {}

type dialerStub struct{}

func (dialerStub) DialContext(context.Context, string) (Transport, error) { return nil, nil }
