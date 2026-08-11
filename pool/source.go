package pool

import (
	"context"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

// Source is a single managed RPC endpoint backed by ethclient only.
// One Source owns at most one long-lived *ethclient.Client; request paths
// must reuse it rather than dialing per call.
type Source struct {
	mu     sync.RWMutex
	dialMu sync.Mutex

	name string
	url  string

	// id is a stable identity used for deduplication (typically normalized URL).
	id string

	// dialer dials the endpoint on first use. Injected for testability.
	dialer dialer

	client     *ethclient.Client
	dialCancel context.CancelFunc
	stats      monitor
	state      SourceState
	closed     bool
}

// clientForCall lazily dials the endpoint on first use.
// It is safe for concurrent callers; only one goroutine performs the dial and
// a successful client is shared.
func (s *Source) clientForCall(ctx context.Context) (*ethclient.Client, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrClosed
	}
	if s.client != nil {
		client := s.client
		s.mu.RUnlock()
		return client, nil
	}
	s.mu.RUnlock()

	s.dialMu.Lock()
	defer s.dialMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if s.client != nil {
		client := s.client
		s.mu.Unlock()
		return client, nil
	}
	dialCtx, cancel := context.WithCancel(ctx)
	s.dialCancel = cancel
	s.mu.Unlock()

	client, err := s.dialer.DialContext(dialCtx, s.url)
	cancel()

	s.mu.Lock()
	s.dialCancel = nil
	if s.closed {
		s.mu.Unlock()
		if client != nil {
			client.Close()
		}
		return nil, ErrClosed
	}
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.client = client
	s.state = SourceReady
	s.mu.Unlock()
	return client, nil
}

func (s *Source) discardClient(client *ethclient.Client) {
	s.mu.Lock()
	if s.client != client || s.closed {
		s.mu.Unlock()
		return
	}
	s.client = nil
	s.state = SourceDown
	s.mu.Unlock()
	client.Close()
}

func (s *Source) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.dialCancel != nil {
		s.dialCancel()
	}
	client := s.client
	s.client = nil
	s.state = SourceDown
	s.mu.Unlock()

	if client != nil {
		client.Close()
	}
}

func (s *Source) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// recordSuccessfulCall updates health on a completed RPC.
func (s *Source) recordSuccessfulCall(rtt time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.stats.observeSuccess(rtt)
	s.state = SourceReady
}

// recordFailedCall updates health on a failed/errored RPC.
func (s *Source) recordFailedCall(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.stats.observeFailure(err)
	s.state = SourceDown
}

// SourceState is the readiness of a managed endpoint.
type SourceState int

const (
	// SourceDown has not connected successfully or its last transport attempt failed.
	SourceDown SourceState = iota
	// SourceReady is connected and has no later observed transport failure.
	SourceReady
)

// Name returns the configured display name.
func (s *Source) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.name
}

// URL returns the dial URL.
func (s *Source) URL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.url
}

// ID returns the deduplication key.
func (s *Source) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// State returns the current readiness state.
func (s *Source) State() SourceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Health returns a consistent, read-only snapshot of source observations.
func (s *Source) Health() HealthSnapshot {
	return s.stats.snapshot()
}
