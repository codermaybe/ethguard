package pool

import (
	"errors"
	"sync"
)

// ErrClosed is returned when an operation requires a live pool after Close.
var ErrClosed = errors.New("pool: closed")

// Pool is the concurrent-safe multi-source RPC manager.
// One Pool is shared by a single ethguard.Client and all caller goroutines.
//
// It holds sources and their health observations. Collect fans a single request
// out to the eligible sources symmetrically and returns raw, unjudged results.
type Pool struct {
	mu      sync.RWMutex
	sources []*Source
	byID    map[string]*Source

	dialer dialer
	closed bool
}

// NewPool creates an empty pool.
// Sources dialed by the pool use the standard network dialer.
func NewPool() *Pool {
	return newPoolWithDialer(standardDialer{})
}

func newPoolWithDialer(d dialer) *Pool {
	p := &Pool{
		byID:   make(map[string]*Source),
		dialer: d,
	}
	return p
}

// Sources returns a snapshot of currently registered sources.
func (p *Pool) Sources() []*Source {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]*Source, len(p.sources))
	copy(out, p.sources)
	return out
}

func (p *Pool) sourcesForCollect() ([]*Source, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
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
