package ethguard

import (
	"context"
	"errors"
	"fmt"

	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// The shared public-operation entry.
//
// Every public business operation — typed reads, writes, anchors, the
// generic Call — passes through here exactly once. The entry owns the facts
// that must be identical across all of them: the capture is bound atomically
// before any validation, the operation's context merges the caller's context
// with the owner lifecycle, the per-call material collector is created only
// for a call with a consumer, the closed gate rejects new work before any
// network contact, and the completion result is published exactly once at
// the outermost layer. Internal sub-operations call shared private
// implementations with explicit state — they never re-enter this entry, so
// an anchor or a reference round can never bind, consume, or publish on a
// capture owned by the outer call.
//
// The two facades differ only in input shape and delivery: a view operation
// passes view=true, which omits material collection when no capture is
// bound (nothing would consume it). A native operation always has an
// explicit result consumer.

// execution carries one public operation's shared state across its body.
type execution struct {
	client *Client
	// ctx is the merged operation context: the caller's context with the
	// owner lifecycle cancellation attached.
	ctx context.Context
	// cancel releases the merged context; the owner cause is recognizable
	// through context.Cause when the owner fired first.
	cancel context.CancelCauseFunc
	// stop detaches the owner-close watcher.
	stop func() bool
	// capture is the caller's result capture; nil when none was bound.
	capture *ResultCapture
	// publishable reports that this operation won the capture binding and
	// must publish the completion result. A losing operation runs its
	// normal business contract and only skips publication.
	publishable bool
	// collector is this call's material collector; nil when collection is
	// off (no configuration, or a view call without a capture consumer).
	collector *uncertain.Capture
}

// begin starts one public operation. It binds the capture first — before any
// validation — then assembles the merged context and the material consumer
// decision. view marks a call made through the ethclient-shaped view: with
// no capture bound, raw material has no consumer and is omitted; business
// behavior never changes either way.
func (c *Client) begin(ctx context.Context, view bool) *execution {
	e := &execution{client: c, capture: captureFromContext(ctx)}
	e.publishable = e.capture.bind()

	merged, cancel := context.WithCancelCause(ctx)
	e.ctx, e.cancel = merged, cancel
	e.stop = context.AfterFunc(c.lifeCtx, func() { cancel(pool.ErrClosed) })

	// Material collection: the per-call budget is this operation's own.
	// A view call without a capture has no consumer and omits collection;
	// a native call always publishes an explicit result.
	if cfg, ok := uncertain.ConfigFromContext(ctx); ok && (!view || e.publishable) {
		e.collector = uncertain.NewCollector(cfg)
		e.ctx = uncertain.WithCollector(e.ctx, e.collector)
	}
	return e
}

// done releases the operation's context resources. It runs after the
// completion result is published; the merged context lives exactly as long
// as the operation.
func (e *execution) done() {
	e.stop()
	e.cancel(nil)
}

// closedGate rejects a new operation on a closed client before any network
// contact. A valid capture still receives this completion result.
func (e *execution) closedGate() (CallResult, error) {
	if !e.client.isClosed() {
		return CallResult{}, nil
	}
	result := CallResult{Err: ErrPoolClosed}
	result = e.finish(result)
	return result, ErrPoolClosed
}

// finish completes the result — attaching this call's material snapshot —
// and publishes it exactly once. A capture that lost its binding is not
// written. The completed result comes back so the caller returns the same
// delivery it published.
func (e *execution) finish(result CallResult) CallResult {
	if e.collector != nil {
		result.Uncertain = materialSnapshot(e.collector)
	}
	if e.publishable {
		e.capture.publish(result)
	}
	return result
}

// mapLifecycleError keeps the two cancellation causes distinguishable: a
// user cancellation or deadline that fired first keeps its context error;
// an owner close that fired first is recognizable as ErrPoolClosed. The
// joined error preserves both matches.
func (e *execution) mapLifecycleError(err error) error {
	cause := context.Cause(e.ctx)
	if cause == nil || !errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(cause, pool.ErrClosed) && !errors.Is(err, ErrPoolClosed) {
		return errors.Join(ErrPoolClosed, err)
	}
	return err
}

// recover aborts the capture and rethrows a propagated panic. Capture
// publication never masks a panic with a half result.
func (e *execution) recoverPanic() {
	if r := recover(); r != nil {
		e.capture.abort()
		panic(r)
	}
}

// operation is the full read-shaped entry: it runs body under the shared
// entry rules and returns the business value, the completion result (Err
// identical to the trailing error), and that error. The generic parameter is
// the operation's business value.
func operation[V any](c *Client, ctx context.Context, view bool, body func(opCtx context.Context) (V, CallResult, error)) (V, CallResult, error) {
	e := c.begin(ctx, view)
	defer e.done()
	defer e.recoverPanic()
	if result, err := e.closedGate(); err != nil {
		var zero V
		return zero, result, err
	}
	value, result, err := body(e.ctx)
	result.Err = err
	if result.Err != nil {
		result.Err = e.mapLifecycleError(result.Err)
	}
	result = e.finish(result)
	return value, result, result.Err
}

// operationPair is the entry for reads with two business values (the
// pending-flagged transaction read). It rides the single-value entry with a
// transient pair, so the entry rules stay defined exactly once.
func operationPair[A, B any](c *Client, ctx context.Context, view bool, body func(opCtx context.Context) (A, B, CallResult, error)) (A, B, CallResult, error) {
	out, result, err := operation(c, ctx, view, func(opCtx context.Context) (pairOf[A, B], CallResult, error) {
		a, b, r, bodyErr := body(opCtx)
		return pairOf[A, B]{first: a, second: b}, r, bodyErr
	})
	return out.first, out.second, result, err
}

type pairOf[A, B any] struct {
	first  A
	second B
}

// writeOperation is the write-shaped entry: the body returns the broadcast
// result, the entry publishes and returns it with the mapped error. view
// marks a call made through the ethclient-shaped view: with no capture
// bound, raw material has no consumer and is omitted.
func writeOperation(c *Client, ctx context.Context, view bool, body func(opCtx context.Context) (CallResult, error)) (CallResult, error) {
	e := c.begin(ctx, view)
	defer e.done()
	defer e.recoverPanic()
	if result, err := e.closedGate(); err != nil {
		return result, err
	}
	result, err := body(e.ctx)
	if err != nil {
		err = e.mapLifecycleError(err)
	}
	result.Err = err
	result = e.finish(result)
	return result, err
}

// establishAnchor is the shared private anchor execution: it establishes the
// reference for tag through the client's resolver and delivers the evidence
// explicitly to the caller. Internal sub-operations (moving-tag reads,
// pending pinning) call this directly with their own context — never a
// public anchor facade — so no nested capture binding and no early
// completion can happen. An empty tag means the default latest reference.
func (c *Client) establishAnchor(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	ref, err := resolveReference(ctx, c.resolver, tag)
	if err == nil && ref.Status != rpc.Corroborated {
		err = fmt.Errorf("uncorroborated reference: %w", rpc.ErrConflict)
	}
	return ref, err
}
