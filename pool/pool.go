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

var (
	ErrClosed = errors.New("pool: closed")

	// ErrNoEligibleSource reports that a fan-out was impossible: the pool has
	// no registered source, or none passed its eligibility gates (open
	// cooldown circuit, quarantined identity). No upstream was contacted.
	ErrNoEligibleSource = errors.New("pool: no eligible upstream source")

	// ErrAllSourcesDown reports that sources were contacted but not one
	// produced a usable response at the transport level. Individual causes
	// remain available on the returned emissions and on Decision.Faults.
	ErrAllSourcesDown = errors.New("pool: all upstream sources failed to respond")
)

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

	// Tip tracking (see WithTipTracking). maxTipLag zero disables the
	// freshness gate; stopTips is non-nil only while sampling runs.
	maxTipLag   uint64
	tipInterval time.Duration
	stopTips    chan struct{}
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

func newPoolWithDialerAndChainID(d dialer, chainID uint64) *Pool {
	return &Pool{
		byID:            make(map[string]*Source),
		dialer:          d,
		expectedChainID: chainID,
	}
}

// Join registers an endpoint if it is not already present. Concurrent calls
// for the same normalized endpoint always return the same Source. Distinct
// endpoints must have distinct display names so diagnostics and metric labels
// cannot collide. A Join whose endpoint is already registered returns the
// existing Source unchanged, even under a different display name: endpoint
// identity wins over naming, so callers must not assume one Join per name.
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
	for _, existing := range p.sources {
		if existing.name == name {
			return nil, fmt.Errorf("pool: duplicate source name %q", name)
		}
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

// VotingMembers returns the exact source objects currently configured. The
// objects are safe to retain for one invocation: removal closes the object but
// does not mutate its identity, so an outer snapshot can fail it closed.
func (p *Pool) VotingMembers() []*Source {
	return p.Sources()
}

// Remove removes one voting member by its configured display name and
// releases its connection (decision 0027). An invocation that already
// captured the member keeps it in its voting basis — its calls fail closed
// with ErrClosed and count in that invocation's denominator — while
// invocations starting after the removal exclude it. Runtime removal never
// follows from upstream behavior; it is always an explicit caller action.
func (p *Pool) Remove(name string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrClosed
	}
	index := -1
	for i, src := range p.sources {
		if src.name == name {
			index = i
			break
		}
	}
	if index < 0 {
		p.mu.Unlock()
		return fmt.Errorf("pool: no source named %q", name)
	}
	src := p.sources[index]
	p.sources = append(p.sources[:index], p.sources[index+1:]...)
	delete(p.byID, src.id)
	p.mu.Unlock()

	src.close()
	return nil
}

func (p *Pool) sourcesForCollect(plan collectPlan) ([]*Source, error) {
	sources, err := p.snapshotSources(true)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var floor uint64
	if plan.applyFreshness {
		floor = p.staleFloor(sources)
	}
	// Fresh filtering slice: sources is a snapshot other paths may still
	// read, so never filter it in place.
	eligible := make([]*Source, 0, len(sources))
	for _, src := range sources {
		if plan.warmOnly {
			// Probe path: observe established connections only, ignoring
			// cooldown circuits so recovery is detected proactively.
			if !src.hasClient() || src.isIdentityInvalid() {
				continue
			}
		} else if !src.eligible(now) {
			continue // user path: identity, circuit, closed gates
		}
		if floor > 0 {
			if tip, ok := src.tipHeight(); ok && tip < floor {
				continue // freshness gate: trailing beyond the bound
			}
		}
		eligible = append(eligible, src)
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
	if p.stopTips != nil {
		close(p.stopTips)
	}
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

func (p *Pool) isClosed() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.closed
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
	// Strip an explicit default port so the same node dialed as
	// https://host and https://host:443 deduplicates to one member.
	// Non-default ports stay significant: they address different listeners.
	if isDefaultPort(u.Scheme, u.Port()) {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]" // IPv6 literal without port
		}
	}
	return u.String(), nil
}

// isDefaultPort reports whether port is the conventional listener for
// scheme. Empty means unspecified, never a default.
func isDefaultPort(scheme, port string) bool {
	switch scheme {
	case "http", "ws":
		return port == "80"
	case "https", "wss":
		return port == "443"
	default:
		return false
	}
}

func (p *Pool) sourcesByIDs(ids []string) ([]*Source, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return nil, ErrClosed
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		if selected[id] || p.byID[id] == nil {
			return nil, fmt.Errorf("pool: invalid fixed membership: %w", ErrNoEligibleSource)
		}
		selected[id] = true
	}
	out := make([]*Source, 0, len(ids))
	for _, src := range p.sources {
		if selected[src.id] {
			out = append(out, src)
		}
	}
	return out, nil
}
