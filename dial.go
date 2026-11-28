package ethguard

import (
	"context"
	"fmt"

	"github.com/codermaybe/ethguard/config"
)

// Dial builds a Client from one or more distinct RPC endpoints using the
// default configuration. One endpoint selects Forwarded mode; two or more
// select Majority mode. The mode stays fixed for the client's lifetime:
//
//	client, err := ethguard.Dial("https://a.example:8545", "https://b.example:8545")
//
// The default configuration means: strict anchoring, the documented collect
// window and tip-tracking defaults, and no chain pinning (Dial never sets a
// ChainID implicitly — pass WithChainID for that). Construction is lazy: no
// endpoint is contacted until the first call. Open accepts Go options;
// per-source options can be configured with New.
//
// Empty and duplicate endpoint lists fail. A repeated URL cannot silently
// shrink the voting basis into single-source mode.
func Dial(rawurls ...string) (*Client, error) {
	return DialContext(context.Background(), rawurls...)
}

// DialContext is Dial with the caller's context bounding construction.
// Construction stays lazy, so the context is checked at entry — a canceled
// or deadline-exceeded context fails the dial before any configuration is
// built. Calls made through the returned Client keep using their own
// per-call contexts; this context never rides a read.
func DialContext(ctx context.Context, rawurls ...string) (*Client, error) {
	return OpenContext(ctx, rawurls)
}

// Open builds a lazy Client from one or more distinct endpoints and options.
// One endpoint selects Forwarded mode; two or more select Majority mode.
// Options have the same meaning and precedence as in New.
func Open(endpoints []string, opts ...Option) (*Client, error) {
	return OpenContext(context.Background(), endpoints, opts...)
}

// OpenContext is Open with an entry-time context check. It performs no network
// I/O and does not bind ctx to the client's lifetime. Each later call supplies
// its own context, including the context used for the first lazy dial.
func OpenContext(ctx context.Context, endpoints []string, opts ...Option) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("ethguard: construction canceled at entry: %w", err)
	}
	cfg, err := config.FromEndpoints(endpoints...)
	if err != nil {
		return nil, err
	}
	return New(cfg, opts...)
}

// LoadFile loads a YAML configuration file and builds a Client from it —
// the file-path entry point for operators who configure EthGuard in YAML.
// Options pass through to New; an explicit Go option wins over a value
// derived from the file, per New's documented precedence.
func LoadFile(path string, opts ...Option) (*Client, error) {
	cfg, err := config.LoadFile(path)
	if err != nil {
		return nil, err
	}
	return New(cfg, opts...)
}
