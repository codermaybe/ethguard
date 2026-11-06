// Package subscription contains the internal event identity and quorum state
// used by the future WSS subscription facade. It is deliberately independent
// of Client, EthClient, unary Transport, and CallResult.
package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrBackpressure    = errors.New("subscription: event queue full")
	ErrQuorumTimeout   = errors.New("subscription: quorum timeout")
	ErrCanceled        = errors.New("subscription: canceled")
	ErrClosed          = errors.New("subscription: closed")
	ErrStaleGeneration = errors.New("subscription: stale source generation")
	ErrUnknownSource   = errors.New("subscription: source is not in snapshot")
)

type Mode uint8

const (
	Strict Mode = iota
	SingleSourceSelected
)

type Kind string

const (
	KindNewHeads Kind = "newHeads"
	KindLogs     Kind = "logs"
)

type Status uint8

const (
	Collecting Status = iota
	Decided
	Selected
	Probing
	Quarantined
	Canceled
	Delivered
	Revoked
)

// Snapshot fixes the event denominator and quorum for the lifetime of an
// event context. Members are source identities, not endpoint URLs.
type Snapshot struct {
	ID         string
	Generation uint64
	Members    []string
	Quorum     int
}

// Config controls an Aggregator. Queue and hard timeout must be positive.
type Config struct {
	Snapshot    Snapshot
	Mode        Mode
	Queue       int
	SoftTimeout time.Duration
	HardTimeout time.Duration
}

// Observation is one canonicalized source observation. ContextKey identifies
// the logical event (height for heads, transaction/index for logs); Key
// identifies the candidate payload and must include reorg-sensitive fields.
type Observation struct {
	Kind       Kind
	ContextKey string
	Key        string
	SourceID   string
	Generation uint64
	Payload    []byte
	Removed    bool
	ObservedAt time.Time
}

type Delivery struct {
	Observation
	Snapshot Snapshot
	Status   Status
	Votes    int
}

type contextState struct {
	first      time.Time
	status     Status
	candidates map[string]*candidate
	delivered  string
	revoked    bool
}
type candidate struct {
	obs     Observation
	sources map[string]struct{}
}

// Aggregator is a synchronous, bounded state machine. Submit and Advance are
// safe for concurrent callers; no hidden goroutine is required for lifecycle
// cleanup, which keeps owner Close and Unsubscribe bounded.
type Aggregator struct {
	mu          sync.Mutex
	cfg         Config
	members     map[string]struct{}
	contexts    map[string]*contextState
	events      chan Delivery
	errs        chan error
	closed      bool
	terminalErr bool
}

func New(cfg Config) (*Aggregator, error) {
	if cfg.Queue <= 0 {
		return nil, fmt.Errorf("subscription: queue must be positive")
	}
	if cfg.Snapshot.ID == "" {
		return nil, fmt.Errorf("subscription: snapshot id is required")
	}
	if len(cfg.Snapshot.Members) == 0 {
		return nil, fmt.Errorf("subscription: snapshot members are required")
	}
	if cfg.Snapshot.Quorum <= 0 || cfg.Snapshot.Quorum > len(cfg.Snapshot.Members) {
		return nil, fmt.Errorf("subscription: invalid quorum")
	}
	if cfg.HardTimeout <= 0 {
		return nil, fmt.Errorf("subscription: hard timeout must be positive")
	}
	if cfg.SoftTimeout < 0 || (cfg.SoftTimeout > 0 && cfg.SoftTimeout >= cfg.HardTimeout) {
		return nil, fmt.Errorf("subscription: soft timeout must be less than hard timeout")
	}
	members := make(map[string]struct{}, len(cfg.Snapshot.Members))
	for _, member := range cfg.Snapshot.Members {
		if member == "" {
			return nil, fmt.Errorf("subscription: empty source member")
		}
		if _, exists := members[member]; exists {
			return nil, fmt.Errorf("subscription: duplicate source member %q", member)
		}
		members[member] = struct{}{}
	}
	cfg.Snapshot.Members = append([]string(nil), cfg.Snapshot.Members...)
	return &Aggregator{cfg: cfg, members: members, contexts: make(map[string]*contextState), events: make(chan Delivery, cfg.Queue), errs: make(chan error, 1)}, nil
}

func (a *Aggregator) Events() <-chan Delivery { return a.events }
func (a *Aggregator) Err() <-chan error       { return a.errs }
func (a *Aggregator) Snapshot() Snapshot      { a.mu.Lock(); defer a.mu.Unlock(); return a.cfg.Snapshot }

// Submit records one source vote. A duplicate source vote is idempotent. A
// stale generation or source outside the fixed snapshot is rejected.
func (a *Aggregator) Submit(obs Observation) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	if _, ok := a.members[obs.SourceID]; !ok {
		return ErrUnknownSource
	}
	if obs.Generation != a.cfg.Snapshot.Generation {
		return ErrStaleGeneration
	}
	if obs.Kind != KindNewHeads && obs.Kind != KindLogs {
		return fmt.Errorf("subscription: unknown event kind %q", obs.Kind)
	}
	if obs.ContextKey == "" || obs.Key == "" {
		return fmt.Errorf("subscription: event key is required")
	}
	ctx := a.contexts[obs.ContextKey]
	if ctx == nil {
		now := obs.ObservedAt
		if now.IsZero() {
			now = time.Now()
		}
		ctx = &contextState{first: now, status: Collecting, candidates: make(map[string]*candidate)}
		a.contexts[obs.ContextKey] = ctx
	}
	if ctx.status == Quarantined || ctx.status == Canceled {
		return nil
	}
	if ctx.status == Delivered || ctx.status == Selected || ctx.status == Revoked {
		if obs.Removed && !ctx.revoked {
			ctx.revoked = true
			ctx.status = Revoked
			votes := 0
			if candidate := ctx.candidates[ctx.delivered]; candidate != nil {
				votes = len(candidate.sources)
			}
			return a.emitLocked(Delivery{Observation: obs, Snapshot: a.cfg.Snapshot, Status: Revoked, Votes: votes})
		}
		return nil
	}
	c := ctx.candidates[obs.Key]
	if c == nil {
		c = &candidate{obs: cloneObservation(obs), sources: make(map[string]struct{})}
		ctx.candidates[obs.Key] = c
	}
	if _, seen := c.sources[obs.SourceID]; seen {
		return nil
	}
	c.sources[obs.SourceID] = struct{}{}
	if a.cfg.Mode == SingleSourceSelected {
		ctx.status, ctx.delivered = Selected, obs.Key
		return a.emitLocked(Delivery{Observation: cloneObservation(obs), Snapshot: a.cfg.Snapshot, Status: Selected, Votes: 1})
	}
	// A strict single-source snapshot may be observed, but it cannot satisfy
	// corroboration. Leave the context collecting until the hard deadline
	// quarantines it instead of silently downgrading the event.
	if len(a.members) == 1 || a.cfg.Snapshot.Quorum < 2 {
		return nil
	}
	if len(c.sources) >= a.cfg.Snapshot.Quorum {
		ctx.status, ctx.delivered = Delivered, obs.Key
		return a.emitLocked(Delivery{Observation: cloneObservation(c.obs), Snapshot: a.cfg.Snapshot, Status: Decided, Votes: len(c.sources)})
	}
	return nil
}

// Advance applies soft/hard deadlines without changing denominator or quorum.
func (a *Aggregator) Advance(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	for _, ctx := range a.contexts {
		if ctx.status == Delivered || ctx.status == Selected || ctx.status == Revoked || ctx.status == Quarantined || ctx.status == Canceled {
			continue
		}
		age := now.Sub(ctx.first)
		if age >= a.cfg.HardTimeout {
			ctx.status = Quarantined
			a.reportLocked(ErrQuorumTimeout)
		} else if a.cfg.SoftTimeout > 0 && age >= a.cfg.SoftTimeout {
			ctx.status = Probing
		}
	}
}

func (a *Aggregator) Status(contextKey string) Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ctx := a.contexts[contextKey]; ctx != nil {
		return ctx.status
	}
	return Canceled
}

func (a *Aggregator) Unsubscribe() { a.close(ErrCanceled) }
func (a *Aggregator) Close()       { a.close(nil) }
func (a *Aggregator) close(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	for _, ctx := range a.contexts {
		if ctx.status != Delivered && ctx.status != Selected && ctx.status != Revoked {
			ctx.status = Canceled
		}
	}
	if err != nil {
		a.reportLocked(err)
	}
	close(a.events)
	close(a.errs)
}

func (a *Aggregator) emitLocked(delivery Delivery) error {
	select {
	case a.events <- delivery:
		return nil
	default:
		a.closed = true
		close(a.events)
		a.reportLocked(ErrBackpressure)
		close(a.errs)
		return ErrBackpressure
	}
}
func (a *Aggregator) reportLocked(err error) {
	if a.terminalErr || err == nil {
		return
	}
	a.terminalErr = true
	a.errs <- err
}

func cloneObservation(obs Observation) Observation {
	obs.Payload = append([]byte(nil), obs.Payload...)
	return obs
}

// PullObservation verifies that a pull result remains bound to the same source
// identity and generation before it enters the normal vote path.
func PullObservation(ctx context.Context, expectedSource string, expectedGeneration uint64, pull func(context.Context) (Observation, error)) (Observation, error) {
	if pull == nil {
		return Observation{}, fmt.Errorf("subscription: nil pull function")
	}
	obs, err := pull(ctx)
	if err != nil {
		return Observation{}, err
	}
	if obs.SourceID != expectedSource {
		return Observation{}, ErrUnknownSource
	}
	if obs.Generation != expectedGeneration {
		return Observation{}, ErrStaleGeneration
	}
	return obs, nil
}

// CanonicalNewHeadKey returns a reorg-sensitive candidate key and a logical
// context key. Hex spelling is normalized before hashing.
func CanonicalNewHeadKey(number, hash, parent string) (contextKey, key string, err error) {
	if number == "" || hash == "" || parent == "" {
		return "", "", fmt.Errorf("subscription: incomplete new head")
	}
	v := struct{ Number, Hash, Parent string }{strings.ToLower(number), strings.ToLower(hash), strings.ToLower(parent)}
	return "head:" + v.Number, digest(KindNewHeads, v), nil
}

func CanonicalLogKey(blockHash, blockNumber, txHash string, index uint64, address, data string, topics []string, removed bool) (contextKey, key string, err error) {
	if blockHash == "" || blockNumber == "" || txHash == "" || address == "" {
		return "", "", fmt.Errorf("subscription: incomplete log")
	}
	v := struct {
		BlockHash, BlockNumber, TxHash string
		Index                          uint64
		Address, Data                  string
		Topics                         []string
		Removed                        bool
	}{strings.ToLower(blockHash), strings.ToLower(blockNumber), strings.ToLower(txHash), index, strings.ToLower(address), strings.ToLower(data), lowerAll(topics), removed}
	return fmt.Sprintf("log:%s:%s:%d", v.BlockHash, v.TxHash, index), digest(KindLogs, v), nil
}

func lowerAll(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = strings.ToLower(value)
	}
	return out
}
func digest(kind Kind, value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(append([]byte(string(kind)+":"), raw...))
	return hex.EncodeToString(sum[:])
}
