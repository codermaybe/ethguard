package subscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
)

type transferBudgetFunc func(uint64) (func(), error)

func (f transferBudgetFunc) Reserve(n uint64) (func(), error) { return f(n) }

func TestTransferReserveCanObserveAndAbortWithoutMainLock(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			source := NewMemoryBudget(0)
			q := NewEventQueue(source)
			if terminal {
				hold, _ := source.Reserve(100)
				_ = q.Close(&TerminalError{Cause: ErrRecoveryExhausted, release: hold})
			} else {
				if err := q.Push(Event{Header: testLease(t, NewPayloadArena(source), "payload")}); err != nil {
					t.Fatal(err)
				}
			}
			destination := NewMemoryBudget(0)
			_, release, err := q.NextWithBudget(context.Background(), transferBudgetFunc(func(n uint64) (func(), error) { q.Abort(); return destination.Reserve(n) }))
			if !errors.Is(err, ErrClosed) || release != nil || source.Used() != 0 || destination.Used() != 0 {
				t.Fatalf("abort transfer err=%v source=%d destination=%d", err, source.Used(), destination.Used())
			}
		})
	}
}

func TestContentCloseJoinsInFlightTransferAndPreservesCauseOnly(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			life, cancel := context.WithCancel(context.Background())
			budget := NewMemoryBudget(0)
			done, watcher := make(chan struct{}), make(chan struct{})
			close(done)
			close(watcher)
			s := &contentSubscription[LogsEvent]{life: life, cancel: cancel, budget: budget, done: done, watcherDone: watcher, terminated: make(chan struct{}), drained: make(chan struct{}), notify: make(chan struct{}, 1)}
			hold, _ := budget.Reserve(100)
			if terminal {
				entry := &auditEntry{record: AuditRecord{Sequence: 1}, release: func() {}}
				wrapped := &TransferError{Cause: fmt.Errorf("fetch: %w", &TerminalError{Cause: ErrRecoveryExhausted, Audit: AuditRange{First: 1, Last: 1, first: entry}}), Audit: AuditRange{First: 1, Last: 1, first: entry}}
				s.finish(wrapped, AuditRange{First: 1, Last: 1, first: entry}, nil, []func(){hold})
				var transfer *TransferError
				var head *TerminalError
				if !errors.Is(s.TerminalError(), ErrRecoveryExhausted) || errors.As(s.TerminalError(), &transfer) || errors.As(s.TerminalError(), &head) {
					t.Fatalf("cause retains ownership: %T %v", s.TerminalError(), s.TerminalError())
				}
			} else {
				if err := s.publish(LogsEvent{}, 0, 1, AnchorNode{}, 0, []func(){hold}); err != nil {
					t.Fatal(err)
				}
			}
			entered, releaseReserve := make(chan struct{}), make(chan struct{})
			nextDone := make(chan struct{})
			destination := NewMemoryBudget(0)
			go func() {
				defer close(nextDone)
				_, _, release, err := s.nextWithBudget(context.Background(), transferBudgetFunc(func(n uint64) (func(), error) {
					_ = s.TerminalError()
					close(entered)
					<-releaseReserve
					return destination.Reserve(n)
				}))
				if release != nil {
					release()
				}
				if !errors.Is(err, ErrClosed) {
					t.Errorf("Next=%v", err)
				}
			}()
			<-entered
			closeDone := make(chan struct{})
			go func() { _ = s.Close(); close(closeDone) }()
			select {
			case <-closeDone:
				t.Fatal("Close escaped in-flight source ownership")
			case <-time.After(10 * time.Millisecond):
			}
			close(releaseReserve)
			<-nextDone
			<-closeDone
			if budget.Used() != 0 || destination.Used() != 0 {
				t.Fatalf("content=%d destination=%d", budget.Used(), destination.Used())
			}
			if terminal && !errors.Is(s.TerminalError(), ErrRecoveryExhausted) {
				t.Fatal("Close changed established cause")
			}
			if !terminal && s.TerminalError() != nil {
				t.Fatal("intentional Close gained failure")
			}
		})
	}
}

func TestContentTransferBudgetRejectsBeforeOwnershipAndAllowsRetry(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	budget := NewMemoryBudget(0)
	s := &contentSubscription[LogsEvent]{life: life, cancel: cancel, budget: budget, terminated: make(chan struct{}), drained: make(chan struct{}), notify: make(chan struct{}, 1)}
	data := ethapi.Data("0x" + strings.Repeat("ab", 1<<20))
	event := LogsEvent{Path: LogsPathDelta{Appended: []LogsDelivery{{Logs: []ethapi.Log{{Data: &data}}}}}}
	hold, _ := budget.Reserve(uint64(len(data)))
	if err := s.publish(event, 0, 1, AnchorNode{}, 0, []func(){hold}); err != nil {
		t.Fatal(err)
	}
	before := budget.Used()
	if _, _, release, err := s.nextWithBudget(context.Background(), NewMemoryBudget(1024)); !errors.Is(err, ErrMemoryBudget) || release != nil {
		t.Fatalf("reserve=%v release=%v", err, release != nil)
	}
	if budget.Used() != before {
		t.Fatal("failure released source ownership")
	}
	destination := NewMemoryBudget(16 << 20)
	got, _, release, err := s.nextWithBudget(context.Background(), destination)
	if err != nil || release == nil || len(*got.Path.Appended[0].Logs[0].Data) != len(data) {
		t.Fatalf("retry=%v", err)
	}
	if budget.Used() != 0 || destination.Used() < uint64(len(data)) {
		t.Fatal("ownership transfer incomplete")
	}
	release()
	release()
	if destination.Used() != 0 {
		t.Fatal("release leaked")
	}
}

func TestContentIntentionalAbortWinsCancellationRace(t *testing.T) {
	for range 100 {
		life, cancel := context.WithCancel(context.Background())
		s := &contentSubscription[LogsEvent]{life: life, cancel: cancel, budget: NewMemoryBudget(0), terminated: make(chan struct{}), drained: make(chan struct{}), notify: make(chan struct{}, 1)}
		var wg sync.WaitGroup
		wg.Go(func() { <-life.Done(); s.finish(context.Canceled, AuditRange{}, nil, nil) })
		s.abort()
		wg.Wait()
		if s.TerminalError() != nil {
			t.Fatal(s.TerminalError())
		}
	}
}

func TestStableTerminalCauseStripsJoinedAuditOwners(t *testing.T) {
	budget := NewMemoryBudget(0)
	release, _ := budget.Reserve(128)
	entry := &auditEntry{record: AuditRecord{Sequence: 1}, release: release}
	audit := AuditRange{First: 1, Last: 1, first: entry}
	first := errors.New("upstream failed")
	second := errors.New("transfer failed")
	raw := fmt.Errorf("outer: %w", errors.Join(&TerminalError{Cause: first, Audit: audit}, &TransferError{Cause: second, Audit: audit}))
	clean := stableTerminalCause(raw)
	var head *TerminalError
	var transfer *TransferError
	if !errors.Is(clean, first) || !errors.Is(clean, second) || errors.As(clean, &head) || errors.As(clean, &transfer) {
		t.Fatalf("wrong stable cause: %v", clean)
	}
	audit.releaseOwned()
	if budget.Used() != 0 {
		t.Fatal("source audit not released")
	}
}

func TestHeadDestinationIncludesDynamicSourceIdentities(t *testing.T) {
	id := strings.Repeat("long-source-", 1<<14)
	event := Event{Evaluation: EvaluationSnapshot{Witnesses: []PathWitness{{Session: SourceSession{SourceID: id}}}}, Delta: &PathDelta{Appended: []DeliveredHeader{{ObjectSupplier: ObjectSupplier{Session: SourceSession{SourceID: id}}}}}, Audit: AuditRange{First: 1, Last: 1, first: &auditEntry{record: AuditRecord{Sequence: 1, Session: SourceSession{SourceID: id}}}}}
	if n := transferEventBytes(event); n < uint64(3*len(id)) {
		t.Fatalf("dynamic identity bytes missing: %d", n)
	}
}

func TestHeadIntentionalTerminationPrecedesQueueAbort(t *testing.T) {
	for _, ownerStop := range []bool{false, true} {
		t.Run(fmt.Sprint(ownerStop), func(t *testing.T) {
			owner, cancel := context.WithCancel(context.Background())
			defer cancel()
			source, _ := newHeadSource("a")
			s, err := startNewHeadSubscription(owner, context.Background(), []Source{source}, testHeadPolicy(), rpc.CorroborationForwarded)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			// Block queue mutation: producer termination must become visible before
			// Close/owner cancellation can acquire the queue and wake its readers.
			s.queue.mu.Lock()
			stopped := make(chan struct{})
			go func() {
				if ownerStop {
					cancel()
				} else {
					_ = s.Close()
				}
				close(stopped)
			}()
			select {
			case <-s.Terminated():
				s.queue.mu.Unlock()
			case <-time.After(time.Second):
				s.queue.mu.Unlock()
				t.Fatal("queue abort precedes stable intentional termination")
			}
			<-stopped
			if s.TerminalError() != nil {
				t.Fatal(s.TerminalError())
			}
			_ = s.Close()
			if s.budget.Used() != 0 {
				t.Fatal("close leaked source memory")
			}
		})
	}
}
