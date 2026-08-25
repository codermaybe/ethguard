package pool

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

var errWaitingForDial = errors.New("pool: waiting for shared dial")

// Source is a single managed RPC endpoint backed by ethclient only.
// One Source owns at most one long-lived *ethclient.Client; request paths
// must reuse it rather than dialing per call.
type Source struct {
	mu sync.RWMutex

	name string
	url  string

	// id is a stable identity used for deduplication (typically normalized URL).
	id string

	// dialer dials the endpoint on first use. Injected for testability.
	dialer           dialer
	expectedChainID  uint64
	identityMu       sync.Mutex
	identityChecked  bool
	identityInvalid  bool
	identityChecking chan struct{}

	// life is cancelled when the source closes; it aborts any in-flight dial.
	life       context.Context
	cancelLife context.CancelFunc

	// dialing is non-nil while a goroutine dials on behalf of the source;
	// it is closed when that dial settles, waking all waiters.
	dialing chan struct{}

	client *ethclient.Client
	stats  monitor
	state  SourceState
	closed bool
}

// clientForCall returns the shared long-lived client, dialing lazily on first
// use. Concurrent first callers do not each dial: exactly one goroutine
// performs the dial while the others wait on dialing; a waiter can give up
// via its own ctx and then receives errWaitingForDial, which callers must
// not record as a source failure.
func (s *Source) clientForCall(ctx context.Context) (*ethclient.Client, error) {
	if client, err, ok := s.cachedClient(); ok {
		return client, err
	}

	// Try to become the dialer without blocking on the network.
	s.mu.Lock()
	if s.dialing == nil {
		if s.closed {
			s.mu.Unlock()
			return nil, ErrClosed
		}
		s.dialing = make(chan struct{})
		dialDone := s.dialing
		s.mu.Unlock()
		client, err := s.dial(ctx)
		close(dialDone)
		return client, err
	}
	wait := s.dialing
	s.mu.Unlock()

	// Someone else is dialing. Wait for their result (published as s.client or
	// s.closed) or for this caller's ctx to give up first.
	select {
	case <-wait:
		if client, err, ok := s.cachedClient(); ok {
			return client, err
		}
		// The shared dial failed; this caller retries its own dial attempt.
		return s.clientForCall(ctx)
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ctx.Err(), errWaitingForDial)
	}
}

func (s *Source) cachedClient() (client *ethclient.Client, err error, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed, true
	}
	if s.client != nil {
		return s.client, nil, true
	}
	return nil, nil, false
}

// dial establishes the shared client without holding mu across network I/O.
// The caller becomes the single dialer (it set dialing before calling).
func (s *Source) dial(ctx context.Context) (*ethclient.Client, error) {
	// Cancel this attempt if the caller gives up or the source is closed.
	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.life, cancel)
	defer stop()

	client, err := s.dialer.DialContext(dialCtx, s.url)

	s.mu.Lock()
	s.dialing = nil
	if s.closed {
		s.mu.Unlock()
		if client != nil {
			client.Close()
		}
		return nil, ErrClosed
	}
	if err != nil {
		s.mu.Unlock()
		if client != nil {
			client.Close()
		}
		return nil, err
	}
	if client == nil {
		s.mu.Unlock()
		return nil, errors.New("pool: dial returned nil client")
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
	s.cancelLife() // aborts any in-flight dial via dial's AfterFunc
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

// hasClient reports whether an established connection exists, without
// dialing. Tip probes use it to keep first contact caller-driven.
func (s *Source) hasClient() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.closed && s.client != nil
}

// isIdentityInvalid reports whether the source failed its chain-ID check.
func (s *Source) isIdentityInvalid() bool {
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	return s.identityInvalid
}

// recordTip stores one block-height sample from the tip sampler.
func (s *Source) recordTip(height uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.stats.observeTip(height)
}

// tipHeight returns the last sampled block height; ok is false when this
// source has never been sampled.
func (s *Source) tipHeight() (uint64, bool) {
	return s.stats.sampledTip()
}

// SourceState is the readiness of a managed endpoint.
type SourceState int

const (
	// SourceDown has not connected successfully or its last transport attempt failed.
	SourceDown SourceState = iota
	// SourceReady is connected and has later observed no transport failure.
	SourceReady
)

// Name returns the configured display name.
// Immutable after Join; no lock required.
func (s *Source) Name() string { return s.name }

// URL returns the dial URL.
// Immutable after Join; no lock required.
func (s *Source) URL() string { return s.url }

// ID returns the deduplication key.
// Immutable after Join; no lock required.
func (s *Source) ID() string { return s.id }

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

// eligible reports whether a source should receive a normal request. A source
// with no observations is eligible for its first lazy dial. After repeated
// failures it is temporarily held out; the cooldown expiry admits a probe.
func (s *Source) eligible(now time.Time) bool {
	if s.isClosed() {
		return false
	}
	s.identityMu.Lock()
	invalid := s.identityInvalid
	s.identityMu.Unlock()
	if invalid {
		return false
	}
	health := s.Health()
	if health.ConsecutiveFailures < eligibleFailureThreshold {
		return true
	}
	return health.LastFailure.IsZero() || now.Sub(health.LastFailure) >= eligibleFailureCooldown
}

var ErrChainIDMismatch = errors.New("pool: upstream chain ID mismatch")

func (s *Source) verifyChainID(ctx context.Context, client *ethclient.Client) error {
	if s.expectedChainID == 0 {
		return nil
	}
	s.identityMu.Lock()
	if s.identityInvalid {
		s.identityMu.Unlock()
		return ErrChainIDMismatch
	}
	if s.identityChecked {
		s.identityMu.Unlock()
		return nil
	}
	if s.identityChecking != nil {
		wait := s.identityChecking
		s.identityMu.Unlock()
		select {
		case <-wait:
			return s.verifyChainID(ctx, client)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	checking := make(chan struct{})
	s.identityChecking = checking
	s.identityMu.Unlock()

	chainID, err := client.ChainID(ctx)
	s.identityMu.Lock()
	s.identityChecking = nil
	defer close(checking)
	defer s.identityMu.Unlock()
	if err != nil {
		return err
	}
	if chainID == nil || chainID.Cmp(new(big.Int).SetUint64(s.expectedChainID)) != 0 {
		s.identityInvalid = true
		return fmt.Errorf("%w: expected %d", ErrChainIDMismatch, s.expectedChainID)
	}
	s.identityChecked = true
	return nil
}
