// Package gethcompat is the explicit construction seam for the go-ethereum
// compatibility channel. Default construction selects native HTTP and
// explicitly configured raw WSS; importing this package pulls in the geth
// adapter as the pool's dialer. Existing users can keep the audited
// compatibility behavior during the transition to the native-only end state —
// it is not a second Client implementation and adds no channel-selection
// parameter to the root facade.
//
// Migration note: gethcompat.New tolerates a WithHTTPAdapter(HTTPAdapterGeth)
// in opts — the seam's own dialer wins, both in the default build and in a
// compatibility build — so a caller can switch to this seam without dropping
// the old option first. Do not pass the option expecting a channel to appear:
// without this package a bare geth selection still fails construction.
// Explicit per-source transport settings conflict with the injected dialer
// and are rejected; omit them when using this compatibility entry point.
package gethcompat

import (
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/transport"
	gethadapter "github.com/codermaybe/ethguard/internal/transport/geth"

	"github.com/codermaybe/ethguard"
)

// Dialer returns the go-ethereum compatibility connection adapter.
func Dialer() transport.Dialer { return gethadapter.StandardDialer{} }

// New builds a Client whose pool dials upstreams through the go-ethereum
// compatibility adapter. Every other construction aspect is identical to
// ethguard.New, whose options pass through unchanged.
func New(cfg config.Config, opts ...ethguard.Option) (*ethguard.Client, error) {
	return ethguard.New(cfg, append(opts, ethguard.DialerOption(Dialer()))...)
}

// Compose note: gethcompat.New tolerates a WithHTTPAdapter(HTTPAdapterGeth)
// option in opts — the explicitly injected dialer wins, so migrating callers
// can keep the old option while switching to this seam. A bare
// WithHTTPAdapter(HTTPAdapterGeth) without this package still fails
// construction by design.
