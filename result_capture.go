package ethguard

import (
	"context"
	"sync"

	internaluncertain "github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/rpc"
)

// ResultCapture receives the execution result of exactly one public call.
// It is opt-in and invocation-scoped: WithResult puts a fresh capture on the
// context, the bound call publishes into it once, and Result reads that
// delivery back. Reading the capture never changes the business result or
// error.
//
// The state machine is lock-guarded: unbound → running → completed, with two
// terminal failure states. A handle that enters a second independent call —
// whether or not the first completed — is permanently invalidated, and
// Result then reports ErrCaptureReused forever. A call that exits abnormally
// (a propagated panic) marks the capture aborted; invalidation wins over an
// abnormal exit. Capture errors never enter business errors and never
// replace CallResult.Err.
type ResultCapture struct {
	mu     sync.Mutex
	state  captureState
	result CallResult
}

type captureState uint8

const (
	captureUnbound captureState = iota
	captureRunning
	captureCompleted
	captureInvalidated
	captureAborted
)

// WithResult returns a derived context carrying a fresh result capture.
// The capture alone enables nothing else: raw material collection still
// needs WithSourceEvidence.
func WithResult(ctx context.Context) (context.Context, *ResultCapture) {
	c := &ResultCapture{}
	return context.WithValue(ctx, resultCaptureKey{}, c), c
}

type resultCaptureKey struct{}

// captureFromContext is the entry-side accessor for the caller's capture.
func captureFromContext(ctx context.Context) *ResultCapture {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(resultCaptureKey{}).(*ResultCapture)
	return c
}

// bind marks the capture as running for exactly one call. The second bind on
// the same handle — concurrent or after completion — invalidates it and
// reports reuse; the calling operation still executes under its normal
// contract, it only stops publishing here. The caller passes the per-call
// material collector (nil when collection is off) so publication can attach
// the evidence snapshot.
func (c *ResultCapture) bind() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case captureUnbound:
		c.state = captureRunning
		return true
	default:
		c.state = captureInvalidated
		return false
	}
}

// publish stores the finished business outcome. Only a running capture
// completes; the stored snapshot owns its material — the capture clones on
// entry so the caller's value can never reach a later Result copy.
func (c *ResultCapture) publish(result CallResult) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != captureRunning {
		return
	}
	c.result = result.Clone()
	c.state = captureCompleted
}

// abort marks a call that exited abnormally (a propagated panic). A
// previously invalidated capture stays invalidated: reuse wins over an
// abnormal exit.
func (c *ResultCapture) abort() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == captureRunning {
		c.state = captureAborted
	}
}

// Result returns the published outcome: the isolated result copy, whether a
// normal completion was published, and a capture-usage error. It never waits
// for the bound call to finish: an unpublished capture returns the zero
// result, false, nil. Capture errors are delivery facts — they never enter
// the business error channel.
func (c *ResultCapture) Result() (CallResult, bool, error) {
	if c == nil {
		return CallResult{}, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case captureCompleted:
		return c.result.Clone(), true, nil
	case captureInvalidated:
		return CallResult{}, false, ErrCaptureReused
	case captureAborted:
		return CallResult{}, false, ErrCaptureAborted
	default:
		// Unbound or still running: nothing has been published.
		return CallResult{}, false, nil
	}
}

// Clone isolates the result's mutable material so a delivered copy can be
// mutated without reaching the capture's snapshot or another Result copy.
func (r CallResult) Clone() CallResult {
	if r.Decision != nil {
		d := r.Decision.Clone()
		r.Decision = &d
	}
	if r.WriteDecision != nil {
		w := r.WriteDecision.Clone()
		r.WriteDecision = &w
	}
	if r.Uncertain != nil {
		u := cloneUncertainResult(*r.Uncertain)
		r.Uncertain = &u
	}
	// Err is not cloned: the error interface keeps errors.Is/As, and
	// published errors are immutable by contract.
	return r
}

// WithSourceEvidence configures raw source-evidence retention for the calls
// made on the derived context: budgetBytes bounds the total material bytes
// one call retains (each raw response at most 64 KiB, at most 256 records
// across raw and fields, each variable metadata text at most 4 KiB). A
// non-positive budget returns the original context and an error wrapping
// ErrInvalidRequest — nothing dials, nothing runs. The last configuration on
// a derivation chain wins. Collection is per call: internal reference
// rounds, the business fan-out, admission dependencies, and broadcast
// material share one call's budget, different calls never share a consumed
// count.
//
// WithSourceEvidence never changes a business rule: a spent budget stops
// retention only — never a request, a quorum, or a vote — and the transport
// response limits stay independent.
func WithSourceEvidence(ctx context.Context, budgetBytes int) (context.Context, error) {
	if budgetBytes <= 0 {
		return ctx, &invalidBudgetError{budgetBytes: budgetBytes}
	}
	return internaluncertain.WithConfig(ctx, internaluncertain.Config{BudgetBytes: budgetBytes}), nil
}

type invalidBudgetError struct{ budgetBytes int }

func (e *invalidBudgetError) Error() string {
	return "ethguard: source evidence budget must be positive"
}

func (e *invalidBudgetError) Unwrap() error { return rpc.ErrInvalidRequest }

// materialSnapshot converts the collector's snapshot into the public
// UncertainResult. The snapshot is already a defensive copy.
func materialSnapshot(c *internaluncertain.Capture) *UncertainResult {
	if c == nil {
		return nil
	}
	snapshot := c.Snapshot()
	return &UncertainResult{
		Fields:    snapshot.Fields,
		Raw:       snapshot.Raw,
		Bytes:     snapshot.Bytes,
		Limit:     snapshot.Limit,
		Dropped:   snapshot.Dropped,
		Truncated: snapshot.Truncated,
	}
}
