// Package gethcompat is the explicit construction seam for the go-ethereum
// compatibility channel. The default build links only the native HTTP
// adapter; importing this package pulls in the geth adapter and installs it
// as the pool's dialer. It exists so existing users can keep the audited
// compatibility behavior during the transition to the native-only end state —
// it is not a second Client implementation and adds no channel-selection
// parameter to the root facade.
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
