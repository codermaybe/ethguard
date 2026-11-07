package subscription

import (
	"errors"
	"sync"
)

// SourceSession identifies one physical subscription lifetime for a source.
// A reconnect always receives a new generation; notifications carrying an
// older generation are stale even when they arrive after the reconnect.
type SourceSession struct {
	SourceID   string
	Generation uint64
}

var (
	ErrSessionClosed = errors.New("subscription: session gate closed")
	ErrSessionStale  = errors.New("subscription: stale source session")
)

// SessionGate is the narrow lifecycle seam between connection management and
// event aggregation. It tracks the current physical session independently for
// every source. It does not remove a source from a quorum snapshot: callers
// decide that separately in the policy layer.
type SessionGate struct {
	mu          sync.RWMutex
	generations map[string]uint64
	closed      bool
}

// NewSessionGate creates a gate for the fixed source identities in a
// subscription snapshot. Membership is immutable for the gate lifetime.
func NewSessionGate(sourceIDs []string) (*SessionGate, error) {
	g := &SessionGate{generations: make(map[string]uint64, len(sourceIDs))}
	for _, sourceID := range sourceIDs {
		if sourceID == "" {
			return nil, ErrUnknownSource
		}
		if _, exists := g.generations[sourceID]; exists {
			return nil, ErrUnknownSource
		}
		g.generations[sourceID] = 0
	}
	return g, nil
}

// Open starts (or reconnects) one source session. The returned generation is
// strictly increasing for that source and is independent of other sources.
func (g *SessionGate) Open(sourceID string) (SourceSession, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return SourceSession{}, ErrSessionClosed
	}
	generation, ok := g.generations[sourceID]
	if !ok {
		return SourceSession{}, ErrUnknownSource
	}
	generation++
	g.generations[sourceID] = generation
	return SourceSession{SourceID: sourceID, Generation: generation}, nil
}

// Accept reports whether a notification belongs to the current physical
// session for its source. A stale frame is rejected without changing any
// quorum membership or source health state.
func (g *SessionGate) Accept(session SourceSession) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed {
		return ErrSessionClosed
	}
	generation, ok := g.generations[session.SourceID]
	if !ok {
		return ErrUnknownSource
	}
	if session.Generation == 0 || generation != session.Generation {
		return ErrSessionStale
	}
	return nil
}

// Current returns the currently active physical session for one source. A
// caller can capture it before a Pull and revalidate it after the response so
// a reconnect cannot make a late unary answer vote in the old session.
func (g *SessionGate) Current(sourceID string) (SourceSession, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed {
		return SourceSession{}, ErrSessionClosed
	}
	generation, ok := g.generations[sourceID]
	if !ok {
		return SourceSession{}, ErrUnknownSource
	}
	if generation == 0 {
		return SourceSession{}, ErrSessionStale
	}
	return SourceSession{SourceID: sourceID, Generation: generation}, nil
}

// Close prevents new sessions and rejects all subsequent notifications. It
// does not alter the caller's fixed source snapshot or emit a quorum result.
func (g *SessionGate) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}
