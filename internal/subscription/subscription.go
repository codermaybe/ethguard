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
	"io"
	"sort"
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
	ErrUnboundRemoval  = errors.New("subscription: removed event has no delivered predecessor")
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
	// KindTransactionReceipts identifies one notification carrying a batch of
	// transaction receipts. A batch is one logical event; its candidate key is
	// computed over the complete canonicalized batch.
	KindTransactionReceipts Kind = "transactionReceipts"
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
	// Generations optionally records the physical session generation per
	// source. When absent, Generation is used for backwards compatibility with
	// the original single-session snapshot shape.
	Generations map[string]uint64
	Members     []string
	Quorum      int
}

// Config controls an Aggregator. Queue and hard timeout must be positive.
type Config struct {
	Snapshot    Snapshot
	Mode        Mode
	Queue       int
	SoftTimeout time.Duration
	HardTimeout time.Duration
	Budget      Budget
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

// ReceiptIdentity is transport-neutral identity material for one receipt in a
// transaction-receipts batch. Canonical contains the complete typed receipt
// representation (including logs and provider-specific fields) after the
// caller has decoded and normalized it. The routing fields identify the block
// and transaction independently from that candidate payload.
type ReceiptIdentity struct {
	TransactionHash  string
	BlockHash        string
	BlockNumber      string
	TransactionIndex uint64
	Canonical        []byte
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
	evidence    int64
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
	if cfg.Snapshot.Generations != nil {
		generations := cfg.Snapshot.Generations
		cfg.Snapshot.Generations = make(map[string]uint64, len(generations))
		for source, generation := range generations {
			if _, ok := members[source]; !ok {
				return nil, fmt.Errorf("subscription: generation for unknown source %q", source)
			}
			cfg.Snapshot.Generations[source] = generation
		}
	}
	return &Aggregator{cfg: cfg, members: members, contexts: make(map[string]*contextState), events: make(chan Delivery, cfg.Queue), errs: make(chan error, 1)}, nil
}

func (a *Aggregator) Events() <-chan Delivery { return a.events }
func (a *Aggregator) Err() <-chan error       { return a.errs }
func (a *Aggregator) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	snapshot := a.cfg.Snapshot
	snapshot.Members = append([]string(nil), snapshot.Members...)
	if a.cfg.Snapshot.Generations != nil {
		snapshot.Generations = make(map[string]uint64, len(a.cfg.Snapshot.Generations))
		for source, generation := range a.cfg.Snapshot.Generations {
			snapshot.Generations[source] = generation
		}
	}
	return snapshot
}

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
	expectedGeneration := a.cfg.Snapshot.Generation
	if generations := a.cfg.Snapshot.Generations; generations != nil {
		if generation, ok := generations[obs.SourceID]; ok {
			expectedGeneration = generation
		}
	}
	if obs.Generation != expectedGeneration {
		return ErrStaleGeneration
	}
	if obs.Kind != KindNewHeads && obs.Kind != KindLogs && obs.Kind != KindTransactionReceipts {
		return fmt.Errorf("subscription: unknown event kind %q", obs.Kind)
	}
	if obs.ContextKey == "" || obs.Key == "" {
		return fmt.Errorf("subscription: event key is required")
	}
	if err := a.cfg.Budget.validatePayload(obs.Payload); err != nil {
		a.reportLocked(err)
		return err
	}
	// Kind is part of event identity. A head and a log may legitimately use
	// the same caller-provided context key, but they must never share votes or
	// delivery state.
	stateKey := string(obs.Kind) + "\x00" + obs.ContextKey
	ctx := a.contexts[stateKey]
	if ctx == nil {
		now := obs.ObservedAt
		if now.IsZero() {
			now = time.Now()
		}
		ctx = &contextState{first: now, status: Collecting, candidates: make(map[string]*candidate)}
		a.contexts[stateKey] = ctx
	}
	if ctx.status == Quarantined || ctx.status == Canceled {
		return nil
	}
	if ctx.status == Delivered || ctx.status == Selected || ctx.status == Revoked {
		if obs.Removed && !ctx.revoked {
			removedCandidate := ctx.candidates[obs.Key]
			if removedCandidate == nil {
				removedCandidate = &candidate{obs: cloneObservation(obs), sources: make(map[string]struct{})}
				ctx.candidates[obs.Key] = removedCandidate
			}
			if _, seen := removedCandidate.sources[obs.SourceID]; seen {
				return nil
			}
			removedCandidate.sources[obs.SourceID] = struct{}{}
			// A selected single-source subscription has no corroboration
			// requirement. Strict subscriptions still require the fixed event
			// quorum before revoking a previously delivered log.
			if a.cfg.Mode != SingleSourceSelected && len(removedCandidate.sources) < a.cfg.Snapshot.Quorum {
				return nil
			}
			ctx.revoked = true
			ctx.status = Revoked
			return a.emitLocked(Delivery{Observation: cloneObservation(removedCandidate.obs), Snapshot: a.cfg.Snapshot, Status: Revoked, Votes: len(removedCandidate.sources)})
		}
		// A new head at the same height is a reorg replacement. Its candidate
		// key includes the block hash, so start a fresh vote context instead of
		// dropping the replacement merely because the old candidate was sent.
		if obs.Kind == KindNewHeads && obs.Key != ctx.delivered {
			ctx.first = obs.ObservedAt
			if ctx.first.IsZero() {
				ctx.first = time.Now()
			}
			ctx.status = Collecting
			ctx.delivered = ""
			ctx.revoked = false
			ctx.candidates = make(map[string]*candidate)
		} else {
			return nil
		}
	}
	if obs.Removed {
		ctx.status = Quarantined
		a.reportLocked(ErrUnboundRemoval)
		return ErrUnboundRemoval
	}
	c := ctx.candidates[obs.Key]
	if c == nil {
		c = &candidate{obs: cloneObservation(obs), sources: make(map[string]struct{})}
		ctx.candidates[obs.Key] = c
	}
	if _, seen := c.sources[obs.SourceID]; seen {
		return nil
	}
	if limit := a.cfg.Budget.MaxEvidenceBytes; limit > 0 && a.evidence+int64(len(obs.Payload)) > limit {
		a.reportLocked(ErrEvidenceBudget)
		return ErrEvidenceBudget
	}
	c.sources[obs.SourceID] = struct{}{}
	a.evidence += int64(len(obs.Payload))
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

// AdvanceContext applies the soft/hard deadline to one logical event without
// advancing unrelated subscription contexts.
func (a *Aggregator) AdvanceContext(kind Kind, contextKey string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx := a.contexts[string(kind)+"\x00"+contextKey]
	if ctx == nil || ctx.status == Delivered || ctx.status == Selected || ctx.status == Revoked || ctx.status == Quarantined || ctx.status == Canceled {
		return
	}
	if now.Sub(ctx.first) >= a.cfg.HardTimeout {
		ctx.status = Quarantined
		a.reportLocked(ErrQuorumTimeout)
	} else if a.cfg.SoftTimeout > 0 && now.Sub(ctx.first) >= a.cfg.SoftTimeout {
		ctx.status = Probing
	}
}

// QuarantineContext closes one logical event's compensation window without
// advancing unrelated contexts. It is used by a Pull probe whose deadline is
// shorter than the aggregator's general hard timeout.
func (a *Aggregator) QuarantineContext(kind Kind, contextKey string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx := a.contexts[string(kind)+"\x00"+contextKey]
	if ctx == nil || ctx.status == Delivered || ctx.status == Selected || ctx.status == Revoked || ctx.status == Quarantined || ctx.status == Canceled {
		return
	}
	ctx.status = Quarantined
	a.reportLocked(ErrQuorumTimeout)
}

func (a *Aggregator) Status(contextKey string) Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, kind := range []Kind{KindNewHeads, KindLogs, KindTransactionReceipts} {
		if ctx := a.contexts[string(kind)+"\x00"+contextKey]; ctx != nil {
			return ctx.status
		}
	}
	return Canceled
}

// Unsubscribe is an intentional, error-free termination. Transport failures
// and policy errors are reported through Err before this lifecycle action; a
// caller explicitly stopping a subscription should not observe a synthetic
// cancellation error.
func (a *Aggregator) Unsubscribe() { a.close(nil) }
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

// CanonicalTransactionReceiptsKey returns a stable logical context and
// candidate key for one transaction-receipts notification. Receipt order is
// normalized by transaction index and hash, while the candidate retains the
// complete caller-supplied typed representation. The context contains only
// block identity, so two providers that disagree on receipt fields still
// vote in the same event and become a conflict instead of two independent
// events.
func CanonicalTransactionReceiptsKey(receipts []ReceiptIdentity) (contextKey, key string, err error) {
	if len(receipts) == 0 {
		return "receipts:empty", digest(KindTransactionReceipts, []any{}), nil
	}
	type canonicalReceipt struct {
		TransactionHash  string          `json:"transactionHash"`
		BlockHash        string          `json:"blockHash"`
		BlockNumber      string          `json:"blockNumber"`
		TransactionIndex uint64          `json:"transactionIndex"`
		Canonical        json.RawMessage `json:"canonical"`
	}
	rows := make([]canonicalReceipt, 0, len(receipts))
	blocks := make([]string, 0, len(receipts))
	for _, receipt := range receipts {
		if receipt.TransactionHash == "" || receipt.BlockHash == "" || receipt.BlockNumber == "" {
			return "", "", fmt.Errorf("subscription: incomplete transaction receipt")
		}
		canonical, canonicalErr := canonicalJSON(receipt.Canonical)
		if canonicalErr != nil {
			return "", "", fmt.Errorf("subscription: malformed transaction receipt: %w", canonicalErr)
		}
		row := canonicalReceipt{
			TransactionHash:  strings.ToLower(receipt.TransactionHash),
			BlockHash:        strings.ToLower(receipt.BlockHash),
			BlockNumber:      strings.ToLower(receipt.BlockNumber),
			TransactionIndex: receipt.TransactionIndex,
			Canonical:        canonical,
		}
		rows = append(rows, row)
		blocks = append(blocks, row.BlockNumber+":"+row.BlockHash)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TransactionIndex != rows[j].TransactionIndex {
			return rows[i].TransactionIndex < rows[j].TransactionIndex
		}
		return rows[i].TransactionHash < rows[j].TransactionHash
	})
	sort.Strings(blocks)
	blocks = compactStrings(blocks)
	contextKey = "receipts:" + digest(KindTransactionReceipts, blocks)
	return contextKey, digest(KindTransactionReceipts, rows), nil
}

func canonicalJSON(raw []byte) (json.RawMessage, error) {
	if len(raw) == 0 {
		return []byte("null"), nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON data")
		}
		return nil, err
	}
	out, err := json.Marshal(value)
	return out, err
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
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
