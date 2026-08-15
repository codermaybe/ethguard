package pool

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

var ErrClosed = errors.New("pool: closed")

// dialer establishes long-lived RPC connections to upstream endpoints.
//
// It is an interface so that tests can inject a fake dialer instead of reaching
// a real network. The production dialer dials over the wire and returns a
// *ethclient.Client owned by a single Source. A source reuses its client for
// every request; it never dials per call (see architecture.md).
type dialer interface {
	DialContext(ctx context.Context, rawURL string) (*ethclient.Client, error)
}

// standardDialer is the default production dialer.
type standardDialer struct{}

func (standardDialer) DialContext(ctx context.Context, rawURL string) (*ethclient.Client, error) {
	return ethclient.DialContext(ctx, rawURL)
}

// Pool is the concurrent-safe multi-source RPC manager.
// One Pool is shared by a single ethguard.Client and all caller goroutines.
//
// It holds sources and their health observations. Collect fans a single request
// out to the eligible sources symmetrically and returns raw, unjudged results.
type Pool struct {
	mu      sync.RWMutex
	sources []*Source
	byID    map[string]*Source

	dialer          dialer
	expectedChainID uint64
	closed          bool
}

// NewPool creates an empty pool.
// Sources dialed by the pool use the standard network dialer.
func NewPool() *Pool {
	return NewPoolWithChainID(0)
}

// NewPoolWithChainID creates a pool that rejects sources reporting another
// Ethereum chain. A zero chain ID disables the check for compatibility.
func NewPoolWithChainID(chainID uint64) *Pool {
	return newPoolWithDialerAndChainID(standardDialer{}, chainID)
}

// newPoolWithDialer lets tests use an in-process RPC transport.
func newPoolWithDialer(d dialer) *Pool {
	return newPoolWithDialerAndChainID(d, 0)
}

func newPoolWithDialerAndChainID(d dialer, chainID uint64) *Pool {
	return &Pool{
		byID:            make(map[string]*Source),
		dialer:          d,
		expectedChainID: chainID,
	}
}

// Join registers an endpoint if it is not already present. Concurrent calls
// for the same normalized endpoint always return the same Source.
func (p *Pool) Join(name, rawURL string) (*Source, error) {
	id, err := normalizeEndpointID(rawURL)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	if existing := p.byID[id]; existing != nil {
		return existing, nil
	}

	life, cancelLife := context.WithCancel(context.Background())
	src := &Source{
		name:            name,
		url:             rawURL,
		id:              id,
		dialer:          p.dialer,
		expectedChainID: p.expectedChainID,
		life:            life,
		cancelLife:      cancelLife,
		state:           SourceDown,
	}
	p.sources = append(p.sources, src)
	p.byID[id] = src
	return src, nil
}

// Sources returns a snapshot of currently registered sources.
func (p *Pool) Sources() []*Source {
	out, _ := p.snapshotSources(false)
	return out
}

func (p *Pool) sourcesForCollect() ([]*Source, error) {
	sources, err := p.snapshotSources(true)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	eligible := sources[:0]
	for _, src := range sources {
		if src.eligible(now) {
			eligible = append(eligible, src)
		}
	}
	return eligible, nil
}

// snapshotSources copies the registered source slice.
// When requireOpen is true, a closed pool returns ErrClosed.
func (p *Pool) snapshotSources(requireOpen bool) ([]*Source, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if requireOpen && p.closed {
		return nil, ErrClosed
	}
	out := make([]*Source, len(p.sources))
	copy(out, p.sources)
	return out, nil
}

// Close releases all source connections. It is safe to call more than once.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	sources := make([]*Source, len(p.sources))
	copy(sources, p.sources)
	p.mu.Unlock()

	for _, src := range sources {
		src.close()
	}
}

// Len returns the number of registered sources.
func (p *Pool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.sources)
}

func normalizeEndpointID(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("pool: parse url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("pool: url must include scheme and host")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	switch u.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return "", fmt.Errorf("pool: unsupported URL scheme %q", u.Scheme)
	}
	u.Host = strings.ToLower(u.Host)
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), nil
}
