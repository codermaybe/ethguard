package subscription

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

var ErrNextAfterTerminal = errors.New("subscription: terminal error already returned")

// TransferError preserves source-owned audit when the destination cannot
// reserve it. NextWithBudget's returned release owns that audit under the
// original budget until the receiving owner consumes/discards the error.
type TransferError struct {
	Cause error
	Audit AuditRange
}

func (e *TransferError) Error() string {
	return fmt.Sprintf("subscription ownership transfer: %v", e.Cause)
}
func (e *TransferError) Unwrap() error { return e.Cause }

// Event owns its payload leases until Next transfers their immutable values
// to the caller. Node is the only topology retained by the queue.
type Event struct {
	Node          AnchorNode
	Header        PayloadLease
	Evidence      PayloadLease
	EvidenceTier  EvidenceTier
	Evaluation    EvaluationSnapshot
	Audit         AuditRange
	HeaderValue   any
	EvidenceValue any
	// Delta is one complete topology transaction; no full path snapshot is retained.
	Delta *PathDelta
}

type queuedEvent struct {
	event       Event
	release     func()
	pathRelease func()
	next        *queuedEvent
}

// EventQueue is a per-subscription, byte-bounded FIFO. Push never waits for a
// consumer and never sends on a caller-owned channel.
type EventQueue struct {
	mu            sync.Mutex
	nextMu        sync.Mutex
	notify        chan struct{}
	done          chan struct{}
	doneOnce      sync.Once
	budget        MemoryBudget
	first, last   *queuedEvent
	closed        bool
	terminal      error
	drainTerminal bool
	errorReturned bool
}

func NewEventQueue(budget MemoryBudget) *EventQueue {
	return &EventQueue{notify: make(chan struct{}, 1), done: make(chan struct{}), budget: budgetOrUnlimited(budget)}
}
func (q *EventQueue) Done() <-chan struct{} { return q.done }
func (q *EventQueue) doneLocked()           { q.doneOnce.Do(func() { close(q.done) }) }

func eventQueueBytes(event Event) uint64 {
	return uint64(unsafe.Sizeof(queuedEvent{})) + evidenceBytes(event)
}

// Push takes ownership of event, both leases and its complete delta on every return path.
func (q *EventQueue) Push(event Event) error { return q.Publish(event, nil) }

// Publish reserves a complete event, commits its version while readers are
// excluded, and then makes the complete event visible. Failure transfers and
// releases event ownership without exposing any partial delta.
func (q *EventQueue) Publish(event Event, commit func(*Event) error) error {
	pathRelease, err := q.budget.Reserve(deltaBytes(event.Delta))
	if err != nil {
		q.dropEvent(event)
		return err
	}
	release, err := q.budget.Reserve(eventQueueBytes(event))
	if err != nil {
		pathRelease()
		q.dropEvent(event)
		return err
	}
	item := &queuedEvent{event: event, release: release, pathRelease: pathRelease}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		release()
		pathRelease()
		q.dropEvent(event)
		return ErrClosed
	}
	if commit != nil {
		if err := commit(&item.event); err != nil {
			q.mu.Unlock()
			release()
			pathRelease()
			q.dropEvent(event)
			return err
		}
	}
	if q.last == nil {
		q.first = item
	} else {
		q.last.next = item
	}
	q.last = item
	q.mu.Unlock()
	q.signal()
	return nil
}

func (q *EventQueue) dropEvent(event Event) {
	event.Audit.releaseOwned()
	event.Header.release()
	event.Evidence.release()
	if event.Delta != nil {
		for _, d := range event.Delta.Appended {
			d.Header.release()
			d.Evidence.release()
		}
	}
}

// Next serializes consumers, preserving FIFO without duplicate deliveries.
func (q *EventQueue) Next(ctx context.Context) (Event, error) {
	event, _, err := q.next(ctx, nil)
	return event, err
}

// NextWithBudget transfers one event (or producer terminal) to another owner.
// The destination reservation succeeds before any source accounting is
// released. The returned release must be kept with the event/error until that
// owner discards it or transfers it to its external consumer. A failed reserve
// transfers only the audit under its original budget; the returned release
// owns that audit, while the source queue still owns the remaining payload.
func (q *EventQueue) NextWithBudget(ctx context.Context, destination MemoryBudget) (Event, func(), error) {
	if destination == nil {
		return Event{}, nil, fmt.Errorf("subscription: missing destination budget")
	}
	return q.next(ctx, destination)
}

func (q *EventQueue) next(ctx context.Context, destination MemoryBudget) (Event, func(), error) {
	q.nextMu.Lock()
	defer q.nextMu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			q.cancel(err)
			return Event{}, nil, err
		}
		q.mu.Lock()
		if q.first != nil {
			item := q.first
			q.first = item.next
			if q.first == nil {
				q.last = nil
			}
			item.next = nil
			q.mu.Unlock()
			var retained func()
			var reserveErr error
			if destination != nil {
				retained, reserveErr = destination.Reserve(transferEventBytes(item.event))
			}
			q.mu.Lock()
			aborted := q.closed && !q.drainTerminal
			if reserveErr != nil && !aborted {
				item.next = q.first
				q.first = item
				if q.last == nil {
					q.last = item
				}
				audit := item.event.Audit
				item.event.Audit = AuditRange{}
				q.mu.Unlock()
				return Event{}, sync.OnceFunc(audit.releaseOwned), &TransferError{Cause: reserveErr, Audit: audit}
			}
			q.mu.Unlock()
			item.release()
			item.pathRelease()
			if aborted {
				q.dropEvent(item.event)
				if retained != nil {
					retained()
				}
				return Event{}, nil, ErrClosed
			}
			if item.event.Delta != nil {
				for i := range item.event.Delta.Appended {
					d := &item.event.Delta.Appended[i]
					d.HeaderValue = d.Header.take()
					d.EvidenceValue = d.Evidence.take()
					d.Header = PayloadLease{}
					d.Evidence = PayloadLease{}
				}
			}
			item.event.HeaderValue = item.event.Header.take()
			item.event.EvidenceValue = item.event.Evidence.take()
			item.event.Header = PayloadLease{}
			item.event.Evidence = PayloadLease{}
			item.event.Audit.releaseOwned()
			return item.event, retained, nil
		}

		if q.closed {
			err := q.terminal
			if q.drainTerminal && !q.errorReturned {
				q.terminal = nil
				q.mu.Unlock()
				var retained func()
				var reserveErr error
				if destination != nil {
					size := uint64(unsafe.Sizeof(TerminalError{}))
					var terminal *TerminalError
					if errors.As(err, &terminal) {
						size += transferAuditBytes(terminal.Audit)
					}
					retained, reserveErr = destination.Reserve(size)
				}
				q.mu.Lock()
				aborted := !q.drainTerminal
				if reserveErr != nil && !aborted {
					var audit AuditRange
					var terminal *TerminalError
					if errors.As(err, &terminal) {
						audit = terminal.Audit
						terminal.Audit = AuditRange{}
					}
					q.terminal = err
					q.mu.Unlock()
					return Event{}, sync.OnceFunc(audit.releaseOwned), &TransferError{Cause: errors.Join(reserveErr, err), Audit: audit}
				}
				if !aborted {
					q.errorReturned = true
					q.doneLocked()
				}
				q.mu.Unlock()
				releaseTerminalOwned(err)
				if aborted {
					if retained != nil {
						retained()
					}
					return Event{}, nil, ErrClosed
				}
				if err == nil {
					err = ErrClosed
				}
				return Event{}, retained, err
			}

			drain := q.drainTerminal
			q.mu.Unlock()
			if drain {
				return Event{}, nil, ErrNextAfterTerminal
			}
			if err != nil {
				return Event{}, nil, err
			}
			return Event{}, nil, ErrClosed
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			q.cancel(ctx.Err())
			return Event{}, nil, ctx.Err()
		case <-q.notify:
		}
	}
}

func transferEventBytes(e Event) uint64 {
	n := eventQueueBytes(e) + deltaBytes(e.Delta) + e.Header.Bytes + e.Evidence.Bytes + transferAuditBytes(e.Audit)
	for _, w := range e.Evaluation.Witnesses {
		n = saturatingAdd(n, uint64(len(w.Session.SourceID)))
	}
	if e.Delta != nil {
		for _, entry := range e.Delta.Appended {
			n = saturatingAdd(n, saturatingAdd(entry.Header.Bytes+entry.Evidence.Bytes, uint64(len(entry.ObjectSupplier.Session.SourceID))))
		}
	}
	return n
}

func transferAuditBytes(a AuditRange) uint64 {
	var size uint64
	for entry := a.first; entry != nil && entry.record.Sequence <= a.Last; entry = entry.next {
		size = saturatingAdd(size, uint64(unsafe.Sizeof(auditEntry{}))+uint64(len(entry.record.Session.SourceID)))
	}
	return size
}

// Close with no error is caller cancellation: discard everything immediately.
// Close(err) is producer termination: drain queued events, then return err
// exactly once. It is idempotent and safe concurrently with Next.
func (q *EventQueue) Close(reason ...error) error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		if len(reason) > 0 {
			releaseTerminalOwned(reason[0])
		}
		return nil
	}
	q.closed = true
	if len(reason) > 0 && reason[0] != nil {
		q.terminal = reason[0]
		q.drainTerminal = true
	} else {
		q.terminal = ErrClosed
		if len(reason) > 0 {
			q.terminal = reason[0]
		}
	}
	if !q.drainTerminal {
		q.dropLocked()
		q.doneLocked()
	}
	q.mu.Unlock()
	q.signal()
	return nil
}

// Abort is an explicit consumer-side cancellation. It overrides a pending
// producer-drain close, discards all queued ownership, and wakes Next.
func (q *EventQueue) Abort() {
	q.mu.Lock()
	q.dropTerminalLocked()
	q.closed = true
	q.terminal = ErrClosed
	q.drainTerminal = false
	q.dropLocked()
	q.doneLocked()
	q.mu.Unlock()
	q.signal()
}

func (q *EventQueue) cancel(err error) {
	q.mu.Lock()
	q.dropTerminalLocked()
	q.closed = true
	q.drainTerminal = false
	q.terminal = err
	q.dropLocked()
	q.doneLocked()
	q.mu.Unlock()
	q.signal()
}
func releaseTerminalOwned(err error) {
	if owned, ok := err.(interface{ releaseOwned() }); ok {
		owned.releaseOwned()
		return
	}
	var terminal *TerminalError
	if errors.As(err, &terminal) {
		terminal.releaseOwned()
	}
}

func (q *EventQueue) dropTerminalLocked() {
	releaseTerminalOwned(q.terminal)
	q.terminal = nil
}

func (q *EventQueue) dropLocked() {
	for q.first != nil {
		item := q.first
		q.first = item.next
		item.release()
		item.pathRelease()
		q.dropEvent(item.event)
	}
	q.last = nil
}

func (q *EventQueue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func deltaBytes(d *PathDelta) uint64 {
	if d == nil {
		return 0
	}
	return uint64(unsafe.Sizeof(PathDelta{})) + uint64(cap(d.Retracted)+cap(d.PreBirthProof))*uint64(unsafe.Sizeof(AnchorNode{})) + uint64(cap(d.Appended))*uint64(unsafe.Sizeof(DeliveredHeader{}))
}
