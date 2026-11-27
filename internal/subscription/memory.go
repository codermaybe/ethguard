package subscription

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var ErrMemoryBudget = errors.New("subscription: memory budget exhausted")

// MemoryBudget reserves all retained subscription memory atomically. Used is
// diagnostic only; correctness depends on Reserve and idempotent release.
type MemoryBudget interface {
	Reserve(bytes uint64) (release func(), err error)
}

type AtomicMemoryBudget struct {
	mu    sync.Mutex
	limit uint64
	used  uint64
}

func NewMemoryBudget(limit uint64) *AtomicMemoryBudget {
	return &AtomicMemoryBudget{limit: limit}
}

func (b *AtomicMemoryBudget) Reserve(bytes uint64) (func(), error) {
	release, ok := b.TryReserve(bytes)
	if !ok {
		return nil, fmt.Errorf("%w: requested=%d limit=%d", ErrMemoryBudget, bytes, b.limit)
	}
	return release, nil
}

// TryReserve is the non-blocking heap admission used by the external pump.
// Atomic memory reservation is the only backpressure.
func (b *AtomicMemoryBudget) TryReserve(bytes uint64) (func(), bool) {
	if bytes == 0 {
		return func() {}, true
	}
	b.mu.Lock()
	// A zero limit removes the configured cap, but the usage ledger must
	// remain representable so every release can return its own reservation.
	if bytes > ^uint64(0)-b.used || (b.limit > 0 && (bytes > b.limit || b.used > b.limit-bytes)) {
		b.mu.Unlock()
		return nil, false
	}
	b.used += bytes
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.used -= bytes
			b.mu.Unlock()
		})
	}, true
}

func (b *AtomicMemoryBudget) Used() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

type unlimitedBudget struct{}

func (unlimitedBudget) Reserve(uint64) (func(), error) { return func() {}, nil }

func budgetOrUnlimited(b MemoryBudget) MemoryBudget {
	if b == nil {
		return unlimitedBudget{}
	}
	return b
}

type payloadState struct {
	once    sync.Once
	release func()
	value   any
}

type PayloadArena struct {
	budget MemoryBudget
	next   atomic.Uint64
}

func NewPayloadArena(budget MemoryBudget) *PayloadArena {
	return &PayloadArena{budget: budgetOrUnlimited(budget)}
}

// Store reserves bytes before exposing value to candidate construction. The
// caller transfers value ownership to the arena and must not mutate it later.
func (a *PayloadArena) Store(value any, bytes uint64) (PayloadLease, error) {
	release, err := a.budget.Reserve(bytes)
	if err != nil {
		return PayloadLease{}, err
	}
	return PayloadLease{ID: a.next.Add(1), Bytes: bytes, state: &payloadState{release: release, value: value}}, nil
}

// Release discards a lease the caller decides it no longer needs.
func (l PayloadLease) Release() { l.release() }

// Take transfers the immutable payload out of the lease exactly once.
func (l PayloadLease) Take() any { return l.take() }

func (l PayloadLease) take() any {
	if l.state == nil {
		return nil
	}
	var value any
	l.state.once.Do(func() {
		value = l.state.value
		l.state.value = nil
		l.state.release()
	})
	return value
}

func (l PayloadLease) release() {
	if l.state == nil {
		return
	}
	l.state.once.Do(func() {
		l.state.value = nil
		l.state.release()
	})
}
