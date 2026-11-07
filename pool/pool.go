package pool

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	upstream "github.com/codermaybe/ethguard/internal/transport"
)

var (
	ErrClosed             = errors.New("pool: closed")
	ErrNoWSSSource        = errors.New("pool: no WSS source configured")
	ErrMultipleWSSSources = errors.New("pool: subscriptions require one explicit WSS source")

	// ErrNoEligibleSource reports that a fan-out was impossible: the pool has
	// no registered source, or none passed its eligibility gates (open
	// cooldown circuit, quarantined identity). No upstream was contacted.
	ErrNoEligibleSource = errors.New("pool: no eligible upstream source")

	// ErrAllSourcesDown reports that sources were contacted but not one
	// produced a usable response at the transport level. Individual causes
	// remain available on the returned emissions and on Decision.Faults.
	ErrAllSourcesDown = errors.New("pool: all upstream sources failed to respond")
)

// SubscriptionSources returns a stable snapshot of all explicitly configured
// WebSocket sources. Membership is fixed for the lifetime of the pool; the
// returned slice is a copy, so source health and connection state may change
// without changing the subscription's source denominator. Transport
// selection stays inside the pool composition boundary; callers receive only
// capability-bearing sources and never parse URLs.
func (p *Pool) SubscriptionSources() ([]*Source, error) {
	p.mu.RLock()
	sources := append([]*Source(nil), p.sources...)
	p.mu.RUnlock()
	out := make([]*Source, 0, len(sources))
	for _, source := range sources {
		e, err := url.Parse(source.URL())
		if err != nil || (e.Scheme != "ws" && e.Scheme != "wss") {
			continue
		}
		out = append(out, source)
	}
	if len(out) == 0 {
		return nil, ErrNoWSSSource
	}
	return out, nil
}

// SubscriptionSource returns the sole explicitly configured WebSocket source.
// It is retained for compatibility with the S1 single-source path. New
// subscription code should use SubscriptionSources so quorum can keep the
// complete WSS membership snapshot.
func (p *Pool) SubscriptionSource() (*Source, error) {
	sources, err := p.SubscriptionSources()
	if err != nil {
		return nil, err
	}
	if len(sources) != 1 {
		return nil, ErrMultipleWSSSources
	}
	return sources[0], nil
}

// Pool is the concurrent-safe multi-source RPC manager.
// One Pool is shared by a single ethguard.Client and all caller goroutines.
//
// Membership is fixed at construction: the pool never adds or removes voting
// members at runtime. It holds sources and their health observations; health
// and scheduling may pause requests to a member, but a paused or failed member
// keeps its place in every voting basis. Collect fans a single request out to
// the eligible sources symmetrically and returns raw, unjudged results.
type Pool struct {
	mu      sync.RWMutex
	sources []*Source
	byID    map[string]*Source

	dialer          dialer
	expectedChainID uint64
	closed          bool

	// Tip tracking (see WithTipTracking). maxTipLag zero disables the
	// freshness gate; stopTips is non-nil only while sampling runs, and
	// tipsDone is closed by the sampler goroutine on its exit so Close can
	// wait for the pool-owned cleanup. tipSampleMaxAge is the explicit
	// validity window for one sampled tip; a sample older than this is
	// unknown to the gate.
	maxTipLag       uint64
	tipInterval     time.Duration
	tipSampleMaxAge time.Duration
	stopTips        chan struct{}
	tipsDone        chan struct{}

	// closeOnce runs the close sequence exactly once; closeDone is closed
	// when the whole sequence — every source transport closed and the tip
	// sampler exited — has completed. Every Close caller, first or
	// concurrent, waits on the same signal, so all of them return under the
	// identical resource-closure conditions.
	closeOnce sync.Once
	closeDone chan struct{}
}

// Member is one upstream configured at pool construction.
type Member struct {
	Name string
	URL  string
	// Dialer selects this member's immutable connection factory. Nil uses
	// the pool-wide default; it does not change membership or voting weight.
	Dialer upstream.Dialer
}

// NewWithMembers creates a pool whose voting membership is fixed for its
// lifetime. Membership is admitted here and never changes at runtime; health
// and scheduling may pause requests to a member, but cannot add, remove, or
// resize the voting basis.
func NewWithMembers(chainID uint64, members []Member, opts ...PoolOption) (*Pool, error) {
	p := newPoolWithDialerAndOptions(nil, chainID, opts...)
	for _, member := range members {
		if _, err := p.admit(member); err != nil {
			p.Close()
			return nil, fmt.Errorf("pool: member %q: %w", member.Name, err)
		}
	}
	return p, nil
}

// NewPool creates an empty pool. Production construction uses
// NewWithMembers; an empty pool has no membership and cannot fan out.
func NewPool() *Pool {
	return NewPoolWithChainID(0)
}

// NewPoolWithChainID creates an empty pool that rejects sources reporting
// another Ethereum chain. A zero chain ID disables the check for compatibility.
func NewPoolWithChainID(chainID uint64) *Pool {
	return newPoolWithDialerAndChainID(nil, chainID)
}

func newPoolWithDialerAndChainID(d dialer, chainID uint64) *Pool {
	return &Pool{
		byID:            make(map[string]*Source),
		dialer:          d,
		expectedChainID: chainID,
	}
}

// admit registers one construction-time member. Calls for the same normalized
// endpoint return the same Source. Distinct endpoints must have distinct
// display names so diagnostics and metric labels cannot collide. Adapter
// selection is immutable and never creates another source registry.
func (p *Pool) admit(member Member) (*Source, error) {
	name, rawURL := member.Name, member.URL
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
		if member.Dialer != nil {
			return nil, fmt.Errorf("pool: endpoint %q is already registered; an explicit adapter cannot be silently ignored", rawURL)
		}
		return existing, nil
	}
	for _, existing := range p.sources {
		if existing.name == name {
			return nil, fmt.Errorf("pool: duplicate source name %q", name)
		}
	}

	selectedDialer := member.Dialer
	if selectedDialer == nil {
		selectedDialer = p.dialer
	}
	life, cancelLife := context.WithCancel(context.Background())
	src := &Source{
		name:            name,
		url:             rawURL,
		id:              id,
		dialer:          selectedDialer,
		expectedChainID: p.expectedChainID,
		life:            life,
		cancelLife:      cancelLife,
		state:           SourceDown,
	}
	p.sources = append(p.sources, src)
	p.byID[id] = src
	return src, nil
}

// Sources returns a snapshot of the configured sources. Membership is fixed at
// construction, so the snapshot cannot gain or lose members.
func (p *Pool) Sources() []*Source {
	out, _ := p.snapshotSources(false)
	return out
}

// VotingMembers returns the exact source objects configured at construction.
// The objects are safe to retain for one invocation; a concurrent Close may
// close them, but it cannot change membership or an invocation's basis.
func (p *Pool) VotingMembers() []*Source {
	return p.Sources()
}

func (p *Pool) sourcesForCollect(plan collectPlan) ([]*Source, error) {
	sources, err := p.snapshotSources(true)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	reference := tipReference{}
	if plan.applyFreshness && plan.tipFreshness.gatesLatestTip() {
		reference = p.tipReference(sources, now)
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
		if reference.floor > 0 {
			tip, _, ok := src.freshTip(now, reference.maxAge)
			if ok && tip < reference.floor {
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

// Close releases all source connections and stops the background tip
// sampler. The close sequence runs exactly once: it marks the pool closed
// and stops the sampler synchronously, then closes every source transport
// and waits for the sampler goroutine to exit on the close goroutine. The
// shared completion signal closes only after all of that has finished, and
// every caller — first, concurrent, or repeated — waits on the same signal,
// so Close returns to all of them under the identical resource-closure
// conditions. It is safe to call from any number of goroutines.
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		p.closeDone = make(chan struct{})
		p.mu.Lock()
		p.closed = true
		if p.stopTips != nil {
			close(p.stopTips)
		}
		tipsDone := p.tipsDone
		sources := make([]*Source, len(p.sources))
		copy(sources, p.sources)
		p.mu.Unlock()
		go func() {
			defer close(p.closeDone)
			for _, src := range sources {
				src.close()
			}
			if tipsDone != nil {
				<-tipsDone
			}
		}()
	})
	<-p.closeDone
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

// SourceByID reports the registered source with this identity. Membership is
// fixed at construction, so a found source is a live member and a missing ID
// is an unknown member. A closed pool keeps its objects addressable for
// callers that still hold them; it does not change membership.
func (p *Pool) SourceByID(id string) (*Source, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	source := p.byID[id]
	return source, source != nil
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
