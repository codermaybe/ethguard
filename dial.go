package ethguard

import (
	"context"
	"fmt"

	"github.com/codermaybe/ethguard/config"
)

// Dial builds a Client from two or more distinct RPC endpoints using the
// default configuration — the programmatic counterpart of ethclient.Dial for
// the corroborating case:
//
//	client, err := ethguard.Dial("https://a.example:8545", "https://b.example:8545")
//
// The default configuration means: strict anchoring, the documented collect
// window and tip-tracking defaults, and no chain pinning (Dial never sets a
// ChainID implicitly — pass WithChainID for that). Construction is lazy: no
// endpoint is contacted until the first call. Advanced control (anchoring
// presets, tip tracking, per-source options) builds a config with
// config.FromEndpoints and constructs with New.
//
// Fewer than two endpoints fails: corroboration needs independent sources,
// and a single source must enter its Forwarded mode explicitly through
// DialUncorroborated. Duplicate endpoints fail the same way the YAML path
// rejects them — a repeated URL shrinks the voting basis without adding a
// source. Errors carry the endpoint index and a fix hint.
func Dial(rawurls ...string) (*Client, error) {
	return DialContext(context.Background(), rawurls...)
}

// DialContext is Dial with the caller's context bounding construction.
// Construction stays lazy, so the context is checked at entry — a canceled
// or deadline-exceeded context fails the dial before any configuration is
// built. Calls made through the returned Client keep using their own
// per-call contexts; this context never rides a read.
func DialContext(ctx context.Context, rawurls ...string) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("ethguard: dial canceled before construction: %w", err)
	}
	if len(rawurls) < 2 {
		return nil, fmt.Errorf("ethguard: dial requires at least two distinct endpoints (got %d): corroboration needs independent sources — dial one endpoint explicitly with DialUncorroborated", len(rawurls))
	}
	cfg, err := config.FromEndpoints(rawurls...)
	if err != nil {
		return nil, err
	}
	return New(cfg)
}

// DialUncorroborated dials exactly one endpoint and enters Forwarded mode
// explicitly — the single-source construction decision 0030 reserves for
// trusted configuration control. Reads ride that one source and are reported
// as Forwarded, never claimed corroborated. Two or more sources belong in
// Dial. Options pass through to New; an explicit Go option wins over the
// derived configuration per New's documented precedence.
func DialUncorroborated(rawurl string, opts ...Option) (*Client, error) {
	cfg, err := config.FromEndpoints(rawurl)
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
